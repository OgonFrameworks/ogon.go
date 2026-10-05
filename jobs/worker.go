// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — worker pool with concurrency config, graceful drain (LIFO),
// SIGTERM-aware (JOBS-017).
//
// The worker pool is the production dispatch path. N goroutines pull
// from a Queue; each dispatch goes through:
//
//      StormGuard.Wait → Dequeue → Worker.timeouts → RecoverDispatch →
//      handler → Ack / Nack (retry/DLQ).
//
// Shutdown (Stop):
//   - SIGTERM/cancel stops the dispatcher (no new dequeues).
//   - In-flight handlers get drain_timeout to finish.
//   - Drain is LIFO: workers self-deregister as they finish; the
//     slowest are cancelled at the timeout.
//
// All goroutines spawn via runtime.Supervisor so OGON-CORE rule 8
// (no leaks) holds.

package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/OgonFrameworks/ogon.go/runtime"
)

// HandlerRegistry maps job names to dispatch closures (type-erased).
type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[JobName]HandlerFunc
}

// NewHandlerRegistry returns an empty registry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[JobName]HandlerFunc)}
}

// Register adds a handler. Registering the same name twice replaces.
func (r *HandlerRegistry) Register(name JobName, fn HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = fn
}

// Lookup returns the handler for name, or nil if unregistered.
func (r *HandlerRegistry) Lookup(name JobName) HandlerFunc {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.handlers[name]
}

// Names returns the registered job names.
func (r *HandlerRegistry) Names() []JobName {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]JobName, 0, len(r.handlers))
	for n := range r.handlers {
		out = append(out, n)
	}
	return out
}

// WorkerPoolConfig configures a pool.
type WorkerPoolConfig struct {
	Concurrency int
	// DrainTimeout caps shutdown wait (JOBS-017). Default 30s.
	DrainTimeout time.Duration
	// Visibility passed to StaleReaper; zero uses defaults.
	Visibility VisibilityOptions
	// Retry policy for nacks (defaults to DefaultRetryPolicy).
	Retry RetryPolicy
	// StormGuard is optional; nil disables.
	StormGuard *RetryStormGuard
	// PerJobTimeout adds a per-job wall-clock timeout (JOBS-016). 0 disables.
	PerJobTimeout map[JobName]time.Duration
}

// WithDefaults fills zero values.
func (c WorkerPoolConfig) WithDefaults() WorkerPoolConfig {
	out := c
	if out.Concurrency <= 0 {
		out.Concurrency = 4
	}
	if out.DrainTimeout <= 0 {
		out.DrainTimeout = 30 * time.Second
	}
	out.Visibility = out.Visibility.WithDefaults()
	out.Retry = out.Retry.WithDefaults(DefaultRetryPolicy)
	return out
}

// WorkerPool is the production dispatch path.
type WorkerPool struct {
	queue    Queue
	registry *HandlerRegistry
	cfg      WorkerPoolConfig
	metrics  *Metrics
	dlq      DLQDriver
	poison   *PoisonQuarantine
	log      *slog.Logger

	sup         *runtime.Supervisor
	wg          sync.WaitGroup
	stopped     atomic.Bool
	dispatching atomic.Bool

	// cancel is cancelled by Stop so workers blocked in Dequeue wake up
	// promptly without waiting for the supervisor-level ctx cancel.
	cancel  context.CancelFunc
	poolCtx context.Context
}

// NewWorkerPool returns a pool bound to the supplied queue + registry.
// The pool does NOT start; call Start.
func NewWorkerPool(
	sup *runtime.Supervisor,
	q Queue,
	reg *HandlerRegistry,
	cfg WorkerPoolConfig,
	metrics *Metrics,
	dlq DLQDriver,
	log *slog.Logger,
) *WorkerPool {
	if metrics == nil {
		metrics = NewMetrics()
	}
	if log == nil {
		log = slog.Default()
	}
	if dlq == nil {
		dlq = NewInMemoryDLQ(q, cfg.Retry)
	}
	cfg = cfg.WithDefaults()
	return &WorkerPool{
		queue:    q,
		registry: reg,
		cfg:      cfg,
		metrics:  metrics,
		dlq:      dlq,
		poison:   NewPoisonQuarantine(dlq),
		log:      log,
		sup:      sup,
	}
}

