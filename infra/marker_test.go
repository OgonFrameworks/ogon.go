// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Marker helper tests (INFRA-038). Verifies Stamp, IsOwned, IsSeeded,
// MarkerOf and the manual-edit detection surface.

package infra

import (
	"strings"
	"testing"
)

// TestStampAddsLicenseAndMarker verifies the header is prepended.
func TestStampAddsLicenseAndMarker(t *testing.T) {
	out := Stamp("body", MarkOwned, "#")
	if !strings.Contains(out, "SPDX-License-Identifier: MIT") {
		t.Error("expected SPDX header")
	}
	if !strings.Contains(out, "ogon:owned") {
		t.Error("expected ogon:owned marker line")
	}
	if !strings.HasSuffix(out, "body") {
		t.Error("expected body preserved at end")
	}
}

// TestStampNoMarker verifies a zero marker still gets the license header.
func TestStampNoMarker(t *testing.T) {
	out := Stamp("body", "", "#")
	if !strings.Contains(out, "SPDX-License-Identifier: MIT") {
		t.Error("expected SPDX header even with zero marker")
	}
	if strings.Contains(out, "ogon:owned") || strings.Contains(out, "ogon:seeded") || strings.Contains(out, "ogon:generated") {
		t.Error("expected no marker line when marker is zero (user-owned file)")
	}
}

// TestIsOwned verifies ownership detection.
func TestIsOwned(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"# ogon:owned\nbody", true},
		{"// ogon:owned\nbody", true},
		{"# ogon:generated\nbody", false}, // generated is passive, not owned
		{"# ogon:seeded\nbody", false},
		{"plain user content", false},
	}
	for _, c := range cases {
		if got := IsOwned(c.content); got != c.want {
			t.Errorf("IsOwned(%q) = %v, want %v", c.content, got, c.want)
		}
	}
}

// TestIsSeeded verifies seeded detection.
func TestIsSeeded(t *testing.T) {
	if !IsSeeded("# ogon:seeded\nbody") {
		t.Error("expected seeded=true")
	}
	if IsSeeded("# ogon:owned\nbody") {
		t.Error("owned is not seeded")
	}
}

// TestIsGenerated verifies generated detection.
func TestIsGenerated(t *testing.T) {
	if !IsGenerated("# ogon:generated\nbody") {
		t.Error("expected generated=true")
	}
	// MarkOwned files implicitly carry MarkGenerated in some templates; verify
	// IsGenerated does not require both.
	if IsGenerated("plain content") {
		t.Error("plain content is not generated")
	}
}

// TestMarkerOf verifies priority: owned > seeded > generated > none.
func TestMarkerOf(t *testing.T) {
	cases := []struct {
		content string
		want    Marker
	}{
		{"# ogon:owned\nbody", MarkOwned},
		{"# ogon:seeded\nbody", MarkSeeded},
		{"# ogon:generated\nbody", MarkGenerated},
		{"plain user content", ""},
	}
	for _, c := range cases {
		if got := MarkerOf(c.content); got != c.want {
			t.Errorf("MarkerOf(%q) = %q, want %q", c.content, got, c.want)
		}
	}
}

// TestIsUserOwned verifies the unowned check (drives --force requirement).
func TestIsUserOwned(t *testing.T) {
	if !IsUserOwned("plain content") {
		t.Error("plain content is user-owned")
	}
	if IsUserOwned("# ogon:owned\nbody") {
		t.Error("owned file is not user-owned")
	}
}

// TestExtractMeta verifies the combined extraction.
func TestExtractMeta(t *testing.T) {
	content := Stamp("body", MarkOwned, "#")
	m, hasLicense := ExtractMeta(content)
	if m != MarkOwned {
		t.Errorf("marker = %q, want %q", m, MarkOwned)
	}
	if !hasLicense {
		t.Error("expected license header present")
	}
}

// TestCommentPrefixFor verifies per-extension prefix selection.
func TestCommentPrefixFor(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"foo.go", "//"},
		{"Dockerfile", "#"},
		{"compose.yaml", "#"},
		{"main.tf", "#"},
		{"run.sh", "#"},
		{"README.md", "<!--"},
		{"Makefile", "#"},
		{"unknown.xyz", "#"},
	}
	for _, c := range cases {
		if got := CommentPrefixFor(c.path); got != c.want {
			t.Errorf("CommentPrefixFor(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestMarkerLine verifies the comment-line rendering.
func TestMarkerLine(t *testing.T) {
	if got := (MarkOwned).MarkerLine("#"); got != "# ogon:owned\n" {
		t.Errorf("marker line = %q, want %q", got, "# ogon:owned\n")
	}
	if got := (MarkSeeded).MarkerLine("//"); got != "// ogon:seeded\n" {
		t.Errorf("marker line = %q, want %q", got, "// ogon:seeded\n")
	}
}

// TestHasMarkerScanLimit verifies marker detection only looks in the first
// 4 KiB (so a marker deep in a file doesn't trigger).
func TestHasMarkerScanLimit(t *testing.T) {
	// 8 KiB of filler then a marker at the end.
	fill := strings.Repeat("a", 8192) + "# ogon:owned"
	if HasMarker(fill, MarkOwned) {
		t.Error("marker beyond scan limit should not be detected")
	}
	// Marker within scan limit is detected.
	closeMarker := strings.Repeat("a", 100) + "# ogon:owned"
	if !HasMarker(closeMarker, MarkOwned) {
		t.Error("marker within scan limit should be detected")
	}
}
