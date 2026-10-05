// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSSEUpgrade verifies the SSE upgrade + Last-Event-ID parsing.
func TestSSEUpgrade(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(LastEventIDHeader, "42")

	err := UpgradeSSE(rec, req, DefaultSSEConfig(), func(c *SSEConn) error {
		if c.LastEventID() != 42 {
			t.Fatalf("last event id: want 42, got %d", c.LastEventID())
		}
		c.Send("43", "ping", "data1")
		c.Send("44", "ping", "data2")
		// Allow drain goroutine to flush.
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("UpgradeSSE: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: data1") {
		t.Fatalf("body missing data1:\n%s", body)
	}
	if !strings.Contains(body, "data: data2") {
		t.Fatalf("body missing data2:\n%s", body)
	}
	if !strings.Contains(body, "id: 43") {
		t.Fatalf("body missing id 43:\n%s", body)
	}
}

// TestSSEHeartbeat verifies the heartbeat comment frame.
func TestSSEHeartbeat(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/event-stream")

	cfg := DefaultSSEConfig()
	cfg.Heartbeat = 20 * time.Millisecond
	err := UpgradeSSE(rec, req, cfg, func(c *SSEConn) error {
		time.Sleep(80 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("UpgradeSSE: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ": hb") {
		t.Fatalf("heartbeat missing:\n%s", body)
	}
}

// TestSSEDropOldest verifies the drop-oldest policy when the queue fills.
func TestSSEDropOldest(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/event-stream")

	cfg := DefaultSSEConfig()
	cfg.QueueSize = 2
	err := UpgradeSSE(rec, req, cfg, func(c *SSEConn) error {
		// Push 10 events into a queue of size 2.
		for i := 0; i < 10; i++ {
			c.Send("1", "drop", "x")
		}
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("UpgradeSSE: %v", err)
	}
	if SSEDropped() == 0 {
		t.Fatal("expected drop count > 0")
	}
}

// TestSSEMultiLineData verifies multi-line SSE data is split correctly.
func TestSSEMultiLineData(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/event-stream")

	err := UpgradeSSE(rec, req, DefaultSSEConfig(), func(c *SSEConn) error {
		c.Send("1", "multiline", "line1\nline2\nline3")
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("UpgradeSSE: %v", err)
	}
	body := rec.Body.String()
	if strings.Count(body, "data: line") != 3 {
		t.Fatalf("expected 3 data lines, got body:\n%s", body)
	}
}
