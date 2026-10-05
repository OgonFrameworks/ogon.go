// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// WebSocket test client (TEST-010). The fixture wraps coder/websocket so
// tests can speak the full WS protocol against an in-process server without
// depending on the production transport surface. The client is plain: dial,
// send, recv with timeouts, close.

package test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// WSClient is a minimal WS test client. One client = one connection.
type WSClient struct {
	server *httptest.Server
	header http.Header
	conn   *websocket.Conn
	mu     sync.Mutex
}

// NewWSClient constructs a client targeting the in-process server.
func NewWSClient(server *httptest.Server) *WSClient {
	return &WSClient{server: server}
}

// WithHeader sets a request header (e.g., Authorization) on the next Dial.
func (c *WSClient) WithHeader(k, v string) *WSClient {
	if c.header == nil {
		c.header = http.Header{}
	}
	c.header.Set(k, v)
	return c
}

// Dial upgrades ws:// or wss:// from the test server's HTTP URL. Blocks up
// to 5 seconds.
func (c *WSClient) Dial(ctx context.Context, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.server == nil {
		return errors.New("ogontest: WSClient has no server")
	}
	wsURL := strings.Replace(c.server.URL, "http://", "ws://", 1) + path
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{
		HTTPHeader: c.header,
	})
	if err != nil {
		return err
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	c.conn = conn
	return nil
}

// SendText writes a text message.
func (c *WSClient) SendText(ctx context.Context, msg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return errors.New("ogontest: WSClient not dialled")
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.conn.Write(wctx, websocket.MessageText, []byte(msg))
}

// SendBinary writes a binary message.
func (c *WSClient) SendBinary(ctx context.Context, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return errors.New("ogontest: WSClient not dialled")
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.conn.Write(wctx, websocket.MessageBinary, data)
}

// Recv reads the next message (text or binary) within the supplied timeout.
func (c *WSClient) Recv(ctx context.Context) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil, false, errors.New("ogontest: WSClient not dialled")
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(rctx)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Close terminates the connection with status NormalClosure.
func (c *WSClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	conn := c.conn
	c.conn = nil
	return conn.Close(websocket.StatusNormalClosure, "ogontest close")
}

// Conn returns the underlying websocket.Conn (escape hatch).
func (c *WSClient) Conn() *websocket.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}
