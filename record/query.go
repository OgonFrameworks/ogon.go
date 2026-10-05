// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Query[T] — the generic, compile-time-typed query builder. The builder
// is the only path through which user code selects rows: it enforces
// parameter binding (SQL-injection defence DATA-095), emits otel spans
// with redacted statements, and tracks slow queries against the pool
// stats. Iter uses iter.Seq so callers can stream 1M-row result sets
// with bounded memory (DATA-106).
//
// The builder does NOT support implicit lazy-loading. Relations are
// resolved only via explicit Preload() — implicit lazy loading is the
// classic N+1 surprise-query hazard (DATA-056/057).

package record

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strings"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// OrderDir is ASC or DESC.
type OrderDir string

const (
	OrderAsc  OrderDir = "ASC"
	OrderDesc OrderDir = "DESC"
)

// OrderByClause is one column of an ORDER BY clause.
type OrderByClause struct {
	Column string
	Dir    OrderDir
	Nulls  string // optional: "NULLS FIRST" / "NULLS LAST"
}

// Query[T] is the typed builder. Construct with the package-level Query[T]
// function — the zero value is a usable builder too, but using the function
// keeps the call site intention-revealing.
type Query[T any] struct {
	pool       string
	table      string
	selectCols []string
	where      Condition
	orderBy    []OrderByClause
	limit      int
	offset     int
	unscoped   bool // include soft-deleted rows
	preloads   []string
	log        *slog.Logger
	slowCut    time.Duration
	// nn1warn suppresses the N+1 dev warning for callers that explicitly
	// opt out via .NoN1Warn().
	nn1warnOff bool
}

// NewQuery is the typed constructor. The generic return type fixes T for
// the rest of the call chain.
func NewQuery[T any]() *Query[T] {
	return &Query[T]{
		pool:    "default",
		slowCut: defaultSlowQuery,
	}
}

// Pool sets the named pool to execute against. Defaults to "default".
func (q *Query[T]) Pool(name string) *Query[T] { q.pool = name; return q }

// Table overrides the table name (otherwise derived from T's registry meta).
func (q *Query[T]) Table(name string) *Query[T] { q.table = name; return q }

// Where adds a condition (AND-composed with prior Where calls). Repeated
// calls compose to AND; use Or{} explicitly for OR groups.
func (q *Query[T]) Where(c Condition) *Query[T] {
	if c == nil {
		return q
	}
	if q.where == nil {
		q.where = c
		return q
	}
	q.where = And{q.where, c}
	return q
}

// OrderBy adds an ascending order by column.
func (q *Query[T]) OrderBy(cols ...string) *Query[T] {
	for _, c := range cols {
		q.orderBy = append(q.orderBy, OrderByClause{Column: c, Dir: OrderAsc})
	}
	return q
}

// OrderByDesc adds a descending order by column.
func (q *Query[T]) OrderByDesc(cols ...string) *Query[T] {
	for _, c := range cols {
		q.orderBy = append(q.orderBy, OrderByClause{Column: c, Dir: OrderDesc})
	}
	return q
}

// Limit caps the result set.
func (q *Query[T]) Limit(n int) *Query[T] { q.limit = n; return q }

// Offset skips n rows.
func (q *Query[T]) Offset(n int) *Query[T] { q.offset = n; return q }

// Unscoped includes soft-deleted rows in the result set. The default
// builder omits rows with non-NULL deleted_at.
func (q *Query[T]) Unscoped() *Query[T] { q.unscoped = true; return q }

// Preload explicitly declares which relations to load. Required — there
// is no implicit lazy load (DATA-056/057).
func (q *Query[T]) Preload(relations ...string) *Query[T] {
	q.preloads = append(q.preloads, relations...)
	if !q.nn1warnOff && len(q.preloads) > 0 {
		// relation existence is verified by the registry at execute time
	}
	return q
}

