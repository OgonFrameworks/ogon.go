// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Health endpoints: /healthz (aggregate liveness), /readyz (readiness with
// dependency checks), /healthz/startup (startup probe). Probe routes are
// excluded from sampling (OBS-019/020/021). Optional internal auth (OBS-022).

package obs

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// HealthStatus is the per-probe verdict.
type HealthStatus string

const (
	HealthUp       HealthStatus = "up"
	HealthDown     HealthStatus = "down"
	HealthStarting HealthStatus = "starting"
	HealthUnknown  HealthStatus = "unknown"
)

// HealthResponse is the JSON body returned by /healthz. (OBS-019)
type HealthResponse struct {
	Status   HealthStatus                 `json:"status"`
	Checks   map[string]HealthCheckResult `json:"checks,omitempty"`
	Version  string                       `json:"version,omitempty"`
	Revision string                       `json:"revision,omitempty"`
	Uptime   time.Duration                `json:"uptime_ms"`
	Runtime  runtimeStatsJSON             `json:"runtime,omitempty"`
}

type runtimeStatsJSON struct {
	Goroutines  int `json:"goroutines"`
	AllocMB     int `json:"alloc_mb"`
	SysMB       int `json:"sys_mb"`
	HeapObjects int `json:"heap_objects"`
}

// HealthCheckResult is a single dependency probe result.
type HealthCheckResult struct {
	Status  HealthStatus  `json:"status"`
	Error   string        `json:"error,omitempty"`
	Latency time.Duration `json:"latency_ms"`
}

// HealthCheckFunc returns nil if the dependency is healthy, otherwise an
// error whose Error() text becomes the check's `error` field.
type HealthCheckFunc func(ctx context.Context) error

// HealthConfig configures the aggregate /healthz, /readyz, and
// /healthz/startup endpoints. (OBS-019/020/021)
type HealthConfig struct {
	// Liveness checks run on /healthz. They MUST be cheap and must not
	// depend on external resources. (OBS-019)
	Liveness []HealthCheck
	// Readiness checks run on /readyz and confirm dependencies are
	// reachable. (OBS-020)
	Readiness []HealthCheck
	// Startup checks run on /healthz/startup and are used by the K8s
	// startup probe to gate traffic until init is done. (OBS-021)
	Startup []HealthCheck
	// Started returns true once the app has finished init. Until then,
	// /healthz returns 503 (Kubernetes convention). (OBS-019)
	Started func() bool
	// AuthToken is an optional bearer token; when set, probe requests
	// must carry `Authorization: Bearer <AuthToken>` or be rejected with
	// 401. Useful for sealed networks where probes are scraped by a
	// sidecar rather than kubelet. (OBS-022)
	AuthToken string
	// Version + Revision are surfaced on /healthz. (OBS-033)
	Version  string
	Revision string
	// Timeout caps each check. Default 1s.
	Timeout time.Duration
	// Logger is the sink for probe-access logs (separate from the request
	// logger so probes don't pollute the access log).
	Logger ProbeLogger
}

// HealthCheck pairs a name with a probe.
type HealthCheck struct {
	Name string
	Run  HealthCheckFunc
}

// ProbeLogger lets tests capture probe access. (OBS-022)
type ProbeLogger interface {
	LogProbe(method, path string, status int, dur time.Duration)
}

// HealthService owns the three endpoints. (OBS-019/020/021)
type HealthService struct {
	cfg         HealthConfig
	startTime   time.Time
	startedOnce sync.Once
	mu          sync.Mutex
	startedFlag bool
}

// NewHealthService builds the service. (OBS-019)
func NewHealthService(cfg HealthConfig) *HealthService {
	if cfg.Timeout == 0 {
		cfg.Timeout = time.Second
	}
	if cfg.Started == nil {
		cfg.Started = func() bool { return true }
	}
	return &HealthService{cfg: cfg, startTime: now()}
}

// MarkStarted transitions the service from starting → started. After
// this, /healthz returns 200 unless a liveness check fails. (OBS-019)
func (h *HealthService) MarkStarted() {
	h.mu.Lock()
	h.startedFlag = true
	h.mu.Unlock()
}

// IsStarted reports whether MarkStarted has been called. (OBS-019)
func (h *HealthService) IsStarted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startedFlag
}

// Mount registers the three endpoints on mux. (OBS-019/020/021)
func (h *HealthService) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.liveness)
	mux.HandleFunc("/readyz", h.readiness)
	mux.HandleFunc("/healthz/startup", h.startup)
}

