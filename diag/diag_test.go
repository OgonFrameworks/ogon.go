// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the diag package.

package diag

import (
	"errors"
	"testing"
)

func TestDiagError(t *testing.T) {
	t.Parallel()
	d := New("OGON-K0001", "bad config", "missing http.addr")
	got := d.Error()
	want := "OGON-K0001: bad config"
	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(d, &Diag{Code: "OGON-K0001"}) {
		t.Fatal("errors.Is should match by code")
	}
}

func TestDiagWith(t *testing.T) {
	t.Parallel()
	base := New("OGON-V0001", "validation", "email required")
	out := base.With(Diag{Where: "app/models/user.go:42", Remedy: "ogon gen route"})
	if out.Where != "app/models/user.go:42" {
		t.Fatalf("Where not merged: %q", out.Where)
	}
	if out.Remedy != "ogon gen route" {
		t.Fatalf("Remedy not merged: %q", out.Remedy)
	}
	if out.Code != base.Code {
		t.Fatal("Code should be preserved")
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()
	err := errors.New("disk full")
	d := Wrap(err, Diag{Code: "OGON-U0001", Title: "disk failure"})
	if d.Code != "OGON-U0001" {
		t.Fatal("Wrap should preserve template code")
	}
	if d.What != "disk full" {
		t.Fatalf("Wrap should keep original message: %q", d.What)
	}
}

func TestFromErrorFallsBack(t *testing.T) {
	t.Parallel()
	d := FromError(errors.New("oops"), nil)
	if d.Code != "OGON-U0001" {
		t.Fatalf("fallback code = %q", d.Code)
	}
}

func TestFormatHumanHasRemedy(t *testing.T) {
	t.Parallel()
	d := New("OGON-K0001", "config invalid", "no addr")
	d.Remedy = "ogon doctor --fix"
	out := d.FormatHuman()
	if !contains(out, "remedy:") {
		t.Fatalf("expected remedy line in: %s", out)
	}
	if !contains(out, "OGON-K0001") {
		t.Fatalf("expected code in: %s", out)
	}
}

func TestSeverityString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    Severity
		want string
	}{
		{SeverityInfo, "info"},
		{SeverityWarning, "warning"},
		{SeverityError, "error"},
		{99, "unknown"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Severity(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
