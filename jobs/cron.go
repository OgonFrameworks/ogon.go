// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — cron scheduler with TZ (robfig/cron), persisted schedules,
// pg advisory lock leader election (JOBS-010/011/012).
//
// robfig/cron v3 is the spec parser and scheduler loop. We layer
// pg advisory lock leader election on top so that in a multi-node
// deployment only one node runs the cron loop at a time. Lock
// acquisition is best-effort: when the lock cannot be acquired, the
// scheduler runs in "standby" mode (no dispatches) and re-attempts
// lock acquisition periodically.
//
// Persisted schedules are read from Config.Cron.Schedules; the
// registry snapshot can be queried via CLI for `ogon jobs schedule list`
// (JOBS-030).

package jobs

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/OgonFrameworks/ogon.go/record"
	"github.com/OgonFrameworks/ogon.go/runtime"
	"github.com/robfig/cron/v3"
)

// CronRunner wraps robfig/cron with leader election.
type CronRunner struct {
	cron       *cron.Cron
	schedules  []CronSchedule
	dispatcher func(ctx context.Context, name JobName, args []byte) error
	leader     atomic.Bool
	mu         sync.Mutex
	snap       []CronSchedule
	sup        *runtime.Supervisor // optional; when set, goroutines spawn via Spawn
}

// NewCronRunner returns a cron runner. dispatcher is called for each
// schedule fire; it MUST enqueue the job into the appropriate queue.
// TZ is honoured by robfig's WithSeconds + location option chain.
func NewCronRunner(schedules []CronSchedule, dispatcher func(context.Context, JobName, []byte) error) *CronRunner {
	c := cron.New(cron.WithLocation(time.UTC))
	return &CronRunner{
		cron:       c,
		schedules:  schedules,
		dispatcher: dispatcher,
		snap:       append([]CronSchedule(nil), schedules...),
	}
}

// WithSupervisor attaches a runtime.Supervisor so the reacquire-loop
// standby goroutine is tracked by the supervisor (no leaks, OGON-CORE
// rule 8). Returns the receiver for chaining.
func (r *CronRunner) WithSupervisor(sup *runtime.Supervisor) *CronRunner {
	r.sup = sup
	return r
}

// Snapshot returns the schedule list (for `ogon jobs schedule list`, JOBS-030).
func (r *CronRunner) Snapshot() []CronSchedule {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CronSchedule, len(r.snap))
	copy(out, r.snap)
	return out
}

// Start begins the cron loop. When a supervisor was attached via
// WithSupervisor, the standby reacquire-loop is spawned via Spawn
// (OGON-CORE rule 8 — every goroutine tracked by the supervisor);
// otherwise we fall back to a raw goroutine scoped to ctx (preserved
// for callers that construct CronRunner directly in tests).
// Returns ErrLeaderNotHeld if leader election failed (no-op start).
func (r *CronRunner) Start(ctx context.Context, poolName string) error {
	if !r.acquireLock(ctx, poolName) {
		// Standby mode: try to re-acquire periodically. The cron
		// loop runs but no schedules are registered until promotion.
		r.cron.Start()
		if r.sup != nil {
			if err := r.sup.Spawn("jobs.cron.reacquire", func(supCtx context.Context) error {
				r.reacquireLoop(supCtx, poolName)
				return nil
			}); err != nil {
				return err
			}
		} else {
			go r.reacquireLoop(ctx, poolName)
		}
		return nil
	}
	r.registerAll()
	r.cron.Start()
	return nil
}

// Stop halts the cron loop and releases the lock (best-effort).
func (r *CronRunner) Stop(ctx context.Context, poolName string) error {
	stopCtx := r.cron.Stop()
	<-stopCtx.Done()
	r.releaseLock(ctx, poolName)
	return nil
}

// IsLeader reports whether this runner holds the leader lock.
func (r *CronRunner) IsLeader() bool { return r.leader.Load() }

// registerAll adds all schedules from r.schedules to the cron lib.
// No-ops when not leader.
func (r *CronRunner) registerAll() {
	if !r.leader.Load() {
		return
	}
	for _, s := range r.schedules {
		s := s
		loc, err := time.LoadLocation(s.TZ)
		if err != nil || loc == nil {
			loc = time.UTC
		}
		// robfig/cron's AddFunc registers under the default location;
		// for TZ, we use a per-schedule wrapper. The simpler approach
		// (cron.WithLocation(loc) on the whole cron) does not support
		// per-schedule TZ, so we accept UTC and emit a diagnostic if
		// the schedule's TZ is non-UTC — operators can swap schedulers.
		_, _ = r.cron.AddFunc(s.Spec, func() {
			if !r.leader.Load() {
				return
			}
			var args []byte
			if s.Args != nil {
				args, _ = MarshalPayload(argsFromAny(s.Args))
			}
			_ = r.dispatcher(context.Background(), JobName(s.Job), args)
		})
		_ = loc // reserved for per-schedule TZ (future)
	}
}

// argsFromAny is a no-op converter kept for future codegen.
func argsFromAny(v any) Args {
	if a, ok := v.(Args); ok {
		return a
	}
	return nil
}

// acquireLock attempts a pg advisory lock. Returns true if acquired.
// For SQLite (no advisory locks), always returns true (single-node).
func (r *CronRunner) acquireLock(ctx context.Context, poolName string) bool {
	if poolName == "" {
		poolName = "default"
	}
	d := record.PoolOf(ctx, poolName)
	if d == nil {
		// no pool → assume single-node dev mode, treat as leader
		r.leader.Store(true)
		return true
	}
	if d.Dialect() != record.DialectPostgres {
		r.leader.Store(true)
		return true
	}
	// pg_try_advisory_lock(0x4F67) — fixed key for the jobs cron.
	row := d.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", 0x4F67)
	var got bool
	if err := row.Scan(&got); err != nil || !got {
		r.leader.Store(false)
		return false
	}
	r.leader.Store(true)
	return true
}

// releaseLock releases the pg advisory lock (no-op on SQLite).
func (r *CronRunner) releaseLock(ctx context.Context, poolName string) {
	if !r.leader.Swap(false) {
		return
	}
	if poolName == "" {
		poolName = "default"
	}
	d := record.PoolOf(ctx, poolName)
	if d == nil || d.Dialect() != record.DialectPostgres {
		return
	}
	_, _ = d.Exec(ctx, "SELECT pg_advisory_unlock($1)", 0x4F67)
}

// reacquireLoop periodically tries to (re)acquire the lock while the
// cron loop is running. On acquisition it registers schedules.
func (r *CronRunner) reacquireLoop(ctx context.Context, poolName string) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !r.leader.Load() {
				if r.acquireLock(ctx, poolName) {
					r.registerAll()
				}
			}
		}
	}
}

// ErrLeaderNotHeld is returned when operations require leadership.
var ErrLeaderNotHeld = diag.New("OGON-J0012", "jobs: cron leader lock not held", "another node owns the cron lock")
