// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// OgonGo public entrypoint: App, Boot, Provide, and lifecycle.

package ogon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/OgonFrameworks/ogon.go/runtime"
	"github.com/OgonFrameworks/ogon.go/runtime/limits"
)

// Version is the framework version. Stamped via ldflags in `ogon build`.
const Version = "1.0.0"

// State identifies the application's position in the lifecycle state
// machine described in PROMPT.md Part V.2.
type State int

const (
	StateNew State = iota
	StateConfigLoaded
	StateDIBuilt
	StateModulesInit
	StateListening
	StateReady
	StateShutdownRequested
	StateDraining
	StateHooksStop
	StateExited
)

// String returns the lower-case state name used in logs and JSON.
func (s State) String() string {
	switch s {
	case StateNew:
		return "new"
	case StateConfigLoaded:
		return "config_loaded"
	case StateDIBuilt:
		return "di_built"
	case StateModulesInit:
		return "modules_init"
	case StateListening:
		return "listening"
	case StateReady:
		return "ready"
	case StateShutdownRequested:
		return "shutdown_requested"
	case StateDraining:
		return "draining"
	case StateHooksStop:
		return "hooks_stop"
	case StateExited:
		return "exited"
	}
	return "unknown"
}

// BootOpts configures an App before it starts.
type BootOpts struct {
	// ConfigPath overrides the default ogon.yaml lookup.
	ConfigPath string
	// Env selects the environment file: ogon.<env>.yaml (dev/test/prod).
	Env string
	// Logger overrides the default slog.Logger.
	Logger *slog.Logger
	// Parent overrides the root context.
	Parent context.Context
	// DrainTimeout caps the shutdown wait. Defaults to 30s.
	DrainTimeout time.Duration
	// DisableRuntimeLimits turns off cgroup-based tuning (dev).
	DisableRuntimeLimits bool
	// Manual bypasses generated DI; the caller wires providers in main.go.
	Manual bool
}

// App is the root application coordinator. Public surface is intentionally
// small; the heavy lifting lives in subsystems.
type App struct {
	mu     sync.RWMutex
	state  atomic.Int64 // State
	opts   BootOpts
	log    *slog.Logger
	sup    *runtime.Supervisor
	limits limits.Limits

	startHooks []Hook
	stopHooks  []Hook

	providers map[string]any
}

// Hook is a lifecycle callback. Start hooks fire in registration order;
// stop hooks fire in reverse. Errors abort the start chain and trigger
// drain.
type Hook struct {
	Name string
	Run  func(ctx context.Context) error
}

// Boot constructs an App from the supplied options. It does not start
// servers; call Run for that. Boot is intentionally cheap — the goal is
// to make `ogon dev` boot under 100 ms for hello-world (PERF-013).
func Boot(opts BootOpts) (*App, error) {
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.DrainTimeout == 0 {
		opts.DrainTimeout = 30 * time.Second
	}
	a := &App{
		opts:      opts,
		log:       opts.Logger,
		sup:       runtime.NewSupervisor(opts.Parent, opts.Logger),
		providers: make(map[string]any),
	}
	a.state.Store(int64(StateNew))

	if !opts.DisableRuntimeLimits {
		l, err := limits.Detect()
		if err != nil {
			return nil, diag.Wrap(err, diag.Diag{
				Code:     "OGON-U0004",
				Title:    "failed to detect runtime limits",
				Remedy:   "set OGON_RUNTIME_LIMITS=off to bypass",
				Severity: diag.SeverityWarning,
			})
		}
		a.limits = l
		if _, _, err := limits.Apply(l); err != nil {
			return nil, diag.Wrap(err, diag.Diag{
				Code:  "OGON-U0004",
				Title: "failed to apply runtime limits",
			})
		}
	}
	a.state.Store(int64(StateConfigLoaded))
	return a, nil
}

// State returns the current lifecycle state. Safe for concurrent reads.
func (a *App) State() State { return State(a.state.Load()) }

// Log returns the app-level logger.
func (a *App) Log() *slog.Logger { return a.log }

// Supervisor returns the goroutine supervisor owning all spawned tasks.
// Calling code SHOULD spawn framework goroutines through this supervisor
// to honour OGON-CORE no-leak law (Part 0 rule 8).
func (a *App) Supervisor() *runtime.Supervisor { return a.sup }

