// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// ProblemDetails: RFC 9457 problem documents for HTTP error responses.
// All framework-generated errors (bind failures, auth denials, route
// conflicts, validation errors) flow through Problem so callers get a
// single, machine-remediable error shape.

package http

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ProblemMediaType is the canonical RFC 9457 content type.
const ProblemMediaType = "application/problem+json"

// FieldError is a single field-level validation problem.
//
// Field is the JSON path of the offending field (dot-separated, with
// indices for slices: "items[2].name"). Code is a stable machine code
// (see diag.Code); Message is human-readable. At least one of Code or
// Message MUST be non-empty.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Value   any    `json:"value,omitempty"`
}

// ProblemDetails is an RFC 9457 problem document.
//
// Invariants:
//   - Type is a URI (default "about:blank" per RFC).
//   - Status is the HTTP status code carried in the response line.
//   - Title is a short, stable summary (does not leak request data).
//   - Detail is the specific reason for THIS occurrence (may be redacted).
//   - Instance is a URI identifying the specific occurrence (usually
//     the request path + request-id).
//   - Errors is the optional field-level map (validation failures).
//
// Extension members are NOT free-form; downstream callers register their
// own typed wrappers. The base struct is intentionally minimal.
type ProblemDetails struct {
	Type     string       `json:"type,omitempty"`
	Title    string       `json:"title,omitempty"`
	Status   int          `json:"status,omitempty"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`

	// extra is for registered extensions. Serialized as merged top-level keys
	// by MarshalJSON.
	extra map[string]any
}

// SetExtension attaches a top-level extension member. Reserved keys
// ("type","title","status","detail","instance","errors") panic: extensions
// MUST NOT collide with the standard vocabulary (RFC 9457 §3.1).
func (p *ProblemDetails) SetExtension(key string, value any) {
	switch key {
	case "type", "title", "status", "detail", "instance", "errors":
		panic("ogon/http: ProblemDetails extension collides with reserved key: " + key)
	}
	if p.extra == nil {
		p.extra = make(map[string]any)
	}
	p.extra[key] = value
}

// MarshalJSON renders the problem document merging extension members as
// top-level keys, per RFC 9457 §3.5.
func (p ProblemDetails) MarshalJSON() ([]byte, error) {
	if len(p.extra) == 0 {
		// Fast path: standard members only. The struct tags already produce
		// the correct shape; fall through to default encoding.
		type alias ProblemDetails
		return json.Marshal(alias(p))
	}
	// Slow path: merge extension keys into a flat map.
	out := map[string]any{}
	if p.Type != "" {
		out["type"] = p.Type
	}
	if p.Title != "" {
		out["title"] = p.Title
	}
	if p.Status != 0 {
		out["status"] = p.Status
	}
	if p.Detail != "" {
		out["detail"] = p.Detail
	}
	if p.Instance != "" {
		out["instance"] = p.Instance
	}
	if len(p.Errors) > 0 {
		out["errors"] = p.Errors
	}
	for k, v := range p.extra {
		out[k] = v
	}
	return json.Marshal(out)
}

// NewProblem constructs a ProblemDetails with sensible RFC 9457 defaults:
// Type defaults to "about:blank"; Status is required.
func NewProblem(status int, title, detail string) *ProblemDetails {
	return &ProblemDetails{
		Type:   "about:blank",
		Title:  title,
		Status: status,
		Detail: detail,
	}
}

// WithFieldError appends a FieldError and returns the receiver for chaining.
func (p *ProblemDetails) WithFieldError(field, code, message string, value any) *ProblemDetails {
	p.Errors = append(p.Errors, FieldError{Field: field, Code: code, Message: message, Value: value})
	return p
}

// WithInstance sets the instance URI (usually request path or request id).
func (p *ProblemDetails) WithInstance(instance string) *ProblemDetails {
	p.Instance = instance
	return p
}

// Write serializes the problem to w using the negotiated codec and the
// ProblemMediaType content type. It sets the response status to p.Status
// (or 500 if zero) and MUST be the last write to the response.
func (p *ProblemDetails) Write(w http.ResponseWriter, codec Codec) error {
	if codec == nil {
		codec = DefaultJSONCodec
	}
	status := p.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", ProblemMediaType)
	w.WriteHeader(status)
	body, err := codec.Encode(p)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// Error implements the error interface so ProblemDetails flows through
// standard error paths. The text is RFC-9457 short form.
func (p *ProblemDetails) Error() string {
	if p == nil {
		return ""
	}
	if p.Detail != "" {
		return fmt.Sprintf("problem: %s: %s (%d)", p.Title, p.Detail, p.Status)
	}
	return fmt.Sprintf("problem: %s (%d)", p.Title, p.Status)
}

// ValidateProblem is a helper that produces a 422 ProblemDetails carrying
// the supplied field errors. Convenience for handlers returning validation
// failures from c.Bind.
func ValidateProblem(detail string, errs ...FieldError) *ProblemDetails {
	return &ProblemDetails{
		Type:   "about:blank",
		Title:  "Validation failed",
		Status: http.StatusUnprocessableEntity,
		Detail: detail,
		Errors: append([]FieldError(nil), errs...),
	}
}

// BindProblem produces a 400 ProblemDetails for malformed input payloads.
func BindProblem(detail string) *ProblemDetails {
	return &ProblemDetails{
		Type:   "about:blank",
		Title:  "Malformed request",
		Status: http.StatusBadRequest,
		Detail: detail,
	}
}

// NotFoundProblem returns a 404 problem for unmatched routes.
func NotFoundProblem(path string) *ProblemDetails {
	return &ProblemDetails{
		Type:     "about:blank",
		Title:    "Not Found",
		Status:   http.StatusNotFound,
		Detail:   fmt.Sprintf("no route for %s", path),
		Instance: path,
	}
}

// MethodNotAllowedProblem returns a 405 problem; Allow header is the caller's
// responsibility (the router sets it before invoking this).
func MethodNotAllowedProblem(method, path string, allow string) *ProblemDetails {
	p := &ProblemDetails{
		Type:     "about:blank",
		Title:    "Method Not Allowed",
		Status:   http.StatusMethodNotAllowed,
		Detail:   fmt.Sprintf("%s not allowed on %s", method, path),
		Instance: path,
	}
	if allow != "" {
		p.SetExtension("allow", allow)
	}
	return p
}
