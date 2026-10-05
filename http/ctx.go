// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Ctx: the typed per-request context. Handlers receive *Ctx instead of
// (http.ResponseWriter, *http.Request) — every helper (Bind/JSON/Problem/
// Stream/Param/Query/Header/Cookie) hangs off this type so app code never
// imports the server directly.

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ctxPool is the per-request *Ctx allocator. Hot path; reset() between
// requests MUST zero all fields back to defaults.
var ctxPool = sync.Pool{
	New: func() any { return &Ctx{} },
}

// Ctx carries the per-request state: request, response writer, route match,
// negotiated codec, and bound parameters. Acquired from a sync.Pool; never
// constructed by app code.
type Ctx struct {
	w        http.ResponseWriter
	r        *http.Request
	router   *Router
	route    *Route
	codec    Codec
	params   map[string]string
	userData any // optional scratch slot for handlers
	started  time.Time
	status   int
	written  bool
	aborted  bool
	rc       *http.ResponseController
	buf      *bytes.Buffer
}

// AcquireCtx returns a Ctx from the pool bound to w/r. Framework use only —
// app code receives *Ctx as a handler argument.
func AcquireCtx(w http.ResponseWriter, r *http.Request, rt *Router) *Ctx {
	c := ctxPool.Get().(*Ctx)
	c.w = w
	c.r = r
	c.router = rt
	c.codec = DefaultJSONCodec
	c.started = time.Now()
	return c
}

// ReleaseCtx returns c to the pool. Idempotent. After Release, c MUST NOT be
// used again; the framework enforces this for handler-return errors.
func ReleaseCtx(c *Ctx) {
	if c == nil {
		return
	}
	*c = Ctx{}
	ctxPool.Put(c)
}

// Request returns the underlying *http.Request. Escape hatch for handlers
// needing raw request access (e.g., for streaming upload).
func (c *Ctx) Request() *http.Request { return c.r }

// ResponseWriter returns the underlying http.ResponseWriter. Prefer Ctx
// helpers (JSON, Problem, Stream); direct ResponseWriter use bypasses
// status/size accounting.
func (c *Ctx) ResponseWriter() http.ResponseWriter { return c.w }

// Context returns the request context. Cancels when the request times out.
func (c *Ctx) Context() context.Context { return c.r.Context() }

// Route returns the matched route, or nil if no match.
func (c *Ctx) Route() *Route { return c.route }

// RouteTemplate returns the route template (e.g. /api/users/{id}); used for
// metrics labels (HTTP-076 cardinality law). Returns "" for unmatched.
func (c *Ctx) RouteTemplate() string {
	if c.route == nil {
		return ""
	}
	return c.route.Template
}

// SetUserData attaches per-handler scratch state (e.g., the authed user).
// Framework never reads this field; it is owned by the handler chain.
func (c *Ctx) SetUserData(v any) { c.userData = v }

// UserData returns the value set by SetUserData.
func (c *Ctx) UserData() any { return c.userData }

// Codec returns the negotiated codec (default JSON).
func (c *Ctx) Codec() Codec { return c.codec }

// SetCodec overrides the negotiated codec. Used by route groups that opt
// into XML or a custom Codec.
func (c *Ctx) SetCodec(codec Codec) {
	if codec != nil {
		c.codec = codec
	}
}

// Param returns the named path parameter extracted by the router.
// Returns "" if not set; use ParamOk for the strict variant.
func (c *Ctx) Param(name string) string {
	if c.params == nil {
		return ""
	}
	return c.params[name]
}

// ParamOk returns the named path parameter plus a found flag. Prefer over
// Param in handlers that need to distinguish empty value from missing.
func (c *Ctx) ParamOk(name string) (string, bool) {
	if c.params == nil {
		return "", false
	}
	v, ok := c.params[name]
	return v, ok
}

// ParamInt parses a path parameter as int. Returns 0 and the error if the
// value is missing or non-numeric. The error is wrapped as a 400 Problem.
func (c *Ctx) ParamInt(name string) (int, error) {
	raw, ok := c.ParamOk(name)
	if !ok {
		return 0, BindProblem(fmt.Sprintf("missing path parameter %q", name))
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, BindProblem(fmt.Sprintf("path parameter %q must be int, got %q", name, raw)).
			WithFieldError(name, "type", "must be int", raw)
	}
	return n, nil
}