// liveness handler. Returns 200 unless Started()==false or a liveness
// check returns an error. (OBS-019)
func (h *HealthService) liveness(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	start := now()
	resp := h.runChecks(h.cfg.Liveness, h.cfg.Timeout)
	resp.Uptime = now().Sub(h.startTime)
	resp.Version = h.cfg.Version
	resp.Revision = h.cfg.Revision

	status := resp.Status
	if !h.IsStarted() {
		status = HealthStarting
	}
	resp.Status = status
	resp.Runtime = snapshotRuntimeJSON()

	code := http.StatusOK
	if status == HealthDown {
		code = http.StatusServiceUnavailable
	} else if status == HealthStarting {
		code = http.StatusServiceUnavailable
	}
	h.logProbe(r, code, now().Sub(start))
	writeJSON(w, code, resp)
}

// readiness handler. (OBS-020)
func (h *HealthService) readiness(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	start := now()
	resp := h.runChecks(h.cfg.Readiness, h.cfg.Timeout)
	resp.Uptime = now().Sub(h.startTime)
	resp.Version = h.cfg.Version
	resp.Revision = h.cfg.Revision
	resp.Runtime = snapshotRuntimeJSON()

	code := http.StatusOK
	if resp.Status != HealthUp {
		code = http.StatusServiceUnavailable
	}
	h.logProbe(r, code, now().Sub(start))
	writeJSON(w, code, resp)
}

// startup handler. (OBS-021)
func (h *HealthService) startup(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	start := now()
	resp := h.runChecks(h.cfg.Startup, h.cfg.Timeout)
	resp.Uptime = now().Sub(h.startTime)
	resp.Version = h.cfg.Version
	resp.Revision = h.cfg.Revision

	code := http.StatusOK
	if resp.Status != HealthUp {
		code = http.StatusServiceUnavailable
	}
	h.logProbe(r, code, now().Sub(start))
	writeJSON(w, code, resp)
}

// checkAuth validates the bearer token when cfg.AuthToken is set.
// Returns false if the request was already rejected. (OBS-022)
func (h *HealthService) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.cfg.AuthToken == "" {
		return true
	}
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || strings.TrimPrefix(header, prefix) != h.cfg.AuthToken {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"status": "unauthorized"})
		return false
	}
	return true
}

// logProbe forwards the probe event to the optional ProbeLogger. (OBS-022)
func (h *HealthService) logProbe(r *http.Request, status int, dur time.Duration) {
	if h.cfg.Logger == nil {
		return
	}
	h.cfg.Logger.LogProbe(r.Method, r.URL.Path, status, dur)
}

// runChecks runs each check under the timeout and returns the aggregate.
func (h *HealthService) runChecks(checks []HealthCheck, timeout time.Duration) HealthResponse {
	resp := HealthResponse{Checks: make(map[string]HealthCheckResult, len(checks))}
	status := HealthUp
	for _, c := range checks {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		start := now()
		err := c.Run(ctx)
		cancel()
		lat := now().Sub(start)
		res := HealthCheckResult{Status: HealthUp, Latency: lat}
		if err != nil {
			res.Status = HealthDown
			res.Error = err.Error()
			status = HealthDown
		}
		resp.Checks[c.Name] = res
	}
	resp.Status = status
	return resp
}

func snapshotRuntimeJSON() runtimeStatsJSON {
	if runtimeMx == nil {
		return runtimeStatsJSON{}
	}
	s := RuntimeSnapshot()
	return runtimeStatsJSON{
		Goroutines:  s.Goroutines,
		AllocMB:     int(s.AllocBytes / 1024 / 1024),
		SysMB:       int(s.SysBytes / 1024 / 1024),
		HeapObjects: int(s.HeapObjects),
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "")
	_ = enc.Encode(body)
}

// envHealthAuthToken reads the probe auth token from OGON_HEALTH_AUTH_TOKEN.
// When empty, probe routes are unauthenticated. (OBS-022)
func envHealthAuthToken(env map[string]string) string {
	return env["OGON_HEALTH_AUTH_TOKEN"]
}

// HealthFromEnv returns a HealthConfig with probe auth wired from env.
func HealthFromEnv(env map[string]string) HealthConfig {
	cfg := HealthConfig{}
	if t := envHealthAuthToken(env); t != "" {
		cfg.AuthToken = t
	}
	return cfg
}

// Hostname returns the OS hostname or "" if unavailable. (OBS-033)
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}
