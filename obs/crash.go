// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Crash-report adapter interface (Sentry optional) + panic→span error
// attrs. Implements OBS-035/036.
//
// CrashReportAdapter is an interface so apps can plug Sentry, Bugsnag,
// or a self-hosted alternative without forcing one on everyone. The
// framework ships a NoopCrashReporter and a HTTPPoster (which routes
// events to a configured URL).

package obs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// CrashReport carries the data captured when a panic or fatal error fires.
type CrashReport struct {
	Service     string            `json:"service"`
	Version     string            `json:"version"`
	Environment string            `json:"environment"`
	Hostname    string            `json:"hostname,omitempty"`
	Timestamp   time.Time         `json:"timestamp"`
	Severity    string            `json:"severity"` // fatal | error | warning
	Message     string            `json:"message"`
	Stack       string            `json:"stack,omitempty"`
	TraceID     string            `json:"trace_id,omitempty"`
	SpanID      string            `json:"span_id,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// CrashReportAdapter is the extension point for Sentry, Bugsnag, etc.
// Implementations MUST be safe for concurrent use and MUST NOT block on
// network I/O in the calling goroutine (they should batch in a worker).
// (OBS-035)
type CrashReportAdapter interface {
	Report(ctx context.Context, rep CrashReport) error
	Name() string
}

// NoopCrashReporter is the default adapter — drops everything. Tests
// should use it to assert no spurious crash reports leak. (OBS-035)
type NoopCrashReporter struct{}

func (NoopCrashReporter) Report(_ context.Context, _ CrashReport) error { return nil }
func (NoopCrashReporter) Name() string                                  { return "noop" }

// HTTPCrashReporter POSTs each report as JSON to a configured endpoint.
// Production code wraps it with a worker pool. (OBS-035)
type HTTPCrashReporter struct {
	Endpoint string
	Client   *http.Client
	Headers  map[string]string
}

// NewHTTPCrashReporter builds an adapter. The client defaults to a 5s
// timeout; pass a custom one for batching. (OBS-035)
func NewHTTPCrashReporter(endpoint string) *HTTPCrashReporter {
	return &HTTPCrashReporter{
		Endpoint: endpoint,
		Client:   &http.Client{Timeout: 5 * time.Second},
	}
}

func (h *HTTPCrashReporter) Name() string { return "http" }

func (h *HTTPCrashReporter) Report(ctx context.Context, rep CrashReport) error {
	body, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.Endpoint, bytesReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("obs: crash report returned %d", resp.StatusCode)
	}
	return nil
}

// globalCrashAdapter is the package-level adapter, default Noop.
var (
	crashAdapterMu sync.RWMutex
	crashAdapter   CrashReportAdapter = NoopCrashReporter{}
)

// SetCrashAdapter installs the global adapter. (OBS-035)
func SetCrashAdapter(a CrashReportAdapter) {
	crashAdapterMu.Lock()
	defer crashAdapterMu.Unlock()
	crashAdapter = a
}

// GetCrashAdapter returns the installed adapter.
func GetCrashAdapter() CrashReportAdapter {
	crashAdapterMu.RLock()
	defer crashAdapterMu.RUnlock()
	return crashAdapter
}

// ReportCrash dispatches a report to the global adapter. (OBS-035)
func ReportCrash(ctx context.Context, rep CrashReport) {
	_ = GetCrashAdapter().Report(ctx, rep)
}

// PanicToSpan records a panic value onto the active span and forwards
// the crash report. Use from a recover() block at the top of a handler
// or supervisor task. (OBS-036)
func PanicToSpan(ctx context.Context, recovered any) {
	sp := oteltrace.SpanFromContext(ctx)
	if sp != nil {
		sp.SetStatus(codes.Error, fmt.Sprintf("panic: %v", recovered))
		sp.AddEvent("panic")
	}
	rep := CrashReport{
		Severity:  "fatal",
		Message:   fmt.Sprintf("panic: %v", recovered),
		Stack:     string(debug.Stack()),
		Timestamp: now(),
		TraceID:   TraceIDFromContext(ctx),
		SpanID:    SpanIDFromContext(ctx),
	}
	ReportCrash(ctx, rep)
}

// ErrToSpan records a non-panic error onto the active span as an error
// event with attrs. (OBS-036)
func ErrToSpan(ctx context.Context, err error) {
	if err == nil {
		return
	}
	sp := oteltrace.SpanFromContext(ctx)
	if sp == nil {
		return
	}
	sp.RecordError(err)
}

// CatchPanic wraps fn in a recover() that calls PanicToSpan and re-raises
// the panic. Use this when callers want a panic to propagate while
// still recording it on the active span. (OBS-036)
func CatchPanic(ctx context.Context, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			PanicToSpan(ctx, r)
			panic(r)
		}
	}()
	fn()
}

// CatchPanicNoReraise is CatchPanic without the re-panic. Use for
// supervisor tasks that already log panics via the runtime package.
func CatchPanicNoReraise(ctx context.Context, fn func()) (recovered any) {
	defer func() {
		if r := recover(); r != nil {
			PanicToSpan(ctx, r)
			recovered = r
		}
	}()
	fn()
	return nil
}

// errors.As shim: panic values may not implement error.
var errPanicRecovered = errors.New("obs: panic recovered")

// bytesReader avoids importing bytes.Reader (we don't otherwise need it).
func bytesReader(b []byte) io.Reader { return &simpleReader{b: b} }

type simpleReader struct{ b []byte }

func (s *simpleReader) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, ioEOF
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

var ioEOF = errors.New("EOF")
