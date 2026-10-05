// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Guards: SSRF (SEC-048), open-redirect (SEC-049), path-traversal
// (SEC-050, SEC-051).
//
// These guards are applied at the edge of every outbound and inbound
// boundary where untrusted input crosses into privileged calls.

package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// IsBlockedIP reports whether ip falls into link-local, loopback, or
// cloud-metadata ranges (the SSRF surface).
//
//   - 127.0.0.0/8 loopback
//   - 169.254.0.0/16 link-local (also covers AWS metadata 169.254.169.254)
//   - 0.0.0.0/8 "this host"
//   - 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 private (RFC 1918)
//   - ::1, fc00::/7, fe80::/10 (IPv6 equivalents)
func IsBlockedIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return true // invalid → block
	}
	blocked := []string{
		"127.0.0.0/8", "169.254.0.0/16", "0.0.0.0/8",
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"::1/128", "fc00::/7", "fe80::/10",
	}
	for _, cidr := range blocked {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if n.Contains(parsed) {
			return true
		}
	}
	// also block unspecified
	if parsed.IsUnspecified() {
		return true
	}
	return false
}

// SSRFGuard validates that rawURL does not resolve to a blocked IP.
// It returns an error if the URL is malformed, has no host, resolves
// to a blocked IP, or uses a non-http(s) scheme. Used by any outbound
// fetch helper.
func SSRFGuard(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return diag.Wrap(err, diag.Diag{Code: "OGON-SEC-048", Title: "ssrf: bad url"})
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return diag.New("OGON-SEC-048", "ssrf: bad scheme", "scheme "+u.Scheme+" not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return diag.New("OGON-SEC-048", "ssrf: no host", "")
	}
	// If host is an IP, check directly. Otherwise resolve + check all.
	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedIP(ip.String()) {
			return diag.New("OGON-SEC-048", "ssrf: blocked ip", host)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return diag.Wrap(err, diag.Diag{Code: "OGON-SEC-048", Title: "ssrf: lookup"})
	}
	for _, ip := range ips {
		if IsBlockedIP(ip.String()) {
			return diag.New("OGON-SEC-048", "ssrf: blocked ip",
				host+" resolves to "+ip.String())
		}
	}
	return nil
}

// OpenRedirectGuard validates that the redirect target is on the same
// origin (or on an explicit allowlist). Used on all auth-redirect
// routes to prevent login-flow abuse.
func OpenRedirectGuard(target string, allowedHosts []string) error {
	if target == "" {
		return nil // empty → no redirect
	}
	u, err := url.Parse(target)
	if err != nil {
		return diag.Wrap(err, diag.Diag{Code: "OGON-SEC-049", Title: "redirect: bad url"})
	}
	// relative URLs are safe
	if !u.IsAbs() {
		if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") {
			return nil
		}
		return diag.New("OGON-SEC-049", "redirect: relative", "must start with single /")
	}
	// absolute URLs must be in allowlist
	for _, h := range allowedHosts {
		if u.Host == h {
			return nil
		}
	}
	return diag.New("OGON-SEC-049", "redirect: not allowed", "host "+u.Host+" not in allowlist")
}

// PathTraversalGuard validates that a user-supplied path stays within
// the supplied root directory. It uses filepath.Rel to ensure no "../"
// escape. Used by static-file serving, upload destinations, etc.
func PathTraversalGuard(root, requested string) (string, error) {
	// clean both sides
	cleanRoot := filepath.Clean(root)
	joined := filepath.Join(cleanRoot, requested)
	cleaned := filepath.Clean(joined)
	rel, err := filepath.Rel(cleanRoot, cleaned)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", diag.New("OGON-SEC-050", "path traversal",
			"requested path escapes root")
	}
	if cleaned == cleanRoot {
		return "", diag.New("OGON-SEC-051", "path traversal",
			"requested path equals root")
	}
	return cleaned, nil
}

// http.Error wrapper for diagnostic-aware error responses.
func serveError(w http.ResponseWriter, _ *http.Request, err error, status int) {
	http.Error(w, err.Error(), status)
}

// SSRFHTTPClient returns an *http.Client whose transport rejects
// blocked IPs at dial time. We wrap the default transport with a
// DialContext that resolves the address and rejects blocked IPs before
// the connect syscall fires.
func SSRFHTTPClient() *http.Client {
	dialer := &net.Dialer{}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// resolve now so we can inspect each IP
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if IsBlockedIP(ip.String()) {
					return nil, errors.New("ssrf: blocked ip " + ip.String())
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}
	return &http.Client{Transport: tr}
}
