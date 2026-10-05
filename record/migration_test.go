// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package record

import (
	"context"
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/record/types"
)

// migrationUser mirrors a developer's app/models/user.go for migration tests.
type migrationUser struct {
	BaseModel
	Email string      `ogon:"column:email,type:text,unique,email"`
	Role  string      `ogon:"column:role,enum"`
	Meta  types.JSONB `ogon:"column:meta,jsonb"`
}

func TestMigration_EmitCreateTable_SQLite(t *testing.T) {
	mm, err := Register(migrationUser{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	sqlStr, err := EmitCreateTable(DialectSQLite, mm)
	if err != nil {
		t.Fatalf("EmitCreateTable: %v", err)
	}
	if !strings.Contains(sqlStr, `CREATE TABLE "migration_user"`) {
		t.Errorf("missing CREATE TABLE: %s", sqlStr)
	}
	if !strings.Contains(sqlStr, `"email" text`) {
		t.Errorf("missing email column: %s", sqlStr)
	}
	if !strings.Contains(sqlStr, `"id" TEXT PRIMARY KEY`) {
		t.Errorf("missing PK: %s", sqlStr)
	}
	if !strings.Contains(sqlStr, `CREATE UNIQUE INDEX`) {
		t.Errorf("missing unique index for email: %s", sqlStr)
	}
}

func TestMigration_EmitCreateTable_Postgres(t *testing.T) {
	mm, _ := Register(migrationUser{})
	sqlStr, _ := EmitCreateTable(DialectPostgres, mm)
	if !strings.Contains(sqlStr, `"id" uuid PRIMARY KEY`) {
		t.Errorf("postgres PK type wrong: %s", sqlStr)
	}
	if !strings.Contains(sqlStr, `"email" text`) {
		t.Errorf("postgres email type wrong: %s", sqlStr)
	}
}

func TestMigration_Diff_NewTable(t *testing.T) {
	_, _ = Register(migrationUser{})
	m, err := Diff(DialectSQLite, &SchemaSnapshot{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(m.Steps) == 0 {
		t.Fatal("expected at least 1 step")
	}
	// First step should be CREATE TABLE
	first := m.Steps[0]
	if !strings.Contains(first.Description, "create table") {
		t.Errorf("first step = %q, want create table", first.Description)
	}
	if !strings.Contains(first.Up, "CREATE TABLE") {
		t.Errorf("Up = %s", first.Up)
	}
	if !strings.Contains(first.Down, "DROP TABLE") {
		t.Errorf("Down = %s", first.Down)
	}
}

func TestMigration_Diff_AlterAddColumn(t *testing.T) {
	mm, _ := Register(migrationUser{})
	// Live schema has the table but is missing the role column.
	live := &SchemaSnapshot{
		Tables: []TableDef{{
			Name: mm.Table,
			Columns: []ColumnDef{
				{Name: "id", Type: "TEXT"},
				{Name: "created_at", Type: "DATETIME"},
				{Name: "updated_at", Type: "DATETIME"},
				{Name: "deleted_at", Type: "DATETIME", Nullable: true},
				{Name: "email", Type: "TEXT"},
				{Name: "meta", Type: "TEXT"},
				// role missing
			},
		}},
	}
	m, err := Diff(DialectSQLite, live)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	found := false
	for _, s := range m.Steps {
		if strings.Contains(s.Description, "add column") && strings.Contains(s.Description, "role") {
			found = true
			if !strings.Contains(s.Up, "ALTER TABLE") {
				t.Errorf("ALTER Up missing: %s", s.Up)
			}
		}
	}
	if !found {
		t.Errorf("did not emit add-column role; steps=%v", m.Steps)
	}
}

func TestMigration_Diff_DropsOrphanAsDangerous(t *testing.T) {
	mm, _ := Register(migrationUser{})
	live := &SchemaSnapshot{
		Tables: []TableDef{
			{Name: "orphan_table", Columns: []ColumnDef{{Name: "id"}}},
			{Name: mm.Table, Columns: []ColumnDef{{Name: "id"}}},
		},
	}
	m, _ := Diff(DialectSQLite, live)
	hasOrphan := false
	for _, s := range m.Steps {
		if strings.Contains(s.Description, "drop orphan") {
			hasOrphan = true
			if !s.Dangerous {
				t.Error("orphan DROP not flagged Dangerous")
			}
		}
	}
	if !hasOrphan {
		t.Errorf("orphan DROP missing: %+v", m.Steps)
	}
}

func TestMigration_DetectDangerous_DropsTable(t *testing.T) {
	m := &Migration{
		Name:    "test",
		Version: 1,
		Steps: []MigrationStep{
			{Description: "drop orphan table foo", Up: "DROP TABLE foo;"},
			{Description: "add column bar", Up: "ALTER TABLE t ADD COLUMN bar TEXT;"},
		},
	}
	ops := DetectDangerous(m)
	if len(ops) != 1 {
		t.Fatalf("ops = %d, want 1", len(ops))
	}
	if ops[0].Kind != "drop_table" {
		t.Errorf("Kind = %q", ops[0].Kind)
	}
}

func TestMigration_DetectDangerous_DropColumn(t *testing.T) {
	m := &Migration{
		Steps: []MigrationStep{{
			Description: "drop column x",
			Up:          "ALTER TABLE t DROP COLUMN x;",
		}},
	}
	ops := DetectDangerous(m)
	if len(ops) != 1 || ops[0].Kind != "drop_column" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestMigration_EnsureInteractiveOrExit_Refuses(t *testing.T) {
	m := &Migration{
		Steps: []MigrationStep{{Dangerous: true, Reason: "drop"}},
	}
	if err := EnsureInteractiveOrExit(m, nil); err == nil {
		t.Error("expected error with nil confirm")
	}
	if err := EnsureInteractiveOrExit(m, NeverConfirm{}); err == nil {
		t.Error("expected error with NeverConfirm")
	}
	if err := EnsureInteractiveOrExit(m, AlwaysConfirm{}); err != nil {
		t.Errorf("AlwaysConfirm refused: %v", err)
	}
}

func TestMigration_ClassifyNarrowing(t *testing.T) {
	cases := []struct {
		from, to string
		narrow   bool
	}{
		{"text", "integer", true},
		{"int", "bigint", false},
		{"numeric", "integer", true},
		{"bool", "integer", false},
		{"text", "text", false},
	}
	for _, c := range cases {
		got := ClassifyNarrowing(c.from, c.to)
		if got != c.narrow {
			t.Errorf("narrow(%s→%s) = %v, want %v", c.from, c.to, got, c.narrow)
		}
	}
}

func TestMigration_StatusOfEmpty(t *testing.T) {
	d := newTestDriver(t)
	if err := RegisterPool("migrate-test", d); err != nil {
		t.Fatalf("RegisterPool: %v", err)
	}
	st, err := MigrationStatusOf(context.Background(), "migrate-test")
	if err != nil {
		t.Fatalf("MigrationStatusOf: %v", err)
	}
	if st.Version != 0 {
		t.Errorf("Version = %d, want 0 on empty", st.Version)
	}
}

// runMigModel is a minimal model used by TestMigration_RunMigrations_Safe.
type runMigModel struct {
	BaseModel
	Email string `ogon:"column:email"`
}

func TestMigration_RunMigrations_Safe(t *testing.T) {
	d := newTestDriver(t)
	if err := RegisterPool("mig-run", d); err != nil {
		t.Fatalf("RegisterPool: %v", err)
	}
	if _, err := Register(runMigModel{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	m, err := Diff(DialectSQLite, &SchemaSnapshot{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	st, err := RunMigrations(context.Background(), "mig-run", m, AlwaysConfirm{})
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if st.Version == 0 {
		t.Error("Version not recorded")
	}
	st2, _ := MigrationStatusOf(context.Background(), "mig-run")
	if st2.Version == 0 {
		t.Errorf("version not persisted (got %d, want %d)", st2.Version, st.Version)
	}
}

func TestMigration_RunMigrations_RefusesDangerous(t *testing.T) {
	d := newTestDriver(t)
	_ = RegisterPool("mig-danger", d)
	m := &Migration{
		Name:    "danger",
		Version: 1,
		Steps: []MigrationStep{{
			Description: "drop orphan table foo",
			Up:          "DROP TABLE foo;",
			Down:        "/* irreversible */",
			Dangerous:   true,
		}},
	}
	_, err := RunMigrations(context.Background(), "mig-danger", m, NeverConfirm{})
	if err == nil {
		t.Fatal("expected refusal error")
	}
	if !strings.Contains(err.Error(), "OGON-D0040") {
		t.Errorf("err = %v, want OGON-D0040", err)
	}
}
