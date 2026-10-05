// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Audit columns convention and audit-trail table generation. The audit
// convention adds four columns to any model tagged `audit`: created_by,
// updated_by, deleted_by, plus a global append-only `ogon_audit_trail`
// table that records every change to audited rows.

package record

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AuditColumns is the canonical set the migration engine adds to models
// tagged `audit`.
var AuditColumns = []struct {
	Name string
	SQL  string
}{
	{"created_by", "TEXT"},
	{"updated_by", "TEXT"},
	{"deleted_by", "TEXT"},
}

// EmitAuditTrailTable produces the SQL to create the global audit-trail
// table. The table is append-only — UPDATE/DELETE on it is forbidden
// by revoke permissions issued separately.
func EmitAuditTrailTable(d Dialect) string {
	var b strings.Builder
	if d == DialectPostgres {
		b.WriteString(`CREATE TABLE IF NOT EXISTS ogon_audit_trail (
  id BIGSERIAL PRIMARY KEY,
  table_name TEXT NOT NULL,
  row_id TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('INSERT','UPDATE','DELETE')),
  actor TEXT,
  payload JSONB,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ogon_audit_table_row ON ogon_audit_trail(table_name, row_id);
CREATE INDEX IF NOT EXISTS idx_ogon_audit_occurred ON ogon_audit_trail(occurred_at);
`)
	} else {
		b.WriteString(`CREATE TABLE IF NOT EXISTS ogon_audit_trail (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  table_name TEXT NOT NULL,
  row_id TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('INSERT','UPDATE','DELETE')),
  actor TEXT,
  payload TEXT,
  occurred_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ogon_audit_table_row ON ogon_audit_trail(table_name, row_id);
CREATE INDEX IF NOT EXISTS idx_ogon_audit_occurred ON ogon_audit_trail(occurred_at);
`)
	}
	return b.String()
}

// AppendAuditTrail inserts one record into ogon_audit_trail. Called from
// the audit trigger or the app-layer audit middleware.
func AppendAuditTrail(ctx context.Context, d Driver, table, rowID, action, actor string, payload []byte) error {
	if d == nil {
		return fmt.Errorf("record: AppendAuditTrail nil driver")
	}
	switch d.Dialect() {
	case DialectPostgres:
		_, err := d.Exec(ctx, `INSERT INTO ogon_audit_trail(table_name, row_id, action, actor, payload, occurred_at) VALUES ($1,$2,$3,$4,$5::jsonb,$6);`,
			table, rowID, action, actor, payload, time.Now().UTC())
		return err
	default:
		_, err := d.Exec(ctx, `INSERT INTO ogon_audit_trail(table_name, row_id, action, actor, payload, occurred_at) VALUES (?,?,?,?,?,?);`,
			table, rowID, action, actor, string(payload), time.Now().UTC().Format(time.RFC3339))
		return err
	}
}

// AuditColumnsFor returns the audit columns to add to a model table. The
// caller (migration engine) appends these when emitting CREATE TABLE.
func AuditColumnsFor(d Dialect) []string {
	out := make([]string, 0, len(AuditColumns))
	for _, c := range AuditColumns {
		out = append(out, fmt.Sprintf(`"%s" %s`, c.Name, c.SQL))
	}
	return out
}