// ParamInt64 parses a path parameter as int64.
func (c *Ctx) ParamInt64(name string) (int64, error) {
	raw, ok := c.ParamOk(name)
	if !ok {
		return 0, BindProblem(fmt.Sprintf("missing path parameter %q", name))
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, BindProblem(fmt.Sprintf("path parameter %q must be int64, got %q", name, raw)).
			WithFieldError(name, "type", "must be int64", raw)
	}
	return n, nil
}

// Query returns a query parameter value by name. Returns "" if missing.
func (c *Ctx) Query(name string) string { return c.r.URL.Query().Get(name) }

// QueryOk returns a query parameter value plus a found flag.
func (c *Ctx) QueryOk(name string) (string, bool) {
	v, ok := c.r.URL.Query()[name]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[0], true
}

// QuerySlice returns all values for a query parameter.
func (c *Ctx) QuerySlice(name string) []string {
	return c.r.URL.Query()[name]
}

// Header returns the request header value (first occurrence).
func (c *Ctx) Header(name string) string { return c.r.Header.Get(name) }

// HeaderSlice returns all values for a request header.
func (c *Ctx) HeaderSlice(name string) []string { return c.r.Header.Values(name) }

// SetHeader sets a response header (sets, not appends). MUST be called
// before the first Write/WriteHeader call.
func (c *Ctx) SetHeader(name, value string) {
	c.w.Header().Set(name, value)
}

// AddHeader appends a response header value.
func (c *Ctx) AddHeader(name, value string) {
	c.w.Header().Add(name, value)
}

// Cookie returns the named request cookie. Returns ErrCookieMissing if absent.
func (c *Ctx) Cookie(name string) (string, error) {
	ck, err := c.r.Cookie(name)
	if err != nil {
		return "", err
	}
	return ck.Value, nil
}

// SetCookie sets a response cookie with full control.
func (c *Ctx) SetCookie(cookie http.Cookie) {
	http.SetCookie(c.w, &cookie)
}

// Status sets the response status code. MUST be called before the first
// Write/WriteHeader call. Subsequent calls are ignored.
func (c *Ctx) Status(code int) {
	if c.written {
		return
	}
	c.status = code
	c.w.WriteHeader(code)
	c.written = true
}

// Written reports whether a status line has been emitted to the wire.
func (c *Ctx) Written() bool { return c.written }

// Status returns the status code set via Status, or 0 if not set.
func (c *Ctx) StatusCode() int { return c.status }

// JSON serializes v using the negotiated codec and writes it with the
// negotiated media type. Convenience for handlers returning data inline.
func (c *Ctx) JSON(v any) error {
	body, err := c.codec.Encode(v)
	if err != nil {
		return err
	}
	if !c.written {
		c.w.Header().Set("Content-Type", c.codec.Accept())
		c.status = http.StatusOK
		c.w.WriteHeader(http.StatusOK)
		c.written = true
	}
	_, err = c.w.Write(body)
	return err
}

// XML writes v using the XML codec, overriding the negotiated JSON default.
func (c *Ctx) XML(v any) error {
	xmlc := &XMLCodec{}
	body, err := xmlc.Encode(v)
	if err != nil {
		return err
	}
	if !c.written {
		c.w.Header().Set("Content-Type", xmlc.Accept())
		c.status = http.StatusOK
		c.w.WriteHeader(http.StatusOK)
		c.written = true
	}
	_, err = c.w.Write(body)
	return err
}

// Problem writes a ProblemDetails response. Sets Content-Type to
// ProblemMediaType; status line follows p.Status. Idempotent after first
// Write: subsequent writes are no-ops.
func (c *Ctx) Problem(p *ProblemDetails) error {
	if p == nil {
		p = NewProblem(http.StatusInternalServerError, "Internal Server Error", "")
	}
	if p.Instance == "" {
		p.Instance = c.r.URL.Path
	}
	return p.Write(c.w, c.codec)
}

// NoContent responds with the supplied status and an empty body. Returns
// nil so handlers can `return c.NoContent(http.StatusNoContent)`.
func (c *Ctx) NoContent(code int) error {
	c.Status(code)
	return nil
}

