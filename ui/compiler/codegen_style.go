// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `<style>` → scoped CSS generator (UI-004). Class names are
// rewritten into a hashed form (`.ogon-<component>-<hash>`) and
// the generated template's elements carry the hashed class so
// styles cannot leak across components.

package compiler

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// StyleCodegen turns a `<style>` block into scoped CSS.
type StyleCodegen struct {
	file *File
}

// NewStyleCodegen constructs the codegen.
func NewStyleCodegen(f *File) *StyleCodegen { return &StyleCodegen{file: f} }

// Scope returns the hashed scope class for this component (stable
// across rebuilds given the same component name).
func (g *StyleCodegen) Scope() string {
	if g.file.Style != nil && g.file.Style.Scope != "" {
		return g.file.Style.Scope
	}
	return "ogon-" + hashComponent(g.file.Name)
}

// Emit rewrites the raw CSS body into scoped form and returns the
// result plus the scope class.
func (g *StyleCodegen) Emit() (css, scope string) {
	scope = g.Scope()
	if g.file.Style == nil {
		return "", scope
	}
	raw := g.file.Style.Raw
	css = scopeCSS(raw, scope)
	g.file.Style.Scope = scope
	return css, scope
}

// hashComponent produces a short hex digest from a component name.
func hashComponent(name string) string {
	h := sha1.Sum([]byte("ogon:" + name))
	return hex.EncodeToString(h[:])[:8]
}

// scopeCSS rewrites the leading class selector of every rule to
// prefix it with the scope class. For example:
//
//	.button { color: red }  →  .ogon-Chat-xxxxxxxx .button { color: red }
//
// Top-level type selectors (`button {}`) become
// `.ogon-Chat-xxxxxxxx button {}`.
func scopeCSS(raw, scope string) string {
	var b strings.Builder
	i := 0
	for i < len(raw) {
		// Find the next rule boundary '{'.
		brace := strings.IndexByte(raw[i:], '{')
		if brace < 0 {
			b.WriteString(raw[i:])
			break
		}
		selector := strings.TrimSpace(raw[i : i+brace])
		rest := raw[i+brace:]
		// Split selector on commas.
		parts := strings.Split(selector, ",")
		for j, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(scope + " " + p)
		}
		// Copy the declaration block through to the closing '}'.
		closeIdx := strings.IndexByte(rest, '}')
		if closeIdx < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:closeIdx+1])
		i += brace + closeIdx + 1
	}
	return b.String()
}
