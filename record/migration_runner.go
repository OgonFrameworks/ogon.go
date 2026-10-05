// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Migration runner — applies migrations with per-DB advisory locking, a
// version table, and a status report. Server processes never auto-migrate
// (DATA-009): only the CLI invokes the runner, and only after explicit
// consent for any Dangerous step.

package record

import (
	"context"
	"fmt"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// MigrationStatus is the runner's report — used by `ogon migrate status`.
type MigrationStatus struct {
	Pool      string
	Dialect   Dialect
	Version   int64
	AppliedAt time.Time
	Steps     []MigrationStep
	Pending   int
	Dangerous int
	Locked    bool
}

// RunMigrations executes the supplied migration against the named pool.
// It takes a per-DB advisory lock, ensures the version table exists,
// applies Up for each step in order, and records the version.
//
// Behaviour invariants:
//   - In a non-interactive session with any Dangerous step, the runner
//     exits with diagnostic OGON-D0040 instead of prompting (CLI-068).
//   - Rollback (down) direction is via RunMigrationRollback below.
//   - Advisory locks are released on return (committed or rolled back).
func RunMigrations(ctx context.Context, poolName string, m *Migration, confirm DangerousConfirmation) (*MigrationStatus, error) {
	if poolName == "" {
		poolName = "default"
	}
	d := PoolOf(ctx, poolName)
	if d == nil {
		return nil, diag.New("OGON-D0001", "no such pool", "pool "+poolName)
	}
	st := &MigrationStatus{Pool: poolName, Dialect: d.Dialect(), Steps: m.Steps}
	for _, s := range m.Steps {
		if s.Dangerous {
			st.Dangerous++
		}
	}
	// Dangerous-op gate — happens BEFORE the advisory lock to avoid
	// stalling concurrent operators on a session that's about to refuse.
	if st.Dangerous > 0 {
		if confirm == nil || !confirm.Allow(st) {
			return nil, diag.New("OGON-D0040", "migration refused",
				"one or more steps are destructive; pass --yes / interactive confirm to proceed")
		}
	}
	if err := ensureVersionTable(ctx, d); err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0041", Title: "version table ensure failed"})
	}
	if err := acquireAdvisoryLock(ctx, d); err != nil {
		st.Locked = false
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0042", Title: "advisory lock failed"})
	}
	st.Locked = true
	defer releaseAdvisoryLock(ctx, d)

	for _, step := range m.Steps {
		if step.Up == "" {
			continue
		}
		if _, err := d.Exec(ctx, step.Up); err != nil {
			return nil, diag.Wrap(err, diag.Diag{
				Code:  "OGON-D0043",
				Title: "migration step failed: " + step.Description,
				What:  redactSQL(step.Up),
			})
		}
	}
	st.Version = m.Version
	st.AppliedAt = time.Now().UTC()
	if _, err := d.Exec(ctx, "INSERT INTO ogon_schema_migrations(version, applied_at) VALUES (?, ?);", m.Version, st.AppliedAt.Format(time.RFC3339)); err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0044", Title: "record migration version failed"})
	}
	return st, nil
}

// RunMigrationRollback applies the Down SQL of the supplied migration in
// reverse step order. Dangerous steps require the same confirmation path.
func RunMigrationRollback(ctx context.Context, poolName string, m *Migration, confirm DangerousConfirmation) (*MigrationStatus, error) {
	if poolName == "" {
		poolName = "default"
	}
	d := PoolOf(ctx, poolName)
	if d == nil {
		return nil, diag.New("OGON-D0001", "no such pool", "pool "+poolName)
	}
	st := &MigrationStatus{Pool: poolName, Dialect: d.Dialect()}
	for _, s := range m.Steps {
		if s.Dangerous {
			st.Dangerous++
		}
	}
	if st.Dangerous > 0 {
		if confirm == nil || !confirm.Allow(st) {
			return nil, diag.New("OGON-D0040", "rollback refused",
				"one or more steps are destructive")
		}
	}
	if err := acquireAdvisoryLock(ctx, d); err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0042", Title: "advisory lock failed"})
	}
	st.Locked = true
	defer releaseAdvisoryLock(ctx, d)

	// apply Down in reverse
	for i := len(m.Steps) - 1; i >= 0; i-- {
		step := m.Steps[i]
		if step.Down == "" || step.Down[0] == '-' { // comment-only
			continue
		}
		if _, err := d.Exec(ctx, step.Down); err != nil {
			return nil, diag.Wrap(err, diag.Diag{
				Code:  "OGON-D0045",
				Title: "rollback step failed: " + step.Description,
				What:  redactSQL(step.Down),
			})
		}
	}
	if _, err := d.Exec(ctx, "DELETE FROM ogon_schema_migrations WHERE version = ?;", m.Version); err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-D0046", Title: "delete migration version failed"})
	}
	return st, nil
}

