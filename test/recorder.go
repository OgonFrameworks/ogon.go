// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Recorder client (TEST-002). Wraps httptest in-process Server with JSON
// helpers and snapshot assertions. Law from Part XIV applies: each helper is
// one line of behaviour plus a t.Helper plus a descriptive message.

package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testFailT narrows the assert surface so a test stub can be passed in
// failure-path tests. *testing.T satisfies it trivially.
type testFailT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// Recorder is the per-test HTTP client bound to an in-process App. Every
// method returns a *Response, so chains compose: app.Login("admin").Get(p).
type Recorder struct {
	app    *App
	t      *testing.T
	client *http.Client
	header http.Header // default headers (auth, content-type, etc.)
}

// NewRecorder constructs a Recorder bound to app. Returned to tests via
// App.Login / App.As / App.Recorder; direct construction is rare.
func NewRecorder(t *testing.T, app *App) *Recorder {
	t.Helper()
	return &Recorder{
		app:    app,
		t:      t,
		client: app.Server().Client(),
		header: http.Header{},
	}
}

// WithHeader returns a clone with the additional default header. The receiver
// is not mutated so the same Recorder can be safely shared.
func (r *Recorder) WithHeader(k, v string) *Recorder {
	nr := r.clone()
	nr.header.Set(k, v)
	return nr
}

// WithBearer returns a clone with Authorization: Bearer <token>.
func (r *Recorder) WithBearer(token string) *Recorder {
	return r.WithHeader("Authorization", "Bearer "+token)
}

// WithJSON sets the default Content-Type to application/json.
func (r *Recorder) WithJSON() *Recorder {
	return r.WithHeader("Content-Type", "application/json")
}

func (r *Recorder) clone() *Recorder {
	nr := &Recorder{
		app:    r.app,
		t:      r.t,
		client: r.client,
		header: http.Header{},
	}
	for k, vs := range r.header {
		for _, v := range vs {
			nr.header.Add(k, v)
		}
	}
	return nr
}

// Get issues a GET to path (relative to App.BaseURL).
func (r *Recorder) Get(path string) *Response {
	return r.do(http.MethodGet, path, nil)
}

// Post issues a POST with a JSON-encoded body.
func (r *Recorder) Post(path string, body any) *Response {
	return r.do(http.MethodPost, path, body)
}

// Put issues a PUT with a JSON-encoded body.
func (r *Recorder) Put(path string, body any) *Response {
	return r.do(http.MethodPut, path, body)
}

// Patch issues a PATCH with a JSON-encoded body.
func (r *Recorder) Patch(path string, body any) *Response {
	return r.do(http.MethodPatch, path, body)
}

// Delete issues a DELETE.
func (r *Recorder) Delete(path string) *Response {
	return r.do(http.MethodDelete, path, nil)
}

// Do issues a custom-method request. Exported so tests can craft edge-case
// requests (PROPFIND, OPTIONS, …) without losing the recorder's helpers.
func (r *Recorder) Do(method, path string, body any) *Response {
	return r.do(method, path, body)
}

func (r *Recorder) do(method, path string, body any) *Response {
	r.t.Helper()
	var rdr io.Reader
	if body != nil {
		switch v := body.(type) {
		case []byte:
			rdr = bytes.NewReader(v)
		case string:
			rdr = strings.NewReader(v)
		case io.Reader:
			rdr = v
		default:
			buf, err := json.Marshal(body)
			if err != nil {
				r.t.Fatalf("ogontest: marshal body: %v", err)
			}
			rdr = bytes.NewReader(buf)
		}
	}
	full := r.app.BaseURL() + path
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, full, rdr)
	if err != nil {
		r.t.Fatalf("ogontest: %s %s: %v", method, path, err)
	}
	for k, vs := range r.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := r.client.Do(req)
	if err != nil {
		r.t.Fatalf("ogontest: %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		r.t.Fatalf("ogontest: read body %s %s: %v", method, path, err)
	}
	return &Response{Raw: resp, Body: data}
}