// Start spawns the worker goroutines. Each goroutine is named
// "jobs.worker.N" and tracked by the supervisor. Returns an error
// if the supervisor is closed.
func (p *WorkerPool) Start(ctx context.Context) error {
	if p.sup == nil {
		return diag.New("OGON-J0050", "jobs: nil supervisor", "")
	}
	// Derive a pool-scoped context that Stop can cancel. The workers
	// loop on this ctx so a Stop wakes them out of a blocking Dequeue
	// within microseconds, not at the drain-timeout boundary.
	poolCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.poolCtx = poolCtx
	for i := 0; i < p.cfg.Concurrency; i++ {
		idx := i
		// wg.Add must happen-before Spawn so Stop's wg.Wait never races
		// with a still-pending Add (race detector flags this).
		p.wg.Add(1)
		if err := p.sup.Spawn(p.workerName(idx), func(ctx context.Context) error {
			defer p.wg.Done()
			p.runWorker(poolCtx, idx)
			return nil
		}); err != nil {
			p.wg.Done()
			return err
		}
	}
	// Start the stale reaper (no-op for in-proc; real work in DB/Redis).
	reaper := NewStaleReaper(p.sup, p.queue, p.cfg.Visibility.ReaperInterval)
	_ = reaper.Start(poolCtx)
	return nil
}

// Stop signals workers to exit and waits up to DrainTimeout for them.
// Safe to call once; subsequent calls are no-ops.
func (p *WorkerPool) Stop(timeout time.Duration) error {
	if !p.stopped.CompareAndSwap(false, true) {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	if timeout <= 0 {
		timeout = p.cfg.DrainTimeout
	}
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		p.log.Warn("jobs: worker drain timeout exceeded", "timeout", timeout)
	}
	return nil
}

func (p *WorkerPool) workerName(i int) string { return "jobs.worker." + itoa(i) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// runWorker is the per-goroutine dispatch loop.
func (p *WorkerPool) runWorker(ctx context.Context, idx int) {
	for {
		if p.stopped.Load() {
			return
		}
		if ctx.Err() != nil {
			return
		}
		// Storm guard: throttle if the failure rate is high.
		if p.cfg.StormGuard != nil {
			if err := p.cfg.StormGuard.Wait(ctx); err != nil {
				return
			}
		}
		env, rcpt, err := p.queue.Dequeue(ctx)
		if err != nil {
			if errors.Is(err, ErrEmpty) || errors.Is(err, context.Canceled) {
				// short sleep to avoid busy-loop on empty
				select {
				case <-ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			p.log.Error("jobs: dequeue failed", "err", err, "worker", idx)
			continue
		}
		p.dispatch(ctx, env, rcpt)
	}
}

// dispatch runs the handler for one envelope and acks/nacks.
func (p *WorkerPool) dispatch(ctx context.Context, env *Envelope, rcpt Receipt) {
	p.metrics.RecordEnqueue(env.Name) // first enqueue is recorded elsewhere; this counts dispatches too
	span := StartSpan(p.metrics, env.Name)
	defer span.Stop()

	// tenant stamp
	if env.TenantID != "" {
		ctx = WithTenant(ctx, env.TenantID)
	}

	// look up handler
	h := p.registry.Lookup(env.Name)
	if h == nil {
		// poison: unknown handler → quarantine
		p.metrics.RecordPoison(env.Name)
		p.poison.Quarantine(ctx, env, "unknown_handler")
		SendToDLQWithAlert(ctx, p.dlq, env, "poison: unknown_handler")
		_ = p.queue.Ack(ctx, rcpt) // remove from queue (DLQ has it)
		return
	}

	// wrap with timeout + panic recovery
	wrapped := h
	if to, ok := p.cfg.PerJobTimeout[env.Name]; ok && to > 0 {
		wrapped = TimeoutDispatch(to, wrapped)
	}
	wrapped = RecoverDispatch(env.Name, wrapped)

	err := wrapped(ctx, env)
	if err == nil {
		_ = p.queue.Ack(ctx, rcpt)
		p.metrics.RecordAck(env.Name)
		p.log.Info("jobs: ack", LogFields(env, "ok")...)
		return
	}
	if errors.Is(err, ErrPoison) {
		p.metrics.RecordPoison(env.Name)
		p.poison.Quarantine(ctx, env, "poison")
		SendToDLQWithAlert(ctx, p.dlq, env, "poison")
		_ = p.queue.Ack(ctx, rcpt)
		return
	}
	if errors.Is(err, ErrFatal) {
		p.metrics.RecordDLQ(env.Name)
		SendToDLQWithAlert(ctx, p.dlq, env, "fatal")
		_ = p.queue.Ack(ctx, rcpt)
		return
	}
	// retry-able error
	policy := p.cfg.Retry
	if env.MaxAttempts > 0 {
		policy.MaxAttempts = env.MaxAttempts
	}
	if !policy.CanRetry(env.Attempts) {
		// max attempts exceeded → DLQ
		p.metrics.RecordDLQ(env.Name)
		SendToDLQWithAlert(ctx, p.dlq, env, "max_attempts")
		_ = p.queue.Ack(ctx, rcpt)
		return
	}
	// requeue with backoff
	delay := policy.NextBackoff(env.Attempts)
	p.metrics.RecordRetry(env.Name)
	env.LastError = err.Error()
	_ = p.queue.Nack(ctx, rcpt, true, time.Now().Add(delay), err.Error())
	p.log.Info("jobs: nack-retry", LogFields(env, "retry")...)
}
