// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Config system: layered configuration loading with stable precedence.

package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// Schema enumerates known config keys and their defaults. The Known set is
// used for unknown-key detection (CFG-009); Defaults seed the lowest
// precedence layer (layer 1 in the PROMPT.md Part V.3 contract).
type Schema struct {
	Known    map[string]struct{}
	Defaults map[string]any
}

// Source records one contribution to a config key's final value. Explain
// returns a list of these for the requested key, in precedence order.
type Source struct {
	// Layer names the source: "defaults", "ogon.yaml", "ogon.dev.yaml",
	// "env:OGON_HTTP_ADDR", "explicit", "cli:--addr", etc.
	Layer string
	// Value is the value this layer supplied for the key. For file://
	// secrets, this is the original "file://path" reference (the resolved
	// secret lives in the resolved map, not here).
	Value any
	// Path is the optional file path for the layer (e.g. the YAML file).
	Path string
}

// UnknownKey flags a key not present in the schema. Suggestion is the nearest
// known key within Levenshtein distance ≤ 2 (empty if none).
type UnknownKey struct {
	Key        string
	Suggestion string
	Layer      string
}

// Explanation is the structured result of Explain(key).
type Explanation struct {
	Key     string
	Value   any
	Sources []Source
}

// Config is the resolved, immutable configuration. Build it via Loader.Load.
//
// Invariants:
//   - resolved[key] holds the final value after all layers applied.
//   - trace[key] lists every source layer that set the key, in precedence
//     order. defaults appears first, CLI flags last.
//   - unknown lists every key seen in any layer that is not in the schema.
type Config struct {
	schema   *Schema
	resolved map[string]any
	trace    map[string][]Source
	unknown  []UnknownKey
	strict   bool
}

// Get returns the resolved value for a key, or nil if not set.
func (c *Config) Get(key string) any {
	return c.resolved[key]
}

