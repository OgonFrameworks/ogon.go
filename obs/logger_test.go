// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package obs

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLogger_JSON(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogConfig{Format: LogFormatJSON, Level: slog.LevelInfo, Writer: &buf})
	log.Info("hello", "name", "ogon")
	var rec map[string]any
	if err := json.NewDecoder(&buf).Decode(&rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec["msg"] != "hello" {
		t.Errorf("msg = %v", rec["msg"])
	}
}

func TestNewLogger_Text(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(LogConfig{Format: LogFormatText, Level: slog.LevelInfo, Writer: &buf})
	log.Info("hello", "name", "ogon")
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("output missing message: %s", out)
	}
	if !strings.Contains(out, "name=ogon") {
		t.Errorf("output missing attr: %s", out)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in  string
		exp slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"WARNING", slog.LevelWarn},
		{"", GlobalLevel()},
	}
	for _, tt := range tests {
		if got := ParseLevel(tt.in); got != tt.exp {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.exp)
		}
	}
}

func TestParseFormat(t *testing.T) {
	if got := ParseFormat("json"); got != LogFormatJSON {
		t.Errorf("json: got %v", got)
	}
	if got := ParseFormat("text"); got != LogFormatText {
		t.Errorf("text: got %v", got)
	}
	if got := ParseFormat("k8s"); got != LogFormatK8s {
		t.Errorf("k8s: got %v", got)
	}
	if got := ParseFormat("unknown"); got != LogFormatJSON {
		t.Errorf("default: got %v", got)
	}
}

func TestIsDevEnv(t *testing.T) {
	cases := map[string]bool{
		"":            true,
		"dev":         true,
		"development": true,
		"local":       true,
		"test":        true,
		"prod":        false,
		"production":  false,
		"staging":     false,
	}
	for env, exp := range cases {
		if got := IsDevEnv(env); got != exp {
			t.Errorf("IsDevEnv(%q) = %v, want %v", env, got, exp)
		}
	}
}

func TestLoggerFromEnv_DevDefaults(t *testing.T) {
	log := LoggerFromEnv(map[string]string{"OGON_ENV": "dev"})
	_ = log
	// Just confirm the call doesn't panic.
}

func TestLoggerFromEnv_Prod(t *testing.T) {
	log := LoggerFromEnv(map[string]string{
		"OGON_ENV":        "prod",
		"OGON_LOG_LEVEL":  "warn",
		"OGON_LOG_FORMAT": "json",
	})
	if log.Enabled(nil, slog.LevelInfo) {
		t.Error("prod logger at warn level should not be Info-enabled")
	}
}

func TestSetGlobalLevel(t *testing.T) {
	prev := GlobalLevel()
	defer SetGlobalLevel(prev)
	SetGlobalLevel(slog.LevelError)
	if GlobalLevel() != slog.LevelError {
		t.Errorf("GlobalLevel = %v", GlobalLevel())
	}
}
