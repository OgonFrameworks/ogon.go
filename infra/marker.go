// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Marker helpers (INFRA-038). Markers are content-level tags embedded at the
// top of every generated artifact; they distinguish "safe to regenerate"
// (MarkOwned) from "user-edited, needs --force" and "seeded once, hand off".
//
// The marker is intentionally a stable substring rather than a separate
// sidecar: grep survives, copy/paste survives, and the same byte-stream the
// user commits is the source of truth. Manual-edit detection is by content
// hash ledger (idempotent.go), not by absence of the marker.

package infra

import (
	"strings"
)

// HasMarker reports whether content carries a given marker line. A marker
// line is a comment line ("# ogon:owned" or "// ogon:owned") anywhere in the
// first 4 KiB of the file.
func HasMarker(content string, m Marker) bool {
	const scanLimit = 4096
	if len(content) > scanLimit {
		content = content[:scanLimit]
	}
	needle := " " + string(m)
	return strings.Contains(content, needle)
}

// IsOwned reports whether content is ogon-owned (MarkOwned). Owned files are
// safe to regenerate; an owned file whose content has been locally edited
// still requires --force — see idempotent.go.
func IsOwned(content string) bool { return HasMarker(content, MarkOwned) }

// IsGenerated reports whether content is generator output (MarkGenerated).
// MarkGenerated is a passive tag; it does NOT make the file regenerable.
func IsGenerated(content string) bool { return HasMarker(content, MarkGenerated) }

// IsSeeded reports whether content is a one-time seed (MarkSeeded). Seeded
// files are written once and then handed to the user; regenerating them
// requires --force.
func IsSeeded(content string) bool { return HasMarker(content, MarkSeeded) }

// MarkerOf reports the strongest ownership marker present in content. Order:
// owned > seeded > generated > none. "none" means the file is user-owned and
// cannot be regenerated without --force.
func MarkerOf(content string) Marker {
	switch {
	case IsOwned(content):
		return MarkOwned
	case IsSeeded(content):
		return MarkSeeded
	case IsGenerated(content):
		return MarkGenerated
	}
	return ""
}

// IsUserOwned reports whether content is unowned (no marker). Such files
// require --force to overwrite (INFRA-038/040).
func IsUserOwned(content string) bool { return MarkerOf(content) == "" }

// Stamp writes the license header + marker line at the top of content. If
// the marker is the zero value, only the license header is written — the
// file becomes a one-time seed requiring --force on regen (INFRA-038).
func Stamp(content string, m Marker, commentPrefix string) string {
	var b strings.Builder
	b.WriteString(LicenseHeader)
	if m != "" {
		b.WriteString(m.MarkerLine(commentPrefix))
	}
	b.WriteString("\n")
	b.WriteString(content)
	return b.String()
}

// ExtractMeta returns the marker present and whether the content also carries
// the license header. Useful for drift reports (diff.go).
func ExtractMeta(content string) (m Marker, hasLicense bool) {
	m = MarkerOf(content)
	hasLicense = strings.Contains(content, "SPDX-License-Identifier")
	return
}

// CommentPrefixFor returns the comment prefix for a file path. Unknown
// extensions default to "#" (works for Dockerfile, YAML, HCL).
func CommentPrefixFor(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "//"
	case strings.HasSuffix(path, ".sh"):
		return "#"
	case strings.HasSuffix(path, ".tf"):
		return "#"
	case strings.HasSuffix(path, ".yml"), strings.HasSuffix(path, ".yaml"):
		return "#"
	case strings.HasSuffix(path, "Dockerfile"), strings.HasSuffix(path, ".dockerignore"):
		return "#"
	case strings.HasSuffix(path, "Makefile"):
		return "#"
	case strings.HasSuffix(path, ".md"):
		return "<!--"
	}
	return "#"
}
