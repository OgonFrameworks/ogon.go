// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Queue (jobs) metrics: depth, enqueue/dequeue counts, retries. OBS-014.

package obs

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// QueueMetrics holds the queue gauges + counters.
type QueueMetrics struct {
	depthGauge      *prometheus.GaugeVec
	enqueuedTotal   *prometheus.CounterVec
	processedTotal  *prometheus.CounterVec
	failedTotal     *prometheus.CounterVec
	retriedTotal    *prometheus.CounterVec
	processDuration *prometheus.HistogramVec
}

var queueMx *QueueMetrics
var queueInitOnce sync.Once

// InitQueueMetrics registers the queue counters. Idempotent. (OBS-014)
func InitQueueMetrics() {
	queueInitOnce.Do(func() {
		m := &QueueMetrics{
			depthGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "ogon_queue_depth",
				Help: "Current number of jobs waiting in queue by queue_name.",
			}, []string{"queue_name"}),
			enqueuedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_queue_enqueued_total",
				Help: "Total jobs enqueued, by queue_name.",
			}, []string{"queue_name"}),
			processedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_queue_processed_total",
				Help: "Total jobs processed, by queue_name and outcome.",
			}, []string{"queue_name", "outcome"}),
			failedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_queue_failed_total",
				Help: "Total jobs that exhausted retries, by queue_name.",
			}, []string{"queue_name"}),
			retriedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "ogon_queue_retried_total",
				Help: "Total jobs retried, by queue_name.",
			}, []string{"queue_name"}),
			processDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
				Name:    "ogon_queue_process_seconds",
				Help:    "Job processing time, by queue_name.",
				Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.5, 1, 5, 30, 60},
			}, []string{"queue_name"}),
		}
		MustRegister(
			m.depthGauge, m.enqueuedTotal, m.processedTotal, m.failedTotal,
			m.retriedTotal, m.processDuration,
		)
		queueMx = m
	})
}

// RecordQueueEnqueue records a job enqueue. (OBS-014)
func RecordQueueEnqueue(queueName string) {
	if queueMx == nil {
		return
	}
	queueMx.enqueuedTotal.WithLabelValues(queueName).Inc()
}

// RecordQueueProcessed records a processed job: outcome is "ok" or "error".
func RecordQueueProcessed(queueName, outcome string, dur time.Duration) {
	if queueMx == nil {
		return
	}
	queueMx.processedTotal.WithLabelValues(queueName, outcome).Inc()
	queueMx.processDuration.WithLabelValues(queueName).Observe(dur.Seconds())
}

// RecordQueueFailed records a permanently-failed job.
func RecordQueueFailed(queueName string) {
	if queueMx == nil {
		return
	}
	queueMx.failedTotal.WithLabelValues(queueName).Inc()
}

// RecordQueueRetried records a retry attempt.
func RecordQueueRetried(queueName string) {
	if queueMx == nil {
		return
	}
	queueMx.retriedTotal.WithLabelValues(queueName).Inc()
}

// SetQueueDepth updates the depth gauge.
func SetQueueDepth(queueName string, depth int) {
	if queueMx == nil {
		return
	}
	queueMx.depthGauge.WithLabelValues(queueName).Set(float64(depth))
}
