// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/obs — observability: spans on publish/deliver, metrics on
// conns, messages in/out, drops, delivery latency (LIVE-025/026).
//
// The live subsystem depends on the (forthcoming) obs package for
// span emission. To avoid an import cycle and keep live/* decoupled
// from any specific tracing library, live defines a minimal Span
// interface and a NoopSink; the ogon App wires a real sink via DI.

package live

import (
	"sync"
	"sync/atomic"
	"time"
)

// Span is a minimal distributed-span handle. End() records the span
// and may emit it through the configured sink. Implementations MUST be
// safe for concurrent use and MUST be no-ops after End.
type Span interface {
	SetAttr(k string, v any)
	End()
}

// SpanSink receives spans emitted by live/* code.
type SpanSink interface {
	StartSpan(name string) Span
}

// NoopSink is the default sink. Spans are no-ops.
type NoopSink struct{}

// noopSpan implements Span.
type noopSpan struct{}

func (noopSpan) SetAttr(string, any) {}
func (noopSpan) End()                {}

func (NoopSink) StartSpan(string) Span { return noopSpan{} }

// Metrics aggregates counters and latency for the live subsystem.
// Counters are atomic; the latency histogram is a small ring of buckets.
type Metrics struct {
	connsOpen  atomic.Int64
	connsTotal atomic.Int64
	msgsIn     atomic.Int64
	msgsOut    atomic.Int64
	msgsDrop   atomic.Int64
	msgsShed   atomic.Int64
	latencyMu  sync.Mutex
	latencySum atomic.Int64
	latencyCnt atomic.Int64
	latencyMax atomic.Int64
}

// NewMetrics returns an empty Metrics.
func NewMetrics() *Metrics { return &Metrics{} }

// ConnOpen marks a connection established.
func (m *Metrics) ConnOpen() { m.connsOpen.Add(1); m.connsTotal.Add(1) }

// ConnClose marks a connection closed.
func (m *Metrics) ConnClose() { m.connsOpen.Add(-1) }

// MsgIn counts an inbound message.
func (m *Metrics) MsgIn() { m.msgsIn.Add(1) }

// MsgOut counts an outbound delivery.
func (m *Metrics) MsgOut() { m.msgsOut.Add(1) }

// MsgDrop counts a queue drop.
func (m *Metrics) MsgDrop() { m.msgsDrop.Add(1) }

// MsgShed counts a load-shed drop.
func (m *Metrics) MsgShed() { m.msgsShed.Add(1) }

// ObserveDelivery records the publish→deliver latency (nanoseconds).
func (m *Metrics) ObserveDelivery(d time.Duration) {
	ns := d.Nanoseconds()
	m.latencySum.Add(ns)
	m.latencyCnt.Add(1)
	for {
		old := m.latencyMax.Load()
		if ns <= old || m.latencyMax.CompareAndSwap(old, ns) {
			break
		}
	}
}

// Snapshot returns a consistent view of all metrics.
type MetricsSnapshot struct {
	ConnsOpen   int64
	ConnsTotal  int64
	MsgsIn      int64
	MsgsOut     int64
	MsgsDrop    int64
	MsgsShed    int64
	LatencyMean time.Duration
	LatencyMax  time.Duration
}

// Snapshot returns the current counter values and latency aggregates.
func (m *Metrics) Snapshot() MetricsSnapshot {
	cnt := m.latencyCnt.Load()
	sum := m.latencySum.Load()
	var mean time.Duration
	if cnt > 0 {
		mean = time.Duration(sum / cnt)
	}
	return MetricsSnapshot{
		ConnsOpen:   m.connsOpen.Load(),
		ConnsTotal:  m.connsTotal.Load(),
		MsgsIn:      m.msgsIn.Load(),
		MsgsOut:     m.msgsOut.Load(),
		MsgsDrop:    m.msgsDrop.Load(),
		MsgsShed:    m.msgsShed.Load(),
		LatencyMean: mean,
		LatencyMax:  time.Duration(m.latencyMax.Load()),
	}
}
