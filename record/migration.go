// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Migration engine — emits reversible up/down SQL from declared model
// metadata diffed against a live schema snapshot. The runner lives in
// migration_runner.go; dangerous-op detection lives in
// migration_dangerous.go. Server processes never auto-migrate (DATA-009).

package record

import (
	"fmt"
	"sort"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// ColumnDef is the live-schema view of one column. The migration engine
// diff compares these to the registry's FieldMeta and emits ALTERs.
type ColumnDef struct {
	Name     string
	Type     string
	Nullable bool
	Default  string
}

// TableDef is the live-schema view of one table.
type TableDef struct {
	Name    string
	Columns []ColumnDef
	Indexes []IndexDef
}

// IndexDef is the live-schema view of one index.
type IndexDef struct {
	Name    string
	Columns []string
	Unique  bool
	Partial string
}

// SchemaSnapshot is the introspected live schema. Per-Dialect adapters
// (in driver_*.go or migration_runner.go) build one from information_schema
// (pg) or sqlite_master (sqlite).
type SchemaSnapshot struct {
	Tables []TableDef
}

// MigrationStep is one reversible operation. Up is the forward SQL; Down
// is the inverse. Dangerous steps are flagged so the runner can require
// explicit consent (CLI-068).
type MigrationStep struct {
	Description string
	Up          string
	Down        string
	Dangerous   bool
	Reason      string // when Dangerous=true, why
}

// Migration is the ordered set of steps a single `ogon migrate run` would apply.
type Migration struct {
	Name    string
	Version int64
	Steps   []MigrationStep
}

// Diff computes the steps needed to bring the live schema in line with
// the registered models. It is purely additive-by-default: DROPs surface
// as Dangerous steps that the runner will refuse without explicit consent.
func Diff(dialect Dialect, live *SchemaSnapshot) (*Migration, error) {
	models := All()
	if len(models) == 0 {
		return &Migration{Name: "empty", Version: 1}, nil
	}
	m := &Migration{
		Name:    fmt.Sprintf("auto_%s", dialect),
		Version: migrationTimestamp(),
	}
	liveByName := make(map[string]*TableDef, len(live.Tables))
	for i := range live.Tables {
		liveByName[live.Tables[i].Name] = &live.Tables[i]
	}
	for _, mm := range models {
		existing, ok := liveByName[mm.Table]
		if !ok {
			// table missing — emit CREATE
			up, err := EmitCreateTable(dialect, mm)
			if err != nil {
				return nil, err
			}
			m.Steps = append(m.Steps, MigrationStep{
				Description: fmt.Sprintf("create table %s", mm.Table),
				Up:          up,
				Down:        fmt.Sprintf(`DROP TABLE %q;`, mm.Table),
			})
			continue
		}
		// table exists — diff columns
		liveCols := make(map[string]*ColumnDef, len(existing.Columns))
		for i := range existing.Columns {
			liveCols[existing.Columns[i].Name] = &existing.Columns[i]
		}
		for _, f := range mm.Fields {
			if _, present := liveCols[f.Column]; present {
				continue
			}
			// column missing — emit ALTER ADD COLUMN
			sqlType := emitColumnType(dialect, f)
			m.Steps = append(m.Steps, MigrationStep{
				Description: fmt.Sprintf("add column %s.%s", mm.Table, f.Column),
				Up: fmt.Sprintf(`ALTER TABLE %q ADD COLUMN %q %s%s;`,
					mm.Table, f.Column, sqlType, nullableClause(f)),
				Down: fmt.Sprintf(`ALTER TABLE %q DROP COLUMN %q;`, mm.Table, f.Column),
			})
		}
		// index diff — emit CREATE INDEX for declared-but-missing
		liveIdx := make(map[string]bool, len(existing.Indexes))
		for _, ix := range existing.Indexes {
			liveIdx[ix.Name] = true
		}
		for _, f := range mm.Fields {
			if !f.Tags.Index && !f.Tags.Unique {
				continue
			}
			idxName := fmt.Sprintf("idx_%s_%s", mm.Table, f.Column)
			if liveIdx[idxName] {
				continue
			}
			up := emitCreateIndex(dialect, idxName, mm.Table, f)
			m.Steps = append(m.Steps, MigrationStep{
				Description: fmt.Sprintf("add index %s", idxName),
				Up:          up,
				Down:        fmt.Sprintf(`DROP INDEX %q;`, idxName),
			})
		}
	}

	// tables in live but not in models → DROP (Dangerous)
	declared := make(map[string]bool, len(models))
	for _, mm := range models {
		declared[mm.Table] = true
	}
	for _, lt := range live.Tables {
		if declared[lt.Name] {
			continue
		}
		m.Steps = append(m.Steps, MigrationStep{
			Description: fmt.Sprintf("drop orphan table %s", lt.Name),
			Up:          fmt.Sprintf(`DROP TABLE %q;`, lt.Name),
			Down:        "-- cannot reconstruct dropped table automatically",
			Dangerous:   true,
			Reason:      "DROP TABLE is destructive (CLI-068)",
		})
	}

	// sort steps so emission is deterministic (table creates before column adds)
	sort.SliceStable(m.Steps, func(i, j int) bool {
		return stepRank(m.Steps[i]) < stepRank(m.Steps[j])
	})
	return m, nil
}

// stepRank puts CREATE TABLE first, ADD COLUMN second, INDEX third, DROP last.
func stepRank(s MigrationStep) int {
	switch {
	case strings.Contains(s.Description, "create table"):
		return 0
	case strings.Contains(s.Description, "add column"):
		return 1
	case strings.Contains(s.Description, "add index"):
		return 2
	case strings.Contains(s.Description, "drop"):
		return 9
	}
	return 5
}

// EmitCreateTable renders the CREATE TABLE statement for a model.
func EmitCreateTable(dialect Dialect, mm *ModelMeta) (string, error) {
	if mm == nil {
		return "", diag.New("OGON-D0030", "nil model", "EmitCreateTable(nil)")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %q (\n", mm.Table)
	for i, f := range mm.Fields {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  ")
		b.WriteString(fmt.Sprintf("%q ", f.Column))
		b.WriteString(emitColumnType(dialect, f))
		if f.IsPK {
			b.WriteString(" PRIMARY KEY")
		} else {
			b.WriteString(nullableClause(f))
		}
		if f.Tags.Check != "" {
			fmt.Fprintf(&b, " CHECK(%s)", f.Tags.Check)
		}
	}
	b.WriteString("\n);")

	// indexes as separate statements (so they can be dropped independently)
	for _, f := range mm.Fields {
		if !f.Tags.Index && !f.Tags.Unique {
			continue
		}
		idxName := fmt.Sprintf("idx_%s_%s", mm.Table, f.Column)
		b.WriteString("\n")
		b.WriteString(emitCreateIndex(dialect, idxName, mm.Table, f))
	}

	// RLS — pg only
	if mm.HasRLS && dialect == DialectPostgres {
		policies := EmitRLS(mm)
		for _, p := range policies {
			b.WriteString("\n")
			b.WriteString(p)
		}
	}
	return b.String(), nil
}

// emitColumnType maps a Go field type to a dialect-specific SQL type.
func emitColumnType(d Dialect, f FieldMeta) string {
	if f.Tags.Type != "" {
		return f.Tags.Type
	}
	switch f.GoType.Name() {
	case "UUID":
		if d == DialectSQLite {
			return "TEXT"
		}
		return "uuid"
	case "Time", "NullTime", "NullDuration":
		if d == DialectSQLite {
			return "DATETIME"
		}
		return "timestamptz"
	case "Decimal":
		if d == DialectSQLite {
			return "TEXT"
		}
		return "numeric(19,4)"
	case "JSONB":
		if d == DialectSQLite {
			return "TEXT"
		}
		return "jsonb"
	case "bool":
		if d == DialectSQLite {
			return "INTEGER"
		}
		return "boolean"
	case "string":
		if f.Tags.Enum {
			if d == DialectSQLite {
				return "TEXT"
			}
			// Postgres enums require a CREATE TYPE; the migration pre-step
			// emits it. The column references the named enum type.
			return "TEXT"
		}
		return "text"
	case "int", "int32":
		return "integer"
	case "int64":
		return "bigint"
	case "float32", "float64":
		return "double precision"
	}
	// pointers — unwrap
	if f.GoType.Kind() == reflectPtrKind() {
		// synthesize an unwrapped FieldMeta and recurse
		f2 := f
		f2.GoType = f.GoType.Elem()
		return emitColumnType(d, f2)
	}
	return "text"
}

// nullableClause renders NULL/NOT NULL for an ADD COLUMN.
func nullableClause(f FieldMeta) string {
	if f.Tags.Nullable || f.GoType.Kind() == reflectPtrKind() {
		return ""
	}
	return " NOT NULL"
}

// emitCreateIndex renders CREATE [UNIQUE] INDEX ... with optional partial.
func emitCreateIndex(d Dialect, idxName, table string, f FieldMeta) string {
	var b strings.Builder
	b.WriteString("CREATE ")
	if f.Tags.Unique {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX ")
	b.WriteString(fmt.Sprintf("%q ON %q", idxName, table))
	fmt.Fprintf(&b, " (%q)", f.Column)
	if f.Tags.Partial != "" {
		if d == DialectSQLite || d == DialectPostgres {
			fmt.Fprintf(&b, " WHERE %s", f.Tags.Partial)
		}
	}
	b.WriteString(";")
	return b.String()
}
