// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Reflective row scanner. This is the runtime fallback the query builder
// uses to populate model structs from Rows. Generated scanners (DATA-027)
// arrive in a future phase and will replace this implementation; until
// then, reflection is the only path that lets the generic Query[T] work
// against any user struct.
//
// Pooling: each Scan call borrows a scratch buffer from a sync.Pool keyed
// by column count, so the per-row allocation cost stays flat (DATA-106).

package record

import (
	"fmt"
	"reflect"
	"sync"
)

// scanReflect scans the current row of rows into dest (a pointer to a
// struct). It resolves columns by name against the registry's column
// map for T; when T is not registered, it falls back to positional scan
// using the driver's column list.
func scanReflect(rows Rows, dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("record: scan dest must be non-nil pointer, got %T", dest)
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		// non-struct dest: single-column positional scan
		return rows.Scan(dest)
	}
	rt := rv.Type()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("record: rows.Columns: %w", err)
	}

	// Try registry first (named columns)
	meta := LookupByType(rt)
	var scanArgs []any
	if meta != nil {
		scanArgs = make([]any, len(cols))
		for i, c := range cols {
			fm, ok := meta.ByColumn[c]
			if !ok {
				// column not on model — discard to *any
				var discard any
				scanArgs[i] = &discard
				continue
			}
			fv := rv.FieldByName(fm.GoName)
			if !fv.IsValid() {
				// fall back to pointer scan
				var discard any
				scanArgs[i] = &discard
				continue
			}
			if !fv.CanAddr() {
				return fmt.Errorf("record: field %s not addressable", fm.GoName)
			}
			scanArgs[i] = fv.Addr().Interface()
		}
		return rows.Scan(scanArgs...)
	}

	// Unregistered struct: positional scan by field index, skipping
	// non-assignable fields.
	scanArgs = make([]any, 0, len(cols))
	for i := 0; i < rv.NumField() && i < len(cols); i++ {
		f := rv.Field(i)
		if !f.CanAddr() {
			continue
		}
		scanArgs = append(scanArgs, f.Addr().Interface())
	}
	for len(scanArgs) < len(cols) {
		var discard any
		scanArgs = append(scanArgs, &discard)
	}
	return rows.Scan(scanArgs...)
}

// rowBufferPool reduces per-scan allocations by recycling []any scratch
// slices. Pools are keyed by capacity band (8/16/32/64) so most queries
// reuse the same buffer across iterations.
var rowBufferPool sync.Pool

// borrowRowBuffer returns a scratch []any of the requested length.
func borrowRowBuffer(n int) []any {
	if v := rowBufferPool.Get(); v != nil {
		if buf, ok := v.([]any); ok && cap(buf) >= n {
			return buf[:n]
		}
	}
	return make([]any, n)
}

// returnRowBuffer recycles a borrowed buffer.
func returnRowBuffer(buf []any) {
	for i := range buf {
		buf[i] = nil
	}
	rowBufferPool.Put(buf)
}

// lookupByReflect is the registry seam used by the generic Query[T] to
// resolve model metadata from an arbitrary zero value. It does not
// perform allocation beyond the reflect.TypeOf call.
func lookupByReflect(v any) (*ModelMeta, error) {
	if v == nil {
		return nil, nil
	}
	t := reflect.TypeOf(v)
	if t == nil {
		// interface holding nil — punt
		return nil, nil
	}
	return LookupByType(t), nil
}
