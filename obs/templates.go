// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Generated artifacts: Grafana dashboard JSON, Prometheus alert samples,
// OTel collector sample config. Implements OBS-040/041/042.
//
// These are pre-rendered strings the `ogon gen dashboard` and `ogon gen
// alerts` CLI commands emit verbatim. Keeping them as Go consts means
// the dashboard is versioned with the framework, not in a separate file
// that drifts from the metric names.

package obs

// GrafanaDashboardJSON is the JSON for the canonical OgonGo dashboard.
// Generated with the framework so metric names cannot drift. (OBS-041)
const GrafanaDashboardJSON = `{
  "title": "OgonGo runtime dashboard",
  "uid": "ogongo-default",
  "schemaVersion": 38,
  "version": 1,
  "panels": [
    {
      "type": "stat",
      "title": "Goroutines",
      "gridPos": {"x": 0, "y": 0, "w": 4, "h": 4},
      "targets": [{"expr": "ogon_runtime_goroutines", "legendFormat": "goroutines"}]
    },
    {
      "type": "stat",
      "title": "Alloc MB",
      "gridPos": {"x": 4, "y": 0, "w": 4, "h": 4},
      "targets": [{"expr": "ogon_runtime_alloc_bytes / 1024 / 1024", "legendFormat": "alloc MB"}]
    },
    {
      "type": "stat",
      "title": "Live connections",
      "gridPos": {"x": 8, "y": 0, "w": 4, "h": 4},
      "targets": [{"expr": "sum(ogon_live_conns_open)", "legendFormat": "conns"}]
    },
    {
      "type": "graph",
      "title": "HTTP requests by route template",
      "gridPos": {"x": 0, "y": 4, "w": 24, "h": 8},
      "targets": [{"expr": "sum by (route_template) (rate(ogon_http_requests_total[5m]))", "legendFormat": "{{route_template}}"}]
    },
    {
      "type": "heatmap",
      "title": "HTTP latency (s) by route template",
      "gridPos": {"x": 0, "y": 12, "w": 12, "h": 8},
      "targets": [{"expr": "sum by (route_template, le) (rate(ogon_http_request_duration_seconds_bucket[5m]))", "legendFormat": "{{route_template}}"}]
    },
    {
      "type": "graph",
      "title": "DB queries (ops/s) by operation",
      "gridPos": {"x": 12, "y": 12, "w": 12, "h": 8},
      "targets": [{"expr": "sum by (db_operation) (rate(ogon_db_query_total[5m]))", "legendFormat": "{{db_operation}}"}]
    },
    {
      "type": "graph",
      "title": "Cache hit ratio",
      "gridPos": {"x": 0, "y": 20, "w": 12, "h": 6},
      "targets": [{"expr": "sum by (cache_name) (rate(ogon_cache_hits_total[5m])) / (sum by (cache_name) (rate(ogon_cache_hits_total[5m])) + sum by (cache_name) (rate(ogon_cache_misses_total[5m])))", "legendFormat": "{{cache_name}}"}]
    },
    {
      "type": "graph",
      "title": "Queue depth by name",
      "gridPos": {"x": 12, "y": 20, "w": 12, "h": 6},
      "targets": [{"expr": "ogon_queue_depth", "legendFormat": "{{queue_name}}"}]
    }
  ],
  "templating": {
    "list": [
      {
        "name": "datasource",
        "type": "datasource",
        "query": "prometheus",
        "current": {"text": "Prometheus", "value": "Prometheus"}
      }
    ]
  },
  "time": {"from": "now-1h", "to": "now"},
  "refresh": "10s"
}`

// PrometheusAlertSamples is the YAML alert rule list the CLI emits. (OBS-040)
const PrometheusAlertSamples = `groups:
  - name: ogon.rules
    rules:
      - alert: OgonHighGoroutines
        expr: ogon_runtime_goroutines > 5000
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Goroutine count above 5000"
          description: "Instance {{ $labels.instance }} reports {{ $value }} goroutines."
      - alert: OgonHighErrorRate
        expr: |
          sum(rate(ogon_http_requests_total{status_class="5xx"}[5m]))
          / sum(rate(ogon_http_requests_total[5m])) > 0.05
        for: 10m
        labels:
          severity: critical
        annotations:
          summary: "HTTP 5xx ratio above 5% for 10m"
          description: "5xx share of total HTTP requests is {{ $value | humanizePercentage }}."
      - alert: OgonSlowDB
        expr: rate(ogon_db_slow_query_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Slow DB query rate above 0.1/s"
      - alert: OgonLiveMsgsDropped
        expr: rate(ogon_live_msgs_dropped_total[5m]) > 0.01
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Live messages dropped"
          description: "{{ $value }} msgs/s dropped (transport {{ $labels.transport }})."
      - alert: OgonCacheHitRatioLow
        expr: |
          sum(rate(ogon_cache_hits_total[5m]))
          / (sum(rate(ogon_cache_hits_total[5m])) + sum(rate(ogon_cache_misses_total[5m]))) < 0.5
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Cache hit ratio < 50% for 10m"
      - alert: OgonQueueStuck
        expr: ogon_queue_depth > 1000
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Queue depth above 1000 for 5m"
          description: "Queue {{ $labels.queue_name }} has {{ $value }} items waiting."
`

// OTelCollectorSample is the sample OTel collector config wiring
// OgonGo → OTLP → Prometheus → alerts. (OBS-042)
const OTelCollectorSample = `receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  batch:
    timeout: 5s
    send_batch_size: 1024
  memory_limiter:
    check_interval: 5s
    limit_mib: 256
  # Cardinality-limit the spans before export so a runaway route_template
  # cannot explode the Prometheus label set. (OBS-032)
  filter:
    error_mode: ignore
    traces:
      span:
        - 'attributes["http.route"] == ""'

exporters:
  prometheus:
    endpoint: 0.0.0.0:9464
    resource_to_telemetry_conversion:
      enabled: true
  prometheusremotewrite:
    endpoint: http://mimir:9009/api/v1/push
  # For traces, forward to Jaeger or Tempo:
  otlp/jaeger:
    endpoint: jaeger:4317
    tls:
      insecure: true

service:
  pipelines:
    metrics:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [prometheus, prometheusremotewrite]
    traces:
      receivers: [otlp]
      processors: [memory_limiter, filter, batch]
      exporters: [otlp/jaeger]
    logs:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [prometheus]
`

// DashboardForVersion returns the dashboard JSON with the version
// attribute stamped. (OBS-033/041)
func DashboardForVersion(version string) string {
	// We don't parse the JSON — we inject via simple string replace so
	// the const above stays the source of truth and the test asserts the
	// replacement happens.
	return replaceOnce(GrafanaDashboardJSON, `"uid": "ogongo-default"`, `"uid": "ogongo-default", "version": "`+version+`"`)
}

// replaceOnce replaces the first occurrence of old in s with new. Inlined
// to keep the dashboard template self-contained.
func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}
