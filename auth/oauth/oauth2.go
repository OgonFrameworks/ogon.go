// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// OAuth2 client: code+PKCE mandatory for public clients (SEC-011,
// SEC-013), provider discovery, org-level SSO config (SEC-081).
//
// We rely on golang.org/x/oauth2 for the wire protocol; PKCE enforcement
// is layered on top.

package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/oauth2"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// Provider describes one upstream OAuth2 IdP.
type Provider struct {
	ID           string // stable internal id, e.g. "github", "acme-google"
	DisplayName  string
	ClientID     string
	ClientSecret string // empty for public clients (forces PKCE)
	RedirectURL  string
	Scopes       []string
	AuthURL      string // from discovery when Discover=true
	TokenURL     string
	UserInfoURL  string // OIDC userinfo; empty for pure OAuth2
	Discover     bool   // use .well-known/openid-configuration
	// Org-level SSO mapping (SEC-081): when set, only users whose
	// verified email/domain matches AllowedDomain may bind.
	AllowedDomain string
	// SSOLinkedTenant, when non-empty, auto-provisions the user into
	// this tenant on first login (org SSO bootstrapping).
	SSOLinkedTenant string
}

// IsPublic returns true for clients without a secret — these MUST use
// PKCE (SEC-011/SEC-013).
func (p Provider) IsPublic() bool { return p.ClientSecret == "" }

// Registry holds configured providers by ID. Concurrent-safe.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register installs or replaces a provider config.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.ID] = p
}

// Get returns a provider by ID.
func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	return p, ok
}

// List returns all provider IDs.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	return out
}

// Client wraps an oauth2 config + provider metadata, enforcing PKCE
// for public clients.
type Client struct {
	provider Provider
	config   *oauth2.Config
}

// NewClient constructs a Client. If provider.Discover is true, the
// auth/token URLs are resolved via OIDC discovery before use.
func NewClient(ctx context.Context, p Provider) (*Client, error) {
	if p.IsPublic() && p.AllowedDomain == "" && p.ClientID == "" {
		// no client id means nothing works; safer to fail loud at boot.
		return nil, diag.New("OGON-SEC-011", "oauth provider misconfigured", p.ID)
	}
	cfg := &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		RedirectURL:  p.RedirectURL,
		Scopes:       p.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  p.AuthURL,
			TokenURL: p.TokenURL,
		},
	}
	if p.Discover {
		// resolve via OIDC discovery
		d, err := Discover(ctx, p.AuthURL) // see oidc.go
		if err == nil && d != nil {
			cfg.Endpoint.AuthURL = d.AuthorizationEndpoint
			cfg.Endpoint.TokenURL = d.TokenEndpoint
			if p.UserInfoURL == "" {
				p.UserInfoURL = d.UserinfoEndpoint
			}
		}
	}
	return &Client{provider: p, config: cfg}, nil
}

// AuthURL builds an authorization URL with PKCE if the provider is
// public. Returns the URL and the verifier the caller MUST keep
// (state-secret equivalent; stashed in session).
func (c *Client) AuthURL(state string) (url, verifier string, err error) {
	if c.provider.IsPublic() {
		v, err := genVerifier()
		if err != nil {
			return "", "", diag.Wrap(err, diag.Diag{Code: "OGON-SEC-013", Title: "pkce: verifier gen"})
		}
		challenge := pkceChallengeS256(v)
		url := c.config.AuthCodeURL(state,
			oauth2.SetAuthURLParam("code_challenge", challenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
		return url, v, nil
	}
	return c.config.AuthCodeURL(state), "", nil
}

// Exchange swaps the code for tokens, verifying PKCE when applicable.
func (c *Client) Exchange(ctx context.Context, code, verifier string) (*oauth2.Token, error) {
	opts := []oauth2.AuthCodeOption{}
	if c.provider.IsPublic() {
		if verifier == "" {
			return nil, diag.New("OGON-SEC-013", "pkce required for public client", c.provider.ID)
		}
		opts = append(opts,
			oauth2.SetAuthURLParam("code_verifier", verifier),
		)
	}
	tok, err := c.config.Exchange(ctx, code, opts...)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-011", Title: "oauth: exchange"})
	}
	return tok, nil
}

// VerifyDomain enforces org-SSO domain binding (SEC-081). Returns nil
// if no domain is configured, or if email matches.
func (c *Client) VerifyDomain(email string) error {
	if c.provider.AllowedDomain == "" {
		return nil
	}
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return errors.New("oauth: email missing @")
	}
	if email[at+1:] != c.provider.AllowedDomain {
		return diag.New("OGON-SEC-081", "oauth: domain mismatch",
			"email domain not in SSO allow-list")
	}
	return nil
}

