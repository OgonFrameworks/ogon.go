// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package token

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"testing"
	"time"

	"crypto/x509"
)

func mustRSA(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("rsa gen: %v", err)
	}
	return k
}

func TestIssueAndVerifyAccess(t *testing.T) {
	k := mustRSA(t, 2048)
	iss, err := NewIssuer(Options{Issuer: "ogon", Audience: "ogon-app"}, &Key{Kid: "k1", Alg: AlgRS256, Key: k})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	tok, err := iss.IssueAccess(context.Background(), "u1", "t1", []string{"read"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	mc, err := iss.VerifyAccess(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if mc["sub"] != "u1" {
		t.Fatalf("wrong sub: %v", mc["sub"])
	}
	if mc["type"] != "access" {
		t.Fatalf("wrong type: %v", mc["type"])
	}
}

func TestVerifyRejectsAlgBlocked(t *testing.T) {
	k := mustRSA(t, 2048)
	iss, _ := NewIssuer(Options{Issuer: "ogon", Audience: "app"},
		&Key{Kid: "k1", Alg: AlgRS256, Key: k})
	// alg-confusion attack: HS256 with forged kid pointing at our key
	forged := "eyJhbGciOiJIUzI1NiIsImtpZCI6ImsxIn0.eyJzdWIiOiJ1MSJ9.bogus"
	if _, err := iss.VerifyAccess(context.Background(), forged); err == nil {
		t.Fatal("should reject HS256 alg (SEC-083 alg-confusion)")
	}
}

func TestVerifyRejectsWrongAudience(t *testing.T) {
	k := mustRSA(t, 2048)
	iss, _ := NewIssuer(Options{Issuer: "ogon", Audience: "app1"}, &Key{Kid: "k1", Alg: AlgRS256, Key: k})
	tok, _ := iss.IssueAccess(context.Background(), "u1", "t1", nil)
	iss2, _ := NewIssuer(Options{Issuer: "ogon", Audience: "app2"}, &Key{Kid: "k1", Alg: AlgRS256, Key: k})
	iss2.jwks.set(&PublicKey{Kid: "k1", Alg: AlgRS256, Key: &k.PublicKey})
	if _, err := iss2.VerifyAccess(context.Background(), tok); err == nil {
		t.Fatal("should reject wrong audience")
	}
}

func TestRefreshRotationSingleUse(t *testing.T) {
	k := mustRSA(t, 2048)
	iss, _ := NewIssuer(Options{Issuer: "ogon", Audience: "app"}, &Key{Kid: "k1", Alg: AlgRS256, Key: k})
	refresh, err := iss.IssueRefresh(context.Background(), "u1", "t1", []string{"read"})
	if err != nil {
		t.Fatalf("issue refresh: %v", err)
	}
	access, newRefresh, err := iss.Refresh(context.Background(), refresh)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if access == "" || newRefresh == "" {
		t.Fatal("empty tokens")
	}
	if newRefresh == refresh {
		t.Fatal("new refresh should differ from old")
	}
	// reuse of old refresh should fail (SEC-083)
	if _, _, err := iss.Refresh(context.Background(), refresh); err == nil {
		t.Fatal("reuse of consumed refresh should fail")
	}
	// new refresh should work once
	if _, _, err := iss.Refresh(context.Background(), newRefresh); err != nil {
		t.Fatalf("second rotation should succeed: %v", err)
	}
}

func TestRefreshExpired(t *testing.T) {
	k := mustRSA(t, 2048)
	iss, _ := NewIssuer(Options{
		Issuer: "ogon", Audience: "app",
		RefreshTTL: 1 * time.Millisecond,
	}, &Key{Kid: "k1", Alg: AlgRS256, Key: k})
	refresh, _ := iss.IssueRefresh(context.Background(), "u1", "t1", nil)
	time.Sleep(50 * time.Millisecond)
	if _, _, err := iss.Refresh(context.Background(), refresh); err == nil {
		t.Fatal("expired refresh should fail")
	}
}

// keep encoding/pem + x509 referenced (JWKS parser uses them in callers).
var _ = pem.EncodeToMemory
var _ = x509.NewCertPool
