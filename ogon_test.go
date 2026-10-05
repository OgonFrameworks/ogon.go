// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the public ogon package.

package ogon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

func TestBootDefaults(t *testing.T) {
	t.Parallel()
	a, err := Boot(BootOpts{DisableRuntimeLimits: true})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if a.State() != StateConfigLoaded {
		t.Fatalf("state = %s, want config_loaded", a.State())
	}
	if a.Log() == nil {
		t.Fatal("logger must be set")
	}
	if a.opts.DrainTimeout != 30*time.Second {
		t.Fatalf("default drain = %v", a.opts.DrainTimeout)
	}
}

func TestProvideReadback(t *testing.T) {
	t.Parallel()
	a, _ := Boot(BootOpts{DisableRuntimeLimits: true})
	type Foo struct{ X int }
	a.Provide("foo", Foo{X: 42})
	got, ok := a.Provider("foo").(Foo)
	if !ok {
		t.Fatal("provider readback wrong type")
	}
	if got.X != 42 {
		t.Fatalf("X = %d", got.X)
	}
}

func TestRunTransitionsToReady(t *testing.T) {
	t.Parallel()
	a, _ := Boot(BootOpts{DisableRuntimeLimits: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	if a.State() != StateReady {
		t.Fatalf("state = %s, want ready", a.State())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if a.State() != StateExited {
		t.Fatalf("state = %s, want exited", a.State())
	}
}

func TestStopHooksLIFO(t *testing.T) {
	t.Parallel()
	a, _ := Boot(BootOpts{DisableRuntimeLimits: true})
	order := []int{}
	a.AddStartHook(Hook{Name: "s1", Run: func(ctx context.Context) error { order = append(order, 1); return nil }})
	a.AddStopHook(Hook{Name: "p1", Run: func(ctx context.Context) error { order = append(order, 10); return nil }})
	a.AddStopHook(Hook{Name: "p2", Run: func(ctx context.Context) error { order = append(order, 20); return nil }})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("order = %v", order)
	}
	if order[0] != 1 || order[1] != 20 || order[2] != 10 {
		t.Fatalf("LIFO stop order wrong: %v", order)
	}
}

func TestStartHookFailureAborts(t *testing.T) {
	t.Parallel()
	a, _ := Boot(BootOpts{DisableRuntimeLimits: true})
	boom := errors.New("boom")
	a.AddStartHook(Hook{Name: "fail", Run: func(ctx context.Context) error { return boom }})
	err := a.Run(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Wrap converts boom to a Diag with code OGON-U0001; original is reachable via Unwrap.
	var target *diag.Diag
	if !errors.As(err, &target) {
		t.Fatalf("expected *diag.Diag, got %T: %v", err, err)
	}
}

func TestStateString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    State
		want string
	}{
		{StateNew, "new"},
		{StateReady, "ready"},
		{StateExited, "exited"},
		{99, "unknown"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("State(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}
