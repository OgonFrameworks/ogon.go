// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Edge cases for the OBS PII lint (P14 bug-bounty / OBS-043/TEST-050).
// Each test exercises one adversarial input shape on the lint + value
// redaction surface: empty attrs, very long values, nested PII,
// unicode PII, binary content, etc. The contract: no panic; correct
// detection of well-known PII patterns; conservative behaviour on
// adversarial input.

package obs

import (
	"strings"
	"testing"
)

// TestEdgePIILintEmptyAttrs — Lint on empty / nil attribute list
// must return nil (no offenders), never panic.
func TestEdgePIILintEmptyAttrs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PIIAttributeLint(empty) panicked: %v", r)
		}
	}()
	if got := PIIAttributeLint(nil); len(got) != 0 {
		t.Fatalf("PIIAttributeLint(nil) = %v; want empty", got)
	}
	if got := PIIAttributeLint([]string{}); len(got) != 0 {
		t.Fatalf("PIIAttributeLint([]) = %v; want empty", got)
	}
	if got := PIIAttributeLintMap(nil); len(got) != 0 {
		t.Fatalf("PIIAttributeLintMap(nil) = %v; want empty", got)
	}
	if got := PIIAttributeLintMap(map[string]string{}); len(got) != 0 {
		t.Fatalf("PIIAttributeLintMap({}) = %v; want empty", got)
	}
}

// TestEdgePIILintVeryLongKeys — A 64k-char key that happens to
// contain a denylisted substring (e.g. "password") must be detected
// without panic.
func TestEdgePIILintVeryLongKeys(t *testing.T) {
	long := strings.Repeat("a", 64*1024) + "password"
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PIIAttributeLint(long) panicked: %v", r)
		}
	}()
	got := PIIAttributeLint([]string{long})
	if len(got) != 1 {
		t.Fatalf("PIIAttributeLint(long-with-pw) = %v; want 1 hit", got)
	}
}

// TestEdgePIILintVeryLongValues — A 64k-char value that matches a
// redaction regex (email-shaped) must be detected without panic.
func TestEdgePIILintVeryLongValues(t *testing.T) {
	longVal := strings.Repeat("a", 64*1024) + "@example.com"
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PIIValueLint(long) panicked: %v", r)
		}
	}()
	got := PIIValueLint([]string{longVal})
	if len(got) != 1 {
		t.Fatalf("PIIValueLint(long-email) = %v; want [0]", got)
	}
}

// TestEdgePIILintMapWithLongKeyAndValue — PIIAttributeLintMap on
// adversarial keys + values must not panic; both key-side and
// value-side offenders must be detected.
func TestEdgePIILintMapWithLongKeyAndValue(t *testing.T) {
	m := map[string]string{
		"user." + strings.Repeat("password", 100): "alice@example.com",
		"ok.key": "not pii",
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PIIAttributeLintMap(long) panicked: %v", r)
		}
	}()
	got := PIIAttributeLintMap(m)
	if len(got) != 1 {
		t.Fatalf("PIIAttributeLintMap = %v; want 1 offender", got)
	}
	if _, ok := got["user."+strings.Repeat("password", 100)]; !ok {
		t.Fatalf("offender key missing in %v", got)
	}
}

// TestEdgePIILintNestedPII — A value containing multiple PII shapes
// (email + phone + ssn + credit card). The lint must flag at least
// one (PIIValueLint returns indices; the actual redaction may
// cascade). The contract: no panic, the value is flagged.
func TestEdgePIILintNestedPII(t *testing.T) {
	value := "alice@example.com +15551234567 123-45-6789 4111111111111111"
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PIIValueLint(nested) panicked: %v", r)
		}
	}()
	got := PIIValueLint([]string{value})
	if len(got) != 1 {
		t.Fatalf("PIIValueLint(nested-PII) = %v; want [0]", got)
	}
}

// TestEdgePIILintUnicodePII — A value with non-ASCII unicode that
// still embeds a PII regex match. Must be detected without panic.
func TestEdgePIILintUnicodePII(t *testing.T) {
	cases := []string{
		"用户邮箱: alice@example.com",      // Chinese "user email"
		"email 사용자: alice@example.com", // Korean
		"japanese email ユーザー: alice@example.com",
		"🔒 alice@example.com 🔥",
		"alice@example.com\t\t\t",
		"\x00alice@example.com\x00", // embedded NULs
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PIIValueLint(unicode) panicked: %v", r)
		}
	}()
	for _, v := range cases {
		got := PIIValueLint([]string{v})
		if len(got) != 1 {
			t.Errorf("PIIValueLint(%q) = %v; want [0]", v, got)
		}
	}
}

