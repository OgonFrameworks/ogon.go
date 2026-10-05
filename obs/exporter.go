// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// OTLP exporter: on in prod (config default), off in dev. Implements OBS-008.

package obs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ExporterConfig configures the OTLP trace exporter.
type ExporterConfig struct {
	// Endpoint is the collector URL. For gRPC, include port but no
	// scheme. For HTTP, http(s)://host:port/path.
	Endpoint string
	// Protocol selects grpc or http. Empty → grpc.
	Protocol string
	// Headers added to every export request (e.g. {"x-tenant": "acme"}).
	Headers map[string]string
	// InsecureSkipVerify disables TLS verification (dev/test only).
	InsecureSkipVerify bool
	// Timeout caps the export round-trip. Default 10s.
	Timeout time.Duration
	// Enabled flips the exporter on. Default off in dev, on in prod.
	Enabled *bool
}

// DefaultExporterConfig returns the prod default: gRPC to localhost:4317,
// 10s timeout, enabled.
func DefaultExporterConfig() ExporterConfig {
	return ExporterConfig{
		Endpoint: "localhost:4317",
		Protocol: "grpc",
		Timeout:  10 * time.Second,
	}
}

// ExporterForEnv returns the prod default for non-dev envs and the
// disabled default for dev. (OBS-008)
func ExporterForEnv(env string) ExporterConfig {
	cfg := DefaultExporterConfig()
	off := false
	on := true
	if IsDevEnv(env) {
		cfg.Enabled = &off
	} else {
		cfg.Enabled = &on
	}
	return cfg
}

// NewTraceExporter builds the OTLP span exporter honoring cfg. When cfg
// indicates the exporter is off (Enabled=false), NewTraceExporter returns
// (nil, nil) so callers can skip wiring. (OBS-008)
func NewTraceExporter(ctx context.Context, cfg ExporterConfig) (*otlptrace.Exporter, error) {
	if cfg.Enabled != nil && !*cfg.Enabled {
		return nil, nil
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	switch strings.ToLower(cfg.Protocol) {
	case "", "grpc":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithTimeout(cfg.Timeout),
		}
		if cfg.InsecureSkipVerify || isLocalhostEndpoint(cfg.Endpoint) {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		return otlptracegrpc.New(ctx, opts...)
	case "http", "https":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(stripScheme(cfg.Endpoint)),
			otlptracehttp.WithTimeout(cfg.Timeout),
		}
		if cfg.InsecureSkipVerify || isLocalhostEndpoint(cfg.Endpoint) {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		return otlptracehttp.New(ctx, opts...)
	}
	return nil, fmt.Errorf("obs: unknown exporter protocol %q", cfg.Protocol)
}

// AttachExporter wires a trace exporter onto an existing TracerProvider.
// Calling code passes the provider returned by InitTracer indirectly —
// this helper exists so the http/record/live/jobs subsystems don't need
// to know the SDK's internals. (OBS-008)
func AttachExporter(ctx context.Context, tp *sdktrace.TracerProvider, exporter *otlptrace.Exporter) error {
	if tp == nil {
		return errors.New("obs: nil TracerProvider")
	}
	if exporter == nil {
		return nil
	}
	batchOpts := []sdktrace.BatchSpanProcessorOption{
		sdktrace.WithBatchTimeout(2 * time.Second),
		sdktrace.WithMaxExportBatchSize(512),
	}
	tp.RegisterSpanProcessor(sdktrace.NewBatchSpanProcessor(exporter, batchOpts...))
	return nil
}

// isLocalhostEndpoint returns true for endpoints that have no TLS at all
// (so we default to WithInsecure to avoid the cost of a TLS handshake).
func isLocalhostEndpoint(endpoint string) bool {
	host := stripScheme(endpoint)
	if i := strings.IndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	host = strings.TrimSpace(host)
	switch host {
	case "", "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}

func stripScheme(s string) string {
	for _, p := range []string{"https://", "http://", "grpc://", "grpcs://"} {
		if strings.HasPrefix(strings.ToLower(s), p) {
			return s[len(p):]
		}
	}
	return s
}

// exporterOnce guards against double-attach on the global provider.
var exporterOnce sync.Once

// AttachExporterOnce is the prod-friendly wrapper: only the first call
// actually attaches. Subsequent calls are no-ops.
func AttachExporterOnce(ctx context.Context, tp *sdktrace.TracerProvider, exporter *otlptrace.Exporter) error {
	var firstErr error
	exporterOnce.Do(func() {
		firstErr = AttachExporter(ctx, tp, exporter)
	})
	return firstErr
}
