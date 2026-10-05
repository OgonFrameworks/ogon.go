// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// URL credential redaction in log/config output (CFG-029).

package config

import (
	"regexp"
)

// urlCredsRe matches the userinfo segment of a URL: scheme://user:pass@.
// Captures the scheme prefix and uses it in the replacement so the
// redacted form is scheme://***:***@host.
//
// The pattern requires a non-empty password; URLs without passwords
// (scheme://user@host) are left untouched.
var urlCredsRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^:@/\s]+):([^@\s]+)@`)

// Redact replaces URL credentials (user:pass@host) with ***:***@host in s.
// URLs without credentials are returned unchanged.
//
// Examples:
//
//	Redact("postgres://alice:s3cr3t@db.internal:5432/app")
//	// → "postgres://***:***@db.internal:5432/app"
//
//	Redact("https://api.example.com/path")
//	// → "https://api.example.com/path" (no change)
//
//	Redact("file:///etc/secrets/db_url")
//	// → "file:///etc/secrets/db_url" (no creds to redact)
func Redact(s string) string {
	return urlCredsRe.ReplaceAllString(s, "${1}***:***@")
}

// RedactValue applies Redact to all string leaves in v (recursively for
// maps and slices). Used by logging and Explain output. The input is
// mutated in place; the same value is returned for convenience.
func RedactValue(v any) any {
	switch x := v.(type) {
	case string:
		return Redact(x)
	case []any:
		for i, item := range x {
			x[i] = RedactValue(item)
		}
		return x
	case []string:
		for i, item := range x {
			x[i] = Redact(item)
		}
		return x
	case map[string]any:
		for k, item := range x {
			x[k] = RedactValue(item)
		}
		return x
	case map[any]any:
		for k, item := range x {
			x[k] = RedactValue(item)
		}
		return x
	}
	return v
}
