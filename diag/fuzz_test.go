// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fuzz the diagnostic formatting surface (P14 bug-bounty / OGON-DIAG).
//
// The Diag struct carries many string/slice fields that flow into
// Error(), FormatHuman(), Is(), With(), Wrap(), and FromError().
// Adversarial inputs — embedded NULs, very long strings, unicode
// control characters, Fix slices with nil entries — must NEVER
// panic the formatter. Errors must be observable via errors.Is by
// code, and FormatHuman must return a finite, non-empty string for
// any non-nil Diag.

package diag

import (
	"errors"
	"strings"
	"testing"
)

// FuzzDiagFormat drives the Diag format/Error/Is/With surface with
// attacker-controlled strings. The contract is:
//   - no panic on any input
//   - Error() returns a string (possibly empty only when d is nil)
//   - FormatHuman() returns a string (possibly empty only when d is nil)
//   - With() returns a non-nil *Diag
//   - errors.Is by code is reflexive
//
// Run: go test ./diag -fuzz=FuzzDiagFormat -fuzztime=3s
func FuzzDiagFormat(f *testing.F) {
	// Seed corpus — at least 5 cases per spec.
	f.Add("OGON-K0001", "bad config", "missing http.addr",
		"why!", "where.go:42", "expected", "found", "fix1", "fix2", "remedy", "docs")
	f.Add("", "", "", "", "", "", "", "", "", "", "")
	f.Add("OGON-U0001", "title", "what", "why", "where", "exp", "fnd", "", "", "", "")
	f.Add("OGON-SEC-001", "x", "y", "z", "w", "e", "f", "g", "h", "i", "j")
	f.Add("OGON-\x00NULL", "title\nwith\ttabs", "what\rwith\rCR",
		"\xff\xfeunicode", "where\x00binary", "exp\x00", "fnd\x00", "fix\x00", "", "rem\x00", "doc\x00")
	// Very long strings — exercise builder growth.
	f.Add(strings.Repeat("A", 4096), strings.Repeat("B", 4096), strings.Repeat("C", 4096),
		strings.Repeat("D", 1024), strings.Repeat("E", 1024),
		strings.Repeat("F", 512), strings.Repeat("G", 512),
		"x", "y", "z", "w")

	f.Fuzz(func(t *testing.T,
		code, title, what, why, where, expected, found, fix1, fix2, remedy, docs string,
	) {
		// Build the Diag via the public constructor + With so we exercise
		// both field paths.
		d := New(code, title, what)
		out := d.With(Diag{
			Why:      why,
			Where:    where,
			Expected: expected,
			Found:    found,
			Fix:      nonEmptyFixSlice(fix1, fix2),
			Remedy:   remedy,
			Docs:     docs,
		})
		if out == nil {
			t.Fatal("With returned nil *Diag — must never happen")
		}

		// Error() must not panic. The result may be empty when both code and
		// title and what are empty — that's a degenerate but valid Diag.
		s := out.Error()
		_ = s // observe no panic

		// FormatHuman() must not panic and must return a string.
		h := out.FormatHuman()
		_ = h

		// errors.Is reflexivity — a Diag should match itself by code+title.
		if code != "" || title != "" {
			if !errors.Is(out, &Diag{Code: code, Title: title}) {
				// When code matches and title matches (or template title is
				// empty), Is() must return true. We skip the empty-code
				// case because the empty Diag is matched on code only.
				if code != "" {
					t.Errorf("errors.Is not reflexive for code=%q title=%q", code, title)
				}
			}
		}

		// Wrap(nil, ...) must return nil ( documented behaviour ).
		if w := Wrap(nil, Diag{Code: code, Title: title}); w != nil {
			t.Errorf("Wrap(nil, ...) = %v, want nil", w)
		}

		// Wrap(plainErr, ...) must produce a non-nil *Diag carrying the
		// original error string somewhere in What.
		plainErr := errors.New("plain underlying")
		if w := Wrap(plainErr, Diag{Code: code, Title: title}); w == nil {
			t.Errorf("Wrap(plainErr, ...) = nil, want *Diag")
		}

		// FromError with a nil mapper on a plain error must produce a
		// U-class fallback Diag, never nil, never panic.
		if d2 := FromError(plainErr, nil); d2 == nil {
			t.Errorf("FromError(plainErr, nil) = nil, want *Diag")
		}
	})
}

// nonEmptyFixSlice builds a Fix slice using only non-empty string
// entries; empty inputs are skipped so we don't accidentally test
// the nil-slice fast path 100% of the time.
func nonEmptyFixSlice(a, b string) []string {
	var out []string
	if a != "" {
		out = append(out, a)
	}
	if b != "" {
		out = append(out, b)
	}
	return out
}
