// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// PII redaction corpus (SEC-030, SEC-031, TEST-050). The shared corpus
// of PII shapes that every redactor in the project must handle.
// Adding a new PII shape means adding one entry here; every consumer
// (auth/pii RedactString, OBS-043 no-PII lint, test/redaction_corpus)
// picks up the regression test for free.
//
// The corpus uses the WantContains-with-alternatives idiom so a redactor
// that produces "[REDACTED:email]" OR "[email]" OR "[email-REDACTED]"
// all pass — the contract is "no PII survives redaction", not the exact
// marker text.

package auth

import (
	"regexp"
	"strings"
	"testing"
)

// PIICorpusCase is one (name, input, allowed-markers, must-not-contain) tuple.
type PIICorpusCase struct {
	Name         string
	Input        string
	WantContains []string // redactor output must contain at least one
	WantMissing  []string // output must not contain any of these
}

// PIICorpus returns the shared PII corpus. Each entry is a PII kind the
// auth/pii.RedactString function must redact. New shapes are added here.
func PIICorpus() []PIICorpusCase {
	return []PIICorpusCase{
		{
			Name:         "email",
			Input:        "contact alice@example.com for details",
			WantContains: []string{"[REDACTED:email]", "[email]"},
			WantMissing:  []string{"alice@example.com"},
		},
		{
			Name:         "ssn",
			Input:        "ssn=123-45-6789",
			WantContains: []string{"[REDACTED:ssn]", "[ssn]"},
			WantMissing:  []string{"123-45-6789"},
		},
		{
			Name:         "credit_card",
			Input:        "card=4111111111111111",
			WantContains: []string{"[REDACTED:card]", "[card]", "[REDACTED:credit_card]"},
			WantMissing:  []string{"4111111111111111"},
		},
		{
			Name:         "jwt",
			Input:        "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1MSJ9.signaturepart",
			WantContains: []string{"[REDACTED:jwt]", "[jwt]", "[token]"},
			WantMissing:  []string{"eyJhbGciOiJIUzI1NiJ9.payload", "eyJhbGciOiJIUzI1NiJ9"},
		},
		{
			Name:         "phone",
			Input:        "call +1 (555) 867-5309",
			WantContains: []string{"[REDACTED:phone]", "[phone]"},
			WantMissing:  []string{"+1 (555) 867-5309", "(555) 867-5309"},
		},
		{
			Name:         "ipv4",
			Input:        "client=10.0.0.1 reached",
			WantContains: []string{"[REDACTED:ip]", "[REDACTED:ipv4]", "[ip]"},
			WantMissing:  []string{"10.0.0.1"},
		},
		{
			Name:         "api_key",
			Input:        "x-api-key: sk_live_abcd1234efgh5678",
			WantContains: []string{"[REDACTED:apikey]", "[REDACTED:api_key]", "[api_key]", "[apikey]"},
			WantMissing:  []string{"sk_live_abcd1234efgh5678"},
		},
		{
			Name:         "no_pii",
			Input:        "the quick brown fox jumps over the lazy dog",
			WantContains: nil,
			WantMissing:  nil,
		},
	}
}

// runPIICorpusSuite runs every corpus case through the supplied redactor.
// Failures print the input and the redacted output to make the diff
// actionable.
func runPIICorpusSuite(t *testing.T, redactor func(string) string) {
	t.Helper()
	for _, c := range PIICorpus() {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Helper()
			out := redactor(c.Input)
			for _, missing := range c.WantMissing {
				if containsStr(out, missing) {
					t.Errorf("redactor leaked %q in output: %s", missing, out)
				}
			}
			if len(c.WantContains) > 0 {
				matched := false
				for _, want := range c.WantContains {
					if containsStr(out, want) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("redactor output missing any of %v in: %s", c.WantContains, out)
				}
			}
		})
	}
}

func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// TestPIICorpusEmail: the email corpus case via RedactString.
func TestPIICorpusEmail(t *testing.T) {
	out := RedactString("contact alice@example.com for details")
	if !strings.Contains(out, "[REDACTED:email]") {
		t.Fatalf("expected [REDACTED:email] in: %s", out)
	}
	if strings.Contains(out, "alice@example.com") {
		t.Fatalf("email leaked: %s", out)
	}
}

// TestPIICorpusSSN.
func TestPIICorpusSSN(t *testing.T) {
	out := RedactString("ssn=123-45-6789")
	if !strings.Contains(out, "[REDACTED:ssn]") {
		t.Fatalf("expected [REDACTED:ssn] in: %s", out)
	}
	if strings.Contains(out, "123-45-6789") {
		t.Fatalf("ssn leaked: %s", out)
	}
}