// Limits returns the detected runtime limits. Use for `ogon explain runtime`.
func (a *App) Limits() limits.Limits { return a.limits }

// Provide registers a manual override for an interface or type. This is
// the escape hatch (DX-P4) for the generated DI container (Part V.5).
// Calling Provide after Run begins is a programmer error and panics.
//
// For type-safe overrides prefer ProvideT; Provide is retained for
// ergonomics and back-compat with the original DI-007 escape hatch.
func (a *App) Provide(name string, impl any) {
	if a.State() >= StateListening {
		panic("ogon: Provide called after Run")
	}
	a.providers[name] = impl
}

// Provider returns a registered override, or nil.
func (a *App) Provider(name string) any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.providers[name]
}

// ProvideT is the typed escape hatch (DI-007). It registers impl under name.
// The type parameter T preserves type safety at the call site; the underlying
// registry is still keyed by name (the override mechanism is intentionally
// string-keyed so generated code can ask for "the UserService override" by
// name without importing the concrete type).
//
// ProvideT is the recommended API for new code; Provide (with any) is
// retained for back-compat.
func ProvideT[T any](a *App, name string, impl T) {
	a.Provide(name, impl)
}

// ProviderT is the typed companion to ProvideT. It returns the registered
// override cast to T, plus a bool indicating whether an override was
// registered with type T.
func ProviderT[T any](a *App, name string) (T, bool) {
	v, ok := a.Provider(name).(T)
	return v, ok
}

// AddStartHook appends a startup hook. Hooks fire in registration order
// after DI is built and before listening.
func (a *App) AddStartHook(h Hook) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.startHooks = append(a.startHooks, h)
}

// AddStopHook appends a shutdown hook. Hooks fire LIFO on drain.
func (a *App) AddStopHook(h Hook) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopHooks = append(a.stopHooks, h)
}

// Run transitions the app through to StateReady, then blocks until ctx is
// cancelled, Stop is called, or a SIGINT/SIGTERM is received (CORE-019).
// Run is safe to call exactly once.
//
// Signal handling: Run traps SIGINT and SIGTERM internally so that the
// scaffolded main.go (`app.Run(ctx)`) drains cleanly without requiring every
// user to wire signal.NotifyContext themselves. Callers who want a different
// signal policy can cancel ctx themselves — Run will drain either way.
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !a.state.CompareAndSwap(int64(StateConfigLoaded), int64(StateDIBuilt)) &&
		a.State() != StateDIBuilt {
		return diag.New("OGON-U0003", "app already running",
			"Run called twice on same App")
	}
	for _, h := range a.startHooks {
		if err := h.Run(ctx); err != nil {
			return diag.Wrap(err, diag.Diag{
				Code:  "OGON-U0001",
				Title: "start hook failed: " + h.Name,
			})
		}
	}
	a.state.Store(int64(StateModulesInit))
	a.state.Store(int64(StateListening))
	a.state.Store(int64(StateReady))

	// Signal handling (CORE-019): SIGINT/SIGTERM trigger graceful shutdown.
	// Buffered to avoid dropping signals sent during the select race.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-ctx.Done():
		// Caller-cancelled (e.g. signal.NotifyContext, test timeout, parent shutdown).
	case <-sigCh:
		// OS signal — fall through to Stop().
	}
	return a.Stop()
}

// Stop triggers drain (idempotent, safe for concurrent callers).
func (a *App) Stop() error {
	if !a.state.CompareAndSwap(int64(StateReady), int64(StateShutdownRequested)) {
		// already shutting down or not ready
		return nil
	}
	a.state.Store(int64(StateDraining))
	var firstErr error
	for i := len(a.stopHooks) - 1; i >= 0; i-- {
		h := a.stopHooks[i]
		if err := h.Run(context.Background()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	a.state.Store(int64(StateHooksStop))
	if err := a.sup.Stop(a.opts.DrainTimeout); err != nil && firstErr == nil {
		firstErr = err
	}
	a.state.Store(int64(StateExited))
	return firstErr
}

// ErrNotRunning is returned when callers ask for runtime resources before Run.
var ErrNotRunning = errors.New("ogon: app not running")
