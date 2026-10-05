// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// ${VAR} interpolation for YAML config values.

package config

import (
	"regexp"
)

// interpRe matches ${VAR} references in string values. VAR must start with
// an uppercase letter or underscore and may contain uppercase letters,
// digits, and underscores. Lowercase vars are intentionally not matched —
// env var conventions are uppercase.
var interpRe = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// Interpolate replaces every ${VAR} occurrence in s with env[VAR]. Unknown
// vars are left untouched (caller may detect them later if desired).
//
// Example:
//
//	Interpolate("postgres://${USER}:${PASS}@host/db", map[string]string{
//	    "USER": "alice",
//	    "PASS": "secret",
//	})
//	// → "postgres://alice:secret@host/db"
func Interpolate(s string, env map[string]string) string {
	return interpRe.ReplaceAllStringFunc(s, func(m string) string {
		// m is the full "${VAR}" match; strip the surrounding ${ }.
		name := m[2 : len(m)-1]
		if v, ok := env[name]; ok {
			return v
		}
		return m
	})
}

// interpolateValue applies Interpolate recursively to all string leaves in
// v. Maps and slices are walked; non-string scalars pass through. This is
// used by the YAML layer to interpolate ${VAR} against the effective env.
func interpolateValue(v any, env map[string]string) any {
	switch x := v.(type) {
	case string:
		return Interpolate(x, env)
	case map[string]any:
		for k, item := range x {
			x[k] = interpolateValue(item, env)
		}
		return x
	case map[any]any:
		out := anyMapToStringMap(x)
		for k, item := range out {
			out[k] = interpolateValue(item, env)
		}
		return out
	case []any:
		for i, item := range x {
			x[i] = interpolateValue(item, env)
		}
		return x
	case []string:
		for i, item := range x {
			x[i] = Interpolate(item, env)
		}
		return x
	}
	return v
}