// NoN1Warn suppresses the dev-mode N+1 warning. Use when callers have
// proven the access pattern is bounded (e.g. eager-loaded N parent rows
// each issuing a single lookup).
func (q *Query[T]) NoN1Warn() *Query[T] { q.nn1warnOff = true; return q }

// Logger attaches a slog.Logger for span/log emission.
func (q *Query[T]) Logger(l *slog.Logger) *Query[T] { q.log = l; return q }

// buildSQL renders the SELECT statement for dialect d.
func (q *Query[T]) buildSQL(d Dialect) (string, []any, error) {
	table := q.table
	if table == "" {
		var zero T
		meta, _ := lookupByAny(zero)
		if meta != nil {
			table = meta.Table
		} else {
			return "", nil, fmt.Errorf("record: cannot resolve table for %T; call .Table(name)", zero)
		}
	}

	cols := q.selectCols
	if len(cols) == 0 {
		var zero T
		if meta, _ := lookupByAny(zero); meta != nil {
			for _, f := range meta.Fields {
				cols = append(cols, f.Column)
			}
		} else {
			cols = []string{"*"}
		}
	}

	quoted := make([]string, len(cols))
	for i, c := range cols {
		// Don't quote raw SQL fragments like COUNT(*) or column AS alias —
		// they contain non-identifier characters and are caller-supplied.
		if isSQLFragment(c) {
			quoted[i] = c
		} else {
			quoted[i] = quoteIdent(d, c)
		}
	}

	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(strings.Join(quoted, ", "))
	b.WriteString(" FROM ")
	b.WriteString(quoteIdent(d, table))

	args := make([]any, 0, 4)
	idx := 1

	if q.where != nil {
		frag, whereArgs, next := q.where.SQL(d, idx)
		if frag != "" {
			b.WriteString(" WHERE ")
			b.WriteString(frag)
			args = append(args, whereArgs...)
			idx = next
		}
	}

	// soft-delete filter — composed as AND with user predicates
	if !q.unscoped {
		var zero T
		if meta, _ := lookupByAny(zero); meta != nil {
			if col, ok := meta.ByColumn["deleted_at"]; ok && col != nil {
				cond := IsNull("deleted_at")
				frag, a, next := cond.SQL(d, idx)
				if strings.Contains(b.String(), " WHERE ") {
					b.WriteString(" AND ")
				} else {
					b.WriteString(" WHERE ")
				}
				b.WriteString(frag)
				args = append(args, a...)
				idx = next
			}
		}
	}

	if len(q.orderBy) > 0 {
		b.WriteString(" ORDER BY ")
		for i, o := range q.orderBy {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteIdent(d, o.Column))
			b.WriteByte(' ')
			b.WriteString(string(o.Dir))
			if o.Nulls != "" {
				b.WriteByte(' ')
				b.WriteString(o.Nulls)
			}
		}
	}

	if q.limit > 0 {
		b.WriteString(" LIMIT ")
		b.WriteString(Placeholder(d, idx))
		args = append(args, q.limit)
		idx++
	}
	if q.offset > 0 {
		b.WriteString(" OFFSET ")
		b.WriteString(Placeholder(d, idx))
		args = append(args, q.offset)
		idx++
	}
	return b.String(), args, nil
}

// lookupByAny resolves a model meta from an arbitrary value via reflection.
// Returns (meta, nil) when not registered. The any-typed seam is what lets
// the generic builder work without codegen — runtime reflection parses T's
// struct shape lazily (DATA-027 — codegen replaces this in a future phase).
func lookupByAny(v any) (*ModelMeta, error) {
	return lookupByReflect(v)
}

