// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package auth

import (
	"strings"
	"testing"
)

func TestRedactEmail(t *testing.T) {
	got := RedactString("contact me at alice@example.com tomorrow")
	if !strings.Contains(got, "[REDACTED:email]") {
		t.Fatalf("expected redacted email, got: %s", got)
	}
	if strings.Contains(got, "alice@example.com") {
		t.Fatalf("email leaked: %s", got)
	}
}

func TestRedactJWT(t *testing.T) {
	tok := "Authorization: Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIn0.eyJzdWIiOiJ1MSJ9.signaturepart"
	got := RedactString(tok)
	if strings.Contains(got, "eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIn0") {
		t.Fatalf("jwt leaked: %s", got)
	}
	if !strings.Contains(got, "[REDACTED:jwt]") {
		t.Fatalf("expected jwt marker: %s", got)
	}
}

func TestRedactSSN(t *testing.T) {
	got := RedactString("ssn 123-45-6789 ok")
	if !strings.Contains(got, "[REDACTED:ssn]") {
		t.Fatalf("expected ssn marker: %s", got)
	}
}

func TestRedactCard(t *testing.T) {
	got := RedactString("card 4111111111111111")
	if !strings.Contains(got, "[REDACTED:card]") {
		t.Fatalf("expected card marker: %s", got)
	}
}

func TestRedactRecursiveMap(t *testing.T) {
	in := map[string]any{
		"user": "alice@example.com",
		"nested": map[string]any{
			"password": "hunter2hunter2",
			"safe":     "hello",
			"jwt":      "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1MSJ9.signature-part-here",
		},
	}
	out := RedactRecursive(in).(map[string]any)
	if out["user"] != "[REDACTED:email]" {
		t.Fatalf("email not redacted in nested: %v", out)
	}
	n := out["nested"].(map[string]any)
	if n["jwt"] != "[REDACTED:jwt]" {
		t.Fatalf("nested jwt not redacted: %v", n)
	}
	if n["safe"] != "hello" {
		t.Fatalf("safe field altered: %v", n)
	}
}

func TestIsPIIField(t *testing.T) {
	if !IsPIIField("password") {
		t.Fatal("password should be PII")
	}
	if !IsPIIField("EMAIL") {
		t.Fatal("case-insensitive match expected")
	}
	if IsPIIField("name") {
		t.Fatal("name should not be PII")
	}
	RegisterPIIField("customer_token")
	if !IsPIIField("customer_token") {
		t.Fatal("registered field should be PII")
	}
}
