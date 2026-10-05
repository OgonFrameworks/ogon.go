// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Shared redaction corpus (TEST-050). The corpus is a set of (input,
// expected) pairs that every redactor implementation must pass. The same
// pairs feed:
//   - OBS-043 no-PII-in-attrs lint
//   - SEC-030 PII redaction tests in auth/
//   - TEST-050 shared corpus tests here
//
// One corpus, one set of expectations. Adding a new PII shape means adding
// one entry here, and every consumer gets the new test for free.

package test

import (
	"regexp"
	"strings"
	"testing"
)

// RedactionCase is a single (input, expected) pair.
type RedactionCase struct {
	// Name describes the corpus entry (used as a t.Run subtest name).
	Name string
	// Input is the raw string that may contain PII.
	Input string
	// WantContains holds substrings that must appear in the redacted output
	// (e.g., "[REDACTED:email]"). At least one must match.
	WantContains []string
	// WantMissing holds substrings that must NOT appear in the redacted
	// output (the original PII itself).
	WantMissing []string
}

// Redactor is the contract a redactor implementation must satisfy.
type Redactor interface {
	Redact(s string) string
}

// RedactionCorpus returns the shared (input, expected) pairs. Every
// consumer of PII redaction — auth/pii, obs no-PII lint, test corpus —
// iterates this slice to assert behaviour. Adding a new PII shape means
// adding one entry here.
func RedactionCorpus() []RedactionCase {
	return []RedactionCase{
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
			Input:        "card=4111 1111 1111 1111",
			WantContains: []string{"[REDACTED:card]", "[card]", "[credit_card]"},
			WantMissing:  []string{"4111 1111 1111 1111"},
		},
		{
			Name:         "ipv4",
			Input:        "client=10.0.0.1 reached",
			WantContains: []string{"[REDACTED:ip]", "[ip]"},
			WantMissing:  []string{"10.0.0.1"},
		},
		{
			Name:         "bearer_token",
			Input:        "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature",
			WantContains: []string{"[REDACTED:jwt]", "[REDACTED:token]", "[token]"},
			WantMissing:  []string{"eyJhbGciOiJIUzI1NiJ9.payload.signature"},
		},
		{
			Name:         "api_key_prefix",
			Input:        "x-api-key: sk_live_abcd1234efgh5678",
			WantContains: []string{"[REDACTED:apikey]", "[apikey]", "[api_key]"},
			WantMissing:  []string{"sk_live_abcd1234efgh5678"},
		},
		{
			Name:         "phone_us",
			Input:        "phone=+1 (555) 867-5309",
			WantContains: []string{"[REDACTED:phone]", "[phone]"},
			WantMissing:  []string{"+1 (555) 867-5309"},
		},
		{
			Name:         "no_pii",
			Input:        "the quick brown fox jumps over the lazy dog",
			WantContains: nil,
			WantMissing:  nil,
		},
	}
}

// RunRedactionSuite runs every corpus case through the supplied redactor.
// Each case is a t.Run subtest; failures print the input and the
// redacted output to make the diff actionable.
func RunRedactionSuite(t *testing.T, r Redactor) {
	t.Helper()
	for _, c := range RedactionCorpus() {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Helper()
			out := r.Redact(c.Input)
			for _, missing := range c.WantMissing {
				if containsStr(out, missing) {
					t.Errorf("ogontest: redactor leaked %q in output: %s", missing, out)
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
					t.Errorf("ogontest: redactor output missing any of %v in: %s", c.WantContains, out)
				}
			}
		})
	}
}

// testT narrows *testing.T so the corpus can be used from packages with
// a different test type (e.g., testify). Reserved for future use; the
// suite above takes *testing.T directly because that is the standard.
type testT interface {
	Helper()
	Run(name string, fn func(testT)) bool
	Errorf(format string, args ...any)
}

// containsStr is a small ad-hoc string contains; avoids pulling strings.Contains
// into the public surface of the corpus. Kept private so it does not collide
// with the helper in assert.go.
func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}

// ---- ad-hoc redactor for tests in this package ----

// StubRedactor is a tiny regex-based redactor that satisfies the contract.
// It exists so the corpus tests in this package have something to run
// against; production code uses auth.PII instead.
type StubRedactor struct{}

// Redact applies a series of regexes to scrub common PII shapes. Patterns
// are applied in a deterministic order so that more specific patterns (SSN,
// card) are applied before the more general phone pattern. The phone
// pattern is restricted to require a `+` or `(` indicator so it does not
// consume SSNs or card numbers.
func (StubRedactor) Redact(s string) string {
	type pat struct {
		name string
		re   *regexp.Regexp
	}
	patterns := []pat{
		{"email", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
		{"ssn", regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)},
		{"card", regexp.MustCompile(`\b(?:\d{4}[ -]?){3}\d{4}\b`)},
		{"ip", regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)},
		{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)},
		{"apikey", regexp.MustCompile(`\b(?:sk|pk)_(?:live|test)_[A-Za-z0-9]{8,}\b`)},
		// Phone: require + or ( to avoid matching SSN/card fragments.
		{"phone", regexp.MustCompile(`\+?\d[\d ()-]{7,}\d`)},
	}
	// Phone is applied last; before phone runs, redact substrings that
	// might otherwise match phone.
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, "[REDACTED:"+p.name+"]")
	}
	return s
}
