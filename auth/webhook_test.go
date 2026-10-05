// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookVerifyGood(t *testing.T) {
	secret := []byte("shh")
	cfg := DefaultWebhookConfig(secret)
	v := NewWebhookVerifier(cfg, nil)
	body := []byte(`{"event":"ping"}`)
	sig := Sign(secret, body, "hex")

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(cfg.HeaderSig, sig)
	r.Header.Set(cfg.HeaderNonce, "nonce-1")
	if err := v.Verify(context.Background(), r, body); err != nil {
		t.Fatalf("verify should pass: %v", err)
	}
}

func TestWebhookReplayRejected(t *testing.T) {
	secret := []byte("shh")
	cfg := DefaultWebhookConfig(secret)
	v := NewWebhookVerifier(cfg, nil)
	body := []byte(`{"event":"ping"}`)
	sig := Sign(secret, body, "hex")

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(cfg.HeaderSig, sig)
	r.Header.Set(cfg.HeaderNonce, "nonce-2")
	if err := v.Verify(context.Background(), r, body); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	// same nonce → reject
	r2 := httptest.NewRequest(http.MethodPost, "/", nil)
	r2.Header.Set(cfg.HeaderSig, sig)
	r2.Header.Set(cfg.HeaderNonce, "nonce-2")
	if err := v.Verify(context.Background(), r2, body); err == nil {
		t.Fatal("replay should be rejected (SEC-038)")
	}
}

func TestWebhookBadSig(t *testing.T) {
	secret := []byte("shh")
	cfg := DefaultWebhookConfig(secret)
	v := NewWebhookVerifier(cfg, nil)
	body := []byte(`{"event":"ping"}`)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(cfg.HeaderSig, "deadbeef")
	r.Header.Set(cfg.HeaderNonce, "nonce-3")
	if err := v.Verify(context.Background(), r, body); err == nil {
		t.Fatal("bad signature should be rejected (SEC-039)")
	}
}

func TestWebhookMissingSig(t *testing.T) {
	v := NewWebhookVerifier(DefaultWebhookConfig([]byte("k")), nil)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := v.Verify(context.Background(), r, []byte("body")); err == nil {
		t.Fatal("missing signature should be rejected")
	}
}

func TestSignPrefixScheme(t *testing.T) {
	got := Sign([]byte("k"), []byte("body"), "prefix")
	if got[:7] != "sha256=" {
		t.Fatalf("prefix scheme expected: %s", got)
	}
}
