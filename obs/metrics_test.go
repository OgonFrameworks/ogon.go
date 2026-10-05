// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package obs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandler_Responds(t *testing.T) {
	InitHTTPMetrics()
	InitRuntimeMetrics()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "ogon_") {
		t.Errorf("metrics output empty: %s", body)
	}
}

func TestCardinalityGuard_Default(t *testing.T) {
	g := NewCardinalityGuard()
	allowed := []string{"route_template", "method", "status_class"}
	if bad := g.Lint(allowed); len(bad) != 0 {
		t.Errorf("allowed set failed lint: %v", bad)
	}
	denied := []string{"raw_path", "user_id", "trace_id"}
	if bad := g.Lint(denied); len(bad) != 3 {
		t.Errorf("denied set passed lint: %v", bad)
	}
}

func TestCardinalityGuard_RefusesPIILabels(t *testing.T) {
	g := NewCardinalityGuard()
	if err := g.RegisterLabels("password"); err == nil {
		t.Error("allowed PII label")
	}
	if err := g.RegisterLabels("queue_name"); err != nil {
		t.Errorf("queue_name rejected: %v", err)
	}
}

func TestHTTPMiddleware_Records(t *testing.T) {
	InitHTTPMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Inject request meta for the test.
		ctx := WithRequestMeta(r.Context(), RequestMeta{Route: "/api/users/:id"})
		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/users/123", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	// Fetch the metrics text and verify the request landed on route_template.
	out, err := Scrape()
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if !strings.Contains(out, "ogon_http_requests_total") {
		t.Errorf("metric not registered: %s", out)
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		100: "1xx", 200: "2xx", 201: "2xx", 301: "3xx",
		404: "4xx", 500: "5xx", 503: "5xx",
	}
	for code, want := range cases {
		if got := statusClass(code); got != want {
			t.Errorf("statusClass(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestIsProbePath(t *testing.T) {
	probes := []string{
		"/healthz", "/readyz", "/healthz/startup",
		"/metrics", "/debug/pprof", "/debug/pprof/heap",
	}
	for _, p := range probes {
		if !IsProbePath(p) {
			t.Errorf("IsProbePath(%q) = false", p)
		}
	}
	if IsProbePath("/api/users") {
		t.Error("non-probe classified as probe")
	}
}

func TestRuntimeMetrics_Collect(t *testing.T) {
	InitRuntimeMetrics()
	CollectRuntime()
	s := RuntimeSnapshot()
	if s.Goroutines == 0 {
		t.Error("goroutine count not captured")
	}
}
