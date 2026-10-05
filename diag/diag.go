// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Diagnostic primitives: structured, machine-remediable error reporting.

package diag

import (
	"fmt"
	"strings"
)

// Severity classifies a diagnostic's impact.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityError
)

// String returns the lower-case name used in JSON rendering.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	}
	return "unknown"
}

// Diag is the canonical structured diagnostic. See PROMPT.md Part II.7.
//
// Invariants:
//   - Code is stable forever; retired codes are reserved.
//   - Severity is non-zero for user-visible failures.
//   - At least one of What or Title is non-empty.
//   - Fix steps are ordered; Remedy (when set) is a runnable shell command.
type Diag struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title,omitempty"`
	What     string   `json:"what,omitempty"`
	Why      string   `json:"why,omitempty"`
	Where    string   `json:"where,omitempty"`
	Expected string   `json:"expected,omitempty"`
	Found    string   `json:"found,omitempty"`
	Fix      []string `json:"fix,omitempty"`
	Remedy   string   `json:"remedy,omitempty"`
	Docs     string   `json:"docs,omitempty"`
}

// Error implements the error interface so Diag values flow through
// standard error paths. The rendered form is single-line human text.
func (d *Diag) Error() string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	if d.Code != "" {
		b.WriteString(d.Code)
		b.WriteString(": ")
	}
	if d.Title != "" {
		b.WriteString(d.Title)
	} else if d.What != "" {
		b.WriteString(d.What)
	}
	if d.Where != "" {
		b.WriteString(" (")
		b.WriteString(d.Where)
		b.WriteByte(')')
	}
	if d.Remedy != "" {
		b.WriteString(" — remedy: ")
		b.WriteString(d.Remedy)
	}
	return b.String()
}

// Is enables errors.Is matching by code.
func (d *Diag) Is(target error) bool {
	if t, ok := target.(*Diag); ok {
		return d.Code == t.Code && (t.Title == "" || d.Title == t.Title)
	}
	return false
}

// With returns a copy of d with the supplied fields merged in. Non-zero
// values in opts replace existing values; nil slices are ignored.
func (d *Diag) With(opts Diag) *Diag {
	out := *d
	if opts.Code != "" {
		out.Code = opts.Code
	}
	if opts.Severity != 0 {
		out.Severity = opts.Severity
	}
	if opts.Title != "" {
		out.Title = opts.Title
	}
	if opts.What != "" {
		out.What = opts.What
	}
	if opts.Why != "" {
		out.Why = opts.Why
	}
	if opts.Where != "" {
		out.Where = opts.Where
	}
	if opts.Expected != "" {
		out.Expected = opts.Expected
	}
	if opts.Found != "" {
		out.Found = opts.Found
	}
	if opts.Fix != nil {
		out.Fix = append([]string(nil), opts.Fix...)
	}
	if opts.Remedy != "" {
		out.Remedy = opts.Remedy
	}
	if opts.Docs != "" {
		out.Docs = opts.Docs
	}
	return &out
}

// New constructs a Diag pointer. Convenience for inline use.
func New(code, title, what string) *Diag {
	return &Diag{Code: code, Severity: SeverityError, Title: title, What: what}
}

// Wrap attaches a diagnostic envelope to an existing error without losing
// the original chain. If err is already a *Diag, its fields are merged
// with template and the original is returned (template wins on conflict).
func Wrap(err error, template Diag) *Diag {
	if err == nil {
		return nil
	}
	if existing, ok := err.(*Diag); ok {
		return existing.With(template)
	}
	d := template
	if d.Code == "" {
		d.Code = "OGON-U0001"
	}
	if d.Title == "" {
		d.Title = "unexpected error"
	}
	if d.What == "" {
		d.What = err.Error()
	}
	return &d
}

// FromError converts a plain error to a Diag using a mapper function. The
// mapper returns nil if it cannot classify the error; in that case a
// generic U-class diagnostic is emitted.
func FromError(err error, mapper func(error) *Diag) *Diag {
	if err == nil {
		return nil
	}
	if d, ok := err.(*Diag); ok {
		return d
	}
	if mapper != nil {
		if d := mapper(err); d != nil {
			return d
		}
	}
	return &Diag{
		Code:     "OGON-U0001",
		Severity: SeverityError,
		Title:    "unexpected error",
		What:     err.Error(),
	}
}

// FormatHuman renders a multi-line human-readable form. Color codes are
// the caller's responsibility; this returns plain text suitable for TTY
// or non-TTY output.
func (d *Diag) FormatHuman() string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	if d.Code != "" {
		fmt.Fprintf(&b, "[%s] ", d.Code)
	}
	if d.Title != "" {
		b.WriteString(d.Title)
		b.WriteByte('\n')
	}
	if d.What != "" {
		b.WriteString("  what:    ")
		b.WriteString(d.What)
		b.WriteByte('\n')
	}
	if d.Why != "" {
		b.WriteString("  why:     ")
		b.WriteString(d.Why)
		b.WriteByte('\n')
	}
	if d.Where != "" {
		b.WriteString("  where:   ")
		b.WriteString(d.Where)
		b.WriteByte('\n')
	}
	if d.Expected != "" || d.Found != "" {
		b.WriteString("  expected: ")
		b.WriteString(d.Expected)
		b.WriteString("\n  found:    ")
		b.WriteString(d.Found)
		b.WriteByte('\n')
	}
	for i, step := range d.Fix {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, step)
	}
	if d.Remedy != "" {
		b.WriteString("  remedy:  ")
		b.WriteString(d.Remedy)
		b.WriteByte('\n')
	}
	if d.Docs != "" {
		b.WriteString("  docs:    ")
		b.WriteString(d.Docs)
		b.WriteByte('\n')
	}
	return b.String()
}
