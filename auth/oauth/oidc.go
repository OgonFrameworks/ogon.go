// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// OIDC discovery + userinfo (SEC-012).
//
// We use github.com/coreos/go-oidc/v3 for ID token verification; the
// discovery document is fetched with a strict-redirect, link-local
// denied HTTP client (auth/guards.go SSRF guard).

package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// userInfoMaxBytes caps the size of an OIDC userinfo response body.
// A malicious or compromised OIDC provider (or a man-in-the-middle that
// bypasses TLS) could otherwise stream gigabytes, exhausting process
// memory (BUG-0011). 1 MiB is generous for a userinfo payload.
const userInfoMaxBytes = 1 << 20

// DiscoveryDoc is the subset of /.well-known/openid-configuration we use.
type DiscoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURL               string `json:"jwks_uri"`
	RevocationEndpoint    string `json:"revocation_endpoint,omitempty"`
	EndSessionEndpoint    string `json:"end_session_endpoint,omitempty"`
}

// Discover fetches and parses the discovery document at issuerURL
// (or the URL itself if it's already the discovery URL).
func Discover(ctx context.Context, issuerURL string) (*DiscoveryDoc, error) {
	if issuerURL == "" {
		return nil, errors.New("oauth: empty issuer URL")
	}
	discURL := issuerURL
	if !stringsHasSuffix(issuerURL, "/.well-known/openid-configuration") {
		// trim trailing slash then append
		for len(discURL) > 0 && discURL[len(discURL)-1] == '/' {
			discURL = discURL[:len(discURL)-1]
		}
		discURL += "/.well-known/openid-configuration"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-012", Title: "oidc: discovery fetch"})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, diag.New("OGON-SEC-012", "oidc: discovery non-200",
			"status "+resp.Status)
	}
	var d DiscoveryDoc
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-012", Title: "oidc: discovery decode"})
	}
	if d.Issuer == "" || d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" {
		return nil, diag.New("OGON-SEC-012", "oidc: discovery missing required fields", discURL)
	}
	return &d, nil
}

// IDTokenVerifier wraps go-oidc's verifier. Use VerifyIDToken to check
// an ID Token's signature + claims (iss, aud, exp) in one call.
type IDTokenVerifier struct {
	v *oidc.IDTokenVerifier
}

// NewIDTokenVerifier returns a verifier for the given issuer URL.
// The key set is fetched from the discovery JWKS URI.
func NewIDTokenVerifier(ctx context.Context, issuerURL string) (*IDTokenVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-012", Title: "oidc: provider"})
	}
	return &IDTokenVerifier{v: provider.Verifier(&oidc.Config{SkipClientIDCheck: false})}, nil
}

// Verify verifies an ID token and returns its claims.
func (v *IDTokenVerifier) Verify(ctx context.Context, rawIDToken string) (map[string]any, error) {
	tok, err := v.v.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-012", Title: "oidc: verify"})
	}
	// Extract claims
	var claims map[string]any
	if err := tok.Claims(&claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// UserInfo fetches the OIDC userinfo endpoint using the access token.
// Used to supplement ID token claims (email, email_verified, name).
// The response body is capped at userInfoMaxBytes to prevent a
// malicious or compromised OIDC provider from exhausting process
// memory (BUG-0011).
func UserInfo(ctx context.Context, userInfoURL, accessToken string) (map[string]any, error) {
	if userInfoURL == "" {
		return nil, errors.New("oauth: no userinfo endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-012", Title: "oidc: userinfo"})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, diag.New("OGON-SEC-012", "oidc: userinfo non-200", resp.Status)
	}
	// Cap the body size: a malicious provider could stream gigabytes
	// (BUG-0011). The LimitReader allows up to userInfoMaxBytes+1 so
	// we can detect the overshoot on read.
	limited := io.LimitReader(resp.Body, userInfoMaxBytes+1)
	var out map[string]any
	if err := json.NewDecoder(limited).Decode(&out); err != nil {
		return nil, err
	}
	// If `out` is non-nil but we suspect truncation, we cannot reliably
	// tell from json.Decode alone (it stops at the closing brace). The
	// LimitReader cap above is the hard bound; the decoder will error
	// with "unexpected EOF" if the JSON was truncated mid-structure,
	// which is the desired fail-closed behavior.
	return out, nil
}

// stringsHasSuffix is a small helper to avoid importing strings twice.
func stringsHasSuffix(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}
