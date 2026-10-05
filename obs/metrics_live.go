// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Live (realtime) metrics: conns, msgs in/out/dropped, latency. OBS-015.

package obs

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// LiveMetrics records WS/SSE connection counts + message counters.
type LiveMetrics struct {
	connsOpenGauge   *prometheus.GaugeVec
	connsTotal       *prometheus.CounterVec
	msgsInTotal      *prometheus.CounterVec
	msgsOutTotal     *prometheus.CounterVec
	msgsDroppedTotal *prometheus.CounterVec
	msgsShedTotal    *prometheus.CounterVec
	latency          *prometheus.HistogramVec
	presenceMembers  *prometheus.GaugeVec
}

var liveMx *LiveMetrics
var liveInitOnce sync.Once

// InitLiveMetrics registers the live metrics. Idempotent. (OBS-015)
func InitLiveMetrics() {
	liveInitOnce.Do(func() {
		m := &LiveMetrics{
			connsOpenGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "ogon_live_conns_open",
				Help: "Currently open connections by transport.",
			}, []string{"transport"}),
			connsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_live_conns_total",
				Help: "Total connections accepted since start.",
			}, []string{"transport"}),
			msgsInTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_live_msgs_in_total",
				Help: "Total inbound messages by event_type.",
			}, []string{"event_type"}),
			msgsOutTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_live_msgs_out_total",
				Help: "Total outbound messages by event_type.",
			}, []string{"event_type"}),
			msgsDroppedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_live_msgs_dropped_total",
				Help: "Messages dropped due to a full queue or rate limit.",
			}, []string{"transport"}),
			msgsShedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_live_msgs_shed_total",
				Help: "Messages shed by the load shedder.",
			}, []string{"channel"}),
			latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "ogon_live_msg_latency_seconds",
				Help:    "Outbound message delivery latency.",
				Buckets: []float64{1e-4, 1e-3, 5e-3, 1e-2, 5e-2, 0.1, 0.5, 1},
			}, []string{"transport"}),
			presenceMembers: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "ogon_live_presence_members",
				Help: "Members currently present in a channel.",
			}, []string{"channel"}),
		}
		MustRegister(
			m.connsOpenGauge, m.connsTotal, m.msgsInTotal, m.msgsOutTotal,
			m.msgsDroppedTotal, m.msgsShedTotal, m.latency, m.presenceMembers,
		)
		liveMx = m
	})
}

// RecordLiveConn records a connection open (inc) or close (dec). (OBS-015)
func RecordLiveConn(transport string, open bool) {
	if liveMx == nil {
		return
	}
	if open {
		liveMx.connsOpenGauge.WithLabelValues(transport).Inc()
		liveMx.connsTotal.WithLabelValues(transport).Inc()
	} else {
		liveMx.connsOpenGauge.WithLabelValues(transport).Dec()
	}
}

// RecordLiveMsgIn records an inbound message by event type.
func RecordLiveMsgIn(eventType string) {
	if liveMx == nil {
		return
	}
	liveMx.msgsInTotal.WithLabelValues(eventType).Inc()
}

// RecordLiveMsgOut records an outbound message by event type + delivery latency.
func RecordLiveMsgOut(eventType, transport string, lat time.Duration) {
	if liveMx == nil {
		return
	}
	liveMx.msgsOutTotal.WithLabelValues(eventType).Inc()
	liveMx.latency.WithLabelValues(transport).Observe(lat.Seconds())
}

// RecordLiveDrop records a dropped message (full queue / rate limit).
func RecordLiveDrop(transport string) {
	if liveMx == nil {
		return
	}
	liveMx.msgsDroppedTotal.WithLabelValues(transport).Inc()
}

// RecordLiveShed records a shed message (load shedder).
func RecordLiveShed(channel string) {
	if liveMx == nil {
		return
	}
	liveMx.msgsShedTotal.WithLabelValues(channel).Inc()
}

// SetLivePresence updates the presence gauge.
func SetLivePresence(channel string, count int) {
	if liveMx == nil {
		return
	}
	liveMx.presenceMembers.WithLabelValues(channel).Set(float64(count))
}