// TestEdgeRedactStringEmpty — RedactString on empty / nil-equivalent
// inputs must return the input unchanged, never panic.
func TestEdgeRedactStringEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RedactString(\"\") panicked: %v", r)
		}
	}()
	if got := RedactString(""); got != "" {
		t.Fatalf("RedactString(\"\") = %q; want %q", got, "")
	}
	if got := RedactString("no pii here"); got != "no pii here" {
		t.Fatalf("RedactString(no-pii) = %q; want unchanged", got)
	}
}

// TestEdgeRedactStringBinary — RedactString on binary garbage must
// not panic; output is observable (no PII match → unchanged).
func TestEdgeRedactStringBinary(t *testing.T) {
	bin := string([]byte{0x00, 0x01, 0x02, 0x03, 'a', '@', 'b', '.', 'c', 'o', 'm'})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RedactString(binary) panicked: %v", r)
		}
	}()
	_ = RedactString(bin)
}

// TestEdgeRedactStringHugeValue — RedactString on a 1MB value must
// complete without panic. The value is constructed to contain one
// email so we can assert the redaction happens.
func TestEdgeRedactStringHugeValue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping huge redaction edge test in -short")
	}
	huge := strings.Repeat("noise ", 200_000) + "alice@example.com"
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RedactString(huge) panicked: %v", r)
		}
	}()
	out := RedactString(huge)
	if !strings.Contains(out, "[REDACTED:email]") {
		t.Fatal("RedactString(huge) did not redact the embedded email")
	}
	if strings.Contains(out, "alice@example.com") {
		t.Fatal("RedactString(huge) leaked the email address")
	}
}

// TestEdgeRedactMapWithNilValue — RedactMap on a map containing a
// nil value must not panic.
func TestEdgeRedactMapWithNilValue(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RedactMap(nil-value) panicked: %v", r)
		}
	}()
	m := map[string]any{"key": nil, "email": "alice@example.com"}
	out := RedactMap(m)
	if got, ok := out["email"].(string); !ok || strings.Contains(got, "alice") {
		t.Fatalf("RedactMap did not redact email value: %v", out["email"])
	}
}

// TestEdgeRedactValueDeepNesting — RedactValue on a deeply nested
// map/slice structure must complete without panic.
func TestEdgeRedactValueDeepNesting(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RedactValue(deep) panicked: %v", r)
		}
	}()
	// 50 levels of nesting — not so deep that it blows the Go stack
	// (we cap at 50; deeper values are caller bugs).
	var v any = "alice@example.com"
	for i := 0; i < 50; i++ {
		v = map[string]any{"nested": v}
	}
	out := RedactValue(v)
	_ = out
}

// TestEdgeRedactValueAllKinds — RedactValue on every Go kind we
// advertise support for. None of these must panic.
func TestEdgeRedactValueAllKinds(t *testing.T) {
	cases := []any{
		nil,
		"plain string",
		42,
		3.14,
		true,
		[]byte("byte slice with alice@example.com"),
		map[string]any{"email": "alice@example.com"},
		[]any{"alice@example.com", "bob@example.com"},
		[]any{1, 2, 3},
	}
	for _, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("RedactValue(%T) panicked: %v", c, r)
				}
			}()
			_ = RedactValue(c)
		}()
	}
}

// TestEdgeAssertNoPIIAttributesEdgeCases — AssertNoPIIAttributes on
// adversarial inputs must not panic.
func TestEdgeAssertNoPIIAttributesEdgeCases(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AssertNoPIIAttributes(adv) panicked: %v", r)
		}
	}()
	if !AssertNoPIIAttributes(nil) {
		t.Fatal("AssertNoPIIAttributes(nil) = false; want true")
	}
	if !AssertNoPIIAttributes([]string{}) {
		t.Fatal("AssertNoPIIAttributes([]) = false; want true")
	}
	if AssertNoPIIAttributes([]string{"password"}) {
		t.Fatal("AssertNoPIIAttributes([password]) = true; want false")
	}
	// Mixed-case should also be caught (the denylist is case-folded).
	if AssertNoPIIAttributes([]string{"USER_PASSWORD"}) {
		t.Fatal("AssertNoPIIAttributes([USER_PASSWORD]) = true; want false (case-folded)")
	}
}

// TestEdgeAssertNoPIIValuesEdgeCases — AssertNoPIIValues on
// adversarial inputs must not panic.
func TestEdgeAssertNoPIIValuesEdgeCases(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AssertNoPIIValues(adv) panicked: %v", r)
		}
	}()
	if !AssertNoPIIValues(nil) {
		t.Fatal("AssertNoPIIValues(nil) = false; want true")
	}
	if !AssertNoPIIValues([]string{}) {
		t.Fatal("AssertNoPIIValues([]) = false; want true")
	}
	if !AssertNoPIIValues([]string{"just a message", "123", "ok"}) {
		t.Fatal("AssertNoPIIValues(non-pii) = false; want true")
	}
	if AssertNoPIIValues([]string{"alice@example.com"}) {
		t.Fatal("AssertNoPIIValues([email]) = true; want false")
	}
}
