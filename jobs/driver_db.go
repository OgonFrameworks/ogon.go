// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — DB-backed driver (JOBS-002).
//
// Default for production: Postgres `FOR UPDATE SKIP LOCKED` for
// contention-free dequeue. SQLite (used in tests and dev) supports
// UPDATE ... RETURNING via modernc.org/sqlite, so the same code path
// works against both dialects. The driver picks the right SQL via
// record.Driver.Dialect().
//
// Schema (created by migrations.go Run):
//
//      CREATE TABLE ogon_jobs_queue (
//          id           TEXT PRIMARY KEY,
//          name         TEXT NOT NULL,
//          payload      BLOB NOT NULL,
//          attempts     INTEGER NOT NULL DEFAULT 0,
//          max_attempts INTEGER NOT NULL DEFAULT 0,
//          priority     INTEGER NOT NULL DEFAULT 0,
//          idem_key     TEXT,
//          tenant_id    TEXT,
//          enqueued_at  INTEGER NOT NULL,
//          visible_at   INTEGER NOT NULL,
//          last_error   TEXT,
//          stack        TEXT
//      );
//      CREATE INDEX ogon_jobs_queue_visible_idx
//          ON ogon_jobs_queue(visible_at, priority DESC);
//      CREATE UNIQUE INDEX ogon_jobs_queue_idem_uniq
//          ON ogon_jobs_queue(idem_key) WHERE idem_key IS NOT NULL;

package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/OgonFrameworks/ogon.go/record"
)

// DBDriver is the database-backed queue driver. It works against any
// registered record.Driver (Postgres or SQLite). One DBDriver per
// pool name; the queue table is created on first Run (idempotent).
type DBDriver struct {
	db   record.Driver
	pool string
	vis  VisibilityOptions
	enis *Enforcer
}

// NewDBDriver constructs a DB-backed queue using the named pool
// (default pool when empty). Visibility options default when zero.
func NewDBDriver(poolName string, vis VisibilityOptions, enf *Enforcer) *DBDriver {
	vis = vis.WithDefaults()
	if enf == nil {
		enf = NewEnforcer(5 * time.Minute)
	}
	if poolName == "" {
		poolName = "default"
	}
	return &DBDriver{pool: poolName, vis: vis, enis: enf}
}

// EnsureSchema creates the queue table if missing. Idempotent.
// Uses a single DDL that works in both Postgres and SQLite.
func (d *DBDriver) EnsureSchema(ctx context.Context) error {
	drv := record.PoolOf(ctx, d.pool)
	if drv == nil {
		return diag.New("OGON-J0020", "jobs: pool not registered", "pool "+d.pool)
	}
	d.db = drv
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ogon_jobs_queue (
                        id           TEXT PRIMARY KEY,
                        name         TEXT NOT NULL,
                        payload      BLOB NOT NULL,
                        attempts     INTEGER NOT NULL DEFAULT 0,
                        max_attempts INTEGER NOT NULL DEFAULT 0,
                        priority     INTEGER NOT NULL DEFAULT 0,
                        idem_key     TEXT,
                        tenant_id    TEXT,
                        enqueued_at  INTEGER NOT NULL,
                        visible_at   INTEGER NOT NULL,
                        last_error   TEXT,
                        stack        TEXT
                )`,
		`CREATE INDEX IF NOT EXISTS ogon_jobs_queue_visible_idx
                        ON ogon_jobs_queue(visible_at, priority DESC)`,
		`CREATE TABLE IF NOT EXISTS ogon_jobs_dlq (
                        id              TEXT PRIMARY KEY,
                        name            TEXT NOT NULL,
                        payload         BLOB NOT NULL,
                        attempts        INTEGER NOT NULL,
                        max_attempts    INTEGER NOT NULL,
                        priority        INTEGER NOT NULL,
                        idem_key        TEXT,
                        tenant_id       TEXT,
                        enqueued_at     INTEGER NOT NULL,
                        visible_at      INTEGER NOT NULL,
                        last_error      TEXT,
                        stack          TEXT,
                        quarantined_at  INTEGER NOT NULL,
                        reason          TEXT
                )`,
	}
	for _, s := range stmts {
		if _, err := drv.Exec(ctx, s); err != nil {
			return diag.Wrap(err, diag.Diag{Code: "OGON-J0021", Title: "jobs: ensure schema", What: err.Error()})
		}
	}
	// unique idempotency index — sqlite syntax differs from postgres
	// for partial unique (WHERE idem_key IS NOT NULL). Both engines
	// accept the syntax below in modern form.
	if _, err := drv.Exec(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS ogon_jobs_queue_idem_uniq
                        ON ogon_jobs_queue(idem_key) WHERE idem_key IS NOT NULL`); err != nil {
		// fall back to non-partial unique index for engines without
		// partial-index support
		if _, err2 := drv.Exec(ctx,
			`CREATE UNIQUE INDEX IF NOT EXISTS ogon_jobs_queue_idem_uniq
                                ON ogon_jobs_queue(idem_key)`); err2 != nil {
			return diag.Wrap(err2, diag.Diag{Code: "OGON-J0022", Title: "jobs: create idem idx", What: err2.Error()})
		}
	}
	return nil
}

