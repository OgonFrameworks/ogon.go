// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// PII redaction (SEC-030, SEC-031).
//
// Redact scans logs/spans for known PII patterns and replaces them
// with [REDACTED:<kind>] markers. Field classification tags (the
// "classify:pii" tag, OBS-043) drive structured redaction in the
// slog adapter.

package auth

import (
	"regexp"
	"strings"
	"sync"
)

// PIISpec describes a redactable pattern.
type PIISpec struct {
	Name    string // e.g. "email", "jwt", "ssn", "card"
	Pattern *regexp.Regexp
	// Replacement controls the marker text; default "[REDACTED:<name>]".
	Replacement string
}

// piiRegistry is the global PII pattern set. Extend at boot via RegisterPII.
var (
	piiMu       sync.RWMutex
	piiRegistry = defaultPIISet()
)

func defaultPIISet() []PIISpec {
	return []PIISpec{
		{
			Name:        "email",
			Pattern:     regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`),
			Replacement: "[REDACTED:email]",
		},
		{
			Name:        "ssn",
			Pattern:     regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			Replacement: "[REDACTED:ssn]",
		},
		{
			Name: "card",
			// Visa/MC/Amex/discover; 13-19 digits, optional separators
			Pattern:     regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`),
			Replacement: "[REDACTED:card]",
		},
		{
			Name:        "jwt",
			Pattern:     regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{4,}\b`),
			Replacement: "[REDACTED:jwt]",
		},
		{
			Name:        "ipv4",
			Pattern:     regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
			Replacement: "[REDACTED:ipv4]",
		},
	}
}

// RegisterPII appends a custom PII pattern (use at boot, not per-request).
func RegisterPII(spec PIISpec) {
	piiMu.Lock()
	defer piiMu.Unlock()
	if spec.Replacement == "" {
		spec.Replacement = "[REDACTED:" + spec.Name + "]"
	}
	piiRegistry = append(piiRegistry, spec)
}

// RedactString scans input and replaces all known PII patterns with
// their redaction markers. The function is safe for concurrent use.
func RedactString(input string) string {
	piiMu.RLock()
	defer piiMu.RUnlock()
	out := input
	for _, spec := range piiRegistry {
		out = spec.Pattern.ReplaceAllString(out, spec.Replacement)
	}
	return out
}

// RedactMap returns a shallow copy of m with all string values redacted.
// Nested maps and slices are NOT traversed; use RedactRecursive for that.
func RedactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch s := v.(type) {
		case string:
			out[k] = RedactString(s)
		default:
			out[k] = v
		}
	}
	return out
}

// RedactRecursive walks m (and any nested map/slice) and redacts every
// string value. Returns a deep copy.
func RedactRecursive(v any) any {
	switch t := v.(type) {
	case string:
		return RedactString(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = RedactRecursive(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = RedactRecursive(val)
		}
		return out
	default:
		return v
	}
}

// IsPIIField reports whether a key matches the "classify:pii" tag set.
// Real systems derive this from a struct tag; we use a small heuristic
// list. Extend at boot via RegisterPIIField.
var (
	piiFieldsMu sync.RWMutex
	piiFields   = map[string]bool{
		"email": true, "password": true, "ssn": true, "card": true,
		"secret": true, "token": true, "api_key": true,
		"access_token": true, "refresh_token": true,
	}
)

// RegisterPIIField adds a key name to the structured-field classification set.
func RegisterPIIField(name string) {
	piiFieldsMu.Lock()
	defer piiFieldsMu.Unlock()
	piiFields[strings.ToLower(name)] = true
}

// IsPIIField returns whether name is in the PII classification set.
func IsPIIField(name string) bool {
	piiFieldsMu.RLock()
	defer piiFieldsMu.RUnlock()
	return piiFields[strings.ToLower(name)]
}
