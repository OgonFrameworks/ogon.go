// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Row-Level Security generation. Postgres-only by design — SQLite has
// no equivalent primitive and any `rls` tag on an SQLite pool is surfaced
// as a documented no-op (DATA-029). The session-var per-tx setter is the
// half that fires on every RLS query, independent of codegen.

package record

import (
	"context"
	"fmt"
	"strings"
)

// EmitRLS produces the SQL statements to enable RLS and create policies
// for one model. The model's TenantColumn (set by the `tenant_id` tag)
// becomes the per-row tenant key. Policy comparison uses the per-session
// variable `app.current_tenant`, set by SetTenantSessionVar below.
func EmitRLS(mm *ModelMeta) []string {
	if mm == nil || !mm.HasRLS {
		return nil
	}
	tenantCol := mm.TenantColumn
	if tenantCol == "" {
		tenantCol = "tenant_id"
	}
	var out []string
	out = append(out, fmt.Sprintf("ALTER TABLE %q ENABLE ROW LEVEL SECURITY;", mm.Table))
	// Force the policy even for table owners (typical multi-tenant case)
	out = append(out, fmt.Sprintf("ALTER TABLE %q FORCE ROW LEVEL SECURITY;", mm.Table))
	// SELECT policy: rows where tenant = current_setting('app.current_tenant')
	out = append(out, fmt.Sprintf(
		"CREATE POLICY %s_tenant_isolation ON %s FOR SELECT USING (%s = current_setting('app.current_tenant', true));",
		mm.Table, quoteIdent(DialectPostgres, mm.Table), quoteIdent(DialectPostgres, tenantCol),
	))
	// INSERT policy: caller's tenant must equal row's tenant
	out = append(out, fmt.Sprintf(
		"CREATE POLICY %s_tenant_insert ON %s FOR INSERT WITH CHECK (%s = current_setting('app.current_tenant', true));",
		mm.Table, quoteIdent(DialectPostgres, mm.Table), quoteIdent(DialectPostgres, tenantCol),
	))
	// UPDATE policy: same tenant — caller can update only their rows
	out = append(out, fmt.Sprintf(
		"CREATE POLICY %s_tenant_update ON %s FOR UPDATE USING (%s = current_setting('app.current_tenant', true));",
		mm.Table, quoteIdent(DialectPostgres, mm.Table), quoteIdent(DialectPostgres, tenantCol),
	))
	// DELETE policy
	out = append(out, fmt.Sprintf(
		"CREATE POLICY %s_tenant_delete ON %s FOR DELETE USING (%s = current_setting('app.current_tenant', true));",
		mm.Table, quoteIdent(DialectPostgres, mm.Table), quoteIdent(DialectPostgres, tenantCol),
	))
	return out
}

// SetTenantSessionVar sets the per-tx Postgres session variable used by
// RLS policies. Must be called inside the transaction whose queries are
// to be scoped. On SQLite the call is a no-op (RLS is pg-only DATA-029).
func SetTenantSessionVar(ctx context.Context, tx *Tx, tenantID string) error {
	if tx == nil {
		return fmt.Errorf("record: SetTenantSessionVar(nil tx)")
	}
	if tx.Dialect() != DialectPostgres {
		return nil // documented no-op on SQLite
	}
	// Use the safe cast form so a missing session var doesn't raise.
	sqlStr := fmt.Sprintf("SELECT set_config('app.current_tenant', $1, true);")
	_, err := tx.Exec(ctx, sqlStr, tenantID)
	return err
}

// PolicySummary renders a human-readable summary for `ogon explain model`.
func PolicySummary(mm *ModelMeta) string {
	if mm == nil || !mm.HasRLS {
		return ""
	}
	stmts := EmitRLS(mm)
	var b strings.Builder
	b.WriteString("RLS policies on table ")
	b.WriteString(mm.Table)
	b.WriteString(":\n")
	for _, s := range stmts {
		b.WriteString("  ")
		b.WriteString(s)
		b.WriteString("\n")
	}
	return b.String()
}