// Enqueue implements Queue.
func (d *DBDriver) Enqueue(ctx context.Context, env *Envelope, opts EnqueueOptions) error {
	if d.db == nil {
		if err := d.EnsureSchema(ctx); err != nil {
			return err
		}
	}
	if env.ID == "" {
		env.ID = NewID()
	}
	now := time.Now().UTC()
	if env.EnqueuedAt.IsZero() {
		env.EnqueuedAt = now
	}
	if !opts.VisibleAt.IsZero() {
		env.VisibleAt = opts.VisibleAt
	} else if env.VisibleAt.IsZero() {
		env.VisibleAt = now
	}
	if opts.MaxAttempts > 0 {
		env.MaxAttempts = opts.MaxAttempts
	}
	if opts.TenantID != "" {
		env.TenantID = opts.TenantID
	}
	env.Priority = opts.Priority
	if opts.IdempotencyKey != "" {
		env.IdempotencyKey = opts.IdempotencyKey
		if !d.enis.Claim(opts.IdempotencyKey) {
			return diag.New("OGON-J0014", "jobs: idempotency collision", "key in flight")
		}
	}
	_, err := d.db.Exec(ctx,
		`INSERT INTO ogon_jobs_queue
                 (id,name,payload,attempts,max_attempts,priority,idem_key,tenant_id,enqueued_at,visible_at,last_error,stack)
                 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		env.ID, string(env.Name), []byte(env.Payload),
		0, env.MaxAttempts, env.Priority,
		nullableString(env.IdempotencyKey), nullableString(env.TenantID),
		env.EnqueuedAt.UnixMilli(), env.VisibleAt.UnixMilli(),
		"", "",
	)
	if err != nil {
		d.enis.Release(opts.IdempotencyKey)
		if isUniqueViolation(err) {
			return diag.New("OGON-J0014", "jobs: idempotency collision", err.Error())
		}
		return diag.Wrap(err, diag.Diag{Code: "OGON-J0023", Title: "jobs: enqueue failed", What: err.Error()})
	}
	return nil
}

// Dequeue implements Queue. Uses FOR UPDATE SKIP LOCKED on Postgres,
// and UPDATE-RETURNING on SQLite (single-writer so SKIP LOCKED is moot).
func (d *DBDriver) Dequeue(ctx context.Context) (*Envelope, Receipt, error) {
	if d.db == nil {
		if err := d.EnsureSchema(ctx); err != nil {
			return nil, nil, err
		}
	}
	now := time.Now().UnixMilli()
	switch d.db.Dialect() {
	case record.DialectPostgres:
		return d.dequeuePostgres(ctx, now)
	default:
		return d.dequeueGeneric(ctx, now)
	}
}

func (d *DBDriver) dequeuePostgres(ctx context.Context, nowMs int64) (*Envelope, Receipt, error) {
	tx, err := d.db.BeginTx(ctx, record.TxOptions{Isolation: record.LevelReadCommitted})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx,
		`SELECT id,name,payload,attempts,max_attempts,priority,idem_key,tenant_id,enqueued_at,visible_at,last_error,stack
                 FROM ogon_jobs_queue
                 WHERE visible_at <= $1
                 ORDER BY priority DESC, visible_at ASC
                 FOR UPDATE SKIP LOCKED
                 LIMIT 1`, nowMs)
	env, err := scanEnvelope(row)
	if err != nil {
		return nil, nil, ignoreNoRows(err)
	}
	lease := d.vis.TimeoutFor(env.Name)
	next := time.Now().Add(lease).UnixMilli()
	env.Attempts++
	if _, err := tx.Exec(ctx,
		`UPDATE ogon_jobs_queue SET attempts=$1, visible_at=$2 WHERE id=$3`,
		env.Attempts, next, env.ID); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	rcpt := ReceiptMeta{ID: env.ID}
	return env, rcpt, nil
}

func (d *DBDriver) dequeueGeneric(ctx context.Context, nowMs int64) (*Envelope, Receipt, error) {
	row := d.db.QueryRow(ctx,
		`UPDATE ogon_jobs_queue
                 SET attempts = attempts + 1,
                     visible_at = ?
                 WHERE id = (
                     SELECT id FROM ogon_jobs_queue
                     WHERE visible_at <= ?
                     ORDER BY priority DESC, visible_at ASC
                     LIMIT 1)
                 RETURNING id,name,payload,attempts,max_attempts,priority,idem_key,tenant_id,enqueued_at,visible_at,last_error,stack`,
		nowMs+int64(d.vis.Default.Milliseconds()), nowMs)
	env, err := scanEnvelope(row)
	if err != nil {
		return nil, nil, ignoreNoRows(err)
	}
	rcpt := ReceiptMeta{ID: env.ID}
	return env, rcpt, nil
}

// Ack implements Queue — deletes the row.
func (d *DBDriver) Ack(ctx context.Context, r Receipt) error {
	_, err := d.db.Exec(ctx, `DELETE FROM ogon_jobs_queue WHERE id = ?`, r.EnvelopeID())
	if err != nil {
		return diag.Wrap(err, diag.Diag{Code: "OGON-J0024", Title: "jobs: ack failed"})
	}
	return nil
}

// Nack implements Queue. If requeue=true: bumps last_error, sets
// visible_at to nextVisibleAt (already in the future for retries).
// If requeue=false: moves the row to ogon_jobs_dlq and deletes it
// from the queue.
func (d *DBDriver) Nack(ctx context.Context, r Receipt, requeue bool, nextVisibleAt time.Time, lastErr string) error {
	if requeue {
		_, err := d.db.Exec(ctx,
			`UPDATE ogon_jobs_queue SET visible_at = ?, last_error = ? WHERE id = ?`,
			nextVisibleAt.UnixMilli(), lastErr, r.EnvelopeID())
		if err != nil {
			return diag.Wrap(err, diag.Diag{Code: "OGON-J0025", Title: "jobs: nack failed"})
		}
		return nil
	}
	// move to DLQ
	row := d.db.QueryRow(ctx,
		`SELECT id,name,payload,attempts,max_attempts,priority,idem_key,tenant_id,enqueued_at,visible_at,last_error,stack
                 FROM ogon_jobs_queue WHERE id = ?`, r.EnvelopeID())
	env, err := scanEnvelope(row)
	if err != nil {
		return ignoreNoRows(err)
	}
	if _, err := d.db.Exec(ctx,
		`INSERT INTO ogon_jobs_dlq
                 (id,name,payload,attempts,max_attempts,priority,idem_key,tenant_id,enqueued_at,visible_at,last_error,stack,quarantined_at,reason)
                 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		env.ID, string(env.Name), []byte(env.Payload), env.Attempts, env.MaxAttempts,
		env.Priority, nullableString(env.IdempotencyKey), nullableString(env.TenantID),
		env.EnqueuedAt.UnixMilli(), env.VisibleAt.UnixMilli(), lastErr, env.Stack,
		time.Now().UnixMilli(), "nack_no_requeue"); err != nil {
		return diag.Wrap(err, diag.Diag{Code: "OGON-J0026", Title: "jobs: dlq insert failed"})
	}
	_, _ = d.db.Exec(ctx, `DELETE FROM ogon_jobs_queue WHERE id = ?`, r.EnvelopeID())
	return nil
}