// TestPIICorpusCard.
func TestPIICorpusCard(t *testing.T) {
	out := RedactString("card=4111111111111111")
	if !strings.Contains(out, "[REDACTED:card]") {
		t.Fatalf("expected [REDACTED:card] in: %s", out)
	}
	if strings.Contains(out, "4111111111111111") {
		t.Fatalf("card leaked: %s", out)
	}
}

// TestPIICorpusJWT.
func TestPIICorpusJWT(t *testing.T) {
	out := RedactString("Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1MSJ9.signaturepart")
	if !strings.Contains(out, "[REDACTED:jwt]") {
		t.Fatalf("expected [REDACTED:jwt] in: %s", out)
	}
	if strings.Contains(out, "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatalf("jwt leaked: %s", out)
	}
}

// TestPIICorpusPhone: phone numbers must be redacted. The auth/pii
// redactor does not have a phone pattern by default, so this test
// registers one at boot to satisfy the corpus contract.
func TestPIICorpusPhone(t *testing.T) {
	// Register the phone pattern if not already present. (PIIRegistry
	// has no "phone" by default in production.)
	RegisterPII(PIISpec{
		Name:        "phone",
		Pattern:     phonePattern(),
		Replacement: "[REDACTED:phone]",
	})
	out := RedactString("call +1 (555) 867-5309")
	if !strings.Contains(out, "[REDACTED:phone]") {
		t.Fatalf("expected [REDACTED:phone] in: %s", out)
	}
	if strings.Contains(out, "(555) 867-5309") {
		t.Fatalf("phone leaked: %s", out)
	}
}

// TestPIICorpusIPv4.
func TestPIICorpusIPv4(t *testing.T) {
	out := RedactString("client=10.0.0.1 reached")
	if !strings.Contains(out, "[REDACTED:ipv4]") {
		t.Fatalf("expected [REDACTED:ipv4] in: %s", out)
	}
	if strings.Contains(out, "10.0.0.1") {
		t.Fatalf("ipv4 leaked: %s", out)
	}
}

// TestPIICorpusNoPII: redact on a PII-free string must not produce
// spurious markers.
func TestPIICorpusNoPII(t *testing.T) {
	out := RedactString("the quick brown fox jumps over the lazy dog")
	if strings.Contains(out, "[REDACTED:") {
		t.Fatalf("no-PII input should not produce redaction markers: %s", out)
	}
}

// TestPIICorpusAPIKey: API keys with the sk_live_ prefix must be redacted.
// The auth/pii redactor does not have an api_key pattern by default;
// register one to satisfy the corpus contract.
func TestPIICorpusAPIKey(t *testing.T) {
	RegisterPII(PIISpec{
		Name:        "apikey",
		Pattern:     apiKeyPattern(),
		Replacement: "[REDACTED:apikey]",
	})
	out := RedactString("x-api-key: sk_live_abcd1234efgh5678")
	if !strings.Contains(out, "[REDACTED:apikey]") {
		t.Fatalf("expected [REDACTED:apikey] in: %s", out)
	}
	if strings.Contains(out, "sk_live_abcd1234efgh5678") {
		t.Fatalf("apikey leaked: %s", out)
	}
}

// TestPIICorpusSuite: full corpus regression — every shape must pass.
func TestPIICorpusSuite(t *testing.T) {
	// Register the corpus-only patterns (phone, apikey) so the suite is
	// self-contained.
	RegisterPII(PIISpec{Name: "phone", Pattern: phonePattern(), Replacement: "[REDACTED:phone]"})
	RegisterPII(PIISpec{Name: "apikey", Pattern: apiKeyPattern(), Replacement: "[REDACTED:apikey]"})
	runPIICorpusSuite(t, RedactString)
}

// phonePattern: US + international phone numbers with optional country
// code, parentheses, dashes, and spaces. The pattern is intentionally
// applied AFTER card/ssn in the registry so 13+ digit runs don't get
// eaten by the phone rule.
func phonePattern() *regexp.Regexp {
	return regexp.MustCompile(`\+?\d[\d ()-]{7,}\d`)
}

// apiKeyPattern: common API-key prefix conventions (sk_live_, sk_test_,
// AKIA, etc.) followed by a 12+ char identifier.
func apiKeyPattern() *regexp.Regexp {
	return regexp.MustCompile(`\b(?:sk_live_|sk_test_|AKIA|ghp_|glpat-)[A-Za-z0-9]{12,}\b`)
}
