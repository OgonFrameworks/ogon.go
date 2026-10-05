// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for modules/conflict.go (MOD-014) and modules/lifecycle.go
// (MOD-012): conflict detection and lifecycle hook ordering.

package modules

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDetectConflictsFindsRouteConflict(t *testing.T) {
	t.Parallel()
	contribs := []Contribution{
		{Module: "a", Kind: ConflictRoute, Key: "GET /users"},
		{Module: "b", Kind: ConflictRoute, Key: "GET /users"},
	}
	cs := DetectConflicts(contribs)
	if !cs.HasConflicts() {
		t.Errorf("HasConflicts = false")
	}
	if len(cs.Route) != 1 {
		t.Errorf("Route conflicts = %d, want 1", len(cs.Route))
	}
	if len(cs.Route[0].Modules) != 2 {
		t.Errorf("modules = %v", cs.Route[0].Modules)
	}
}

func TestDetectConflictsSeparatesByKind(t *testing.T) {
	t.Parallel()
	contribs := []Contribution{
		{Module: "a", Kind: ConflictRoute, Key: "GET /x"},
		{Module: "b", Kind: ConflictRoute, Key: "GET /x"},
		{Module: "a", Kind: ConflictConfig, Key: "feature.flag"},
		{Module: "b", Kind: ConflictConfig, Key: "feature.flag"},
		{Module: "a", Kind: ConflictCLI, Key: "mycmd"},
		{Module: "b", Kind: ConflictCLI, Key: "mycmd"},
	}
	cs := DetectConflicts(contribs)
	if len(cs.Route) != 1 || len(cs.Config) != 1 || len(cs.CLI) != 1 {
		t.Errorf("counts: route=%d config=%d cli=%d", len(cs.Route), len(cs.Config), len(cs.CLI))
	}
}

func TestDetectConflictsSameModuleNoConflict(t *testing.T) {
	t.Parallel()
	contribs := []Contribution{
		{Module: "a", Kind: ConflictRoute, Key: "GET /x"},
		{Module: "a", Kind: ConflictRoute, Key: "GET /x"}, // same module — no conflict
	}
	cs := DetectConflicts(contribs)
	if cs.HasConflicts() {
		t.Errorf("HasConflicts = true (same module dupes are not conflicts)")
	}
}

func TestDetectConflictsAllSorted(t *testing.T) {
	t.Parallel()
	contribs := []Contribution{
		{Module: "a", Kind: ConflictRoute, Key: "z"},
		{Module: "b", Kind: ConflictRoute, Key: "z"},
		{Module: "a", Kind: ConflictRoute, Key: "a"},
		{Module: "b", Kind: ConflictRoute, Key: "a"},
	}
	cs := DetectConflicts(contribs)
	all := cs.All()
	if len(all) != 2 {
		t.Fatalf("len = %d", len(all))
	}
	if all[0].Key != "a" {
		t.Errorf("first = %q, want a", all[0].Key)
	}
}

func TestConflictString(t *testing.T) {
	t.Parallel()
	c := Conflict{Kind: ConflictRoute, Key: "GET /x", Modules: []string{"a", "b"}}
	if !strings.Contains(c.String(), "GET /x") || !strings.Contains(c.String(), "a") {
		t.Errorf("String() = %q", c.String())
	}
}

func TestDetectConflictsFromRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	m1 := m("a", "1.0.0", ">=1.8")
	m1.Provides = []string{ContributionConfig}
	m2 := m("b", "1.0.0", ">=1.8")
	m2.Provides = []string{ContributionConfig}
	_ = r.Register(m1, "1.8.0")
	_ = r.Register(m2, "1.8.0")
	cs := DetectConflictsFromRegistry(r)
	// Each module owns its own config namespace; no conflicts by construction.
	if cs.HasConflicts() {
		t.Errorf("expected no conflicts, got %v", cs.All())
	}
}

// ---- lifecycle tests ----