// MigrationStatusOf reports the currently-applied version and pending steps.
func MigrationStatusOf(ctx context.Context, poolName string) (*MigrationStatus, error) {
	if poolName == "" {
		poolName = "default"
	}
	d := PoolOf(ctx, poolName)
	if d == nil {
		return nil, diag.New("OGON-D0001", "no such pool", "pool "+poolName)
	}
	st := &MigrationStatus{Pool: poolName, Dialect: d.Dialect()}
	// try version table read — if it doesn't exist, surface 0
	row := d.QueryRow(ctx, "SELECT version, applied_at FROM ogon_schema_migrations ORDER BY version DESC LIMIT 1;")
	var v int64
	var ts string
	if err := row.Scan(&v, &ts); err != nil {
		st.Version = 0
	} else {
		st.Version = v
		if parsed, perr := time.Parse(time.RFC3339, ts); perr == nil {
			st.AppliedAt = parsed
		}
	}
	return st, nil
}

// DangerousConfirmation is the consent callback the runner invokes when it
// encounters a Dangerous step. In interactive mode, the CLI surfaces the
// list and asks y/N; in non-interactive mode the caller never wires this
// and the runner exits with OGON-D0040 (CLI-068).
type DangerousConfirmation interface {
	Allow(status *MigrationStatus) bool
}

// AlwaysConfirm is the dev convenience: always allows Dangerous steps.
// Never use in production.
type AlwaysConfirm struct{}

func (AlwaysConfirm) Allow(_ *MigrationStatus) bool { return true }

// NeverConfirm is the safe default: always refuses Dangerous steps.
type NeverConfirm struct{}

func (NeverConfirm) Allow(_ *MigrationStatus) bool { return false }

// ensureVersionTable creates the version-tracking table idempotently.
// On SQLite uses TEXT-typed applied_at; on Postgres uses timestamptz.
func ensureVersionTable(ctx context.Context, d Driver) error {
	switch d.Dialect() {
	case DialectPostgres:
		_, err := d.Exec(ctx, `CREATE TABLE IF NOT EXISTS ogon_schema_migrations(
    version BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
		return err
	default:
		_, err := d.Exec(ctx, `CREATE TABLE IF NOT EXISTS ogon_schema_migrations(
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);`)
		return err
	}
}

// acquireAdvisoryLock takes a per-DB advisory lock. For Postgres this is
// pg_advisory_lock(0x6f676f6e) — the ASCII 'ogon' as a 32-bit key. For
// SQLite there is no advisory lock primitive; we approximate with a
// per-DB mutex table row (best-effort).
func acquireAdvisoryLock(ctx context.Context, d Driver) error {
	switch d.Dialect() {
	case DialectPostgres:
		_, err := d.Exec(ctx, "SELECT pg_advisory_lock(1869377901);") // 'ogon' hex
		return err
	default:
		_, err := d.Exec(ctx, `CREATE TABLE IF NOT EXISTS ogon_advisory_lock(id INTEGER PRIMARY KEY);`)
		if err != nil {
			return err
		}
		_, err = d.Exec(ctx, `INSERT OR IGNORE INTO ogon_advisory_lock(id) VALUES (1);`)
		return err
	}
}

func releaseAdvisoryLock(ctx context.Context, d Driver) error {
	switch d.Dialect() {
	case DialectPostgres:
		_, err := d.Exec(ctx, "SELECT pg_advisory_unlock(1869377901);")
		return err
	default:
		_, err := d.Exec(ctx, `DELETE FROM ogon_advisory_lock WHERE id = 1;`)
		return err
	}
}

// Reason: the ogon_advisory_lock key constant is 'ogon' interpreted as
// little-endian int32: 'o'=0x6f, 'g'=0x67, 'o'=0x6f, 'n'=0x6e → 0x6e6f676f
// = 1869377901. Using a stable key means concurrent processes share the
// lock across versions.
var _ = fmt.Sprintf
