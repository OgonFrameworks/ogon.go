// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tmp-dir, env, config and faker helpers (TEST-027/028/029). These are the
// small primitives every other fixture in this package is built on. Each one
// is obvious enough that a reader who knows Go but not this codebase can hold
// the entire helper in their head — that is the law from Part XIV: writing
// test infrastructure must never exceed writing the behavior under test.

package test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// TmpDir returns an isolated temporary directory and registers a cleanup
// that removes it on test end. Use this for any on-disk state a fixture
// might leave behind (TEST-027/037).
func TmpDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ogontest-")
	if err != nil {
		t.Fatalf("ogontest: mkdtemp: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort: ignore errors from files held open elsewhere.
		_ = os.RemoveAll(dir)
	})
	return dir
}

// TmpFile writes the supplied content into a fresh file inside a TmpDir and
// returns the absolute path. Useful for fixture config files.
func TmpFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := TmpDir(t)
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("ogontest: write %s: %v", full, err)
	}
	return full
}

// WithEnv sets an env var for the duration of the test and restores the
// previous value (or unsets) on cleanup (TEST-027).
func WithEnv(t *testing.T, key, value string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("ogontest: setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// WithEnvs sets multiple env vars at once; previous values are restored on
// cleanup. Order is preserved for readability.
func WithEnvs(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		WithEnv(t, k, v)
	}
}

// ConfigBuilder is a tiny YAML emitter for test config files. The goal is to
// avoid scattering string literals across test files: build the config,
// write it, hand the path to the app fixture (TEST-028).
type ConfigBuilder struct {
	root map[string]any
}

// NewConfig returns an empty ConfigBuilder.
func NewConfig() *ConfigBuilder {
	return &ConfigBuilder{root: map[string]any{}}
}

// Set assigns a key path (dot-separated) to v. Intermediate maps are created
// on demand. Example: cfg.Set("http.addr", ":0").
func (c *ConfigBuilder) Set(path string, v any) *ConfigBuilder {
	parts := strings.Split(path, ".")
	cur := c.root
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = v
			break
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	return c
}

// String returns the YAML representation.
func (c *ConfigBuilder) String() string {
	out, err := yaml.Marshal(c.root)
	if err != nil {
		return fmt.Sprintf("# ogontest: marshal error: %v\n", err)
	}
	return string(out)
}

// WriteTo returns the path of a fresh tmp file holding this config (TEST-028).
func (c *ConfigBuilder) WriteTo(t *testing.T) string {
	t.Helper()
	return TmpFile(t, "ogon.test.yaml", c.String())
}

// FakerSeed derives a stable per-test seed from t.Name() so that faker output
// is deterministic per test name and parallel-safe (different names →
// different seeds) (TEST-029). Callers should pass the result to math/rand
// or a faker lib's Seed.
func FakerSeed(t *testing.T) int64 {
	t.Helper()
	name := t.Name()
	var h int64 = int64(0xcbf29ce484222325 & 0x7fffffffffffffff) // FNV-1a 64-bit offset basis, sign-stripped
	for i := 0; i < len(name); i++ {
		h ^= int64(name[i])
		h *= 0x100000001b3 // FNV prime
	}
	if h < 0 {
		h = -h
	}
	return h
}

// RandHex returns n bytes of cryptographically random hex. Used for generating
// throwaway IDs, secrets, or session tokens in tests where predictability
// across runs is undesirable.
func RandHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Should never happen on a working /dev/urandom; fall back to a
		// fixed string so tests do not abort in degenerate CI sandboxes.
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// SkipIfShort skips the test under `-short`. Use on integration-grade
// fixtures (testcontainers, soak, load).
func SkipIfShort(t *testing.T, reason string) {
	t.Helper()
	if testing.Short() {
		t.Skipf("ogontest: skipped under -short: %s", reason)
	}
}

// MustEqual is a tiny assert — prefer over importing testify. Used by tests
// in this package itself; exported because helpers here are documented as
// the "minimal assert lib" (TEST-026 wraps this — see assert.go).
func MustEqual(t testFailT, want, got any, msg ...string) {
	t.Helper()
	if !equalAny(want, got) {
		if len(msg) > 0 {
			t.Fatalf("ogontest: %s: want %#v, got %#v", msg[0], want, got)
			return
		}
		t.Fatalf("ogontest: want %#v, got %#v", want, got)
	}
}
