// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// modules/lifecycle.go: module lifecycle hooks (MOD-012). Hooks fire in
// topological (init) order on startup and reverse order on shutdown.

package modules

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// HookKind enumerates the lifecycle hook points.
type HookKind string

const (
	HookInit     HookKind = "init"     // before the module's contributions are activated
	HookStart    HookKind = "start"    // after all modules are wired, before serving traffic
	HookStop     HookKind = "stop"     // on drain, before the module's deps shut down
	HookShutdown HookKind = "shutdown" // after stop; final cleanup
)

// Hooks is the set of callbacks a module registers. All are optional. The
// context passed to Start/Stop is cancelled when shutdown begins.
type Hooks struct {
	Init     func(ctx context.Context) error
	Start    func(ctx context.Context) error
	Stop     func(ctx context.Context) error
	Shutdown func(ctx context.Context) error
}

// HookRegistration pairs a module name with its Hooks.
type HookRegistration struct {
	Module string
	Hooks  Hooks
}

// Lifecycle drives the module lifecycle: Init → Start → (serve) → Stop →
// Shutdown. Each phase runs hooks in init (or reverse-init) order. A failure
// in any phase aborts and triggers rollback of the already-started modules.
type Lifecycle struct {
	mu            sync.Mutex
	registrations []HookRegistration
	// started tracks modules whose Start hook has fired; used for rollback.
	started []string
	// inited tracks modules whose Init hook has fired.
	inited []string
}

// NewLifecycle returns an empty lifecycle driver.
func NewLifecycle() *Lifecycle {
	return &Lifecycle{}
}

// Register attaches a module's hooks. Returns an error if the module is
// already registered.
func (l *Lifecycle) Register(name string, h Hooks) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.registrations {
		if r.Module == name {
			return fmt.Errorf("modules: lifecycle hooks already registered for %q", name)
		}
	}
	l.registrations = append(l.registrations, HookRegistration{Module: name, Hooks: h})
	return nil
}

// Init fires every Init hook in init order (MOD-012). On error, already-init'd
// modules have their Shutdown hook invoked (best-effort).
func (l *Lifecycle) Init(ctx context.Context, r *Registry) error {
	if r == nil {
		return ErrNoRegistry
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	order, err := r.InitOrder()
	if err != nil {
		return fmt.Errorf("modules: lifecycle init: %w", err)
	}
	for _, m := range order {
		reg := l.find(m.Name)
		if reg == nil || reg.Hooks.Init == nil {
			continue
		}
		if err := reg.Hooks.Init(ctx); err != nil {
			// Rollback init'd modules in reverse.
			l.rollbackInit(ctx)
			return fmt.Errorf("modules: init %q: %w", m.Name, err)
		}
		l.inited = append(l.inited, m.Name)
	}
	return nil
}

// Start fires every Start hook in init order. On error, already-started
// modules have their Stop hook invoked (best-effort) and Init'd modules their
// Shutdown hook.
func (l *Lifecycle) Start(ctx context.Context, r *Registry) error {
	if r == nil {
		return ErrNoRegistry
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	order, err := r.InitOrder()
	if err != nil {
		return fmt.Errorf("modules: lifecycle start: %w", err)
	}
	for _, m := range order {
		reg := l.find(m.Name)
		if reg == nil || reg.Hooks.Start == nil {
			continue
		}
		if err := reg.Hooks.Start(ctx); err != nil {
			// Rollback started modules in reverse.
			l.rollbackStart(ctx)
			l.rollbackInit(ctx)
			return fmt.Errorf("modules: start %q: %w", m.Name, err)
		}
		l.started = append(l.started, m.Name)
	}
	return nil
}

// Stop fires every Stop hook in reverse init order (DI-013-style).
// Errors are collected; the first error is returned.
func (l *Lifecycle) Stop(ctx context.Context, r *Registry) error {
	if r == nil {
		return ErrNoRegistry
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	order, err := r.ShutdownOrder()
	if err != nil {
		return fmt.Errorf("modules: lifecycle stop: %w", err)
	}
	var firstErr error
	for _, m := range order {
		reg := l.find(m.Name)
		if reg == nil || reg.Hooks.Stop == nil {
			continue
		}
		if err := reg.Hooks.Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("modules: stop %q: %w", m.Name, err)
		}
	}
	l.started = l.started[:0]
	return firstErr
}

// Shutdown fires every Shutdown hook in reverse init order, after Stop.
func (l *Lifecycle) Shutdown(ctx context.Context, r *Registry) error {
	if r == nil {
		return ErrNoRegistry
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	order, err := r.ShutdownOrder()
	if err != nil {
		return fmt.Errorf("modules: lifecycle shutdown: %w", err)
	}
	var firstErr error
	for _, m := range order {
		reg := l.find(m.Name)
		if reg == nil || reg.Hooks.Shutdown == nil {
			continue
		}
		if err := reg.Hooks.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("modules: shutdown %q: %w", m.Name, err)
		}
	}
	l.inited = l.inited[:0]
	return firstErr
}

// find returns the registration for a module name, or nil.
func (l *Lifecycle) find(name string) *HookRegistration {
	for i := range l.registrations {
		if l.registrations[i].Module == name {
			return &l.registrations[i]
		}
	}
	return nil
}

// rollbackStart fires Stop on already-started modules in reverse order.
func (l *Lifecycle) rollbackStart(ctx context.Context) {
	for i := len(l.started) - 1; i >= 0; i-- {
		name := l.started[i]
		reg := l.find(name)
		if reg == nil || reg.Hooks.Stop == nil {
			continue
		}
		_ = reg.Hooks.Stop(ctx)
	}
	l.started = l.started[:0]
}

// rollbackInit fires Shutdown on already-init'd modules in reverse order.
func (l *Lifecycle) rollbackInit(ctx context.Context) {
	for i := len(l.inited) - 1; i >= 0; i-- {
		name := l.inited[i]
		reg := l.find(name)
		if reg == nil || reg.Hooks.Shutdown == nil {
			continue
		}
		_ = reg.Hooks.Shutdown(ctx)
	}
	l.inited = l.inited[:0]
}

// ErrNoRegistry is returned when a lifecycle method is called with a nil registry.
var ErrNoRegistry = errors.New("modules: nil registry")