// GetString returns the resolved value formatted as a string. Missing keys
// return "". Lists and maps are rendered via fmt.Sprint. The returned string
// is the raw value; use Redact before logging.
func (c *Config) GetString(key string) string {
	v, ok := c.resolved[key]
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// UnknownKeys returns unknown keys encountered during load (with suggestions).
func (c *Config) UnknownKeys() []UnknownKey {
	return c.unknown
}

// fileSystem is the file access interface (overridable for tests).
type fileSystem interface {
	ReadFile(name string) ([]byte, error)
}

type osFS struct{}

func (osFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// Loader builds a Config by applying sources in precedence order. Configure
// it via NewLoader + With* options, then call Load.
//
// Precedence (lowest → highest, per PROMPT.md Part V.3):
//  1. struct defaults (schema.Defaults)
//  2. ogon.yaml
//  3. ogon.<env>.yaml (dev|test|prod)
//  4. .env (populates env vars, never overrides existing env)
//  5. environment variables OGON_SECTION_KEY (e.g. OGON_HTTP_ADDR)
//  6. explicit code (ogon.Provide / Boot options)
//  7. CLI flags (ogon run --addr)
type Loader struct {
	schema *Schema
	strict bool
	log    *slog.Logger
	fs     fileSystem

	yamlPath  string
	yamlBytes []byte

	envName      string
	envYAMLPath  string
	envYAMLBytes []byte

	dotEnvPath  string
	dotEnvBytes []byte

	envReader func() map[string]string
	envSet    bool

	explicit map[string]any
	flags    map[string]string
}

// LoaderOpt customizes a Loader.
type LoaderOpt func(*Loader)

// NewLoader constructs a Loader with the default schema and logger, then
// applies the supplied options.
func NewLoader(opts ...LoaderOpt) *Loader {
	l := &Loader{
		schema: DefaultSchema(),
		log:    slog.Default(),
		fs:     osFS{},
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// WithSchema overrides the default schema.
func WithSchema(s *Schema) LoaderOpt {
	return func(l *Loader) { l.schema = s }
}

// WithLogger overrides the default logger.
func WithLogger(log *slog.Logger) LoaderOpt {
	return func(l *Loader) { l.log = log }
}

// WithFileSystem overrides the file system (for tests).
func WithFileSystem(fs fileSystem) LoaderOpt {
	return func(l *Loader) { l.fs = fs }
}

// WithStrict enables strict mode: unknown keys fail Load (CFG-009).
func WithStrict() LoaderOpt {
	return func(l *Loader) { l.strict = true }
}

// WithYAMLFile sets the path to ogon.yaml. A missing file is silently
// ignored (zero-config apps).
func WithYAMLFile(path string) LoaderOpt {
	return func(l *Loader) { l.yamlPath = path }
}

// WithYAMLBytes injects YAML content directly, bypassing the file read.
// Used for tests and inline configuration.
func WithYAMLBytes(b []byte) LoaderOpt {
	return func(l *Loader) { l.yamlBytes = b }
}

// WithEnvYAML selects ogon.<env>.yaml from the current directory.
func WithEnvYAML(env string) LoaderOpt {
	return func(l *Loader) {
		l.envName = env
		if env != "" {
			l.envYAMLPath = "ogon." + env + ".yaml"
		}
	}
}

// WithEnvYAMLFile sets an explicit path to ogon.<env>.yaml.
func WithEnvYAMLFile(path string) LoaderOpt {
	return func(l *Loader) { l.envYAMLPath = path }
}

// WithEnvYAMLBytes injects env YAML content directly.
func WithEnvYAMLBytes(b []byte) LoaderOpt {
	return func(l *Loader) { l.envYAMLBytes = b }
}

// WithDotEnv sets the path to a .env file. Missing files are ignored.
func WithDotEnv(path string) LoaderOpt {
	return func(l *Loader) { l.dotEnvPath = path }
}

// WithDotEnvBytes injects .env content directly.
func WithDotEnvBytes(b []byte) LoaderOpt {
	return func(l *Loader) { l.dotEnvBytes = b }
}

// WithEnv reads OGON_* env vars (and any other vars used for ${VAR}
// interpolation) from the real environment via os.Environ.
func WithEnv() LoaderOpt {
	return func(l *Loader) {
		l.envReader = func() map[string]string {
			out := make(map[string]string)
			for _, kv := range os.Environ() {
				if i := strings.IndexByte(kv, '='); i >= 0 {
					out[kv[:i]] = kv[i+1:]
				}
			}
			return out
		}
		l.envSet = true
	}
}

// WithEnvMap provides an explicit env var map, used in place of the real
// environment. Useful for tests and for embedding configs in code.
func WithEnvMap(m map[string]string) LoaderOpt {
	return func(l *Loader) {
		l.envReader = func() map[string]string {
			out := make(map[string]string, len(m))
			for k, v := range m {
				out[k] = v
			}
			return out
		}
		l.envSet = true
	}
}

// WithExplicit sets explicit code overrides (layer 6: ogon.Provide / Boot).
func WithExplicit(values map[string]any) LoaderOpt {
	return func(l *Loader) { l.explicit = values }
}

// WithFlags sets CLI flag overrides (layer 7). Keys are dotted config paths
// (e.g. "http.addr" → value).
func WithFlags(flags map[string]string) LoaderOpt {
	return func(l *Loader) { l.flags = flags }
}

// Load applies all configured sources in precedence order and returns a
// Config. Missing files are silently ignored; parse failures return a
// diag.Diag with Code OGON-K0001. In strict mode, unknown keys return
// OGON-K0003 (CFG-009).
func (l *Loader) Load(ctx context.Context) (*Config, error) {
	// 1. Build effective env: real env (or provided map) augmented by .env.
	envMap := l.buildEffectiveEnv()

	cfg := &Config{
		schema:   l.schema,
		resolved: make(map[string]any),
		trace:    make(map[string][]Source),
		strict:   l.strict,
	}

	// Layer 1: struct defaults.
	l.applyDefaults(cfg)

	// Layer 2: ogon.yaml (with ${VAR} interpolation + file:// resolution).
	if l.yamlBytes != nil || l.yamlPath != "" {
		if err := l.applyYAMLLayer(cfg, "ogon.yaml", l.yamlPath, l.yamlBytes, envMap); err != nil {
			return nil, err
		}
	}

	// Layer 3: ogon.<env>.yaml (with ${VAR} interpolation + file:// resolution).
	if l.envYAMLBytes != nil || l.envYAMLPath != "" {
		name := "ogon.<env>.yaml"
		if l.envName != "" {
			name = "ogon." + l.envName + ".yaml"
		}
		if err := l.applyYAMLLayer(cfg, name, l.envYAMLPath, l.envYAMLBytes, envMap); err != nil {
			return nil, err
		}
	}

	// Layer 5: OGON_* env overrides. (.env was consumed in step 1; its
	// OGON_* keys flow through here with layer name "env:NAME (.env)".)
	l.applyEnvOverrides(cfg, envMap)

	// Layer 6: explicit code overrides.
	if l.explicit != nil {
		l.applyFlatLayer(cfg, "explicit", l.explicit)
	}

	// Layer 7: CLI flags.
	if l.flags != nil {
		flagVals := make(map[string]any, len(l.flags))
		for k, v := range l.flags {
			flagVals[k] = v
		}
		l.applyFlatLayer(cfg, "cli", flagVals)
	}

	// Detect unknown keys (warn or fail, CFG-009).
	l.detectUnknownKeys(cfg)

	if cfg.strict && len(cfg.unknown) > 0 {
		var keys []string
		for _, u := range cfg.unknown {
			keys = append(keys, u.Key)
		}
		return nil, &diag.Diag{
			Code:     string(diag.CodeConfigUnknownKey),
			Severity: diag.SeverityError,
			Title:    "unknown config keys in strict mode",
			What:     strings.Join(keys, ", "),
			Fix: []string{
				"remove the unknown keys from your config",
				"or add them to the schema via config.WithSchema",
			},
		}
	}

	for _, u := range cfg.unknown {
		if u.Suggestion != "" {
			l.log.Warn("unknown config key (typo?)",
				"key", u.Key, "layer", u.Layer,
				"suggestion", u.Suggestion)
		} else {
			l.log.Warn("unknown config key",
				"key", u.Key, "layer", u.Layer)
		}
	}

	return cfg, nil
}
