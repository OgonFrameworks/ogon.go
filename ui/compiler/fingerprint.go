// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fingerprint cache + incremental compile (UI-080, UI-079).
//
// A fingerprint is a SHA-1 of the .ogon source plus a hash of the
// compiler version. The cache stores the generated artefacts keyed
// by fingerprint so that unchanged components are skipped on the
// next `ogon build`. The cache is on-disk for build-to-build use
// and in-memory for tests and the dev HMR path (UI-040/041).

package compiler

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
)

// CompilerVersion is bumped whenever the compiler pipeline changes in
// a way that invalidates the cache. Tests use this to force re-runs.
const CompilerVersion = "ogon-ui-v1"

// Fingerprint returns the stable per-file digest.
func Fingerprint(src string) string {
	h := sha1.Sum([]byte(CompilerVersion + ":" + src))
	return hex.EncodeToString(h[:])
}

// CompileCache stores artefacts keyed by component fingerprint.
// The on-disk cache lives under `cacheDir/<component>/<fingerprint>/{go,js,css}`.
type CompileCache struct {
	mu       sync.Mutex
	cacheDir string
	mem      map[string]CacheEntry // in-memory cache (used when cacheDir is "")
}

// CacheEntry is a single cached artefact set.
type CacheEntry struct {
	Fingerprint string
	Go          string
	JS          string
	CSS         string
	Manifest    string
}

// NewCompileCache constructs a cache rooted at cacheDir. Pass an
// empty string to use an in-memory cache (good for tests).
func NewCompileCache(cacheDir string) *CompileCache {
	return &CompileCache{cacheDir: cacheDir, mem: map[string]CacheEntry{}}
}

// Get returns a cached entry if one matches the fingerprint.
func (c *CompileCache) Get(name, fp string) (CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cacheDir == "" {
		e, ok := c.mem[name]
		return e, ok && e.Fingerprint == fp
	}
	// On-disk: read sidecar manifest.
	path := filepath.Join(c.cacheDir, name, fp, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return CacheEntry{}, false
	}
	_ = data // actual artefact reads deferred to GetArtefact
	return CacheEntry{Fingerprint: fp, Manifest: string(data)}, true
}

// Put stores a compiled artefact set keyed by fingerprint.
func (c *CompileCache) Put(name string, e CacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cacheDir == "" {
		c.mem[name] = e
		return nil
	}
	dir := filepath.Join(c.cacheDir, name, e.Fingerprint)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".go"), []byte(e.Go), 0o644); err != nil {
		return err
	}
	if e.JS != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".js"), []byte(e.JS), 0o644); err != nil {
			return err
		}
	}
	if e.CSS != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".css"), []byte(e.CSS), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Invalidate removes all cached artefacts for a component. Used by
// the HMR path (UI-040/041) when a component source is edited.
func (c *CompileCache) Invalidate(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cacheDir == "" {
		delete(c.mem, name)
		return
	}
	_ = os.RemoveAll(filepath.Join(c.cacheDir, name))
}

// IncrementalCompile reports whether the component should be rebuilt.
// Returns true when:
//   - no cache entry exists; or
//   - the fingerprint differs from the cached one; or
//   - the compiler version changed.
func (c *CompileCache) IncrementalCompile(name, src string) bool {
	fp := Fingerprint(src)
	e, ok := c.Get(name, fp)
	if !ok {
		return true
	}
	return e.Fingerprint != fp
}
