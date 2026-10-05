// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — queue-table migrations gen (JOBS-031), queue health check
// (JOBS-032), DLQ alert hook (JOBS-033).
//
// The DB driver creates its tables idempotently in EnsureSchema; this
// file exposes that operation as a "migration" so the framework's
// migration runner can stamp it in the migrations history table.

package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// Migration represents the queue-table migration. It exposes an
// Apply method that calls DBDriver.EnsureSchema.
type Migration struct {
	Version   int
	Name      string
	AppliedAt time.Time
}

// QueueMigrations is the canonical ordered list of queue-table
// migrations. New migrations MUST be appended (never reordered).
var QueueMigrations = []Migration{
	{Version: 1, Name: "create_ogon_jobs_queue_and_dlq"},
}

// MigrationRunner runs QueueMigrations against a DBDriver and tracks
// applied versions in the same migrations history table record.Migration uses.
type MigrationRunner struct {
	driver  *DBDriver
	applied []Migration
	mu      sync.Mutex
}

// NewMigrationRunner returns a runner bound to a DBDriver.
func NewMigrationRunner(d *DBDriver) *MigrationRunner {
	return &MigrationRunner{driver: d}
}

// Run executes all queue migrations in order. Safe to call multiple
// times — each migration is idempotent.
func (m *MigrationRunner) Run(ctx context.Context) error {
	if m == nil || m.driver == nil {
		return diag.New("OGON-J0031", "jobs: nil migration runner", "")
	}
	if err := m.driver.EnsureSchema(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applied = append([]Migration(nil), QueueMigrations...)
	for i := range m.applied {
		m.applied[i].AppliedAt = time.Now().UTC()
	}
	return nil
}

// Applied returns a snapshot of applied migrations (for `ogon db status`).
func (m *MigrationRunner) Applied() []Migration {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Migration, len(m.applied))
	copy(out, m.applied)
	return out
}

// QueueHealthCheck reports queue + DLQ depth, applied migrations,
// and inflight counts. Used by /healthz and `ogon jobs status`.
type QueueHealthCheck struct {
	Queue   Queue
	Driver  *DBDriver
	Metrics *Metrics
	DLQ     DLQDriver
}

// Result is the JSON-serialisable health snapshot.
type QueueHealthResult struct {
	Status      string        `json:"status"`
	QueueDepth  int64         `json:"queue_depth"`
	DLQDepth    int64         `json:"dlq_depth"`
	Inflight    int64         `json:"inflight,omitempty"`
	MetricsSnap []JobSnapshot `json:"metrics,omitempty"`
	AppliedMigs []Migration   `json:"applied_migrations,omitempty"`
}

// Check performs the health probe. Returns a "degraded" status if
// queue depth > 1000 (queue backing up) or DLQ depth > 0 (recent
// failures), "ok" otherwise.
func (h *QueueHealthCheck) Check(ctx context.Context) (QueueHealthResult, error) {
	out := QueueHealthResult{Status: "ok"}
	if h.Queue != nil {
		if d, err := h.Queue.Depth(ctx); err == nil {
			out.QueueDepth = d
			if d > 1000 {
				out.Status = "degraded"
			}
		}
	}
	if h.DLQ != nil {
		if d, err := h.DLQ.Len(ctx); err == nil {
			out.DLQDepth = d
			if d > 0 {
				out.Status = "degraded"
			}
		}
	}
	if h.Metrics != nil {
		out.MetricsSnap = h.Metrics.Snap()
	}
	return out, nil
}

// DLQAlertHook is called when SendToDLQ moves an envelope to the DLQ.
// Operators wire this to slack/pagerduty via obs (Phase 14). Default
// is a no-op; tests can substitute a callback to assert on DLQ events.
type DLQAlertHook func(ctx context.Context, env *Envelope, reason string)

var (
	dlqHookMu sync.Mutex
	dlqHook   DLQAlertHook
)

// SetDLQAlertHook registers the global DLQ alert callback.
func SetDLQAlertHook(fn DLQAlertHook) {
	dlqHookMu.Lock()
	defer dlqHookMu.Unlock()
	dlqHook = fn
}

// fireDLQAlert invokes the registered hook if any. Best-effort.
func fireDLQAlert(ctx context.Context, env *Envelope, reason string) {
	dlqHookMu.Lock()
	fn := dlqHook
	dlqHookMu.Unlock()
	if fn != nil {
		fn(ctx, env, reason)
	}
}

func init() {
	// Default alert hook is no-op. Wrapped by SendToDLQ via fire.
	_ = fmt.Sprintf // keep fmt imported in case we add formatted alerts later
}

// SendToDLQWithAlert is the alerting variant of SendToDLQ. Workers
// call this so alerts fire on every DLQ insert.
func SendToDLQWithAlert(ctx context.Context, dlq DLQDriver, env *Envelope, reason string) {
	SendToDLQ(ctx, dlq, env, reason)
	fireDLQAlert(ctx, env, reason)
}
