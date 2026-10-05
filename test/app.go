// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// In-process App fixture (TEST-001/037). Boots an httptest.Server around an
// arbitrary http.Handler so tests can speak HTTP to the real stack without
// binding a port. Every piece of mutable state (tmp dir, env, db tx, recorded
// state) is owned by t.Cleanup so the fixture is parallel-safe.

package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
)

// App is the in-process fixture: an httptest.Server, optional isolated tmp
// dir, optional DB transaction, optional seed hooks, and an AuthRegistry of
// known roles. Call NewApp to construct one; the test never imports any
// framework subsystem directly.
type App struct {
	t       *testing.T
	server  *httptest.Server
	baseURL *url.URL
	tmpdir  string
	mu      sync.Mutex
	authed  map[string]string // role -> bearer header value
	dbFix   *DBFixture        // optional; nil when no DB is wired
	fakeclk *FakeClock        // optional; nil when no clock injected
	jobRun  *JobRunner        // optional; nil when no jobs wired
	stopped atomic.Bool
}

// AppOption mutates an App at construction time. Functional-options style
// so the fixture's surface stays small and discoverable.
type AppOption func(*App)

// WithHandler is the primary entrypoint: wrap any net/http.Handler in an
// in-process test server (TEST-001/037).
//
// Tests that already own an ogon/http Server can pass srv.Handler().
func WithHandler(h http.Handler) AppOption {
	return func(a *App) {
		srv := httptest.NewServer(h)
		a.server = srv
		u, err := url.Parse(srv.URL)
		if err != nil {
			a.t.Fatalf("ogontest: httptest URL parse: %v", err)
		}
		a.baseURL = u
		a.t.Cleanup(func() {
			srv.Close()
		})
	}
}

// WithTmpDir gives the App its own isolated tmp dir and registers cleanup
// (TEST-027). Idempotent: a second call returns the existing dir.
func WithTmpDir() AppOption {
	return func(a *App) {
		if a.tmpdir != "" {
			return
		}
		a.tmpdir = TmpDir(a.t)
	}
}

// WithDB wires a DB tx-rollback fixture (TEST-004). The supplied factory
// builds the per-test DBFixture; the fixture handles rollback on cleanup.
func WithDB(factory func(t *testing.T) *DBFixture) AppOption {
	return func(a *App) {
		a.dbFix = factory(a.t)
		a.t.Cleanup(func() {
			if a.dbFix != nil {
				a.dbFix.Close()
			}
		})
	}
}

// WithFakeClock injects a controllable clock into the app (TEST-007). The
// clock is accessible via App.FakeClock().
func WithFakeClock(now int64) AppOption {
	return func(a *App) {
		a.fakeclk = NewFakeClock(now)
	}
}

// WithJobRunner registers a synchronous job runner (TEST-008).
func WithJobRunner() AppOption {
	return func(a *App) {
		a.jobRun = NewJobRunner()
		a.t.Cleanup(func() {
			if a.jobRun != nil {
				_ = a.jobRun.Drain(context.Background())
			}
		})
	}
}

// Seeds is a marker option: the named seed files (in testdata/) are loaded
// into the App's DB fixture before tests run. The actual load is performed
// by a registered SeedLoader (see RegisterSeedLoader). Seed names are simply
// the file basenames without extension.
func Seeds(names ...string) AppOption {
	return func(a *App) {
		for _, n := range names {
			a.loadSeed(n)
		}
	}
}

// SeedLoader loads a named seed into the App. Tests register a loader
// appropriate to their stack (sqlite, json fixtures, etc.).
type SeedLoader func(t *testing.T, app *App, name string) error

var (
	seedLoadersMu sync.RWMutex
	seedLoaders   = map[string]SeedLoader{}
)

// RegisterSeedLoader installs a SeedLoader under the given engine name
// ("sqlite", "json", etc.). Tests pick the engine via WithDB's factory.
// Safe to call from init() in any test file.
func RegisterSeedLoader(engine string, l SeedLoader) {
	seedLoadersMu.Lock()
	defer seedLoadersMu.Unlock()
	seedLoaders[engine] = l
}

// loadSeed is the App's per-instance seed dispatch. Without a registered
// loader it is a no-op; this keeps tests that don't use DB seeding simple.
func (a *App) loadSeed(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seedLoadersMu.RLock()
	defer seedLoadersMu.RUnlock()
	if l, ok := seedLoaders["sqlite"]; ok {
		if err := l(a.t, a, name); err != nil {
			a.t.Fatalf("ogontest: seed %q: %v", name, err)
		}
	}
}

// NewApp constructs an in-process fixture. At minimum a handler must be
// supplied via WithHandler; other options are optional. The returned App
// is parallel-safe as long as callers do not share it across tests.
func NewApp(t *testing.T, opts ...AppOption) *App {
	t.Helper()
	a := &App{
		t:      t,
		authed: map[string]string{},
	}
	for _, o := range opts {
		o(a)
	}
	if a.server == nil {
		a.t.Fatalf("ogontest: NewApp requires WithHandler")
	}
	return a
}

// BaseURL returns the in-process server's URL. Recorders use this as the
// default endpoint prefix.
func (a *App) BaseURL() string { return a.server.URL }

// Server returns the underlying httptest.Server (escape hatch).
func (a *App) Server() *httptest.Server { return a.server }

// TmpDir returns the App's isolated tmp dir (empty if WithTmpDir not used).
func (a *App) TmpDir() string { return a.tmpdir }

// DB returns the wired DB fixture, or nil.
func (a *App) DB() *DBFixture { return a.dbFix }

// FakeClock returns the injected clock, or nil.
func (a *App) FakeClock() *FakeClock { return a.fakeclk }

// JobRunner returns the synchronous job runner, or nil.
func (a *App) JobRunner() *JobRunner { return a.jobRun }

// AddCleanup registers an additional teardown. Useful when a test wires up
// extra state on top of the fixture.
func (a *App) AddCleanup(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.t.Cleanup(fn)
}
