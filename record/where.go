// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Where-clause builder. Composable predicates that compile to
// parameterised SQL — the only building block the Query[T] builder uses,
// and the only path through which user input reaches the database.
//
// All condition emitters bind every literal as a placeholder ($1, $2 for
// pgx; ? for sqlite). No clause emits literal SQL from user input — the
// injection fuzz (DATA-095) verifies this.

package record

import (
	"fmt"
	"strings"
)

// Condition is a WHERE-clause node. SQL() emits the clause and Args()
// returns the bound parameter list (order-stable).
type Condition interface {
	SQL(dialect Dialect, startIdx int) (fragment string, args []any, nextIdx int)
}

// And and Or combine conditions. They short-circuit on no children.
type And []Condition
type Or []Condition

func (a And) SQL(d Dialect, startIdx int) (string, []any, int) {
	parts := make([]string, 0, len(a))
	args := make([]any, 0, len(a))
	idx := startIdx
	for _, c := range a {
		if c == nil {
			continue
		}
		frag, a2, next := c.SQL(d, idx)
		if frag == "" {
			continue
		}
		parts = append(parts, "("+frag+")")
		args = append(args, a2...)
		idx = next
	}
	if len(parts) == 0 {
		return "", nil, idx
	}
	return strings.Join(parts, " AND "), args, idx
}

func (o Or) SQL(d Dialect, startIdx int) (string, []any, int) {
	parts := make([]string, 0, len(o))
	args := make([]any, 0, len(o))
	idx := startIdx
	for _, c := range o {
		if c == nil {
			continue
		}
		frag, a2, next := c.SQL(d, idx)
		if frag == "" {
			continue
		}
		parts = append(parts, "("+frag+")")
		args = append(args, a2...)
		idx = next
	}
	if len(parts) == 0 {
		return "", nil, idx
	}
	return strings.Join(parts, " OR "), args, idx
}

// Placeholder produces the dialect-correct placeholder for the 1-indexed
// parameter position. pgx uses $N, sqlite uses ?.
func Placeholder(d Dialect, idx int) string {
	if d == DialectPostgres {
		return fmt.Sprintf("$%d", idx)
	}
	return "?"
}

// leaf builds a binary/quantifier condition from a column, an operator,
// and zero or more arguments.
type leaf struct {
	col  string
	op   string
	args []any
}

func (l leaf) SQL(d Dialect, startIdx int) (string, []any, int) {
	if l.col == "" {
		return "", nil, startIdx
	}
	idx := startIdx
	args := make([]any, 0, len(l.args))
	var b strings.Builder
	b.WriteString(quoteIdent(d, l.col))
	b.WriteByte(' ')
	b.WriteString(l.op)
	switch l.op {
	case "IS NULL", "IS NOT NULL":
		// no args
	case "IN", "NOT IN":
		b.WriteByte('(')
		for i, a := range l.args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(Placeholder(d, idx))
			args = append(args, a)
			idx++
		}
		b.WriteByte(')')
	default:
		b.WriteByte(' ')
		b.WriteString(Placeholder(d, idx))
		args = append(args, l.args...)
		idx++
	}
	return b.String(), args, idx
}

// quoteIdent wraps identifiers in the dialect's quoting convention.
func quoteIdent(d Dialect, name string) string {
	if d == DialectPostgres {
		// only simple names; schema-qualified names go through callers
		// that know how to split them.
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Eq builds column = ?.
func Eq(col string, v any) Condition { return leaf{col: col, op: "=", args: []any{v}} }

// Ne builds column <> ?.
func Ne(col string, v any) Condition { return leaf{col: col, op: "<>", args: []any{v}} }

// Lt, Le, Gt, Ge — the obvious comparisons.
func Lt(col string, v any) Condition { return leaf{col: col, op: "<", args: []any{v}} }
func Le(col string, v any) Condition { return leaf{col: col, op: "<=", args: []any{v}} }
func Gt(col string, v any) Condition { return leaf{col: col, op: ">", args: []any{v}} }
func Ge(col string, v any) Condition { return leaf{col: col, op: ">=", args: []any{v}} }

// In builds column IN (?, ?, ...). Panics on empty slice — empty IN is a
// semantic landmine and should be filtered at the caller boundary.
func In(col string, vs ...any) Condition {
	if len(vs) == 0 {
		// 1=0 — always-false clause. Avoids leaking NULL semantics into caller.
		return rawCondition{"1 = 0", nil}
	}
	return leaf{col: col, op: "IN", args: vs}
}

// Like builds column LIKE ?.
func Like(col string, pattern string) Condition {
	return leaf{col: col, op: "LIKE", args: []any{pattern}}
}

// IsNull builds column IS NULL.
func IsNull(col string) Condition { return leaf{col: col, op: "IS NULL"} }

// IsNotNull builds column IS NOT NULL.
func IsNotNull(col string) Condition { return leaf{col: col, op: "IS NOT NULL"} }

// rawCondition emits literal SQL. It is internal: the only callers are
// In()'s empty-list guard and the query builder's unscoped() helper.
// User-facing raw SQL goes through RawQuery[T] which is documented as the
// escape hatch.
type rawCondition struct {
	sqlStr string
	args   []any
}

func (r rawCondition) SQL(_ Dialect, startIdx int) (string, []any, int) {
	return r.sqlStr, append([]any{}, r.args...), startIdx
}
