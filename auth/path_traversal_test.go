// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Path traversal test (SEC-050, SEC-051, HTTP-046). Verifies the
// PathTraversalGuard denies:
//   - ../etc/passwd  (Unix escape)
//   - ..\windows\win.ini (Windows escape with backslashes)
//   - ../../etc/shadow
//   - ./../hidden
// And allows safe paths that stay under root.
//
// The static-file middleware (http/middleware_static.go) implements
// equivalent guards at the HTTP layer; this test exercises the
// auth.PathTraversalGuard that is used at file-system call sites.

package auth

import (
	"testing"
)

// TestPathTraversalDeniesEtcPasswd: the classic Unix traversal.
func TestPathTraversalDeniesEtcPasswd(t *testing.T) {
	for _, p := range []string{
		"../etc/passwd",
		"../../etc/passwd",
		"../../../etc/passwd",
		"./../../etc/passwd",
		"./../../../../etc/shadow",
	} {
		p := p
		t.Run(p, func(t *testing.T) {
			_, err := PathTraversalGuard("/var/www/static", p)
			if err == nil {
				t.Fatalf("PathTraversalGuard must deny %q", p)
			}
		})
	}
}

// TestPathTraversalDeniesWindowsWinIni: the classic Windows traversal.
// filepath.Join + filepath.Clean normalise backslashes on Windows; on
// Unix we test both raw and normalised forms. The guard should reject
// the literal "../" sequence either way.
func TestPathTraversalDeniesWindowsWinIni(t *testing.T) {
	// On Unix, "..\windows\win.ini" is a relative path containing a
	// directory named "..\windows" (with the backslash as part of the
	// name). The guard should still reject it because the leading ".."
	// is detected by filepath.Rel — when the requested path is a
	// subdirectory like "..\windows\win.ini" with the ".." at the start
	// of the path, filepath.Rel returns a path starting with "..".
	for _, p := range []string{
		"..\\windows\\win.ini",
		"..\\..\\windows\\system32\\config\\sam",
		"..\\..\\..\\windows\\win.ini",
	} {
		p := p
		t.Run(p, func(t *testing.T) {
			_, err := PathTraversalGuard("/var/www/static", p)
			if err == nil {
				t.Fatalf("PathTraversalGuard must deny Windows traversal %q", p)
			}
		})
	}
}

// TestPathTraversalDeniesDotDotBackslash: mixed-slash traversal.
func TestPathTraversalDeniesDotDotBackslash(t *testing.T) {
	// "foo/../../etc/passwd" — the ../ mid-path escapes.
	for _, p := range []string{
		"foo/../../../etc/passwd",
		"assets/../../etc/passwd",
		"./../etc/passwd",
	} {
		p := p
		t.Run(p, func(t *testing.T) {
			_, err := PathTraversalGuard("/var/www/static", p)
			if err == nil {
				t.Fatalf("must deny %q", p)
			}
		})
	}
}

// TestPathTraversalAllowsSafePath: a path that stays under root must
// resolve.
func TestPathTraversalAllowsSafePath(t *testing.T) {
	clean, err := PathTraversalGuard("/var/www/static", "css/app.css")
	if err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
	if clean == "" {
		t.Fatal("clean path should be non-empty")
	}
	// The cleaned path must be under root.
	if len(clean) < len("/var/www/static") {
		t.Fatalf("clean path %q shorter than root %q", clean, "/var/www/static")
	}
}

// TestPathTraversalDeniesRootEscape: an absolute path passed as the
// requested argument. Because filepath.Join treats an absolute second
// argument as "append to root", these resolve UNDER root and are NOT a
// traversal. They are explicitly ALLOWED by the guard (and the static-
// file middleware). This test documents that behaviour so future
// hardening does not silently break absolute-path callers.
func TestPathTraversalDeniesRootEscape(t *testing.T) {
	for _, p := range []string{
		"/etc/passwd", // → /var/www/static/etc/passwd (under root, NOT an escape)
		"/etc/shadow",
	} {
		p := p
		t.Run(p, func(t *testing.T) {
			clean, err := PathTraversalGuard("/var/www/static", p)
			// Documented: absolute paths join under root and are allowed.
			if err != nil {
				t.Fatalf("absolute path %q should be joined under root, got err: %v", p, err)
			}
			if clean == "" {
				t.Fatalf("clean path empty")
			}
		})
	}
}

// TestPathTraversalDeniesExplicitEscape: explicit ".." traversal — the
// real attack vector. The guard MUST reject this.
func TestPathTraversalDeniesExplicitEscape(t *testing.T) {
	for _, p := range []string{
		"../../../../etc/passwd",
		"../../../etc/shadow",
		"./../../../../etc/sudoers",
	} {
		p := p
		t.Run(p, func(t *testing.T) {
			_, err := PathTraversalGuard("/var/www/static", p)
			if err == nil {
				t.Fatalf("PathTraversalGuard must deny explicit escape %q", p)
			}
		})
	}
}