// Provider returns the underlying provider config.
func (c *Client) Provider() Provider { return c.provider }

// genVerifier produces a 43-128 char cryptographically random code
// verifier per RFC 7636 §4.1.
func genVerifier() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// pkceChallengeS256 computes BASE64URL(SHA256(verifier)).
func pkceChallengeS256(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// StateStash is a temporary store for the OAuth2 state+PKCE verifier
// between the redirect-out and the callback. MUST be tamper-proof
// (HMAC-signed) and short-TTL. A real implementation uses the session
// store; tests can use the in-memory variant.
type StateStash interface {
	Save(ctx context.Context, key, state, verifier string) error
	Load(ctx context.Context, key string) (state, verifier string, err error)
}

// MemoryStateStash is an in-process implementation.
type MemoryStateStash struct {
	mu sync.Mutex
	m  map[string]sv
}

type sv struct {
	state    string
	verifier string
}

// NewMemoryStateStash returns a ready MemoryStateStash.
func NewMemoryStateStash() *MemoryStateStash {
	return &MemoryStateStash{m: map[string]sv{}}
}

// Save stores state+verifier under key.
func (s *MemoryStateStash) Save(_ context.Context, key, state, verifier string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = sv{state: state, verifier: verifier}
	return nil
}

// Load returns state+verifier for key. The entry is removed (single-use).
func (s *MemoryStateStash) Load(_ context.Context, key string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", "", fmt.Errorf("oauth: state not found")
	}
	delete(s.m, key)
	return v.state, v.verifier, nil
}

// Handler is a small helper wiring the redirect-out and callback for
// HTTP servers. Real frameworks plug this into their router.
type Handler struct {
	client *Client
	stash  StateStash
}

// NewHandler constructs a Handler.
func NewHandler(c *Client, stash StateStash) *Handler { return &Handler{client: c, stash: stash} }

// Begin writes the redirect-out: generates state, builds the URL,
// stashes state+verifier keyed by a cookie, and 302s the user.
func (h *Handler) Begin(w http.ResponseWriter, r *http.Request) {
	state := genVerifierID() // 16-byte random
	url, verifier, err := h.client.AuthURL(state)
	if err != nil {
		http.Error(w, "oauth: "+err.Error(), http.StatusInternalServerError)
		return
	}
	cookie := genVerifierID()
	_ = h.stash.Save(r.Context(), cookie, state, verifier)
	http.SetCookie(w, &http.Cookie{
		Name: "ogon.oauth.state", Value: cookie,
		Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: 600,
	})
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback consumes the code, validates state, exchanges for tokens.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie("ogon.oauth.state")
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	state, verifier, err := h.stash.Load(r.Context(), ck.Value)
	if err != nil {
		http.Error(w, "stale state", http.StatusBadRequest)
		return
	}
	if state != r.URL.Query().Get("state") {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	if _, err := h.client.Exchange(r.Context(), r.URL.Query().Get("code"), verifier); err != nil {
		http.Error(w, "exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	// real handler now creates a session; we just 200 for the test surface.
	w.WriteHeader(http.StatusOK)
}

func genVerifierID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