// Response captures the result of a Recorder call.
type Response struct {
	Raw  *http.Response
	Body []byte
}

// Status returns the HTTP status code.
func (r *Response) Status() int {
	if r == nil || r.Raw == nil {
		return 0
	}
	return r.Raw.StatusCode
}

// JSON decodes the body into out.
func (r *Response) JSON(out any) error {
	return json.Unmarshal(r.Body, out)
}

// AssertStatus fails the test if the response status != want (TEST-002).
func AssertStatus(t testFailT, r *Response, want int) {
	t.Helper()
	if r == nil {
		t.Fatalf("ogontest: AssertStatus: nil response")
		return
	}
	if r.Status() != want {
		t.Fatalf("ogontest: status %d, want %d; body=%s", r.Status(), want, truncate(r.Body))
	}
}

// AssertJSON unmarshals the response body and compares to expected using
// canonical JSON form (snapshot-style, TEST-002/012).
func AssertJSON(t testFailT, r *Response, expected string) {
	t.Helper()
	if r == nil {
		t.Fatalf("ogontest: AssertJSON: nil response")
		return
	}
	var got any
	if err := json.Unmarshal(r.Body, &got); err != nil {
		t.Fatalf("ogontest: AssertJSON: decode body: %v; body=%s", err, truncate(r.Body))
		return
	}
	var want any
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatalf("ogontest: AssertJSON: decode expected: %v", err)
		return
	}
	if !jsonEqual(got, want) {
		gotB, _ := json.MarshalIndent(got, "", "  ")
		wantB, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("ogontest: JSON mismatch\nwant: %s\ngot:  %s", wantB, gotB)
	}
}

// AssertJSONContains unmarshals the body and verifies every field in
// expected is present in the body with the same value. Fields present in
// the body but not in expected are ignored.
func AssertJSONContains(t testFailT, r *Response, expected string) {
	t.Helper()
	if r == nil {
		t.Fatalf("ogontest: AssertJSONContains: nil response")
		return
	}
	var got, want map[string]any
	if err := json.Unmarshal(r.Body, &got); err != nil {
		t.Fatalf("ogontest: AssertJSONContains: decode body: %v; body=%s", err, truncate(r.Body))
		return
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatalf("ogontest: AssertJSONContains: decode expected: %v", err)
		return
	}
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("ogontest: AssertJSONContains: missing key %q in body=%s", k, truncate(r.Body))
			return
		}
		if !jsonEqual(gv, v) {
			t.Fatalf("ogontest: AssertJSONContains: key %q: want %v, got %v", k, v, gv)
		}
	}
}

// AssertHeader fails if the response header doesn't match the expected value.
func AssertHeader(t testFailT, r *Response, key, want string) {
	t.Helper()
	if r == nil || r.Raw == nil {
		t.Fatalf("ogontest: AssertHeader: nil response")
		return
	}
	got := r.Raw.Header.Get(key)
	if got != want {
		t.Fatalf("ogontest: header %s: want %q, got %q", key, want, got)
	}
}

// AssertBodyContains fails if the body lacks substr.
func AssertBodyContains(t testFailT, r *Response, substr string) {
	t.Helper()
	if r == nil {
		t.Fatalf("ogontest: AssertBodyContains: nil response")
		return
	}
	if !bytes.Contains(r.Body, []byte(substr)) {
		t.Fatalf("ogontest: body missing %q; body=%s", substr, truncate(r.Body))
	}
}

// ---- helpers ----

func truncate(b []byte) string {
	const max = 256
	if len(b) <= max {
		return string(b)
	}
	return fmt.Sprintf("%s... (truncated %d bytes)", b[:max], len(b)-max)
}

func jsonEqual(a, b any) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// QueryValues is a tiny helper to build query strings without pulling
// net/url into every test file.
func QueryValues(pairs ...string) string {
	if len(pairs)%2 != 0 {
		return ""
	}
	v := url.Values{}
	for i := 0; i < len(pairs); i += 2 {
		v.Add(pairs[i], pairs[i+1])
	}
	return v.Encode()
}
