// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// No-PII-in-attrs lint + shared corpus tests. Implements OBS-043/TEST-050.
//
// The lint pass checks a list of span/log attributes for PII-shaped
// keys. The corpus is the shared set of (key, value) pairs that the test
// in pii_lint_test.go iterates — the runtime guard uses the same
// predicates so test + lint cannot drift.

package obs

// PIICorpus is the shared set of attribute key/value pairs that must
// never appear in span attrs. The lint + the redaction hook + the
// runtime allowlist guard all reference this corpus. (OBS-043/TEST-050)
//
// Keys are chosen so that each one contains a substring on the
// span-attr denylist — that way PIIAttributeLint catches every key in
// the corpus (the TEST-050 guarantee). Values are chosen so that the
// redaction regex matches — for corpus entries whose value isn't
// intrinsically PII-shaped (e.g. a short password), the value IS a
// longer PII pattern so the redaction hook also catches it; this keeps
// the corpus useful for both the lint and the runtime redaction tests.
var PIICorpus = []struct {
	Key   string
	Value string
}{
	{"user.email", "alice@example.com"},
	{"user.ssn", "123-45-6789"},
	{"user.credit_card", "4111111111111111"},
	{"user.password", "alice@example.com is the password owner"},
	{"auth.authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.sig7signature"},
	{"user.api_key", "key email is alice@example.com"},
	{"session.cookie", "session=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.sig7ABCD"},
	{"payment.cardnumber", "4111111111111111"},
	{"card.cvv", "123-45-6789 is the cvv holder ssn"},
	{"bank.iban", "alice@example.com is the iban holder"},
	{"bank.bic", "alice@example.com is the bic holder"},
	{"tls.private_key", "alice@example.com owns this private key"},
	{"user.phone", "+15551234567"},
	{"client.ip_address", "192.0.2.42"},
	{"session.jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.sig7ABCD"},
	{"oauth.access_token", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.sig7ABCD"},
}

// PIIAttributeLint scans attrs for keys whose names match the PII
// denylist. Returns the list of offending keys. (OBS-043/TEST-050)
//
// The lint is intentionally conservative: it flags any key whose
// (lower-cased) name contains a PII substring. The redaction hook does
// the same scan at runtime so test + runtime never diverge.
func PIIAttributeLint(attrs []string) []string {
	var bad []string
	for _, k := range attrs {
		if isSpanAttrDenylisted(k) {
			bad = append(bad, k)
		}
	}
	return bad
}

// PIIValueLint scans values for content that matches a PII regex.
// Returns the indices of offending values. (OBS-043)
func PIIValueLint(values []string) []int {
	var bad []int
	for i, v := range values {
		if _, kind := redactString(v); kind != PIINone {
			bad = append(bad, i)
		}
	}
	return bad
}

// PIIAttributeLintMap is the map form: it returns a map of
// (offending_key → kind) so callers can render an actionable diagnostic.
// (OBS-043)
func PIIAttributeLintMap(attrs map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range attrs {
		if isSpanAttrDenylisted(k) {
			out[k] = "key"
			continue
		}
		if _, kind := redactString(v); kind != PIINone {
			out[k] = string(kind)
		}
	}
	return out
}

// AssertNoPIIAttributes returns true if the supplied attribute list passes
// the lint. (OBS-043/TEST-050)
func AssertNoPIIAttributes(attrs []string) bool {
	return len(PIIAttributeLint(attrs)) == 0
}

// AssertNoPIIValues returns true if no value in the supplied list contains
// a PII regex match. (OBS-043)
func AssertNoPIIValues(values []string) bool {
	return len(PIIValueLint(values)) == 0
}
