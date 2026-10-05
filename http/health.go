// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Health endpoints: /healthz (liveness), /readyz (readiness),
// /healthz/startup (startup probes). Each is a separate probe so
// Kubernetes can distinguish boot-up from steady-state readiness.

package http

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// HealthStatus is one probe's current state.
type HealthStatus int

const (
	HealthUnknown HealthStatus = iota
	HealthStarting
	HealthUp
	HealthDegraded
	HealthDown
)

// String returns the canonical string for the probe status.
func (h HealthStatus) String() string {
	switch h {
	case HealthStarting:
		return "starting"
	case HealthUp:
		return "up"
	case HealthDegraded:
		return "degraded"
	case HealthDown:
		return "down"
	}
	return "unknown"
}

// HealthProbe tracks one probe's state and dependencies.
type HealthProbe struct {
	name      string
	status    atomic.Int64
	startedAt time.Time
	checks    []func() error
}

// NewHealthProbe constructs a probe with the supplied name and checks.
func NewHealthProbe(name string, checks ...func() error) *HealthProbe {
	p := &HealthProbe{
		name:      name,
		startedAt: time.Now(),
		checks:    checks,
	}
	p.status.Store(int64(HealthStarting))
	return p
}

// Set sets the probe status.
func (p *HealthProbe) Set(s HealthStatus) {
	p.status.Store(int64(s))
}

// Status returns the current status; runs the registered checks lazily.
func (p *HealthProbe) Status() HealthStatus {
	if p == nil {
		return HealthUnknown
	}
	cur := HealthStatus(p.status.Load())
	if cur == HealthDown || cur == HealthStarting {
		return cur
	}
	// Run the checks; first failure → degraded.
	for _, fn := range p.checks {
		if fn != nil {
			if err := fn(); err != nil {
				return HealthDegraded
			}
		}
	}
	return cur
}

// uptime returns the probe's lifetime.
func (p *HealthProbe) uptime() time.Duration {
	return time.Since(p.startedAt)
}

// HealthService owns the three probes: liveness, readiness, startup.
type HealthService struct {
	Liveness *HealthProbe
	Ready    *HealthProbe
	Startup  *HealthProbe
}

// NewHealthService returns a service with all three probes initialized to
// starting. Call SetReady() when the app finishes booting.
func NewHealthService() *HealthService {
	return &HealthService{
		Liveness: NewHealthProbe("liveness"),
		Ready:    NewHealthProbe("readiness"),
		Startup:  NewHealthProbe("startup"),
	}
}

// SetReady marks liveness and startup as Up and readiness as Up. Called by
// the runtime when StateReady is reached.
func (h *HealthService) SetReady() {
	h.Liveness.Set(HealthUp)
	h.Ready.Set(HealthUp)
	h.Startup.Set(HealthUp)
}

// Mount registers the /healthz, /readyz, /healthz/startup routes on the
// supplied router. The routes are unauthenticated; LBs probe them directly.
func (h *HealthService) Mount(r *Router) {
	r.MapGet("/healthz", h.livenessHandler)
	r.MapGet("/readyz", h.readyHandler)
	r.MapGet("/healthz/startup", h.startupHandler)
}

// livenessHandler returns 200 once the process has finished starting and is
// not in an explicitly Down state. Kubernetes uses this to decide restarts:
// 503 here means "not yet alive" or "explicitly down".
func (h *HealthService) livenessHandler(c *Ctx) error {
	s := h.Liveness.Status()
	code := http.StatusOK
	if s == HealthDown || s == HealthStarting {
		code = http.StatusServiceUnavailable
	}
	c.Status(code)
	return c.JSON(map[string]any{
		"status":   s.String(),
		"uptime_s": int(h.Liveness.uptime().Seconds()),
	})
}

// readyHandler returns 200 only when fully ready.
func (h *HealthService) readyHandler(c *Ctx) error {
	s := h.Ready.Status()
	code := http.StatusOK
	if s != HealthUp {
		code = http.StatusServiceUnavailable
	}
	c.Status(code)
	return c.JSON(map[string]any{
		"status": s.String(),
	})
}

// startupHandler returns 200 once startup completes.
func (h *HealthService) startupHandler(c *Ctx) error {
	s := h.Startup.Status()
	code := http.StatusOK
	if s == HealthStarting {
		code = http.StatusServiceUnavailable
	}
	c.Status(code)
	return c.JSON(map[string]any{
		"status": s.String(),
	})
}

// jsonEncode shim for the few places we need direct JSON encoding outside
// the codec (used by tests).
var _ = json.Marshal
