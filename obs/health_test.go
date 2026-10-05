// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package obs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthService_LivenessUp(t *testing.T) {
	h := NewHealthService(HealthConfig{})
	mux := http.NewServeMux()
	h.Mount(mux)
	h.MarkStarted()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != HealthUp {
		t.Errorf("status = %v, want up", resp.Status)
	}
}

func TestHealthService_LivenessStarting(t *testing.T) {
	h := NewHealthService(HealthConfig{})
	mux := http.NewServeMux()
	h.Mount(mux)
	// Don't call MarkStarted → startup should report starting.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	var resp HealthResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != HealthStarting {
		t.Errorf("status = %v, want starting", resp.Status)
	}
}

func TestHealthService_LivenessDown(t *testing.T) {
	h := NewHealthService(HealthConfig{
		Liveness: []HealthCheck{
			{Name: "self", Run: func(_ context.Context) error { return nil }},
			{Name: "broken", Run: func(_ context.Context) error { return errors.New("nope") }},
		},
	})
	h.MarkStarted()
	mux := http.NewServeMux()
	h.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	var resp HealthResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != HealthDown {
		t.Errorf("status = %v, want down", resp.Status)
	}
	if resp.Checks["broken"].Error != "nope" {
		t.Errorf("error text = %v", resp.Checks["broken"].Error)
	}
}

func TestHealthService_ReadinessOK(t *testing.T) {
	h := NewHealthService(HealthConfig{
		Readiness: []HealthCheck{
			{Name: "db", Run: func(_ context.Context) error { return nil }},
		},
	})
	h.MarkStarted()
	mux := http.NewServeMux()
	h.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHealthService_Startup(t *testing.T) {
	h := NewHealthService(HealthConfig{
		Startup: []HealthCheck{
			{Name: "migrations", Run: func(_ context.Context) error { return nil }},
		},
	})
	mux := http.NewServeMux()
	h.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/healthz/startup", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHealthService_Auth(t *testing.T) {
	h := NewHealthService(HealthConfig{AuthToken: "secret"})
	mux := http.NewServeMux()
	h.Mount(mux)

	// No token: 401.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no-token status = %d, want 401", w.Code)
	}

	// Wrong token: 401.
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong-token status = %d, want 401", w.Code)
	}

	// Right token: 200 (after MarkStarted).
	h.MarkStarted()
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("right-token status = %d, want 200", w.Code)
	}
}

func TestHealthService_CheckTimeout(t *testing.T) {
	h := NewHealthService(HealthConfig{
		Readiness: []HealthCheck{
			{Name: "slow", Run: func(ctx context.Context) error {
				select {
				case <-time.After(200 * time.Millisecond):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}},
		},
		Timeout: 50 * time.Millisecond,
	})
	h.MarkStarted()
	mux := http.NewServeMux()
	h.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (timeout)", w.Code)
	}
}