func TestLifecycleInitStartStopShutdownOrder(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	_ = r.Register(m("b", "1.0.0", ">=1.8", "a"), "1.8.0")
	_ = r.Register(m("c", "1.0.0", ">=1.8", "b"), "1.8.0")

	var trace []string
	lc := NewLifecycle()
	_ = lc.Register("a", Hooks{
		Init:     func(ctx context.Context) error { trace = append(trace, "init-a"); return nil },
		Start:    func(ctx context.Context) error { trace = append(trace, "start-a"); return nil },
		Stop:     func(ctx context.Context) error { trace = append(trace, "stop-a"); return nil },
		Shutdown: func(ctx context.Context) error { trace = append(trace, "shutdown-a"); return nil },
	})
	_ = lc.Register("b", Hooks{
		Init:     func(ctx context.Context) error { trace = append(trace, "init-b"); return nil },
		Start:    func(ctx context.Context) error { trace = append(trace, "start-b"); return nil },
		Stop:     func(ctx context.Context) error { trace = append(trace, "stop-b"); return nil },
		Shutdown: func(ctx context.Context) error { trace = append(trace, "shutdown-b"); return nil },
	})
	_ = lc.Register("c", Hooks{
		Init:     func(ctx context.Context) error { trace = append(trace, "init-c"); return nil },
		Start:    func(ctx context.Context) error { trace = append(trace, "start-c"); return nil },
		Stop:     func(ctx context.Context) error { trace = append(trace, "stop-c"); return nil },
		Shutdown: func(ctx context.Context) error { trace = append(trace, "shutdown-c"); return nil },
	})

	ctx := context.Background()
	if err := lc.Init(ctx, r); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := lc.Start(ctx, r); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := lc.Stop(ctx, r); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := lc.Shutdown(ctx, r); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	want := []string{
		"init-a", "init-b", "init-c",
		"start-a", "start-b", "start-c",
		"stop-c", "stop-b", "stop-a", // reverse init
		"shutdown-c", "shutdown-b", "shutdown-a",
	}
	if len(trace) != len(want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}
	for i, w := range want {
		if trace[i] != w {
			t.Errorf("trace[%d] = %q, want %q (full: %v)", i, trace[i], w, trace)
		}
	}
}

func TestLifecycleInitRollbackOnError(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	_ = r.Register(m("b", "1.0.0", ">=1.8", "a"), "1.8.0")
	var shutdownA int32
	lc := NewLifecycle()
	_ = lc.Register("a", Hooks{
		Init:     func(ctx context.Context) error { return nil },
		Shutdown: func(ctx context.Context) error { atomic.AddInt32(&shutdownA, 1); return nil },
	})
	boom := errors.New("boom")
	_ = lc.Register("b", Hooks{
		Init: func(ctx context.Context) error { return boom },
	})
	err := lc.Init(context.Background(), r)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if atomic.LoadInt32(&shutdownA) != 1 {
		t.Errorf("shutdownA = %d, want 1 (rollback)", shutdownA)
	}
}

func TestLifecycleStartRollbackOnError(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	_ = r.Register(m("b", "1.0.0", ">=1.8", "a"), "1.8.0")
	var stopA, shutdownA int32
	lc := NewLifecycle()
	_ = lc.Register("a", Hooks{
		Init:     func(ctx context.Context) error { return nil },
		Start:    func(ctx context.Context) error { return nil },
		Stop:     func(ctx context.Context) error { atomic.AddInt32(&stopA, 1); return nil },
		Shutdown: func(ctx context.Context) error { atomic.AddInt32(&shutdownA, 1); return nil },
	})
	boom := errors.New("boom")
	_ = lc.Register("b", Hooks{
		Init:  func(ctx context.Context) error { return nil },
		Start: func(ctx context.Context) error { return boom },
	})
	_ = lc.Init(context.Background(), r)
	err := lc.Start(context.Background(), r)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if atomic.LoadInt32(&stopA) != 1 {
		t.Errorf("stopA = %d, want 1", stopA)
	}
	if atomic.LoadInt32(&shutdownA) != 1 {
		t.Errorf("shutdownA = %d, want 1", shutdownA)
	}
}

func TestLifecycleRegisterDuplicate(t *testing.T) {
	t.Parallel()
	lc := NewLifecycle()
	_ = lc.Register("a", Hooks{})
	if err := lc.Register("a", Hooks{}); err == nil {
		t.Errorf("expected duplicate registration error")
	}
}

func TestLifecycleStopCollectsFirstError(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(m("a", "1.0.0", ">=1.8"), "1.8.0")
	_ = r.Register(m("b", "1.0.0", ">=1.8", "a"), "1.8.0")
	lc := NewLifecycle()
	first := errors.New("first-stop")
	_ = lc.Register("a", Hooks{
		Stop: func(ctx context.Context) error { return errors.New("second-stop") },
	})
	_ = lc.Register("b", Hooks{
		Stop: func(ctx context.Context) error { return first },
	})
	_ = lc.Init(context.Background(), r)
	_ = lc.Start(context.Background(), r)
	// Stop order is reverse-init: b → a, so b's "first-stop" wins.
	err := lc.Stop(context.Background(), r)
	if !errors.Is(err, first) {
		t.Errorf("err = %v, want first-stop (first error wins)", err)
	}
}

func TestLifecycleNilRegistryErrors(t *testing.T) {
	t.Parallel()
	lc := NewLifecycle()
	if err := lc.Init(context.Background(), nil); err == nil {
		t.Errorf("expected error for nil registry")
	}
}

func TestErrNoRegistry(t *testing.T) {
	t.Parallel()
	if ErrNoRegistry == nil {
		t.Fatal("ErrNoRegistry is nil")
	}
	if ErrNoRegistry.Error() == "" {
		t.Error("ErrNoRegistry has no message")
	}
}
