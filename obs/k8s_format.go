// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// K8s-friendly log format option. Implements OBS-034.
//
// Kubernetes log streams are parsed by stack drivers that expect a
// specific JSON shape:
//
//   {"log":"<line>","severity":"INFO","time":"2026-01-01T00:00:00Z",
//    "trace_id":"<hex>","span_id":"<hex>"}
//
// The k8sHandler wraps a JSON handler and re-serialises each record into
// the k8s shape. Output goes to stdout for INFO/DEBUG and stderr for
// WARN/ERROR so a side-car can split streams if desired.

package obs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"time"
)

// k8sHandler wraps a slog.Handler so each record is rendered in the
// Kubernetes log format. (OBS-034)
type k8sHandler struct {
	inner slog.Handler
	out   io.Writer
	err   io.Writer
}

// newK8sHandler installs the k8s wrapper around the supplied handler.
// (OBS-034)
func newK8sHandler(inner slog.Handler) slog.Handler {
	return &k8sHandler{inner: inner, out: stdout(), err: stderr()}
}

func (k *k8sHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return k.inner.Enabled(ctx, l)
}

func (k *k8sHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &k8sHandler{inner: k.inner.WithAttrs(attrs), out: k.out, err: k.err}
}

func (k *k8sHandler) WithGroup(name string) slog.Handler {
	return &k8sHandler{inner: k.inner.WithGroup(name), out: k.out, err: k.err}
}

func (k *k8sHandler) Handle(ctx context.Context, rec slog.Record) error {
	// Build the k8s JSON envelope from the record.
	body := map[string]any{
		"log":      rec.Message,
		"severity": k8sSeverity(rec.Level),
		"time":     rec.Time.UTC().Format(time.RFC3339Nano),
	}
	// Carry through attrs into a flat payload map; trace_id and span_id
	// are pulled up if present.
	rec.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "trace_id", "span_id":
			body[a.Key] = a.Value.String()
		default:
			body[a.Key] = a.Value.Any()
		}
		return true
	})
	buf, err := json.Marshal(body)
	if err != nil {
		// Fall back to the inner handler so we never lose a record.
		return k.inner.Handle(ctx, rec)
	}
	// Pick the right stream.
	w := k.out
	if rec.Level >= slog.LevelWarn {
		w = k.err
	}
	_, err = w.Write(append(buf, '\n'))
	return err
}

// k8sSeverity maps slog.Level to the K8s severity string. (OBS-034)
func k8sSeverity(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	}
	return "DEBUG"
}

// stdout/stderr indirections so tests can substitute destinations.
var (
	stdout = func() io.Writer { return defaultStdout() }
	stderr = func() io.Writer { return defaultStderr() }
)

// SetStdoutForTest substitutes the stdout sink for tests.
func SetStdoutForTest(w io.Writer) func() {
	prev := stdout
	stdout = func() io.Writer { return w }
	return func() { stdout = prev }
}

// SetStderrForTest substitutes the stderr sink for tests.
func SetStderrForTest(w io.Writer) func() {
	prev := stderr
	stderr = func() io.Writer { return w }
	return func() { stderr = prev }
}
