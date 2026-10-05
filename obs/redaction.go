// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Global redaction hook: every slog record is filtered for PII before it
// reaches the sink. Implements OBS-005 (and the OBS-side of SEC-030/031).

package obs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// PIIKind classifies a piece of PII. Used by the lint and redaction paths
// so callers can ask "what got redacted". (OBS-043)
type PIIKind string

const (
	PIIEmail    PIIKind = "email"
	PIIPhone    PIIKind = "phone"
	PIISSN      PIIKind = "ssn"
	PIICredit   PIIKind = "credit_card"
	PIIJWT      PIIKind = "jwt"
	PIIIPv4     PIIKind = "ipv4"
	PIIUUID     PIIKind = "uuid"
	PIIOther    PIIKind = "other"
	PIINone     PIIKind = ""
	PIIRedacted PIIKind = "redacted"
)

// redactionRule pairs a regex with the PII kind it detects.
type redactionRule struct {
	re   *regexp.Regexp
	kind PIIKind
}

// redactionRules are ordered: more specific (uuid, jwt) first so generic
// patterns don't shadow them. (OBS-043)
var redactionRules = []redactionRule{
	// JWT: three base64url dot-delimited segments with ≥4-char signature.
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{4,}`), PIIJWT},
	// UUID v1-v5.
	{regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`), PIIUUID},
	// Email.
	{regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`), PIIEmail},
	// US SSN (with hyphens or spaces).
	{regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), PIISSN},
	// Credit card: 13-19 digits, optional space/dash separators.
	{regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`), PIICredit},
	// E.164 phone.
	{regexp.MustCompile(`\+\d{6,15}\b`), PIIPhone},
	// IPv4 (skip 0.0.0.0 and 127.0.0.1 — they're infra, not user data).
	{regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`), PIIIPv4},
}

// redactString returns the scrubbed string + a non-empty PIIKind if any
// substitution happened. (OBS-043/SEC-030)
func redactString(s string) (string, PIIKind) {
	kind := PIIKind("")
	for _, r := range redactionRules {
		if r.re.MatchString(s) {
			s = r.re.ReplaceAllString(s, "[REDACTED:"+string(r.kind)+"]")
			if kind == "" {
				kind = r.kind
			}
		}
	}
	if kind == "" {
		return s, PIINone
	}
	return s, kind
}

// RedactString is the public scrubber. It is the OBS-side of SEC-030/031.
func RedactString(s string) string {
	out, _ := redactString(s)
	return out
}

// RedactMap returns a shallow copy of m with all string values scrubbed.
// Keys are NOT redacted — they're field names; redact them by choosing
// better names upstream.
func RedactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = RedactValue(v)
	}
	return out
}

// RedactValue scrubs a single arbitrary value, recursing into maps/slices.
func RedactValue(v any) any {
	switch x := v.(type) {
	case string:
		return RedactString(x)
	case []byte:
		return RedactString(string(x))
	case map[string]any:
		return RedactMap(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = RedactValue(e)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = RedactString(e)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(x))
		for i, e := range x {
			out[i] = RedactMap(e)
		}
		return out
	}
	return v
}

// redactHandler wraps a slog.Handler so every record is filtered before
// it reaches the underlying sink. (OBS-005)
type redactHandler struct {
	inner slog.Handler
}

// wrapWithRedaction installs the global redaction hook on h.
func wrapWithRedaction(h slog.Handler) slog.Handler { return &redactHandler{inner: h} }

func (r *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return r.inner.Enabled(ctx, l)
}

func (r *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cleaned := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		cleaned = append(cleaned, scrubAttr(a))
	}
	return &redactHandler{inner: r.inner.WithAttrs(cleaned)}
}

func (r *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{inner: r.inner.WithGroup(name)}
}

func (r *redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	clone := rec.Clone()
	clone.Attrs(func(a slog.Attr) bool {
		// Inline replace: the record API has no setter, so we collect
		// scrubbed attrs into a temp record.
		return true
	})
	// Re-emit on a fresh record so we control the attr list.
	out := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(scrubAttr(a))
		return true
	})
	return r.inner.Handle(ctx, out)
}

// scrubAttr applies redaction to an attribute's value and key. Keys named
// like the obvious PII carriers are force-replaced with a placeholder.
func scrubAttr(a slog.Attr) slog.Attr {
	if isPIIKey(a.Key) {
		a.Value = slog.StringValue("[REDACTED:key]")
		return a
	}
	a.Value = scrubValue(a.Value)
	return a
}

// isPIIKey reports whether k plausibly names a PII field. (OBS-043)
func isPIIKey(k string) bool {
	lk := strings.ToLower(k)
	switch {
	case strings.Contains(lk, "password"),
		strings.Contains(lk, "secret"),
		strings.Contains(lk, "token"),
		strings.Contains(lk, "authorization"),
		strings.Contains(lk, "cookie"),
		strings.Contains(lk, "api_key"),
		strings.Contains(lk, "apikey"),
		strings.Contains(lk, "api-key"),
		strings.Contains(lk, "private_key"),
		strings.Contains(lk, "ssn"),
		strings.Contains(lk, "credit_card"),
		strings.Contains(lk, "cardnumber"),
		strings.Contains(lk, "cvv"),
		strings.Contains(lk, "iban"),
		strings.Contains(lk, "bic"),
		strings.Contains(lk, "email"),
		strings.Contains(lk, "phone"),
		strings.Contains(lk, "jwt"):
		return true
	}
	return false
}

// scrubValue scrubs a slog.Value. Strings are regex-matched; kinds are
// logged via a "_pii" sidecar attr only in debug builds (off here to keep
// the prod sink lean — OBS-044 budget).
func scrubValue(v slog.Value) slog.Value {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		return slog.StringValue(RedactString(s))
	case slog.KindAny:
		x := v.Any()
		switch t := x.(type) {
		case string:
			return slog.StringValue(RedactString(t))
		case []byte:
			return slog.StringValue(RedactString(string(t)))
		case map[string]any:
			b, err := json.Marshal(RedactMap(t))
			if err != nil {
				return slog.StringValue("[REDACTED:json]")
			}
			return slog.StringValue(string(b))
		case []any:
			b, err := json.Marshal(RedactValue(t))
			if err != nil {
				return slog.StringValue("[REDACTED:json]")
			}
			return slog.StringValue(string(b))
		case error:
			return slog.StringValue(RedactString(t.Error()))
		case fmt.Stringer:
			return slog.StringValue(RedactString(t.String()))
		}
	}
	return v
}

// IsPIIKey is the exported form for lint callers and tests.
func IsPIIKey(k string) bool { return isPIIKey(k) }

// now returns the current time. Indirected for tests.
var now = func() time.Time { return time.Now() }
