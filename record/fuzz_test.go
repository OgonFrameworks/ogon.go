// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fuzz the query builder (P14 bug-bounty / DATA-008).
//
// The query builder's buildSQL() must NEVER panic on adversarial
// field names, table names, or condition values. The placeholder/
// quoteIdent machinery is the SQL-injection guard (DATA-008): the
// contract is that any attacker-supplied column name is escaped via
// double-quote doubling, and any attacker-supplied value is bound
// as a placeholder — never string-interpolated.
//
// This fuzz target drives buildSQL with random column names and
// random values to ensure no panic + no SQL-injection leak.

package record

import (
	"strings"
	"testing"
)

// FuzzQueryBuilder drives Query[T].buildSQL with adversarial column
// names and where-clause values. The contract:
//   - no panic on any input
//   - generated SQL never contains a literal DROP/INSERT/UPDATE
//     keyword coming from a VALUE (placeholder binding must be used)
//   - generated SQL never contains a raw " from a column name (it
//     must be doubled to "")
//   - args slice length matches the number of placeholders
//
// Run: go test ./record -fuzz=FuzzQueryBuilder -fuzztime=3s
func FuzzQueryBuilder(f *testing.F) {
	// Seed corpus — at least 5 cases per spec.
	f.Add("email", "alice@example.com", 1)
	f.Add("", "", 0)
	f.Add("users; DROP TABLE users; --", "x", 1)
	f.Add("col\"with\"quotes", "val\"with\"quotes", 1)
	f.Add("col_with_unicode_éàü", "value_éàü", 10)
	f.Add(strings.Repeat("a", 1024), strings.Repeat("b", 1024), 100)
	f.Add("\x00null\x00col", "\x00null\x00val", 5)
	f.Add("emoji_col_🔒", "value_🔒", 1)
	f.Add("1", "2", 0) // numeric-only names — quoting still applies

	f.Fuzz(func(t *testing.T, colName, value string, limit int) {
		// The fuzz target does NOT need a registered model — we go
		// through the lower-level buildSQL path which lets us supply
		// the table name explicitly via .Table().
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("buildSQL panicked on col=%q val=%q limit=%d: %v",
					truncStr(colName), truncStr(value), limit, r)
			}
		}()

		// Negative limit could overflow the int? The builder ignores
		// zero/negative limits (limit > 0 check). Cap to avoid huge
		// allocations.
		if limit < 0 {
			limit = 0
		}
		if limit > 1000 {
			limit = 1000
		}

		q := NewQuery[scannerUser]().
			Table("test_table").
			Where(Eq(colName, value)).
			Limit(limit)

		sqlStr, args, err := q.buildSQL(DialectSQLite)
		if err != nil {
			// Errors are acceptable; we only assert no panic.
			return
		}

		// SQL-injection guard: the value MUST NOT appear as a literal
		// in the SQL (it must be a bound placeholder). We only check
		// values long enough that they cannot be a coincidental
		// substring of SQL keywords like AND/OR/IN/SELECT.
		if len(value) > 8 && strings.Contains(sqlStr, value) {
			t.Fatalf("SQL injection: value %q found in SQL %q (must be placeholder-bound)",
				truncStr(value), sqlStr)
		}

		// Column name must be quoted (every " in colName must be doubled).
		// The quoteIdent function wraps in " and replaces " with "".
		if colName != "" {
			// Build the expected quoted form.
			expected := `"` + strings.ReplaceAll(colName, `"`, `""`) + `"`
			if !strings.Contains(sqlStr, expected) {
				// Could be inside WHERE col = ? — search for it.
				// If the column name contains a special char that
				// breaks quoting, that's a bug.
				if !strings.Contains(sqlStr, expected) {
					t.Fatalf("column name %q not properly quoted in SQL %q (want %q)",
						truncStr(colName), sqlStr, expected)
				}
			}
		}

		// Placeholder count must equal args count. We strip quoted
		// identifiers before counting so a column literally named "?"
		// does not inflate the count.
		stripped := stripQuotedIdentifiers(sqlStr)
		phCount := strings.Count(stripped, "?")
		if phCount != len(args) {
			t.Fatalf("placeholder count %d != args count %d (SQL=%q stripped=%q)",
				phCount, len(args), sqlStr, stripped)
		}
	})
}

// FuzzConditionSQL drives the where.Condition.SQL method on And/Or/Eq/In
// with adversarial column names and values.
func FuzzConditionSQL(f *testing.F) {
	f.Add("col", "val")
	f.Add("", "")
	f.Add("col; DROP TABLE x; --", "val")
	f.Add("col\"\"", "val\"\"")
	f.Add("unicode_é", "value_é")
	f.Add("\x00col", "\x00val")

	f.Fuzz(func(t *testing.T, colName, value string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("condition SQL panicked on col=%q val=%q: %v",
					truncStr(colName), truncStr(value), r)
			}
		}()

		// Eq must never panic and must always produce SQL with a
		// placeholder (not the literal value). Only check values long
		// enough to not be a substring of SQL keywords.
		frag, args, _ := Eq(colName, value).SQL(DialectSQLite, 1)
		if len(value) > 8 && strings.Contains(frag, value) {
			t.Fatalf("Eq leaked value into SQL: %q in %q",
				truncStr(value), frag)
		}
		if colName != "" && !strings.Contains(frag, `"`) {
			// colName should be quoted even when empty.
			t.Fatalf("Eq did not quote column %q: %q", truncStr(colName), frag)
		}
		_ = args

		// In() with no args short-circuits to "1 = 0" (always-false).
		frag2, _, _ := In(colName).SQL(DialectSQLite, 1)
		if frag2 != "1 = 0" {
			t.Fatalf("empty In should return '1 = 0', got %q", frag2)
		}

		// And + Or composition.
		frag3, args3, _ := And{Eq(colName, value), Ne(colName, "x")}.SQL(DialectSQLite, 1)
		if len(value) > 8 && strings.Contains(frag3, value) {
			t.Fatalf("And leaked value into SQL: %q in %q",
				truncStr(value), frag3)
		}
		_ = args3

		frag4, _, _ := Or{Eq(colName, value)}.SQL(DialectSQLite, 1)
		if len(value) > 8 && strings.Contains(frag4, value) {
			t.Fatalf("Or leaked value into SQL: %q in %q",
				truncStr(value), frag4)
		}
		_ = frag4
	})
}

// truncStr keeps test failure messages readable.
func truncStr(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

// stripQuotedIdentifiers removes the contents of every "..." quoted
// identifier from s, leaving only the placeholders. This is used by
// the placeholder-count check so a column literally named "?" does
// not inflate the count. (A naive strings.Count("?", sql) would see
// the quoted "?" as a placeholder — false positive.)
//
// The implementation is a simple state machine that respects the
// doubled-quote escape ("") inside an identifier. It does NOT need
// to understand SQL semantics — only the quoting rule.
func stripQuotedIdentifiers(s string) string {
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !inQuote {
			if c == '"' {
				inQuote = true
				continue
			}
			b.WriteByte(c)
			continue
		}
		// in quote — look for doubled-quote escape.
		if c == '"' {
			if i+1 < len(s) && s[i+1] == '"' {
				i++ // skip the doubled quote
				continue
			}
			inQuote = false
			continue
		}
		// inside an identifier — skip.
	}
	return b.String()
}
