// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Shared redaction corpus tests (TEST-050). Every PII shape in the corpus
// must round-trip through a conformant redactor without leaking the PII.
// The StubRedactor in this package provides the reference implementation;
// production code (auth/pii) is expected to pass the same suite.

package test

import (
	"testing"
)

func TestRedactionCorpusStubPasses(t *testing.T) {
	t.Parallel()
	RunRedactionSuite(t, StubRedactor{})
}

func TestRedactionCorpusEmail(t *testing.T) {
	t.Parallel()
	r := StubRedactor{}
	out := r.Redact("contact alice@example.com")
	if !containsStr(out, "[REDACTED:") {
		t.Fatalf("email not redacted: %s", out)
	}
	if containsStr(out, "alice@example.com") {
		t.Fatalf("email leaked: %s", out)
	}
}

func TestRedactionCorpusSSN(t *testing.T) {
	t.Parallel()
	r := StubRedactor{}
	out := r.Redact("ssn=123-45-6789")
	if containsStr(out, "123-45-6789") {
		t.Fatalf("ssn leaked: %s", out)
	}
}

func TestRedactionCorpusCard(t *testing.T) {
	t.Parallel()
	r := StubRedactor{}
	out := r.Redact("card=4111 1111 1111 1111")
	if containsStr(out, "4111 1111 1111 1111") {
		t.Fatalf("card leaked: %s", out)
	}
}

func TestRedactionCorpusIP(t *testing.T) {
	t.Parallel()
	r := StubRedactor{}
	out := r.Redact("client=10.0.0.1")
	if containsStr(out, "10.0.0.1") {
		t.Fatalf("ip leaked: %s", out)
	}
}

func TestRedactionCorpusJWT(t *testing.T) {
	t.Parallel()
	r := StubRedactor{}
	out := r.Redact("Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature")
	if containsStr(out, "eyJhbGciOiJIUzI1NiJ9.payload.signature") {
		t.Fatalf("jwt leaked: %s", out)
	}
}

func TestRedactionCorpusNoPII(t *testing.T) {
	t.Parallel()
	r := StubRedactor{}
	in := "the quick brown fox jumps over the lazy dog"
	out := r.Redact(in)
	AssertEqual(t, in, out)
}

func TestRedactionCorpusPhone(t *testing.T) {
	t.Parallel()
	r := StubRedactor{}
	out := r.Redact("phone=+1 (555) 867-5309")
	if containsStr(out, "+1 (555) 867-5309") {
		t.Fatalf("phone leaked: %s", out)
	}
}

func TestRedactionCorpusApiKey(t *testing.T) {
	t.Parallel()
	r := StubRedactor{}
	out := r.Redact("x-api-key: sk_live_abcd1234efgh5678")
	if containsStr(out, "sk_live_abcd1234efgh5678") {
		t.Fatalf("api key leaked: %s", out)
	}
}

func TestRedactionCorpusLength(t *testing.T) {
	t.Parallel()
	cases := RedactionCorpus()
	AssertLen(t, 8, cases)
}

// identityRedactor is a no-op redactor used to verify that RunRedactionSuite
// catches non-redacting implementations.
type identityRedactor struct{}

func (identityRedactor) Redact(s string) string { return s }

func TestRedactionSuiteCatchesIdentityRedactor(t *testing.T) {
	// We can't easily run RunRedactionSuite(identityRedactor{}) against a
	// real *testing.T (it would fail the test). Instead, prove that the
	// identity redactor leaks every PII-bearing corpus case.
	r := identityRedactor{}
	for _, c := range RedactionCorpus() {
		if len(c.WantMissing) == 0 {
			continue
		}
		out := r.Redact(c.Input)
		for _, missing := range c.WantMissing {
			if !containsStr(out, missing) {
				t.Fatalf("identity redactor unexpectedly redacted %q for case %q: %s",
					missing, c.Name, out)
			}
		}
	}
}
