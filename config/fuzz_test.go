// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fuzz the YAML loader (P14 bug-bounty). The loader must NEVER
// panic on adversarial YAML — malformed bytes, huge nesting depth,
// embedded NULs, anchor/alias abuse, scalar-as-map, and empty docs
// are all on the bug-bounty surface.

package config

import (
	"context"
	"strings"
	"testing"
)

// FuzzYAMLLoader drives the loader's YAML parse + flatten path with
// attacker-controlled bytes. The contract:
//   - no panic on any input
//   - errors are returned (never raised)
//   - load completes in finite time
//   - the returned Config (when no error) is non-nil and Get-able
//
// Run: go test ./config -fuzz=FuzzYAMLLoader -fuzztime=3s
func FuzzYAMLLoader(f *testing.F) {
	// Seed corpus — at least 5 cases per spec.
	f.Add([]byte("http:\n  addr: \":3000\"\n  port: 3000\n"))
	f.Add([]byte("not yaml: [unclosed"))
	f.Add([]byte("---\nhttp:\n  addr: \":3000\"\n"))
	f.Add([]byte("# just a comment\n"))
	f.Add([]byte(""))
	f.Add([]byte("deeply:\n  nested:\n    a:\n      b:\n        c:\n          d: 1\n"))
	f.Add([]byte("\x00\x01\x02binary: garbage\n"))
	f.Add([]byte("k1: v1\nk1: v2\n"))                 // duplicate keys
	f.Add([]byte("list:\n  - a\n  - b\n  - c\n"))     // list under a key
	f.Add([]byte("a: &anchor\n  x: 1\nb: *anchor\n")) // anchor/alias

	f.Fuzz(func(t *testing.T, data []byte) {
		// Recovery guard so a stack-overflow or invalid-pointer deref
		// surfaces as a test failure rather than killing the process.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("loader panicked on input %q: %v", truncate(string(data), 80), r)
			}
		}()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ld := NewLoader(
			WithYAMLBytes(data),
			WithEnvMap(map[string]string{}),
		)
		cfg, err := ld.Load(ctx)
		if err != nil {
			// Errors are acceptable; we only require "no panic".
			// Sanity: the error string must be finite and printable.
			_ = err.Error()
			return
		}
		if cfg == nil {
			t.Fatal("Load returned (nil, nil)")
		}
		// Get on a known key must be safe — the loader's flat map must
		// not contain a non-string value at a known location.
		_ = cfg.GetString("http.addr")
	})
}

// FuzzDotEnvParser drives the .env parser with random bytes. The parser
// is line-oriented and must not panic on adversarial input.
func FuzzDotEnvParser(f *testing.F) {
	f.Add([]byte("KEY=value\n"))
	f.Add([]byte(""))
	f.Add([]byte("=nokey\n"))
	f.Add([]byte("KEY=\nKEY2=val\n"))
	f.Add([]byte("KEY=\"unclosed quote\n"))
	f.Add([]byte("KEY\t=\ttab\tval\n"))
	f.Add([]byte("# comment\nKEY=val\n"))
	f.Add([]byte("NOEQUALSIGN\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("dotenv parser panicked on %q: %v", truncate(string(data), 80), r)
			}
		}()
		m := parseDotEnv(data)
		// Iteration over a nil map is safe; we only assert no panic.
		for k, v := range m {
			_ = k + v
		}
	})
}

// FuzzInterpolation drives the variable interpolation routine with
// random strings + random env maps. The interpolator must not panic
// on adversarial input, malformed ${VAR refs, or recursion-bait.
func FuzzInterpolation(f *testing.F) {
	f.Add("plain text", "VALUE")
	f.Add("${MISSING}", "VALUE")
	f.Add("${VAR}", "VAR=resolved\n")
	f.Add("${NESTED_${SUB}}", "SUB=x\nx=hi\n")
	f.Add("${", "x=y\n")
	f.Add("}", "")
	f.Add("${A}${B}${A}${B}", "A=1\nB=2\n")
	f.Add(strings.Repeat("${X}", 256), "X=v\n")

	f.Fuzz(func(t *testing.T, s, envBlob string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("interpolate panicked on %q: %v", truncate(s, 80), r)
			}
		}()
		env := parseDotEnv([]byte(envBlob))
		out := Interpolate(s, env)
		_ = out
	})
}

// truncate keeps test failure messages readable.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
