// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — tenant-aware jobs (carry tenant), RLS session (JOBS-044/045).
//
// When a request enqueues a job, the originating tenant ID is
// stamped on the envelope. The worker restores the tenant context
// before dispatching so handler code (which may issue DB queries)
// sees RLS-filtered rows.
//
// The DB worker additionally calls record.SetTenantSessionVar to
// stamp the tenant into the connection's session variables for
// Postgres RLS policies. (The SQLite driver is a no-op for this.)

package jobs

import (
	"context"
)

// tenantKey is the context key for the current job's tenant.
type tenantKey struct{}

// WithTenant stamps tenantID into ctx for the duration of one
// dispatch. Handlers retrieve it via TenantFrom.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenantID)
}

// TenantFrom returns the tenant ID stamped on ctx by WithTenant, or
// empty string if none. This is the programmatic read path; the DB
// worker also stamps the session var via record.SetTenantSessionVar.
func TenantFrom(ctx context.Context) string {
	v, _ := ctx.Value(tenantKey{}).(string)
	return v
}

// TenantScopedQueue wraps a Queue so every enqueue stamps the
// tenant from ctx onto the envelope (unless the caller already set
// Envelope.TenantID). This is a thin convenience — the worker
// restores the context, the queue does not need to know about RLS.
type TenantScopedQueue struct {
	Queue
	resolve func(context.Context) string
}

// NewTenantScopedQueue returns a wrapper that calls resolve on each
// Enqueue to determine the tenant (or returns "" if the ctx has none).
func NewTenantScopedQueue(q Queue, resolve func(context.Context) string) *TenantScopedQueue {
	if resolve == nil {
		resolve = TenantFrom
	}
	return &TenantScopedQueue{Queue: q, resolve: resolve}
}

// Enqueue stamps the tenant from ctx (if env.TenantID is empty).
func (t *TenantScopedQueue) Enqueue(ctx context.Context, env *Envelope, opts EnqueueOptions) error {
	if env.TenantID == "" {
		env.TenantID = t.resolve(ctx)
	}
	if opts.TenantID == "" {
		opts.TenantID = env.TenantID
	}
	return t.Queue.Enqueue(ctx, env, opts)
}
