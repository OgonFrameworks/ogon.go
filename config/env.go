// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// OGON_SECTION_KEY env var mapping (CFG-008, layer 5 of PROMPT.md V.3).

package config

import "strings"

// OGONPrefix is the env var prefix that maps to config keys.
// Example: OGON_HTTP_ADDR → http.addr.
const OGONPrefix = "OGON_"

// ParseEnvName converts an OGON_* env var name to its config key path.
// Without schema context, the first underscore becomes a dot and the rest
// is left as-is (so OGON_HTTP_READ_TIMEOUT → http.read_timeout). With
// schema context, a secondary candidate (all underscores → dots) is
// tried if the primary doesn't match a known key.
//
// Returns "" if envName does not start with OGON_.
//
// Examples (no schema):
//
//	ParseEnvName("OGON_HTTP_ADDR")         // → "http.addr"
//	ParseEnvName("OGON_HTTP_READ_TIMEOUT") // → "http.read_timeout"
//	ParseEnvName("NOT_OGON")               // → ""
func ParseEnvName(envName string) string {
	return ParseEnvNameWithSchema(envName, nil)
}

// ParseEnvNameWithSchema is the schema-aware variant. When primary mapping
// doesn't yield a known key, the secondary mapping (all underscores → dots)
// is checked. Useful for nested keys:
//
//	ParseEnvNameWithSchema("OGON_DB_POOL_MAX", schema.Known) // → "db.pool.max"
//
// If neither candidate is known, primary is returned (still useful for
// traceability — the unknown-key detector will flag it).
func ParseEnvNameWithSchema(envName string, known map[string]struct{}) string {
	if !strings.HasPrefix(envName, OGONPrefix) {
		return ""
	}
	rest := envName[len(OGONPrefix):]
	if rest == "" {
		return ""
	}
	lower := strings.ToLower(rest)

	// Primary: replace only the first underscore with a dot.
	// HTTP_ADDR → http.addr, HTTP_READ_TIMEOUT → http.read_timeout.
	primary := strings.Replace(lower, "_", ".", 1)
	if known == nil {
		return primary
	}
	if _, ok := known[primary]; ok {
		return primary
	}

	// Secondary: replace all underscores with dots.
	// DB_POOL_MAX → db.pool.max.
	secondary := strings.ReplaceAll(lower, "_", ".")
	if _, ok := known[secondary]; ok {
		return secondary
	}

	return primary
}
