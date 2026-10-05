// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package obs

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactString_Email(t *testing.T) {
	in := "user email is alice@example.com please"
	out, kind := redactString(in)
	if kind != PIIEmail {
		t.Errorf("kind = %v, want email", kind)
	}
	if !strings.Contains(out, "[REDACTED:email]") {
		t.Errorf("redacted: %q", out)
	}
	if strings.Contains(out, "alice@example.com") {
		t.Errorf("leaked email: %q", out)
	}
}

func TestRedactString_SSN(t *testing.T) {
	in := "ssn is 123-45-6789 yes"
	out, kind := redactString(in)
	if kind != PIISSN {
		t.Errorf("kind = %v, want ssn", kind)
	}
	if strings.Contains(out, "123-45-6789") {
		t.Errorf("leaked ssn: %q", out)
	}
}

func TestRedactString_CreditCard(t *testing.T) {
	in := "card 4111111111111111 ok"
	out, kind := redactString(in)
	if kind != PIICredit {
		t.Errorf("kind = %v, want credit_card", kind)
	}
	if strings.Contains(out, "4111111111111111") {
		t.Errorf("leaked card: %q", out)
	}
}

func TestRedactString_JWT(t *testing.T) {
	in := "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.sig7ABCD"
	out, kind := redactString(in)
	if kind != PIIJWT {
		t.Errorf("kind = %v, want jwt", kind)
	}
	if strings.Contains(out, "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.sig7ABCD") {
		t.Errorf("leaked jwt: %q", out)
	}
}

func TestRedactString_UUID(t *testing.T) {
	in := "id is 123e4567-e89b-12d3-a456-426614174000 ok"
	out, kind := redactString(in)
	if kind != PIIUUID {
		t.Errorf("kind = %v, want uuid", kind)
	}
	if strings.Contains(out, "123e4567-e89b-12d3-a456-426614174000") {
		t.Errorf("leaked uuid: %q", out)
	}
}

func TestRedactString_Phone(t *testing.T) {
	in := "phone is +15551234567"
	out, kind := redactString(in)
	if kind != PIIPhone {
		t.Errorf("kind = %v, want phone", kind)
	}
	if strings.Contains(out, "+15551234567") {
		t.Errorf("leaked phone: %q", out)
	}
}

func TestRedactString_IPv4(t *testing.T) {
	in := "client 192.0.2.42 connected"
	out, kind := redactString(in)
	if kind != PIIIPv4 {
		t.Errorf("kind = %v, want ipv4", kind)
	}
	if strings.Contains(out, "192.0.2.42") {
		t.Errorf("leaked ipv4: %q", out)
	}
}

func TestRedactString_NoPII(t *testing.T) {
	in := "just a normal log message"
	out, kind := redactString(in)
	if kind != PIINone {
		t.Errorf("kind = %v, want none", kind)
	}
	if out != in {
		t.Errorf("output changed: %q", out)
	}
}

func TestRedactMap(t *testing.T) {
	in := map[string]any{
		"user":   "alice@example.com",
		"safe":   "ok",
		"nested": map[string]any{"email": "bob@example.com"},
	}
	out := RedactMap(in)
	if strings.Contains(out["user"].(string), "alice@example.com") {
		t.Error("user email leaked")
	}
	if out["safe"] != "ok" {
		t.Errorf("safe field changed: %v", out["safe"])
	}
	nested, _ := json.Marshal(out["nested"])
	if strings.Contains(string(nested), "bob@example.com") {
		t.Error("nested email leaked")
	}
}

func TestRedactionHandler_StripsAttr(t *testing.T) {
	// Build a logger with the redaction handler, then verify a PII
	// attribute is scrubbed from the output JSON.
	var buf = &bytesWriter{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	log := slog.New(wrapWithRedaction(h))
	log.Info("test", "email", "alice@example.com", "password", "hunter2")
	if strings.Contains(buf.String(), "alice@example.com") {
		t.Errorf("redacted JSON leaked email: %s", buf.String())
	}
	if strings.Contains(buf.String(), "hunter2") {
		t.Errorf("redacted JSON leaked password: %s", buf.String())
	}
}

func TestIsPIIKey(t *testing.T) {
	good := []string{"password", "user_token", "x-api-key", "credit_card", "user.email", "session.jwt"}
	for _, k := range good {
		if !isPIIKey(k) {
			t.Errorf("isPIIKey(%q) = false, want true", k)
		}
	}
	bad := []string{"method", "route", "status", "duration_ms"}
	for _, k := range bad {
		if isPIIKey(k) {
			t.Errorf("isPIIKey(%q) = true, want false", k)
		}
	}
}

// bytesWriter is a tiny io.Writer that stores bytes for assertion.
type bytesWriter struct {
	buf []byte
}

func (b *bytesWriter) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *bytesWriter) String() string { return string(b.buf) }
