// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Fuzz the `.ogon` parser (UI-085). The fuzz target must not panic
// on arbitrary input — every input is either parsed successfully
// or produces a ParseError.
//
// (P14 bug-bounty: extended seed corpus with more adversarial
// fragments — deeply nested elements, malformed comments, mixed
// block orders, control bytes, surrogate unicode, etc.)

package compiler

import (
	"strings"
	"testing"
)

func FuzzParser(f *testing.F) {
	// Seed corpus: real and adversarial fragments.
	seeds := []string{
		// Original seeds:
		"<go>x</go>",
		"<template><div>hi</div></template>",
		"<go>type Props struct { Title string }</go><template><h1>{{Title}}</h1></template>",
		"<style>.a{color:red}</style>",
		"<template><div ogon:click=\"X\" ogon:for=\"i in S\">{{i}}</div></template>",
		"<template><input ogon:model=\"S.X\" /></template>",
		"<!-- just a comment -->",
		"",
		"<go>abc",
		"<template><div" + string([]byte{0x00}) + ">",
		"{{{{}}}",
		"<a><b><c></c></b></a>",

		// P14 seed extensions:
		"<template></template>", // empty template
		"<go></go>",             // empty go block
		"<style></style>",       // empty style
		"<template>" + strings.Repeat("<div>", 64) + strings.Repeat("</div>", 64) + "</template>", // deep
		"<template><!-- --><!-- --></template>",                                                   // multiple comments
		"<go>package x</go><go>package y</go>",                                                    // duplicate go block (semi-spec)
		"<template><div ogon:if=\"A\" ogon:else=\"B\">x</div></template>",
		"<template><div ogon:for=\"i in items\" ogon:key=\"i.id\">{{i.id}}</div></template>",
		"<template><slot name=\"header\" /></template>",
		"<template>" + strings.Repeat(" ", 1024) + "<div>x</div></template>",                 // huge whitespace
		"<template><div class=" + strings.Repeat("x", 1024) + ">x</div></template>",          // huge attr
		"<template>\r\n\t<div>\r\n\t\tx\r\n\t</div>\r\n</template>",                          // CRLF + tabs
		"<template><div>🔒 emoji 🔥</div></template>",                                          // emoji content
		"<template><div ogon:click=\"function() { return true; }\">x</div></template>",       // JS-ish expr
		"<TEMPLATE><DIV>uppercase</DIV></TEMPLATE>",                                          // uppercase (semi-spec)
		"<template><div>" + strings.Repeat("{{x}}", 256) + "</div></template>",               // many interpolations
		"<template><div>" + strings.Repeat("{{{{", 32) + "</div></template>",                 // broken interpolation
		"<template><div ogon:for=\"" + strings.Repeat("i in ", 32) + "\">x</div></template>", // repeated
		"<template><div></div>" + strings.Repeat("<span></span>", 100) + "</template>",       // many siblings
		"<!-- unclosed comment",
		"<template><div>" + strings.Repeat("<p>", 100) + "</div></template>", // unbalanced tags
		"<template><div ogon:click=\"'\"  ogon:for=\"'\">x</div></template>", // single quotes
		"<template><div ogon:click=\"\\\"x\\\"\">x</div></template>",         // escaped quotes
		string([]byte{0x00, 0x01, 0x02, 0x03}),                               // pure binary
		strings.Repeat("<go>x</go>", 100),                                    // many blocks
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		// The parser must not panic. Errors are acceptable.
		_, err := ParseFile("Fuzz", "components", src)
		if err != nil {
			// Ensure errors render cleanly (no panic in Error()).
			_ = err.Error()
			return
		}
	})
}

func FuzzLexer(f *testing.F) {
	seeds := []string{
		"<go>x</go>",
		"<template><div>hi</div></template>",
		"<style>.a{color:red}</style>",
		"",
		"<a><b><c></c></b></a>",
		"<!-- comment -->",
		"<div ogon:click=\"X\" />",
		"{{{{}}}",

		// P14 extensions:
		"<template>" + strings.Repeat("<div>", 64) + "</template>",
		"<style>" + strings.Repeat("a{color:red}", 32) + "</style>",
		"<go>" + strings.Repeat("x", 1024) + "</go>",
		"<!-- " + strings.Repeat("-", 1024) + " -->",
		"<template>" + strings.Repeat("{{x}}", 256) + "</template>",
		"\x00\x01\x02binary",
		strings.Repeat(" ", 1024),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		_, err := NewLexer(src).Lex()
		if err != nil {
			_ = err.Error()
			return
		}
	})
}

// FuzzParserFull is the P14 bug-bounty full-pipeline fuzz target.
// It exercises ParseFile + Lex together so adversarial inputs are
// measured through the entire compilation entry point — not just
// the parser alone. The contract: no panic, errors render cleanly
// via Error().
//
// Run: go test ./ui/compiler -fuzz=FuzzParserFull -fuzztime=3s
func FuzzParserFull(f *testing.F) {
	// Reuse the FuzzParser seeds (representative real fragments) plus
	// a few additional adversarial shapes specifically targeting the
	// full pipeline.
	seeds := []string{
		"<go>type X struct{ A string }</go><template><div>{{X.A}}</div></template>",
		"<style>.x{color:red}</style><template><div class=\"x\">hi</div></template>",
		"<go>package main</go><template><h1>title</h1></template><style>h1{}</style>",
		"",
		"<template><div ogon:if=\"A\" ogon:for=\"i in S\">{{i}}</div></template>",
		"<template><input ogon:model=\"a.b.c.d\" /></template>",
		"<go>abc<template>unterminated",
		strings.Repeat("<div>", 100) + strings.Repeat("</div>", 100),
		"<template><div>" + strings.Repeat("{{x}}", 256) + "</div></template>",
		"<!-- " + strings.Repeat("-", 1024) + " --><template><div>x</div></template>",
		"<template><div ogon:click=\"" + strings.Repeat("a", 1024) + "\">x</div></template>",
		"\x00binary\x00input",
		"<TEMPLATE><DIV>UPPER</DIV></TEMPLATE>",
		"<go>" + strings.Repeat("x", 2048) + "</go>",
		"<template><div>" + strings.Repeat("🔒", 100) + "</div></template>",
		"<template><div ogon:for=\"i in " + strings.Repeat("s.", 64) + "x\">x</div></template>",
		"<template><div ogon:if=\"a == b && c != d || e > f\">x</div></template>",

		// P14 bug-bounty: 3 additional adversarial shapes targeting
		// parser robustness on inputs not previously seeded.
		// 1. BOM-prefixed template (UTF-8 BOM at byte 0; lexer must
		//    skip BOM without mis-tokenising it as an identifier).
		"\uFEFF<template><div ogon:click=\"X\">bom</div></template>",
		// 2. Element with an extremely long attribute NAME (not value)
		//    — exercises the lexer's ident-run scanner cap.
		"<template><div " + strings.Repeat("data", 256) + "=\"v\">x</div></template>",
		// 3. Many directives on a single element (invalid per spec
		//    but must not panic; parser must surface a clean diag).
		"<template><div" + strings.Repeat(" ogon:if=\"x\"", 32) + ">y</div></template>",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseFile panicked on %q: %v", truncUI(src), r)
			}
		}()
		_, err := ParseFile("FuzzFull", "components", src)
		if err != nil {
			// Error.Error() must not panic.
			_ = err.Error()
		}
	})
}

// truncUI keeps test failure messages readable.
func truncUI(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
