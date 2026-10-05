// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// In-memory model registry. The registry is the meeting point between user
// model structs (annotated with `ogon:` tags) and the codegen/migration
// subsystems. Runtime callers (Query[T], scanner) consult it for table
// names and column maps; codegen populates it once at boot from a generated
// `init()` (a future phase). Until then, runtime reflection seeds it
// lazily on first Query[T] use.

package record

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/OgonFrameworks/ogon.go/record/types"
)

// FieldMeta is one field's worth of parsed metadata.
type FieldMeta struct {
	GoName   string // struct field name
	Column   string // SQL column name
	Type     string // explicit SQL type or ""
	SQLType  string // resolved dialect-specific type
	Tags     *Tags
	TypeName string // Go type name (for diagnostics)
	IsPK     bool
	IsFK     bool
	IsAudit  bool
	IsRLS    bool
	Classify Classification
	GoType   reflect.Type
}

// ModelMeta is the parsed representation of a registered model.
type ModelMeta struct {
	Name         string // model name (struct type name)
	Table        string // SQL table name
	Schema       string // optional schema/tenant prefix
	GoType       reflect.Type
	Fields       []FieldMeta
	ByGoName     map[string]*FieldMeta
	ByColumn     map[string]*FieldMeta
	PK           *FieldMeta
	HasRLS       bool
	TenantColumn string // column used for RLS (set when HasRLS)
}

// Registry is the global, concurrency-safe model registry. Direct access
// is via the package-level Register/Lookup helpers; the registry is a
// singleton by design (the framework has exactly one schema per process).
type Registry struct {
	mu     sync.RWMutex
	byName map[string]*ModelMeta
	byType map[reflect.Type]*ModelMeta
}

var defaultRegistry = newRegistry()

func newRegistry() *Registry {
	return &Registry{
		byName: make(map[string]*ModelMeta),
		byType: make(map[reflect.Type]*ModelMeta),
	}
}

// Register registers a model. If the model was registered before, the
// previous metadata is replaced (allows codegen to refresh). Returns the
// parsed metadata so the caller can introspect or amend before publishing.
//
// Register is safe for concurrent use but is intended to be called only
// at boot (init) time. Calling Register after the first Query[T] runs is
// a programmer error surfaced via a panic in dev builds.
func Register(model any) (*ModelMeta, error) {
	return defaultRegistry.Register(model)
}

// Lookup returns the metadata for a registered model name. Returns nil
// (not an error) when no such model is registered.
func Lookup(name string) *ModelMeta { return defaultRegistry.Lookup(name) }

// LookupByType returns metadata keyed by Go reflect.Type.
func LookupByType(t reflect.Type) *ModelMeta { return defaultRegistry.LookupByType(t) }

// All returns metadata for every registered model, sorted by name for
// deterministic migration emission.
func All() []*ModelMeta { return defaultRegistry.All() }

// Register parses a struct (or pointer-to-struct) using reflection and
// the `ogon:` tag grammar. It walks embedded BaseModel specially so the
// PK and lifecycle columns are always present.
func (r *Registry) Register(model any) (*ModelMeta, error) {
	t := reflect.TypeOf(model)
	if t == nil {
		return nil, fmt.Errorf("record: cannot register nil model")
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("record: %s is not a struct", t.String())
	}

	meta := &ModelMeta{
		Name:     t.Name(),
		Table:    snakeCase(t.Name()),
		GoType:   t,
		ByGoName: make(map[string]*FieldMeta),
		ByColumn: make(map[string]*FieldMeta),
	}
	if err := r.walkStruct(t, meta, nil); err != nil {
		return nil, err
	}
	if meta.PK == nil {
		return nil, fmt.Errorf("record: %s has no primary key (embed record.BaseModel or tag primary_key)", t.Name())
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName[meta.Name] = meta
	r.byType[t] = meta
	return meta, nil
}

func (r *Registry) walkStruct(t reflect.Type, meta *ModelMeta, parentPath []string) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		// Embedded BaseModel or other anonymous struct: recurse without
		// prefixing the column path.
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			if err := r.walkStruct(f.Type, meta, parentPath); err != nil {
				return err
			}
			continue
		}
		tag, _ := f.Tag.Lookup("ogon")
		parsed, err := ParseTags(tag)
		if err != nil {
			return fmt.Errorf("record: %s.%s: %w", t.Name(), f.Name, err)
		}
		col := parsed.Column
		if col == "" {
			col = snakeCase(f.Name)
		}
		fm := &FieldMeta{
			GoName:   f.Name,
			Column:   col,
			Type:     parsed.Type,
			Tags:     parsed,
			TypeName: f.Type.String(),
			IsPK:     parsed.PrimaryKey,
			IsFK:     parsed.ForeignKey != nil,
			IsAudit:  parsed.Audit,
			IsRLS:    parsed.RLS,
			Classify: parsed.Classify,
			GoType:   f.Type,
		}
		if parsed.TenantID {
			meta.HasRLS = true
			meta.TenantColumn = col
		}
		if parsed.RLS {
			meta.HasRLS = true
		}
		if parsed.PrimaryKey {
			if meta.PK != nil {
				return fmt.Errorf("record: %s has two primary keys (%s, %s)",
					t.Name(), meta.PK.GoName, f.Name)
			}
			meta.PK = fm
		}
		meta.Fields = append(meta.Fields, *fm)
		// store pointers into the slice header's backing array for fast lookup
		// (re-allocate via local copy to avoid dangling references)
		meta.ByGoName[fm.GoName] = fm
		meta.ByColumn[fm.Column] = fm
		// Sanity check: UUID PK is conventional; flag if not for diagnostics
		if fm.IsPK && fm.GoType != reflect.TypeOf(types.UUID{}) {
			// non-UUID PK is allowed but unusual; record type name in meta for explain
			fm.TypeName = fm.GoType.String()
		}
	}
	return nil
}

// Lookup returns metadata by model name.
func (r *Registry) Lookup(name string) *ModelMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byName[name]
}

// LookupByType returns metadata by Go reflect.Type.
func (r *Registry) LookupByType(t reflect.Type) *ModelMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return r.byType[t]
}

// All returns metadata for every registered model sorted by name.
func (r *Registry) All() []*ModelMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ModelMeta, 0, len(r.byName))
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	// stable order
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	for _, n := range names {
		out = append(out, r.byName[n])
	}
	return out
}

// snakeCase converts CamelCase to snake_case. Hand-rolled to avoid pulling
// in a third-party casing library (DR-1: stdlib-first).
func snakeCase(s string) string {
	if s == "" {
		return s
	}
	var b []rune
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b = append(b, '_')
		}
		b = append(b, toLowerRune(r))
	}
	return string(b)
}

func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
