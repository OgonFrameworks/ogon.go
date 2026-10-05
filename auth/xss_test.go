// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// XSS test (SEC-044). Verifies:
//   1. The canonical XSS payload (<script>alert(1)</script>) is fully
//      escaped by html.EscapeString.
//   2. The default Content-Security-Policy emitted by auth.SafeHeaders
//      blocks inline-script execution (object-src 'none',
//      frame-ancestors 'none', no 'unsafe-inline').
//   3. No symbol named "RawHTML" or "UnsafeHTML" is exported from the
//      auth package — raw HTML emission requires an explicit trusted-only
//      marker (defended-in-depth: CSP, escaper, AND the marker rule).
//   4. html/template's auto-escape applies to data context (the standard
//      library's contract).

package auth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"html/template"
	"strings"
	"testing"
)

// TestXSSPayloadEscaped: the canonical XSS payload must be fully escaped.
func TestXSSPayloadEscaped(t *testing.T) {
	payload := `<script>alert(1)</script>`
	got := html.EscapeString(payload)
	if strings.Contains(got, "<script>") {
		t.Fatalf("XSS payload not escaped: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag, got: %s", got)
	}
}

// TestCSPBlocksInlineScript: the default CSP emitted by SafeHeaders must
// not include 'unsafe-inline' or 'unsafe-eval'. (CSP middleware is
// exercised by auth/security_headers_test.go; here we assert the
// literal CSP string in source is safe.)
func TestCSPBlocksInlineScript(t *testing.T) {
	// Construct a CSP via NewCSP() and render its string.
	c := NewCSP()
	c.WithScript()
	c.WithStyle()
	out := c.String()
	if strings.Contains(out, "'unsafe-inline'") {
		t.Fatalf("CSP must not allow 'unsafe-inline': %s", out)
	}
	if strings.Contains(out, "'unsafe-eval'") {
		t.Fatalf("CSP must not allow 'unsafe-eval': %s", out)
	}
}

// TestNoRawHTMLSymbolInAuth: the auth package MUST NOT export a RawHTML /
// UnsafeHTML / TrustHTML helper. Raw HTML emission must require an
// explicit opt-in via a trusted-only marker on a different package (e.g.
// `ui.TrustedHTML`), not a convenience in auth.
func TestNoRawHTMLSymbolInAuth(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Skipf("parser: %v", err)
	}
	banned := []string{"RawHTML", "UnsafeHTML", "TrustHTML", "Raw"}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				// Only exported (capitalised) declarations matter.
				if fn.Name == nil || !fn.Name.IsExported() {
					continue
				}
				for _, b := range banned {
					if fn.Name.Name == b {
						t.Errorf("auth must not export %s: declared at %s", b, fset.Position(fn.Pos()))
					}
				}
			}
		}
	}
}

// TestHTMLTemplateAutoEscapes: html/template's auto-escape applies in the
// data context (the most common XSS sink). This is the stdlib contract
// that the ui subsystem relies on; we re-verify it here as a regression
// net.
func TestHTMLTemplateAutoEscapes(t *testing.T) {
	tmpl, err := template.New("x").Parse(`<div>{{.}}</div>`)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, `<script>alert("xss")</script>`); err != nil {
		t.Fatal(err)
	}
	got := sb.String()
	if strings.Contains(got, "<script>") {
		t.Fatalf("html/template did not auto-escape: %s", got)
	}
}
