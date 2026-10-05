// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Config sources: schema defaults, YAML, .env, env overrides, secrets.

package config

import (
	"errors"
	"os"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/goccy/go-yaml"
)

// DefaultSchema returns the canonical OgonGo config schema (OGON-CFG).
// A complete default app needs none of these to be set explicitly —
// the Defaults are sensible for local development.
func DefaultSchema() *Schema {
	keys := []string{
		"ogon",
		"app.name", "app.env",
		"http.addr", "http.read_timeout", "http.body_limit", "http.trusted_proxies",
		"db.primary", "db.pool.min", "db.pool.max", "db.pool.max_lifetime",
		"cache.redis",
		"auth.mode", "auth.session_ttl",
		"obs.otlp", "obs.log",
		"jobs.driver", "jobs.concurrency",
		"infra.cloud", "infra.region",
	}
	known := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		known[k] = struct{}{}
	}
	defaults := map[string]any{
		"ogon":              "1.0",
		"app.env":           "dev",
		"http.addr":         ":3000",
		"http.read_timeout": "30s",
		"http.body_limit":   "1MB",
		"auth.mode":         "session",
		"auth.session_ttl":  "24h",
		"jobs.driver":       "db",
		"jobs.concurrency":  10,
		"obs.log":           "info",
	}
	return &Schema{Known: known, Defaults: defaults}
}

// applyDefaults seeds cfg with schema defaults (layer 1). Defaults are
// flat (dotted keys), but flatten() makes this idempotent if a caller
// constructs nested defaults.
func (l *Loader) applyDefaults(cfg *Config) {
	flat := flatten(l.schema.Defaults)
	for k, v := range flat {
		cfg.resolved[k] = v
		cfg.trace[k] = append(cfg.trace[k], Source{Layer: "defaults", Value: v})
	}
}

// applyFlatLayer applies a flat (already-dotted) values map under layerName.
// Used by layers 6 (explicit) and 7 (CLI flags).
func (l *Loader) applyFlatLayer(cfg *Config, layerName string, values map[string]any) {
	for k, v := range values {
		cfg.resolved[k] = v
		cfg.trace[k] = append(cfg.trace[k], Source{Layer: layerName, Value: v})
	}
}

// applyYAMLLayer parses YAML (from bytes or file), interpolates ${VAR},
// resolves file:// secrets, and applies the resulting flat map under
// layerName. Missing files are silently ignored (zero-config apps).
func (l *Loader) applyYAMLLayer(cfg *Config, layerName, path string, bytes []byte, envMap map[string]string) error {
	var data []byte
	switch {
	case bytes != nil:
		data = bytes
	case path != "":
		b, err := l.fs.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return diag.Wrap(err, diag.Diag{
				Code:  string(diag.CodeConfigInvalid),
				Title: "failed to read " + layerName,
				What:  path,
			})
		}
		data = b
	default:
		return nil
	}

	flat, err := parseYAML(data)
	if err != nil {
		return diag.Wrap(err, diag.Diag{
			Code:  string(diag.CodeConfigInvalid),
			Title: "failed to parse " + layerName,
			What:  err.Error(),
		})
	}

	// Interpolate ${VAR} in string values, then resolve file:// secrets.
	// For file://-sourced values, the trace records the original
	// "file://<resolved>" marker instead of the secret content so Explain
	// never reveals secrets (CFG-029).
	for k, v := range flat {
		interpolated := interpolateValue(v, envMap)
		resolved := l.resolveSecret(interpolated)
		cfg.resolved[k] = resolved

		traceVal := resolved
		if s, ok := interpolated.(string); ok && hasFileRef(s) {
			traceVal = "file://<resolved>"
		}
		cfg.trace[k] = append(cfg.trace[k], Source{
			Layer: layerName,
			Value: traceVal,
			Path:  path,
		})
	}
	return nil
}

// hasFileRef reports whether s is a file:// reference (used to decide
// whether to redact the resolved content in trace).
func hasFileRef(s string) bool {
	return strings.HasPrefix(s, "file://")
}

// applyEnvOverrides scans envMap for OGON_* vars, maps each to a config
// key, and applies it as an override (layer 5). .env-sourced OGON_* vars
// are tagged with "(.env)" in the layer name.
func (l *Loader) applyEnvOverrides(cfg *Config, envMap map[string]string) {
	dotEnvKeys := l.dotEnvKeySet()

	for envName, val := range envMap {
		if !strings.HasPrefix(envName, OGONPrefix) {
			continue
		}
		cfgKey := ParseEnvNameWithSchema(envName, l.schema.Known)
		if cfgKey == "" {
			continue
		}
		layerName := "env:" + envName
		if dotEnvKeys[envName] {
			layerName = "env:" + envName + " (.env)"
		}
		cfg.resolved[cfgKey] = val
		cfg.trace[cfgKey] = append(cfg.trace[cfgKey], Source{Layer: layerName, Value: val})
	}
}

