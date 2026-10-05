// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — metrics, spans, structured logs (JOBS-020/021/022/046).
//
// Metric labels are deliberately bounded: only JobName and Outcome
// ("ok"/"retry"/"dlq"/"poison") label series. This keeps cardinality
// low enough for Prometheus exposition without unbounded growth.
//
// Metrics are surfaced via the obs subsystem (Phase 14); this file
// defines the in-memory counters and a Snap function for /healthz
// and `ogon jobs status`.

package jobs

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics holds bounded-cardinality job metrics. One instance per
// worker pool; safe for concurrent reads and writes.
type Metrics struct {
	// Enqueued counts total enqueues (any outcome).
	Enqueued atomic.Uint64
	// Dequeued counts total dispatches.
	Dequeued atomic.Uint64
	// Acked counts successful dispatches.
	Acked atomic.Uint64
	// Retried counts dispatches that ended in retry.
	Retried atomic.Uint64
	// DLQ counts dispatches that ended in DLQ.
	DLQ atomic.Uint64
	// Poison counts poison quarantines.
	Poison atomic.Uint64

	// perJob tracks per-job-name counts for hot-path metrics.
	mu     sync.Mutex
	perJob map[JobName]*jobMetrics
}

type jobMetrics struct {
	enqueued uint64
	dequeued uint64
	acked    uint64
	retried  uint64
	dlq      uint64
	poison   uint64
	totalDur int64 // ns
	maxDur   int64
}

// NewMetrics constructs an empty metrics holder.
func NewMetrics() *Metrics {
	return &Metrics{perJob: make(map[JobName]*jobMetrics)}
}

// RecordEnqueue bumps counters for name.
func (m *Metrics) RecordEnqueue(name JobName) {
	m.Enqueued.Add(1)
	m.job(name, func(j *jobMetrics) { j.enqueued++ })
}

// RecordDispatch bumps dequeued counters and records duration.
func (m *Metrics) RecordDispatch(name JobName, dur time.Duration) {
	m.Dequeued.Add(1)
	m.job(name, func(j *jobMetrics) {
		j.dequeued++
		j.totalDur += int64(dur)
		if int64(dur) > j.maxDur {
			j.maxDur = int64(dur)
		}
	})
}

// RecordAck bumps ack counters.
func (m *Metrics) RecordAck(name JobName) {
	m.Acked.Add(1)
	m.job(name, func(j *jobMetrics) { j.acked++ })
}

// RecordRetry bumps retry counters.
func (m *Metrics) RecordRetry(name JobName) {
	m.Retried.Add(1)
	m.job(name, func(j *jobMetrics) { j.retried++ })
}

// RecordDLQ bumps DLQ counters.
func (m *Metrics) RecordDLQ(name JobName) {
	m.DLQ.Add(1)
	m.job(name, func(j *jobMetrics) { j.dlq++ })
}

// RecordPoison bumps poison counters.
func (m *Metrics) RecordPoison(name JobName) {
	m.Poison.Add(1)
	m.job(name, func(j *jobMetrics) { j.poison++ })
}

// FailureRate returns (retried+dlq+poison) / dequeued for a job.
// Returns 0 if no dispatches.
func (m *Metrics) FailureRate(name JobName) float64 {
	m.mu.Lock()
	j, ok := m.perJob[name]
	m.mu.Unlock()
	if !ok || j.dequeued == 0 {
		return 0
	}
	fail := j.retried + j.dlq + j.poison
	return float64(fail) / float64(j.dequeued)
}

// JobSnapshot is a per-job metric row for /healthz.
type JobSnapshot struct {
	Name        JobName `json:"name"`
	Enqueued    uint64  `json:"enqueued"`
	Dequeued    uint64  `json:"dequeued"`
	Acked       uint64  `json:"acked"`
	Retried     uint64  `json:"retried"`
	DLQ         uint64  `json:"dlq"`
	Poison      uint64  `json:"poison"`
	FailureRate float64 `json:"failure_rate"`
	AvgDuration string  `json:"avg_duration"`
	MaxDuration string  `json:"max_duration"`
}

// Snap returns a per-job snapshot suitable for JSON export.
// Bounded to the (small) set of job names ever enqueued.
func (m *Metrics) Snap() []JobSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]JobSnapshot, 0, len(m.perJob))
	for name, j := range m.perJob {
		var avg time.Duration
		if j.dequeued > 0 {
			avg = time.Duration(j.totalDur / int64(j.dequeued))
		}
		fail := j.retried + j.dlq + j.poison
		var rate float64
		if j.dequeued > 0 {
			rate = float64(fail) / float64(j.dequeued)
		}
		out = append(out, JobSnapshot{
			Name:        name,
			Enqueued:    j.enqueued,
			Dequeued:    j.dequeued,
			Acked:       j.acked,
			Retried:     j.retried,
			DLQ:         j.dlq,
			Poison:      j.poison,
			FailureRate: rate,
			AvgDuration: avg.String(),
			MaxDuration: time.Duration(j.maxDur).String(),
		})
	}
	return out
}

func (m *Metrics) job(name JobName, fn func(*jobMetrics)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.perJob[name]
	if !ok {
		j = &jobMetrics{}
		m.perJob[name] = j
	}
	fn(j)
}

// Span is a lightweight timing scope for a single dispatch. Stop
// records the elapsed under the supplied metrics+name.
type Span struct {
	m    *Metrics
	name JobName
	t0   time.Time
}

// StartSpan begins a Span.
func StartSpan(m *Metrics, name JobName) *Span {
	return &Span{m: m, name: name, t0: time.Now()}
}

// Stop records the duration and bumps dispatch counters.
func (s *Span) Stop() time.Duration {
	dur := time.Since(s.t0)
	if s.m != nil {
		s.m.RecordDispatch(s.name, dur)
	}
	return dur
}

// LogFields returns the structured-log fields for one dispatch —
// bounded to (job, env_id, attempt, outcome). Used by the worker
// to ensure every log line carries the same minimum context.
func LogFields(env *Envelope, outcome string) []any {
	return []any{
		"job", string(env.Name),
		"env_id", env.ID,
		"attempt", env.Attempts,
		"outcome", outcome,
		"tenant", env.TenantID,
	}
}

// HealthCheck returns a simple health snapshot for /healthz.
func (m *Metrics) HealthCheck(ctx context.Context, q Queue) (map[string]any, error) {
	depth, err := q.Depth(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"depth":    depth,
		"enqueued": m.Enqueued.Load(),
		"dequeued": m.Dequeued.Load(),
		"acked":    m.Acked.Load(),
		"retried":  m.Retried.Load(),
		"dlq":      m.DLQ.Load(),
		"poison":   m.Poison.Load(),
	}, nil
}
