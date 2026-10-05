// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Passkeys / WebAuthn (SEC-016, SEC-062).
//
// We use github.com/go-webauthn/webauthn for the protocol layer;
// ceremony rate limiting is layered on top via auth/ratelimit.go.

package passkey

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// User is the framework-side contract for WebAuthn users.
type User interface {
	WebAuthnID() []byte          // stable, unique, unguessable
	WebAuthnName() string        // display name
	WebAuthnDisplayName() string // human-friendly
	WebAuthnCredentials() []webauthn.Credential
	AddCredential(c *webauthn.Credential)
}

// CeremonyLimiter gates registration and authentication ceremonies
// (SEC-062 rate limits). Implementations should use auth/ratelimit.
type CeremonyLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// Service wraps a webauthn.WebAuthn instance plus ceremony bookkeeping.
type Service struct {
	w       *webauthn.WebAuthn
	limiter CeremonyLimiter

	mu      sync.Mutex
	pending map[string]*Ceremony // sessionID → in-flight ceremony
}

// Ceremony represents an in-flight registration or authentication.
type Ceremony struct {
	UserID    string
	Kind      string // "registration" or "authentication"
	Challenge string
	CreatedAt time.Time
}

// Config configures the WebAuthn service.
type Config struct {
	RPDisplayName string
	RPID          string
	RPOrigins     []string
	Timeout       time.Duration // default 60s
}

// New constructs a Service.
func New(cfg Config, limiter CeremonyLimiter) (*Service, error) {
	if cfg.RPID == "" || cfg.RPDisplayName == "" {
		return nil, diag.New("OGON-SEC-016", "webauthn: missing RP config", "")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	w, err := webauthn.New(&webauthn.Config{
		RPDisplayName: cfg.RPDisplayName,
		RPID:          cfg.RPID,
		RPOrigins:     cfg.RPOrigins,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: cfg.Timeout, TimeoutUVD: cfg.Timeout},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: cfg.Timeout, TimeoutUVD: cfg.Timeout},
		},
	})
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-016", Title: "webauthn: init"})
	}
	return &Service{
		w:       w,
		limiter: limiter,
		pending: map[string]*Ceremony{},
	}, nil
}

// BeginRegistration returns the credential-creation options to send to
// the browser. The sessionID is opaque and must be stashed server-side.
func (s *Service) BeginRegistration(ctx context.Context, u User, sessionID string) (*protocol.CredentialCreation, error) {
	if s.limiter != nil {
		ok, err := s.limiter.Allow(ctx, "reg:"+string(u.WebAuthnID()))
		if err != nil || !ok {
			return nil, diag.New("OGON-SEC-062", "webauthn: rate limited", "registration ceremony throttled")
		}
	}
	opts, sess, err := s.w.BeginRegistration(u)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-016", Title: "webauthn: begin registration"})
	}
	s.store(sessionID, &Ceremony{
		UserID: u.WebAuthnName(), Kind: "registration",
		Challenge: string(sess.Challenge), CreatedAt: time.Now(),
	})
	return opts, nil
}

// FinishRegistration validates the credential-creation response and
// stores the new credential on u.
func (s *Service) FinishRegistration(ctx context.Context, u User, sessionID string,
	parsed *protocol.ParsedCredentialCreationData) (webauthn.Credential, error) {
	cer, ok := s.load(sessionID)
	if !ok || cer.Kind != "registration" {
		return webauthn.Credential{}, errors.New("webauthn: no pending registration")
	}
	// webauthn library handles signature verification internally.
	cred, err := s.w.CreateCredential(u, webauthn.SessionData{
		Challenge: cer.Challenge,
		UserID:    u.WebAuthnID(),
	}, parsed)
	if err != nil {
		return webauthn.Credential{}, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-016", Title: "webauthn: create credential"})
	}
	u.AddCredential(cred)
	s.delete(sessionID)
	return *cred, nil
}

// BeginAuthentication returns the assertion options for the browser.
func (s *Service) BeginAuthentication(ctx context.Context, u User, sessionID string) (*protocol.CredentialAssertion, error) {
	if s.limiter != nil {
		ok, err := s.limiter.Allow(ctx, "auth:"+string(u.WebAuthnID()))
		if err != nil || !ok {
			return nil, diag.New("OGON-SEC-062", "webauthn: rate limited", "authentication ceremony throttled")
		}
	}
	opts, sess, err := s.w.BeginLogin(u)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-016", Title: "webauthn: begin authentication"})
	}
	s.store(sessionID, &Ceremony{
		UserID: u.WebAuthnName(), Kind: "authentication",
		Challenge: string(sess.Challenge), CreatedAt: time.Now(),
	})
	return opts, nil
}

// FinishAuthentication validates the assertion response. Returns the
// credential that was used (so callers can update sign-count).
func (s *Service) FinishAuthentication(ctx context.Context, u User, sessionID string,
	parsed *protocol.ParsedCredentialAssertionData) (webauthn.Credential, error) {
	cer, ok := s.load(sessionID)
	if !ok || cer.Kind != "authentication" {
		return webauthn.Credential{}, errors.New("webauthn: no pending authentication")
	}
	cred, err := s.w.ValidateLogin(u, webauthn.SessionData{
		Challenge: cer.Challenge,
		UserID:    u.WebAuthnID(),
	}, parsed)
	if err != nil {
		return webauthn.Credential{}, diag.Wrap(err, diag.Diag{Code: "OGON-SEC-016", Title: "webauthn: validate login"})
	}
	s.delete(sessionID)
	return *cred, nil
}

// CeremonyRequestParser is the type of the protocol.ParseCredentialCreation*
// functions supplied by go-webauthn. We accept them as opaque args.
type CeremonyRequestParser interface{}

// HandleRegistrationResponse is a thin HTTP helper for tests; real
// frameworks plug their JSON decoder in directly.
func (s *Service) HandleRegistrationResponse(w http.ResponseWriter, r *http.Request,
	u User, sessionID string, parseFn func(*http.Request) (*protocol.ParsedCredentialCreationData, error)) {
	parsed, err := parseFn(r)
	if err != nil {
		http.Error(w, "parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.FinishRegistration(r.Context(), u, sessionID, parsed); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- internal session storage ----

func (s *Service) store(id string, c *Ceremony) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[id] = c
	// expire stale ceremonies (defense in depth)
	for k, v := range s.pending {
		if time.Since(v.CreatedAt) > 5*time.Minute {
			delete(s.pending, k)
		}
	}
}

func (s *Service) load(id string) (*Ceremony, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.pending[id]
	if !ok {
		return nil, false
	}
	if time.Since(c.CreatedAt) > 5*time.Minute {
		delete(s.pending, id)
		return nil, false
	}
	return c, true
}

func (s *Service) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}
