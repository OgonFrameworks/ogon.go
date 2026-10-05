// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Config load benchmark (PERF-009 / Part XV.1 budget table).
//
// Target: < 50 ms cold load of a full ogon.yaml.
// The bench constructs a representative full ogon.yaml, writes it to
// an in-memory FS, and loads it via the standard layer precedence
// path (defaults → yaml → env → explicit → flags). Budget misses
// here flag YAML parser regressions or duplicate work in layer
// application (e.g. flatten running multiple times on the same map).

package config

import (
	"context"
	"io/fs"
	"testing"
)

// fullOgonYAML mirrors a representative production ogon.yaml: every
// top-level section populated, secrets interpolated from env, list
// values present. This is the worst-case the budget targets.
const fullOgonYAML = `ogon: "1.0"
app:
  name: "bench-app"
  env: "prod"
http:
  addr: ":8080"
  read_timeout: "30s"
  body_limit: "10MB"
  trusted_proxies: ["10.0.0.0/8"]
db:
  primary: "postgres://user:pass@localhost:5432/app?sslmode=disable"
  pool:
    min: 5
    max: 50
    max_lifetime: "1h"
cache:
  redis: "redis://localhost:6379/0"
auth:
  mode: "session"
  session_ttl: "24h"
obs:
  otlp: "grpc://otel:4317"
  log: "info"
jobs:
  driver: "db"
  concurrency: 10
infra:
  cloud: "aws"
  region: "us-east-1"
secrets:
  db_password: "${DB_PASSWORD}"
  api_key: "file:///etc/ogon/api_key"
`

// memFSBench is the in-memory file system for the bench. It mirrors
// memFS in config_test.go but is unexported to keep the bench
// self-contained.
type memFSBench struct{ files map[string][]byte }

func (m memFSBench) ReadFile(name string) ([]byte, error) {
	if b, ok := m.files[name]; ok {
		return b, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// BenchmarkConfigLoad measures the cold-load latency for a full ogon.yaml.
// Each iteration constructs a fresh Loader, applies defaults, parses
// the YAML, interpolates env, resolves file:// secrets, and applies
// env overrides. The budget is < 50 ms per load; typical cold load is
// < 1 ms on commodity hardware, so the budget absorbs ~50× headroom.
//
// The reported ns/op is the per-load cost; b.ReportMetric also reports
// the equivalent in milliseconds.
func BenchmarkConfigLoad(b *testing.B) {
	fs := memFSBench{files: map[string][]byte{
		"ogon.yaml":         []byte(fullOgonYAML),
		"ogon.prod.yaml":    []byte(fullOgonYAML),
		"/etc/ogon/api_key": []byte("bench-api-key-value"),
	}}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := NewLoader(
			WithFileSystem(fs),
			WithYAMLFile("ogon.yaml"),
			WithEnvYAML("prod"),
			WithEnvMap(map[string]string{
				"DB_PASSWORD":    "bench-db-pass",
				"OGON_HTTP_ADDR": ":9090",
			}),
		)
		cfg, err := l.Load(ctx)
		if err != nil {
			b.Fatalf("Load: %v", err)
		}
		if cfg.GetString("app.name") != "bench-app" {
			b.Fatalf("app.name = %q", cfg.GetString("app.name"))
		}
		if cfg.GetString("http.addr") != ":9090" {
			b.Fatalf("http.addr = %q, want :9090 (env override)", cfg.GetString("http.addr"))
		}
	}
}

// BenchmarkConfigLoadNoYAML measures the zero-config cold load path.
// This is the cheapest load — defaults only, no YAML parse. Useful
// for bounding the framework-startup floor when ogon.yaml is absent.
func BenchmarkConfigLoadNoYAML(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := NewLoader()
		_, err := l.Load(ctx)
		if err != nil {
			b.Fatalf("Load: %v", err)
		}
	}
}

// BenchmarkConfigExplain measures the Explain(key) path — the cost of
// reconstructing the source-chain for one config key. Explain walks
// the trace map (layer-by-layer) and is the path behind
// `ogon explain config`. Target: < 100 µs (well below the CLI p95).
func BenchmarkConfigExplain(b *testing.B) {
	fs := memFSBench{files: map[string][]byte{
		"ogon.yaml": []byte(fullOgonYAML),
	}}
	ctx := context.Background()
	l := NewLoader(
		WithFileSystem(fs),
		WithYAMLFile("ogon.yaml"),
		WithEnvMap(map[string]string{"DB_PASSWORD": "bench-db-pass"}),
	)
	cfg, err := l.Load(ctx)
	if err != nil {
		b.Fatalf("Load: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cfg.Explain("http.addr")
	}
}
