// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestWSPoolAddRemoveDrain verifies pool accounting and drain behavior.
//
// We avoid calling Drain on a pool containing zero-value *websocket.Conn
// values because coder/websocket.Close dereferences internal mutex fields
// that are nil for a zero-value Conn — Drain's recover guard prevents the
// crash, but the underlying handshake attempts a 5s context-wait that
// would slow this test to a halt. Instead we exercise the accounting path
// (add / remove / cap) and then Drain an empty pool to verify the
// closed-flag side effect.
func TestWSPoolAddRemoveDrain(t *testing.T) {
	pool := NewWSPool(2)

	// Stand-in zero-value conns; we only exercise pool accounting, never
	// call Close on these (see comment above).
	c1 := &websocket.Conn{}
	c2 := &websocket.Conn{}
	c3 := &websocket.Conn{}

	if err := pool.add(c1); err != nil {
		t.Fatal(err)
	}
	if err := pool.add(c2); err != nil {
		t.Fatal(err)
	}
	if err := pool.add(c3); err != ErrWSPoolFull {
		t.Fatalf("third add: want ErrWSPoolFull, got %v", err)
	}
	if pool.Count() != 2 {
		t.Fatalf("count: want 2, got %d", pool.Count())
	}

	// Remove the stand-ins so Drain has no conns to close; this isolates
	// the drain accounting path from coder/websocket's Close handshake.
	pool.remove(c1)
	pool.remove(c2)
	if pool.Count() != 0 {
		t.Fatalf("count after remove: want 0, got %d", pool.Count())
	}

	// Drain on empty pool: closed flag must be set, count must stay 0.
	pool.Drain(100 * time.Millisecond)
	if !pool.closed.Load() {
		t.Fatal("pool not marked closed after drain")
	}
	if pool.Count() != 0 {
		t.Fatalf("count post-drain: want 0, got %d", pool.Count())
	}

	// Add after drain must be rejected.
	if err := pool.add(c1); err != ErrWSPoolClosed {
		t.Fatalf("add after drain: want ErrWSPoolClosed, got %v", err)
	}
}

// TestWSUpgradeForbiddenOrigin verifies the origin allowlist.
func TestWSUpgradeForbiddenOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use a no-op Ctx so we can call WSUpgrade directly.
		c := AcquireCtx(w, r, NewRouter())
		defer ReleaseCtx(c)
		c.route = &Route{Template: "/ws"}

		_ = WSUpgrade(nil, WSConfig{
			AllowOrigins: []string{"https://allowed.example.com"},
		}, func(c *Ctx, conn *websocket.Conn) error {
			return nil
		})(c)
	}))
	defer srv.Close()

	// Use forbidden origin.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/ws", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d", resp.StatusCode)
	}
}

// TestWSRoundTrip exercises a complete upgrade → echo → close cycle.
func TestWSRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c := AcquireCtx(w, r, NewRouter())
		defer ReleaseCtx(c)
		c.route = &Route{Template: "/ws"}

		_ = WSUpgrade(nil, WSConfig{
			AllowAllOrigins: true,
		}, func(c *Ctx, conn *websocket.Conn) error {
			defer conn.Close(websocket.StatusNormalClosure, "")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return nil
				}
				if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
					return nil
				}
			}
		})(c)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("echo: want hello, got %q", data)
	}
}