// Depth implements Queue.
func (d *DBDriver) Depth(ctx context.Context) (int64, error) {
	row := d.db.QueryRow(ctx, `SELECT COUNT(*) FROM ogon_jobs_queue`)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Peek implements Queue.
func (d *DBDriver) Peek(ctx context.Context, n int) ([]*Envelope, error) {
	if n <= 0 {
		n = 50
	}
	rows, err := d.db.Query(ctx,
		`SELECT id,name,payload,attempts,max_attempts,priority,idem_key,tenant_id,enqueued_at,visible_at,last_error,stack
                 FROM ogon_jobs_queue
                 WHERE visible_at <= ?
                 ORDER BY priority DESC, visible_at ASC
                 LIMIT ?`, time.Now().UnixMilli(), n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Envelope, 0, n)
	for rows.Next() {
		env, err := scanEnvelope(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

// Close implements Queue — no resources to release beyond the pool
// (owned by record.RegisterPool).
func (d *DBDriver) Close() error { return nil }

// --- DLQDriver impl ---

// ListDLQ returns recent DLQ entries (newest first).
func (d *DBDriver) ListDLQ(ctx context.Context, n int) ([]DLQEntry, error) {
	if d.db == nil {
		if err := d.EnsureSchema(ctx); err != nil {
			return nil, err
		}
	}
	if n <= 0 {
		n = 50
	}
	rows, err := d.db.Query(ctx,
		`SELECT id,name,payload,attempts,max_attempts,priority,idem_key,tenant_id,enqueued_at,visible_at,last_error,stack,quarantined_at,reason
                 FROM ogon_jobs_dlq
                 ORDER BY quarantined_at DESC
                 LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DLQEntry{}
	for rows.Next() {
		entry, err := scanDLQRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// scanDLQRow scans one DLQ row. Accepts record.Rows.
func scanDLQRow(r record.Rows) (DLQEntry, error) {
	var env Envelope
	var quarantined int64
	var reason string
	var idem, tenant, lastErr, stack sql.NullString
	var enqMs, visMs int64
	if err := r.Scan(
		&env.ID, &env.Name, &env.Payload, &env.Attempts, &env.MaxAttempts,
		&env.Priority, &idem, &tenant, &enqMs, &visMs, &lastErr, &stack,
		&quarantined, &reason); err != nil {
		return DLQEntry{}, err
	}
	env.IdempotencyKey = idem.String
	env.TenantID = tenant.String
	env.LastError = lastErr.String
	env.Stack = stack.String
	env.EnqueuedAt = time.UnixMilli(enqMs).UTC()
	env.VisibleAt = time.UnixMilli(visMs).UTC()
	return DLQEntry{Envelope: env, Reason: reason, QuarantinedAt: time.UnixMilli(quarantined).UTC()}, nil
}

// scanEnvelope scans one queue row from any Row/Rows.
func scanEnvelope(r record.Row) (*Envelope, error) {
	var env Envelope
	var idem, tenant, lastErr, stack sql.NullString
	var enqMs, visMs int64
	if err := r.Scan(
		&env.ID, &env.Name, &env.Payload, &env.Attempts, &env.MaxAttempts,
		&env.Priority, &idem, &tenant, &enqMs, &visMs, &lastErr, &stack); err != nil {
		return nil, err
	}
	env.IdempotencyKey = idem.String
	env.TenantID = tenant.String
	env.LastError = lastErr.String
	env.Stack = stack.String
	env.EnqueuedAt = time.UnixMilli(enqMs).UTC()
	env.VisibleAt = time.UnixMilli(visMs).UTC()
	return &env, nil
}

func ignoreNoRows(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEmpty
	}
	return err
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "UNIQUE") || contains(msg, "unique") || contains(msg, "duplicate")
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var _ = fmt.Sprintf // keep fmt imported for future debug helpers
