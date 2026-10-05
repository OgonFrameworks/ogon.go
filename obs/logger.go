// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Logger setup: slog JSON in prod, human-readable in dev; levels via
// config/env. Implements OBS-001/002/003.

package obs

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// LogFormat selects the rendered form of slog records. (OBS-002)
type LogFormat int

const (
	// LogFormatJSON emits one JSON object per record (production default).
	LogFormatJSON LogFormat = iota
	// LogFormatText emits human-readable coloured text (dev default).
	LogFormatText
	// LogFormatK8s emits a kubernetes-friendly single-line JSON variant.
	// See k8s_format.go for the handler wrapper. (OBS-034)
	LogFormatK8s
)

// LogLevel mirrors slog.Level so callers can construct values from config
// without importing log/slog. (OBS-001)
type LogLevel = slog.Level

// LogConfig configures the framework logger.
type LogConfig struct {
	// Level is parsed from OGON_LOG_LEVEL (debug|info|warn|error).
	// Empty defaults to Info in prod, Debug in dev. (OBS-001)
	Level LogLevel
	// Format selects JSON (prod) or Text (dev). (OBS-002)
	Format LogFormat
	// AddSource annotates records with the calling file:line. Dev-only.
	AddSource bool
	// Writer sinks records. nil → os.Stderr.
	Writer io.Writer
	// K8sFormat toggles the k8s-friendly wrapper (OBS-034). When true,
	// Format is forced to JSON and the wrapper injects severity →
	// stdout/stderr routing metadata.
	K8sFormat bool
}

// defaultLevel holds the active level when no override is set. Swapped via
// SetGlobalLevel so tests can flip the default at runtime without
// re-configuring each logger. (OBS-001)
var defaultLevel atomic.Int32

func init() {
	defaultLevel.Store(int32(slog.LevelInfo))
}

// SetGlobalLevel changes the package-level default level. (OBS-001)
func SetGlobalLevel(l LogLevel) { defaultLevel.Store(int32(l)) }

// GlobalLevel returns the package-level default level.
func GlobalLevel() LogLevel { return LogLevel(defaultLevel.Load()) }

// ParseLevel maps common config/env strings to slog.Level. Unknown values
// fall back to the current global default. (OBS-001)
func ParseLevel(s string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	}
	return GlobalLevel()
}

// ParseFormat maps common config/env strings to LogFormat. (OBS-002)
func ParseFormat(s string) LogFormat {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "json":
		return LogFormatJSON
	case "text", "dev", "console":
		return LogFormatText
	case "k8s", "kubernetes":
		return LogFormatK8s
	}
	return LogFormatJSON
}

// IsDevEnv returns true for the supplied env name. Used to pick the dev
// defaults (text format, debug level, OTLP off).
func IsDevEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "", "dev", "development", "local", "test":
		return true
	}
	return false
}

// NewLogger constructs a slog.Logger from cfg. The returned logger is wired
// through the redaction hook (OBS-005) so no PII ever reaches the sink.
func NewLogger(cfg LogConfig) *slog.Logger {
	if cfg.Writer == nil {
		cfg.Writer = os.Stderr
	}
	lvl := cfg.Level
	if lvl == 0 {
		lvl = GlobalLevel()
	}
	opts := &slog.HandlerOptions{
		Level:     lvl,
		AddSource: cfg.AddSource,
	}
	var h slog.Handler
	switch {
	case cfg.K8sFormat:
		h = newK8sHandler(slog.NewJSONHandler(cfg.Writer, opts))
		cfg.Format = LogFormatK8s
	case cfg.Format == LogFormatText:
		h = slog.NewTextHandler(cfg.Writer, opts)
	default:
		h = slog.NewJSONHandler(cfg.Writer, opts)
	}
	h = wrapWithRedaction(h)
	return slog.New(h)
}

// DefaultLogger is a convenience for tests and quick-start code: prod JSON
// to stderr at Info level.
func DefaultLogger() *slog.Logger { return NewLogger(LogConfig{}) }

// DevLogger returns a coloured text logger at Debug level — the dev default.
func DevLogger() *slog.Logger {
	return NewLogger(LogConfig{Format: LogFormatText, Level: slog.LevelDebug, AddSource: true})
}

// ProdLogger returns a JSON logger at Info level — the prod default.
func ProdLogger() *slog.Logger {
	return NewLogger(LogConfig{Format: LogFormatJSON, Level: slog.LevelInfo})
}

// LoggerFromEnv builds a logger from OGON_LOG_* environment variables.
//   - OGON_LOG_LEVEL  (debug|info|warn|error)
//   - OGON_LOG_FORMAT (json|text|k8s)
//   - OGON_LOG_SOURCE (true|false)
//   - OGON_ENV        (dev|prod; dev → text+debug defaults)
func LoggerFromEnv(env map[string]string) *slog.Logger {
	cfg := LogConfig{}
	if IsDevEnv(env["OGON_ENV"]) {
		cfg.Format = LogFormatText
		cfg.Level = slog.LevelDebug
		cfg.AddSource = true
	}
	if v, ok := env["OGON_LOG_FORMAT"]; ok {
		cfg.Format = ParseFormat(v)
	}
	if v, ok := env["OGON_LOG_LEVEL"]; ok {
		cfg.Level = ParseLevel(v)
	}
	if v, ok := env["OGON_LOG_SOURCE"]; ok {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.AddSource = true
		case "0", "false", "no", "off":
			cfg.AddSource = false
		}
	}
	if v, ok := env["OGON_LOG_K8S"]; ok {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.K8sFormat = true
		}
	}
	return NewLogger(cfg)
}