// dotEnvKeySet returns the set of keys defined in the .env file (whether
// they came from bytes or a file path). Used to tag env overrides sourced
// from .env for Explain attribution.
func (l *Loader) dotEnvKeySet() map[string]bool {
	if l.dotEnvBytes != nil {
		return keySet(parseDotEnv(l.dotEnvBytes))
	}
	if l.dotEnvPath != "" {
		if data, err := l.fs.ReadFile(l.dotEnvPath); err == nil {
			return keySet(parseDotEnv(data))
		}
	}
	return map[string]bool{}
}

func keySet(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// buildEffectiveEnv merges the env reader output (real env or WithEnvMap)
// with .env additions. .env never overrides existing env vars (PROMPT.md
// Part V.3 rule 4). The result is used for both ${VAR} interpolation in
// YAML layers and OGON_* override mapping.
func (l *Loader) buildEffectiveEnv() map[string]string {
	envMap := make(map[string]string)
	if l.envReader != nil {
		for k, v := range l.envReader() {
			envMap[k] = v
		}
	}

	var dotEnv map[string]string
	switch {
	case l.dotEnvBytes != nil:
		dotEnv = parseDotEnv(l.dotEnvBytes)
	case l.dotEnvPath != "":
		if data, err := l.fs.ReadFile(l.dotEnvPath); err == nil {
			dotEnv = parseDotEnv(data)
		}
	}
	for k, v := range dotEnv {
		// .env never overrides existing env (rule 4).
		if _, exists := envMap[k]; !exists {
			envMap[k] = v
		}
	}
	return envMap
}

// resolveSecret resolves file://path references by reading the file. The
// value is left untouched on read failure (logged as a warning). Used by
// the YAML layer (CFG-012 secrets via mounted files).
func (l *Loader) resolveSecret(v any) any {
	s, ok := v.(string)
	if !ok || !strings.HasPrefix(s, "file://") {
		return v
	}
	path := strings.TrimPrefix(s, "file://")
	data, err := l.fs.ReadFile(path)
	if err != nil {
		l.log.Warn("failed to read secret file",
			"path", path, "err", err.Error())
		return v
	}
	return strings.TrimSpace(string(data))
}

// parseYAML unmarshals data into a flat (dotted-key) map[string]any.
// Nested maps are flattened via flatten().
func parseYAML(data []byte) (map[string]any, error) {
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return map[string]any{}, nil
	}
	return flatten(out), nil
}

// flatten converts a nested map[string]any into a flat map with dotted
// keys (e.g. {"http": {"addr": ":3000"}} → {"http.addr": ":3000"}).
// Already-flat keys (no nesting) pass through unchanged. Lists and
// scalars are leaves.
func flatten(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	var rec func(prefix string, m map[string]any)
	rec = func(prefix string, m map[string]any) {
		for k, v := range m {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			switch x := v.(type) {
			case map[string]any:
				rec(key, x)
			case map[any]any:
				rec(key, anyMapToStringMap(x))
			default:
				out[key] = v
			}
		}
	}
	rec("", in)
	return out
}

// anyMapToStringMap converts a map[any]any (some YAML parsers' default)
// to map[string]any. Non-string keys are formatted via fmt.Sprint.
func anyMapToStringMap(m map[any]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		ks, _ := k.(string)
		if ks == "" {
			ks = "unknown"
		}
		out[ks] = v
	}
	return out
}

// parseDotEnv parses a .env file into a map. Each line is KEY=VALUE;
// blank lines and # comments are skipped. Surrounding quotes on values
// are stripped.
func parseDotEnv(data []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		out[k] = v
	}
	return out
}

// detectUnknownKeys walks every layer in cfg.trace and flags keys not in
// the schema. Each unknown key is attributed to the first non-defaults
// layer that set it. The nearest known key within Levenshtein distance
// ≤ 2 is suggested as a typo fix (CFG-009).
func (l *Loader) detectUnknownKeys(cfg *Config) {
	seen := make(map[string]bool)
	for key, sources := range cfg.trace {
		if _, ok := l.schema.Known[key]; ok {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		layer := ""
		for _, s := range sources {
			if s.Layer != "defaults" {
				layer = s.Layer
				break
			}
		}
		cfg.unknown = append(cfg.unknown, UnknownKey{
			Key:        key,
			Suggestion: suggest(key, l.schema.Known),
			Layer:      layer,
		})
	}
}

// suggest returns the nearest known key within Levenshtein distance ≤ 2,
// or "" if none. Used for typo suggestions on unknown keys.
func suggest(key string, known map[string]struct{}) string {
	best := ""
	bestDist := 3 // > 2 means no suggestion
	for k := range known {
		d := levenshtein(key, k)
		if d < bestDist {
			bestDist = d
			best = k
		}
	}
	return best
}

// levenshtein computes the edit distance between a and b. Used for
// typo suggestions on unknown config keys (CFG-009).
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	if a == b {
		return 0
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := prev[j] + 1
			if x := curr[j-1] + 1; x < d {
				d = x
			}
			if x := prev[j-1] + cost; x < d {
				d = x
			}
			curr[j] = d
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