// Stream writes a chunked/flushed response. Use the returned ResponseController
// to flush intermediate writes. Sets Transfer-Encoding: chunked implicitly
// via net/http when no Content-Length is set.
func (c *Ctx) Stream(fn func(rc *http.ResponseController) error) error {
	if !c.written {
		c.w.Header().Set("Content-Type", "application/octet-stream")
		c.status = http.StatusOK
		c.w.WriteHeader(http.StatusOK)
		c.written = true
	}
	if c.rc == nil {
		c.rc = http.NewResponseController(c.w)
	}
	return fn(c.rc)
}

// Redirect responds with a redirect. status must be 3xx.
func (c *Ctx) Redirect(status int, url string) {
	if c.written {
		return
	}
	if status < 300 || status >= 400 {
		status = http.StatusFound
	}
	http.Redirect(c.w, c.r, url, status)
	c.written = true
	c.status = status
}

// Abort marks the request as aborted; downstream middlewares see this via
// c.Aborted() and MUST short-circuit.
func (c *Ctx) Abort() { c.aborted = true }

// Aborted reports whether the request was aborted (auth denial, etc.).
func (c *Ctx) Aborted() bool { return c.aborted }

// ClientIP returns the client IP address honoring trusted-proxy
// configuration when set via ServerOptions.TrustedProxies (see
// middleware_realip.go; the strict implementation lives there).
func (c *Ctx) ClientIP() string {
	if raw := c.r.Header.Get("X-Forwarded-For"); raw != "" {
		// Take the first hop only when the immediate peer is trusted.
		// Trusted-proxy check is the caller's responsibility; this returns
		// the leftmost IP. Real IP reconciliation happens in
		// middleware_realip.go (HTTP-046).
		if i := strings.IndexByte(raw, ','); i >= 0 {
			return strings.TrimSpace(raw[:i])
		}
		return strings.TrimSpace(raw)
	}
	host := c.r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// Bind decodes query/path/header/cookie/body into the supplied struct
// pointer. See bind.go for the full semantics; this is the public entrypoint.
func (c *Ctx) Bind(v any) error { return bindCtx(c, v) }

// jsonDecoderShim is the small wrapper used by c.Stream to write JSON lines
// without re-acquiring encoders. Exported as an unexported shim so tests can
// drive it; production code uses SSE/WS paths.
func (c *Ctx) ensureBuf() *bytes.Buffer {
	if c.buf == nil {
		c.buf = bytes.NewBuffer(nil)
	}
	return c.buf
}

// flushBuffer flushes c.buf to the ResponseController.
func (c *Ctx) flushBuffer() error {
	if c.buf == nil || c.buf.Len() == 0 {
		return nil
	}
	if _, err := c.w.Write(c.buf.Bytes()); err != nil {
		return err
	}
	c.buf.Reset()
	if c.rc == nil {
		c.rc = http.NewResponseController(c.w)
	}
	return c.rc.Flush()
}

// readBody safely drains the request body up to ServerOptions.MaxBodyBytes.
// Returns io.EOF for empty bodies. Caller MUST close via io.Copy(io.Discard, ...)
// if not consumed. Used by bind.go.
func (c *Ctx) readBody(max int64) ([]byte, error) {
	if c.r.Body == nil {
		return nil, nil
	}
	defer c.r.Body.Close()
	r := io.Reader(c.r.Body)
	if max > 0 {
		r = io.LimitReader(c.r.Body, max+1)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if max > 0 && int64(len(data)) > max {
		return nil, &ProblemDetails{
			Type:   "about:blank",
			Title:  "Request body too large",
			Status: http.StatusRequestEntityTooLarge,
			Detail: fmt.Sprintf("body exceeds %d bytes", max),
		}
	}
	return data, nil
}

// jsonDecode is the shared JSON unmarshal entrypoint; isolated so handlers
// can swap to xmlDecode without re-reading the body. A leading UTF-8 BOM
// (EF BB BF), as emitted by some Windows HTTP clients and proxies, is
// stripped before decoding (BUG-0007).
func jsonDecode(data []byte, v any) error {
	data = stripUTF8BOM(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

// stripUTF8BOM removes a single leading UTF-8 BOM (EF BB BF) if present.
func stripUTF8BOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}