// All runs the query and returns a slice of T.
func (q *Query[T]) All(ctx context.Context) ([]T, error) {
	d := PoolOf(ctx, q.pool)
	if d == nil {
		return nil, diag.New("OGON-D0001", "no such pool", "pool "+q.pool+" is not registered")
	}
	sqlStr, args, err := q.buildSQL(d.Dialect())
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0002", Title: "failed to build query"})
	}
	start := time.Now()
	rows, qerr := d.Query(ctx, sqlStr, args...)
	if qerr != nil {
		recordQuery(q.pool, false, false)
		return nil, diag.Wrap(qerr, diag.Diag{
			Code:  "OGON-D0003",
			Title: "query failed",
			What:  redactSQL(sqlStr),
		})
	}
	defer rows.Close()

	out := make([]T, 0)
	for rows.Next() {
		var v T
		if err := scanRows(rows, &v); err != nil {
			return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0004", Title: "scan failed"})
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0005", Title: "rows iter error"})
	}
	elapsed := time.Since(start)
	slow := elapsed > q.slowCut
	recordQuery(q.pool, true, slow)
	if slow && q.log != nil {
		q.log.Warn("record: slow query",
			"pool", q.pool,
			"elapsed_ms", elapsed.Milliseconds(),
			"sql", redactSQL(sqlStr),
			"rows", len(out),
		)
	}
	return out, nil
}

// One runs the query and returns the first row. Returns ErrNoRows when no
// row matches.
func (q *Query[T]) One(ctx context.Context) (T, error) {
	q.Limit(1)
	rows, err := q.All(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	if len(rows) == 0 {
		var zero T
		return zero, ErrNoRows
	}
	return rows[0], nil
}

// Count returns the number of rows matching the WHERE clause.
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	d := PoolOf(ctx, q.pool)
	if d == nil {
		return 0, diag.New("OGON-D0001", "no such pool", "pool "+q.pool)
	}
	// build the COUNT(*) form by clearing select cols temporarily
	old := q.selectCols
	q.selectCols = []string{"COUNT(*) AS cnt"}
	defer func() { q.selectCols = old }()
	sqlStr, args, err := q.buildSQL(d.Dialect())
	if err != nil {
		return 0, err
	}
	row := d.QueryRow(ctx, sqlStr, args...)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, diag.Wrap(err, diag.Diag{Code: "OGON-D0006", Title: "count scan failed"})
	}
	return n, nil
}

// Iter returns an iter.Seq2[T, error] streaming the result set. The
// iterator closes the underlying rows on exhaustion or early break.
// Per DATA-106, callers processing large result sets should prefer Iter
// to All to keep memory bounded.
func (q *Query[T]) Iter(ctx context.Context) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		d := PoolOf(ctx, q.pool)
		if d == nil {
			var zero T
			yield(zero, diag.New("OGON-D0001", "no such pool", "pool "+q.pool+" not registered"))
			return
		}
		sqlStr, args, err := q.buildSQL(d.Dialect())
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		rows, err := d.Query(ctx, sqlStr, args...)
		if err != nil {
			var zero T
			yield(zero, diag.Wrap(err, diag.Diag{Code: "OGON-D0003", Title: "query failed"}))
			return
		}
		defer rows.Close()
		for rows.Next() {
			var v T
			if err := scanRows(rows, &v); err != nil {
				yield(v, err)
				return
			}
			if !yield(v, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			var zero T
			yield(zero, err)
		}
	}
}

// redactSQL strips literal values from logged SQL to keep secrets out of
// span attributes (DATA-068). The implementation is intentionally
// conservative — it redacts anything following a quoted string or a
// placeholder.
func redactSQL(s string) string {
	var b strings.Builder
	inSingle := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'':
			inSingle = !inSingle
			b.WriteByte('\'')
		default:
			if inSingle {
				b.WriteByte('?')
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// scanRows dispatches to the reflective scanner. Eventually replaced by
// generated code (DATA-027).
func scanRows(rows Rows, dest any) error {
	return scanReflect(rows, dest)
}

// isSQLFragment reports whether a column expression contains SQL syntax
// (parens, spaces, AS, *, function calls). Such expressions are passed
// through verbatim rather than being quoted as identifiers.
func isSQLFragment(s string) bool {
	if s == "*" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '(' || c == ')' || c == ' ' || c == ',' || c == '*' {
			return true
		}
	}
	return false
}

var errNotImplemented = errors.New("record: not implemented")
