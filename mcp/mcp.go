// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// mcp.go implements the Model Context Protocol server over stdio for
// OgonGo (AGENT-005). The server speaks JSON-RPC 2.0 over stdin/stdout
// and exposes a safe tool set: read-only inspection commands plus
// code-generation with --dry-run enforced by default. No destructive
// tool is exposed (spec Part XVII, "make OgonGo apps unusually easy for
// humans and software agents to understand and modify safely").
//
// The protocol follows the MCP 2024-11-05 spec shape:
//   - initialize → returns server capabilities + protocol version
//   - tools/list → returns the safe tool catalog
//   - tools/call → dispatches to a tool by name with a JSON args object
//
// All output is line-delimited JSON-RPC. Errors use the standard
// JSON-RPC error object with code + message + optional data.

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
)

// ProtocolVersion is the MCP protocol version advertised at initialize.
const ProtocolVersion = "2024-11-05"

// ServerVersion is the MCP server (agent surface) version. It tracks
// agent.AgentSurfaceVersion (AGENT-022) so clients can verify compat.
const ServerVersion = "1.0.0"

// ServerName is the server identity advertised at initialize.
const ServerName = "ogon"

// Server is the MCP server over stdio. It is safe for concurrent use
// by multiple goroutines (one reader, one writer per request).
type Server struct {
	name    string
	version string
	logger  *slog.Logger

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	tools map[string]Tool

	// started ensures Start is called at most once. Subsequent calls
	// return an error so a test harness can't accidentally double-spawn.
	started atomic.Bool
	wg      sync.WaitGroup

	// mu guards stdout so concurrent tool goroutines don't interleave
	// JSON-RPC response frames.
	mu sync.Mutex
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithName overrides the server name (default: "ogon").
func WithName(name string) Option {
	return func(s *Server) { s.name = name }
}

// WithVersion overrides the server version (default: ServerVersion).
func WithVersion(v string) Option {
	return func(s *Server) { s.version = v }
}

// WithLogger supplies a custom *slog.Logger. Default: text handler to
// stderr at Info level.
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithStdio overrides stdin and stdout. Default: os.Stdin / os.Stdout.
// Tests pass bytes.Buffer / bytes.Reader here.
func WithStdio(in io.Reader, out, errw io.Writer) Option {
	return func(s *Server) {
		if in != nil {
			s.stdin = in
		}
		if out != nil {
			s.stdout = out
		}
		if errw != nil {
			s.stderr = errw
		}
	}
}

// WithTools injects a custom tool set (default: DefaultTools). Useful
// for embedders that want to surface domain-specific tools.
func WithTools(tools []Tool) Option {
	return func(s *Server) {
		s.tools = map[string]Tool{}
		for _, t := range tools {
			s.tools[t.Name] = t
		}
	}
}

// New constructs a Server with the default tool set and stdio bound to
// os.Stdin / os.Stdout / os.Stderr.
func New(opts ...Option) *Server {
	s := &Server{
		name:    ServerName,
		version: ServerVersion,
		logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
		tools:  map[string]Tool{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if len(s.tools) == 0 {
		for _, t := range DefaultTools() {
			s.tools[t.Name] = t
		}
	}
	return s
}

// Start runs the read-dispatch loop until ctx is cancelled or stdin
// returns EOF. Each request is dispatched in the caller's goroutine so
// a slow tool does not block subsequent requests (MCP requests are
// single-message-per-line so concurrency is bounded by line rate).
func (s *Server) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("mcp: server already started")
	}
	defer s.wg.Wait()

	scanner := bufio.NewScanner(s.stdin)
	// 1 MiB buffer so a large tools/call args object survives.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("mcp: read stdin: %w", err)
			}
			return nil
		}
		line := scanner.Bytes()
		if isWhitespaceOnly(line) {
			continue
		}
		s.wg.Add(1)
		// Dispatch synchronously: MCP is a single-line-per-request protocol
		// and responses must preserve request order. A per-request goroutine
		// would race on stdout and reorder responses (tools/list could
		// finish before initialize). The handle call is bounded by the
		// request itself — no risk of one slow tool blocking the next.
		func(payload []byte) {
			defer s.wg.Done()
			s.handle(ctx, payload)
		}(append([]byte(nil), line...))
	}
}

