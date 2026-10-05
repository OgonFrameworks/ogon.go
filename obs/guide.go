// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Observability guide stub. Implements OBS-045.
//
// The full guide lives in docs/observability.md (rendered by the docs
// subsystem). This file is the runtime entrypoint the `ogon explain
// observability` command surfaces so callers don't have to walk the
// filesystem to find the canonical advice.

package obs

// GuideText is the short observability guide rendered by `ogon explain
// observability`. (OBS-045)
const GuideText = `OgonGo observability guide (OBS-045)

This guide is the canonical reference for "what to log, what to trace,
what to count". The full version lives in docs/observability.md; this
stub is rendered by 'ogon explain observability' for quick lookups.

LOGGING
- Use slog with NewLogger(); never write to os.Stdout directly. The
  framework's redaction hook will scrub PII from every record. (OBS-005)
- Levels: Debug (dev), Info (prod default), Warn (slow / 4xx), Error
  (5xx / unexpected). Set via OGON_LOG_LEVEL.
- For request logs use RequestLoggerMiddleware; it carries route
  template + trace_id so you can grep across logs and traces. (OBS-004)

TRACING
- InitTracer() sets up W3C TraceContext + Baggage propagation and
  parent-based sampling (1.0 in dev, 0.1 in prod). (OBS-006/018)
- Use CustomSpan(ctx, name) for application spans; pass error to the
  returned end func so the span is marked errored. (OBS-030)
- Slow spans (>SlowSpanThreshold) are force-exported via the slow-span
  sink. Tune with SetSlowSpanThreshold. (OBS-027)

METRICS
- /metrics is the Prometheus scrape endpoint. Install with
  MountMetrics(mux). (OBS-009)
- All metrics use route_template / status_class / db_operation labels
  — NEVER raw path, user_id, or trace_id. Run CardinalityCheck(labels)
  before adding a new metric. (OBS-032)
- Use NewCustomCounter/NewCustomGauge/NewCustomHistogram for app-defined
  metrics. They refuse PII-shaped labels up front. (OBS-029)

HEALTH
- /healthz (liveness), /readyz (readiness), /healthz/startup (startup
  probe). Probe routes are excluded from sampling. (OBS-019/020/021)
- Set HealthConfig.AuthToken to require a bearer token on probes.
  (OBS-022)

PROFILING
- pprof routes are OFF by default; enable with OGON_PPROF_ENABLED=true.
  (OBS-023)
- 'ogon inspect runtime' shows a one-screen live snapshot. (OBS-024)

PRIVACY
- No PII in span attrs or log fields. The framework's redaction hook +
  span attr allowlist enforce this at runtime; the lint in pii_lint.go
  catches new violations at authoring time. (OBS-043/TEST-050)

CRASH
- Wrap a CrashReportAdapter (NoopCrashReporter is the default). Plug
  Sentry by implementing the interface. (OBS-035)
- Use CatchPanic() in handlers so panics land on the active span +
  reach the crash adapter. (OBS-036)

GENERATED ARTIFACTS
- Grafana dashboard: ogon gen dashboard (GrafanaDashboardJSON). (OBS-041)
- Prometheus alerts: ogon gen alerts (PrometheusAlertSamples). (OBS-040)
- OTel collector sample: ogon gen collector (OTelCollectorSample). (OBS-042)
`

// GuideSection returns one section by name. Returns "" if not found.
// (OBS-045)
func GuideSection(name string) string {
	for _, sec := range guideSections {
		if sec.Name == name {
			return sec.Body
		}
	}
	return ""
}

var guideSections = []struct {
	Name string
	Body string
}{
	{"logging", "Use slog with NewLogger(). Levels via OGON_LOG_LEVEL. Request logs carry route template + trace_id. (OBS-001..005)"},
	{"tracing", "InitTracer sets up W3C + parent-based sampling. Use CustomSpan for application spans. (OBS-006/018)"},
	{"metrics", "/metrics endpoint, route_template labels only. Use NewCustom* for app metrics. (OBS-009..015/032)"},
	{"health", "/healthz /readyz /healthz/startup. Probe routes excluded from sampling. (OBS-019..022)"},
	{"profiling", "pprof gated by OGON_PPROF_ENABLED. 'ogon inspect runtime' for live snapshot. (OBS-023/024)"},
	{"privacy", "No PII in span attrs/log fields. Redaction hook + lint enforce. (OBS-043/TEST-050)"},
	{"crash", "CrashReportAdapter interface. CatchPanic records on span. (OBS-035/036)"},
	{"artifacts", "Grafana dashboard, Prometheus alerts, OTel collector sample — generated JSON consts. (OBS-040..042)"},
}

// GuideSections returns the list of section names. (OBS-045)
func GuideSections() []string {
	out := make([]string, len(guideSections))
	for i, s := range guideSections {
		out[i] = s.Name
	}
	return out
}
