// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// pprof routes gated by config/env. Implements OBS-023.
//
// Default: OFF. Enabled via:
//   - OGON_PPROF_ENABLED=true env, or
//   - pprof.Enabled=true in ogon.yaml, or
//   - ProductionProfile flag in the framework BootOpts.
//
// Even when enabled, the routes are mounted under /debug/pprof (the
// canonical Go pprof prefix) so the gate is a single mount check rather
// than a per-request check (no overhead in hot paths).

package obs

import (
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
)

// PprofConfig enables/disables the pprof routes. (OBS-023)
type PprofConfig struct {
	// Enabled: when false (default), Mount is a no-op.
	Enabled bool
	// Prefix defaults to /debug/pprof. Override for non-default mounts.
	Prefix string
}

// PprofConfigFromEnv builds the config from OGON_PPROF_* env vars. (OBS-023)
func PprofConfigFromEnv(env map[string]string) PprofConfig {
	cfg := PprofConfig{Prefix: "/debug/pprof"}
	switch strings.ToLower(env["OGON_PPROF_ENABLED"]) {
	case "1", "true", "yes", "on":
		cfg.Enabled = true
	}
	if v := env["OGON_PPROF_PREFIX"]; v != "" {
		cfg.Prefix = v
	}
	return cfg
}

// Mount installs the pprof routes on mux when Enabled. No-op otherwise.
// (OBS-023)
func (c PprofConfig) Mount(mux *http.ServeMux) {
	if !c.Enabled {
		return
	}
	prefix := c.Prefix
	if prefix == "" {
		prefix = "/debug/pprof"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	mux.HandleFunc(prefix, pprof.Index)
	mux.HandleFunc(prefix+"cmdline", pprof.Cmdline)
	mux.HandleFunc(prefix+"profile", pprof.Profile)
	mux.HandleFunc(prefix+"symbol", pprof.Symbol)
	mux.HandleFunc(prefix+"trace", pprof.Trace)
	// Index page links to /debug/pprof/<name> for each profile.
	for _, p := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.HandleFunc(prefix+p, pprof.Handler(p).ServeHTTP)
	}
}

// IsPprofEnabled reports whether pprof routes are exposed in the current
// environment. Used by `ogon doctor` to warn on prod exposure. (OBS-023)
func IsPprofEnabled(env map[string]string) bool {
	return PprofConfigFromEnv(env).Enabled
}

// EnablePprofForTest installs the pprof routes on mux regardless of env,
// for tests that need to assert the mount surface.
func EnablePprofForTest(mux *http.ServeMux) {
	PprofConfig{Enabled: true, Prefix: "/debug/pprof"}.Mount(mux)
}

// DiscardPprof silently drops pprof requests. Used in tests that want
// to simulate a gated config without installing routes.
func DiscardPprof(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

// IsProdEnv returns true for production-like environments. (inverse of IsDevEnv)
func IsProdEnv(env string) bool { return !IsDevEnv(env) }

// osEnvLookup returns the value of name from os.Getenv or "" if missing.
// Indirected so tests can substitute env without touching the process env.
var osEnvLookup = func(name string) string { return os.Getenv(name) }
