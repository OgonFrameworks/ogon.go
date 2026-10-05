// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package compiler

import (
	"strings"
	"testing"
)

func TestLexer_TopLevelBlocks(t *testing.T) {
	src := `<go>package x</go><template><div>hi</div></template>`
	toks, err := NewLexer(src).Lex()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	var kinds []string
	for _, tk := range toks {
		kinds = append(kinds, tokenName(tk.Kind))
	}
	// Expect at least: BlockOpen(go), Text, BlockEnd(go), BlockOpen(template), TagOpen, TagName(div), ...
	if !contains(kinds, "BlockOpen") || !contains(kinds, "BlockEnd") {
		t.Fatalf("missing block tokens; got %v", kinds)
	}
	if !contains(kinds, "TagName") {
		t.Fatalf("missing tag name token")
	}
}

func TestLexer_TemplateWithAttrs(t *testing.T) {
	src := `<template><button ogon:click="Send" class="btn">x</button></template>`
	toks, err := NewLexer(src).Lex()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	var names []string
	for _, tk := range toks {
		if tk.Kind == TokAttrName {
			names = append(names, tk.Value)
		}
	}
	if !contains(names, "ogon:click") {
		t.Fatalf("expected ogon:click attr; got %v", names)
	}
	if !contains(names, "class") {
		t.Fatalf("expected class attr; got %v", names)
	}
}

func TestLexer_SelfClosingTag(t *testing.T) {
	src := `<template><img src="x.png" /></template>`
	toks, err := NewLexer(src).Lex()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	hasSelf := false
	for _, tk := range toks {
		if tk.Kind == TokTagSelf {
			hasSelf = true
		}
	}
	if !hasSelf {
		t.Fatalf("expected TokTagSelf; got %v", toks)
	}
}

func TestLexer_UnterminatedBlock(t *testing.T) {
	src := `<go>package x`
	_, err := NewLexer(src).Lex()
	if err == nil {
		t.Fatal("expected error on unterminated block")
	}
	if !strings.Contains(err.Error(), "missing </go>") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLexer_CommentSkipped(t *testing.T) {
	src := `<template><!-- comment --><div>x</div></template>`
	_, err := NewLexer(src).Lex()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
}

func TestLexer_QuotedValueWithSpaces(t *testing.T) {
	src := `<template><div class="a b c"></div></template>`
	toks, err := NewLexer(src).Lex()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	for _, tk := range toks {
		if tk.Kind == TokAttrValue && tk.Value == "a b c" {
			return
		}
	}
	t.Fatalf("missing quoted value with spaces; got %v", toks)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func tokenName(k TokenKind) string {
	switch k {
	case TokEOF:
		return "EOF"
	case TokText:
		return "Text"
	case TokTagOpen:
		return "TagOpen"
	case TokTagClose:
		return "TagClose"
	case TokTagSelf:
		return "TagSelf"
	case TokTagName:
		return "TagName"
	case TokAttrName:
		return "AttrName"
	case TokAttrValue:
		return "AttrValue"
	case TokBlockOpen:
		return "BlockOpen"
	case TokBlockEnd:
		return "BlockEnd"
	case TokError:
		return "Error"
	}
	return "Unknown"
}
