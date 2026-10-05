// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fuzz seed registry (TEST-019). Fuzz targets in test files register their
// existence here so the CI generator (TEST-017) can enumerate them and run
// each one with a fixed time budget. The registry is process-global; tests
// call RegisterFuzzSeed from init().

package test

import (
	"sort"
	"sync"
)

// FuzzSeed describes a registered fuzz target.
type FuzzSeed struct {
	// Name is the fuzz function name (e.g., "FuzzParseRoute"). Must be unique.
	Name string
	// Package is the import path of the package holding the fuzz function.
	Package string
	// Seeds is a list of baseline seed inputs (corpus entries). Each is
	// a free-form string; tests cast as needed.
	Seeds []string
	// Duration is the suggested per-run fuzz time, e.g., "30s". Empty → 30s.
	Duration string
}

var (
	fuzzMu    sync.RWMutex
	fuzzSeeds = map[string]FuzzSeed{}
)

// RegisterFuzzSeed adds (or replaces) a fuzz target in the registry. Safe
// to call from init() in any test file. The registry is enumerated by the
// CI generator (TEST-017) and exposed to `ogon doc test fuzz` via List.
func RegisterFuzzSeed(s FuzzSeed) {
	fuzzMu.Lock()
	defer fuzzMu.Unlock()
	if s.Duration == "" {
		s.Duration = "30s"
	}
	fuzzSeeds[s.Name] = s
}

// MustRegisterFuzzSeed panics if Name is empty. Convenience wrapper.
func MustRegisterFuzzSeed(s FuzzSeed) {
	if s.Name == "" {
		panic("ogontest: RegisterFuzzSeed: empty name")
	}
	RegisterFuzzSeed(s)
}

// ListFuzzSeeds returns the registry contents sorted by Name.
func ListFuzzSeeds() []FuzzSeed {
	fuzzMu.RLock()
	defer fuzzMu.RUnlock()
	out := make([]FuzzSeed, 0, len(fuzzSeeds))
	for _, s := range fuzzSeeds {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FuzzSeedFor returns the registered seed entry for name, or false.
func FuzzSeedFor(name string) (FuzzSeed, bool) {
	fuzzMu.RLock()
	defer fuzzMu.RUnlock()
	s, ok := fuzzSeeds[name]
	return s, ok
}

// FuzzSeedsForPackage returns the registered seeds whose Package matches.
func FuzzSeedsForPackage(pkg string) []FuzzSeed {
	fuzzMu.RLock()
	defer fuzzMu.RUnlock()
	var out []FuzzSeed
	for _, s := range fuzzSeeds {
		if s.Package == pkg {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
