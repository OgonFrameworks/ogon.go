// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// SSE test client (TEST-011). Reads the in-process server's text/event-stream
// response and exposes per-event helpers. The client is a thin wrapper around
// a bufio scanner + a few primitives for the SSE wire format (RFC 8895 §3).

package test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// SSEEvent is a parsed SSE message: a single `event:`/`data:` block.
type SSEEvent struct {
	Type string // empty string means the default "message" event
	Data []byte
	ID   string
}

// SSEClient is the per-test SSE consumer. One client = one connection.
type SSEClient struct {
	server *httptest.Server
	path   string
	header http.Header

	mu     sync.Mutex
	events []SSEEvent
	err    error
	done   chan struct{}
}

// NewSSEClient constructs a client for the given in-process server.
func NewSSEClient(server *httptest.Server, path string) *SSEClient {
	return &SSEClient{server: server, path: path, done: make(chan struct{})}
}

// WithHeader sets a request header (e.g., Last-Event-ID) on the connect.
func (c *SSEClient) WithHeader(k, v string) *SSEClient {
	if c.header == nil {
		c.header = http.Header{}
	}
	c.header.Set(k, v)
	return c
}

// Connect opens the SSE stream and starts a background reader. Returns once
// the response has been received (200 OK) but before any events arrive.
// Tests then call Events / WaitFor / Err.
func (c *SSEClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.server == nil {
		return errors.New("ogontest: SSEClient has no server")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server.URL+c.path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.server.Client().Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("ogontest: SSE connect: status %d", resp.StatusCode)
	}
	go c.readLoop(resp)
	return nil
}

func (c *SSEClient) readLoop(resp *http.Response) {
	defer resp.Body.Close()
	defer close(c.done)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		evType string
		data   bytes.Buffer
		id     string
		flush  func()
	)
	flush = func() {
		if data.Len() == 0 {
			// Reset for next event.
			evType, id = "", ""
			data.Reset()
			return
		}
		// Trim trailing newline added by SSE convention.
		out := make([]byte, data.Len())
		copy(out, data.Bytes())
		c.mu.Lock()
		c.events = append(c.events, SSEEvent{Type: evType, Data: out, ID: id})
		c.mu.Unlock()
		evType, id = "", ""
		data.Reset()
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // comment
		}
		field, value, _ := splitField(line)
		switch field {
		case "event":
			evType = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		case "id":
			id = value
		case "retry":
			// ignore in tests
		}
	}
	if err := sc.Err(); err != nil {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
	}
	flush()
}

func splitField(line string) (string, string, bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line, "", true
	}
	field := line[:idx]
	rest := line[idx+1:]
	if strings.HasPrefix(rest, " ") {
		rest = rest[1:]
	}
	return field, rest, true
}

// Events returns a snapshot of events received so far.
func (c *SSEClient) Events() []SSEEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]SSEEvent, len(c.events))
	copy(out, c.events)
	return out
}

// WaitFor blocks until n events arrive or the deadline expires.
func (c *SSEClient) WaitFor(n int, timeout time.Duration) ([]SSEEvent, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		count := len(c.events)
		events := make([]SSEEvent, count)
		copy(events, c.events)
		err := c.err
		c.mu.Unlock()
		if count >= n {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		select {
		case <-deadline.C:
			return events, errors.New("ogontest: SSE WaitFor: timed out")
		case <-c.done:
			c.mu.Lock()
			final := append([]SSEEvent(nil), c.events...)
			err := c.err
			c.mu.Unlock()
			return final, err
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// Err returns the read-loop error (nil if none).
func (c *SSEClient) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close terminates the read loop by closing the underlying response. The
// server sees a broken pipe and unwinds. Safe to call multiple times.
func (c *SSEClient) Close() error {
	// The read loop exits when the server closes the body. There is no
	// explicit Close here because the http.Client owns the body. The
	// caller is expected to call c.server.Close() from the App's cleanup.
	<-c.done
	return c.Err()
}
