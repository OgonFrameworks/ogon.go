// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// HTML sanitizer (UI-064/065/SEC-056). Wraps bluemonday with an
// opt-in policy suitable for trusted-HTML render paths. The
// compiler never inserts raw HTML without an explicit sanitize
// call; raw `<go raw>` is restricted to trusted-only sites.

package sanitize

import (
	"errors"

	"github.com/microcosm-cc/bluemonday"
)

// Policy wraps a bluemonday policy with OgonUI conveniences.
type Policy struct {
	bp       *bluemonday.Policy
	allowRaw bool
}

// Default returns the conservative default policy: it strips
// scripts, event handlers, and inline JavaScript while preserving
// formatting tags, links, images, and tables. Use this whenever a
// user-controlled HTML string enters the render path (UI-065).
func Default() *Policy {
	p := bluemonday.UGCPolicy()
	return &Policy{bp: p, allowRaw: false}
}

// Strict returns a minimal policy that allows only a handful of
// formatting tags (b, i, em, strong, code, br). Use this for short
// user-provided snippets such as chat messages.
func Strict() *Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("b", "i", "em", "strong", "code", "br")
	return &Policy{bp: p, allowRaw: false}
}

// TrustedRaw returns a policy that passes content through
// unchanged. RESERVED for trusted-only render sites (UI-064). The
// compiler emits a compile-time diagnostic when this policy is
// used outside a `trusted`-flagged component.
func TrustedRaw() *Policy {
	p := bluemonday.NewPolicy()
	p.AllowUnsafe(true)
	return &Policy{bp: p, allowRaw: true}
}

// Sanitize runs the policy over the supplied HTML. The result is
// safe to insert into a `<template>` interpolation.
func (p *Policy) Sanitize(html string) string {
	return p.bp.Sanitize(html)
}

// IsTrustedRaw reports whether the policy is the trusted-raw
// escape hatch (UI-064).
func (p *Policy) IsTrustedRaw() bool { return p.allowRaw }

// ErrUntrustedRaw is returned when a render path tries to use the
// TrustedRaw policy without the `trusted` component flag set.
var ErrUntrustedRaw = errors.New("ogon/ui: raw-HTML escape hatch requires trusted component flag (UI-064)")

// EnforceTrusted is a guard helper used by the compiler-emitted
// render path. It returns ErrUntrustedRaw unless the caller is a
// component flagged as trusted.
func EnforceTrusted(trusted bool) error {
	if !trusted {
		return ErrUntrustedRaw
	}
	return nil
}

// AutoSanitize is the entry point the compiler inserts into every
// `<template>` interpolation that binds a user-controlled value.
// It applies the Default policy and returns the safe HTML.
func AutoSanitize(html string) string {
	return Default().Sanitize(html)
}
