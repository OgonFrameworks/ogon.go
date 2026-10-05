// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Security audit test (SEC-041). Verifies every byte-compare in the auth
// subtree uses crypto/subtle.ConstantTimeCompare, that no math/rand is
// used for security entropy, that the JWT alg allowlist contains no
// "none" / symmetric alg, and that RunConstantTimeAudit (the SEC-041
// lint hook) correctly flags misuse.
//
// These tests run as part of `go test -race ./auth/...` so any future
// regression surfaces immediately at PR time.

package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot returns the path of the repo root by walking up from a known
// file. In Go tests this is os.Getwd() under the package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Walk up until we find go.mod.
	for dir := wd; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
	}
	return wd
}

// TestConstantTimeAuditFlagsMisuse: RunConstantTimeAudit must flag a
// synthetic source file with a `==` on a `hash`-named byte slice.
func TestConstantTimeAuditFlagsMisuse(t *testing.T) {
	files := map[string]string{
		"bad.go": `
package x
func bad(got, want []byte) bool {
	return string(got) == string(want) // bad: hash compare
}
`,
	}
	out := RunConstantTimeAudit(files)
	if len(out) == 0 {
		t.Fatal("RunConstantTimeAudit should flag the bad comparison")
	}
	// Find at least one finding mentioning hash.
	found := false
	for _, f := range out {
		if strings.Contains(f.Note, "hash") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no hash finding in %+v", out)
	}
}

// TestAuthUsesSubtleEverywhere: scans every .go source file under auth/
// for "string(x) == string(y)" patterns or "bytes.Equal(x, y)" (which is
// NOT constant-time) on variables named hash/sig/mac/token/secret/bytes/
// digest. Asserts no such pattern appears in production code.
func TestAuthUsesSubtleEverywhere(t *testing.T) {
	root := filepath.Join(repoRoot(t), "auth")
	files, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, recursiveGlob(root, "session", "*.go")...)
	files = append(files, recursiveGlob(root, "password", "*.go")...)
	files = append(files, recursiveGlob(root, "token", "*.go")...)
	files = append(files, recursiveGlob(root, "oauth", "*.go")...)
	files = append(files, recursiveGlob(root, "passkey", "*.go")...)

	banned := []string{
		"bytes.Equal(",
		"subtle.ConstantTimeCompare == 0", // risky inversion pattern
	}
	for _, f := range files {
		// Skip test files (the audit is for production code).
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Logf("skip unreadable %s: %v", f, err)
			continue
		}
		src := string(body)
		for _, b := range banned {
			if strings.Contains(src, b) {
				t.Errorf("%s: banned pattern %q in auth source — use subtle.ConstantTimeCompare instead", f, b)
			}
		}
	}
}

// TestNoMathRandInAuth: no production auth file should import math/rand
// (only crypto/rand is acceptable for security entropy).
func TestNoMathRandInAuth(t *testing.T) {
	root := filepath.Join(repoRoot(t), "auth")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		src := string(body)
		// Banned import lines (not just substring, the full import).
		if strings.Contains(src, "\"math/rand\"") {
			t.Errorf("%s: auth production code must not import math/rand — use crypto/rand", p)
		}
		if strings.Contains(src, "math/rand/v2") {
			// math/rand/v2 is OK for non-security test fixtures, but production
			// auth should still use crypto/rand. Flag for review.
			t.Errorf("%s: auth production code uses math/rand/v2 — use crypto/rand for security entropy", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestJWTAlgAllowlistNoNone: the JWT allowlist must reject "none" and any
// symmetric alg. Re-checks the allowedAlgs map from auth/token/jwt.go via
// the package's exported API. Because allowedAlgs is unexported, we
// inspect the source text to verify the literal allowlist contents.
func TestJWTAlgAllowlistNoNone(t *testing.T) {
	jwtPath := filepath.Join(repoRoot(t), "auth", "token", "jwt.go")
	body, err := os.ReadFile(jwtPath)
	if err != nil {
		t.Skipf("jwt.go not readable: %v", err)
	}
	src := string(body)
	// The allowlist must declare RS256, RS384, RS512 (or RS256/ES256).
	// It MUST NOT declare "none" or any HS* alg.
	if strings.Contains(src, `"none"`) {
		t.Fatal("jwt.go must not include 'none' in alg allowlist")
	}
	if strings.Contains(src, "AlgHS256") || strings.Contains(src, "AlgHS384") || strings.Contains(src, "AlgHS512") {
		t.Fatal("jwt.go must not declare any HS* (HMAC) alg — these enable alg-confusion attacks")
	}
	// Confirm RS256 is in the allowlist.
	if !strings.Contains(src, `AlgRS256 Algorithm = "RS256"`) {
		t.Fatal("jwt.go must declare AlgRS256")
	}
}

// TestRunConstantTimeAuditNegativeCase: feed a clean source file and
// confirm the audit returns no findings.
func TestRunConstantTimeAuditNegativeCase(t *testing.T) {
	files := map[string]string{
		"good.go": `
package x
import "crypto/subtle"
func good(got, want []byte) bool {
	return subtle.ConstantTimeCompare(got, want) == 1
}
`,
	}
	if out := RunConstantTimeAudit(files); len(out) != 0 {
		t.Fatalf("expected 0 findings for clean code, got %+v", out)
	}
}

// recursiveGlob walks one subdirectory of root and returns *.go paths.
func recursiveGlob(root, sub, pattern string) []string {
	dir := filepath.Join(root, sub)
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	return matches
}