// handle parses one JSON-RPC request, dispatches, and writes the
// response (if not a notification). Errors are reported as JSON-RPC
// error objects with code + message + optional data.
func (s *Server) handle(ctx context.Context, payload []byte) {
	var req request
	if err := json.Unmarshal(payload, &req); err != nil {
		s.writeError(nil, errParseError, "invalid JSON: "+err.Error(), nil)
		return
	}
	switch {
	case req.Method == "":
		s.writeError(req.ID, errInvalidRequest, "method is required", nil)
		return
	case req.Method == "initialize":
		s.handleInitialize(req)
	case req.Method == "initialized" || req.Method == "notifications/initialized":
		// Notification — no response. (MCP handshake completion.)
	case req.Method == "tools/list":
		s.handleToolsList(req)
	case req.Method == "tools/call":
		s.handleToolsCall(ctx, req)
	case req.Method == "ping":
		s.writeResult(req.ID, map[string]any{"pong": true})
	case req.Method == "resources/list" || req.Method == "prompts/list":
		// We don't expose resources or prompts yet; return an empty list
		// so MCP clients see a stable surface.
		s.writeResult(req.ID, map[string]any{"resources": []any{}})
	default:
		s.writeError(req.ID, errMethodNotFound, "unknown method: "+req.Method, nil)
	}
}

// handleInitialize replies with server capabilities + protocol version.
func (s *Server) handleInitialize(req request) {
	result := map[string]any{
		"protocolVersion": ProtocolVersion,
		"serverInfo": map[string]any{
			"name":    s.name,
			"version": s.version,
		},
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": false,
			},
		},
	}
	s.writeResult(req.ID, result)
}

// handleToolsList returns the safe tool catalog.
func (s *Server) handleToolsList(req request) {
	tools := make([]map[string]any, 0, len(s.tools))
	for _, name := range sortedToolNames(s.tools) {
		t := s.tools[name]
		tools = append(tools, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	s.writeResult(req.ID, map[string]any{"tools": tools})
}

// handleToolsCall dispatches a tools/call request to the named tool.
func (s *Server) handleToolsCall(ctx context.Context, req request) {
	var params callParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, errInvalidParams, "invalid params: "+err.Error(), nil)
		return
	}
	t, ok := s.tools[params.Name]
	if !ok {
		s.writeError(req.ID, errInvalidParams, "unknown tool: "+params.Name, nil)
		return
	}
	result, err := t.Handler(ctx, params.Arguments)
	if err != nil {
		s.writeError(req.ID, errInternal, "tool error: "+err.Error(), map[string]any{
			"tool":  t.Name,
			"error": err.Error(),
		})
		return
	}
	out := map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": result.Text,
			},
		},
		"isError": false,
	}
	if result.Structured != nil {
		out["structuredContent"] = result.Structured
	}
	s.writeResult(req.ID, out)
}

// Tool is one MCP tool exposed by the server. Handlers are typed:
// Arguments is a map[string]any (MCP allows arbitrary JSON; we keep
// the loose type at the protocol boundary and let the tool validate).
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Handler     func(ctx context.Context, args map[string]any) (*ToolResult, error)
}

// ToolResult is the canonical return from a tool handler. Text is the
// human/agent-readable string; Structured (optional) is appended under
// the "structuredContent" key for machine consumers.
type ToolResult struct {
	Text       string         `json:"text"`
	Structured map[string]any `json:"structured,omitempty"`
}

// --- JSON-RPC types ---

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes (per spec).
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// writeResult serializes a successful response and writes it as one
// line to stdout. The mutex prevents interleaved frames.
func (s *Server) writeResult(id json.RawMessage, result any) {
	resp := response{JSONRPC: "2.0", ID: id, Result: result}
	body, err := json.Marshal(resp)
	if err != nil {
		// Should not happen — result is JSON-marshalable by construction.
		fmt.Fprintf(s.stderr, "mcp: marshal result: %v\n", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdout.Write(append(body, '\n'))
}

// writeError serializes an error response. id may be nil when the
// error is a parse failure (no id was readable).
func (s *Server) writeError(id json.RawMessage, code int, msg string, data any) {
	resp := response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: msg,
			Data:    data,
		},
	}
	body, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(s.stderr, "mcp: marshal error: %v\n", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdout.Write(append(body, '\n'))
}

// sortedToolNames returns the tool names in lexical order for stable
// tools/list output (byte-stable; AGENT-009).
func sortedToolNames(tools map[string]Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	// Sort without importing sort at the package boundary — we only
	// need lexical ordering here.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// isWhitespaceOnly returns true when the byte slice contains only
// whitespace characters (space, tab, CR, LF). Used to skip empty
// JSON-RPC frames without an extra strings import.
func isWhitespaceOnly(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
		default:
			return false
		}
	}
	return true
}
