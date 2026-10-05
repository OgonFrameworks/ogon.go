// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// SSRF test (SEC-048). Verifies the SSRF guard denies the canonical
// blocked-IP set:
//   - AWS instance metadata 169.254.169.254
//   - IPv4 loopback 127.0.0.1
//   - IPv6 loopback ::1
//   - Private 10.x, 192.168.x
//   - localhost (resolves to 127.0.0.1)
//   - Invalid hosts
//
// And accepts a public-IP URL (8.8.8.8 is the canonical "safe" sample
// because the test environment does not let us resolve arbitrary names;
// we only assert the guard returns nil for well-formed public IPs).

package auth

import (
	"testing"
)

// TestSSRFDeniesAWSMetadata: the canonical AWS metadata endpoint must be
// blocked at the IP level — even if the URL is well-formed.
func TestSSRFDeniesAWSMetadata(t *testing.T) {
	if err := SSRFGuard("http://169.254.169.254/latest/meta-data/iam/security-credentials/"); err == nil {
		t.Fatal("SSRF guard must block 169.254.169.254 (AWS metadata)")
	}
}

// TestSSRFDeniesLoopback: 127.0.0.1 (loopback) must be blocked.
func TestSSRFDeniesLoopback(t *testing.T) {
	if err := SSRFGuard("http://127.0.0.1/admin"); err == nil {
		t.Fatal("SSRF guard must block 127.0.0.1")
	}
	if err := SSRFGuard("http://127.0.0.1:8080/internal"); err == nil {
		t.Fatal("SSRF guard must block 127.0.0.1:8080")
	}
}

// TestSSRFDeniesIPv6Loopback: ::1 must be blocked.
func TestSSRFDeniesIPv6Loopback(t *testing.T) {
	if err := SSRFGuard("http://[::1]/admin"); err == nil {
		t.Fatal("SSRF guard must block ::1")
	}
}

// TestSSRFDeniesPrivate10: 10.x private range must be blocked.
func TestSSRFDeniesPrivate10(t *testing.T) {
	for _, ip := range []string{"10.0.0.1", "10.1.2.3", "10.255.255.255"} {
		if err := SSRFGuard("http://" + ip + "/x"); err == nil {
			t.Errorf("SSRF guard must block %s", ip)
		}
	}
}

// TestSSRFDeniesPrivate192: 192.168.x private range must be blocked.
func TestSSRFDeniesPrivate192(t *testing.T) {
	for _, ip := range []string{"192.168.0.1", "192.168.1.1", "192.168.99.99"} {
		if err := SSRFGuard("http://" + ip + "/x"); err == nil {
			t.Errorf("SSRF guard must block %s", ip)
		}
	}
}

// TestSSRFDeniesLocalhost: the literal hostname "localhost" must be
// blocked (it resolves to 127.0.0.1). This test may be skipped in
// sandboxes where DNS does not resolve localhost — but the guard's
// net.LookupIP call should succeed in any normal test environment.
func TestSSRFDeniesLocalhost(t *testing.T) {
	if err := SSRFGuard("http://localhost/admin"); err == nil {
		// Some sandboxes block DNS entirely; if LookupIP failed, the guard
		// would have returned an error (also a deny). Only fail if the
		// guard returned nil.
		t.Fatal("SSRF guard must block localhost (resolves to loopback)")
	}
}

// TestSSRFAllowsPublic: a public IP must pass the guard. We use 8.8.8.8
// (Google DNS) — the canonical "safe" public IP.
func TestSSRFAllowsPublic(t *testing.T) {
	if err := SSRFGuard("https://8.8.8.8/dns"); err != nil {
		t.Fatalf("SSRF guard should allow 8.8.8.8: %v", err)
	}
}

// TestSSRFDeniesBadScheme: javascript:, file:, gopher: must be blocked.
func TestSSRFDeniesBadScheme(t *testing.T) {
	for _, u := range []string{
		"javascript:alert(1)",
		"file:///etc/passwd",
		"gopher://localhost/x",
		"ftp://8.8.8.8/x",
	} {
		if err := SSRFGuard(u); err == nil {
			t.Errorf("SSRF guard must reject scheme in %q", u)
		}
	}
}

// TestSSRFDeniesInvalidHost: empty host, malformed URL.
func TestSSRFDeniesInvalidHost(t *testing.T) {
	for _, u := range []string{
		"http://",
		"://nohost",
		"http:///",
	} {
		if err := SSRFGuard(u); err == nil {
			t.Errorf("SSRF guard must reject malformed URL %q", u)
		}
	}
}

// TestIsBlockedIPDirect: directly assert IsBlockedIP against the full
// canonical blocked-IP set.
func TestIsBlockedIPDirect(t *testing.T) {
	blocked := []string{
		"169.254.169.254", // AWS metadata
		"127.0.0.1",       // loopback
		"::1",             // IPv6 loopback
		"10.0.0.1",        // private 10/8
		"192.168.1.1",     // private 192.168/16
		"172.16.0.1",      // private 172.16/12
		"0.0.0.0",         // unspecified
		"fe80::1",         // link-local IPv6
		"fc00::1",         // ULA IPv6
	}
	for _, ip := range blocked {
		if !IsBlockedIP(ip) {
			t.Errorf("IsBlockedIP(%q) = false, want true", ip)
		}
	}
	allowed := []string{
		"8.8.8.8",              // Google DNS
		"1.1.1.1",              // Cloudflare DNS
		"2606:4700:4700::1111", // Cloudflare IPv6
	}
	for _, ip := range allowed {
		if IsBlockedIP(ip) {
			t.Errorf("IsBlockedIP(%q) = true, want false", ip)
		}
	}
	// invalid IP → blocked (defensive).
	if !IsBlockedIP("not-an-ip") {
		t.Error("IsBlockedIP('not-an-ip') should be true (defensive)")
	}
}
