// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `.ogon` parser: turns the lexer token stream into an AST (UI-001,
// UI-081). Diagnostics carry line:col for every error path.

package compiler

import (
	"strings"
)

// Parser consumes a token stream and produces a File AST. Errors are
// returned as ParseError values that include line:col (UI-081).
type Parser struct {
	toks []Token
	pos  int
	name string
	pkg  string
}

// NewParser builds a Parser over the supplied token slice.
func NewParser(name, pkg string, toks []Token) *Parser {
	return &Parser{toks: toks, name: name, pkg: pkg}
}

// Parse runs the parser and returns the assembled File AST.
func (p *Parser) Parse() (*File, error) {
	f := &File{Name: p.name, PkgName: p.pkg}
	if f.PkgName == "" {
		f.PkgName = "components"
	}
	for !p.atEnd() {
		t := p.peek()
		switch t.Kind {
		case TokBlockOpen:
			if err := p.parseBlock(f); err != nil {
				return nil, err
			}
		case TokTagOpen:
			// Bare template element (no <template> wrapper).
			root, err := p.parseElement()
			if err != nil {
				return nil, err
			}
			f.Root = root
		case TokEOF, TokText:
			p.advance()
		default:
			return nil, p.errorf(t, "unexpected token at top level")
		}
	}
	return f, nil
}

// ParseFile parses a `.ogon` source string in one call.
func ParseFile(name, pkg, src string) (*File, error) {
	l := NewLexer(src)
	toks, err := l.Lex()
	if err != nil {
		return nil, err
	}
	return NewParser(name, pkg, toks).Parse()
}

func (p *Parser) parseBlock(f *File) error {
	open := p.peek()
	name := strings.ToLower(open.Value)
	switch name {
	case "go":
		f.Go = &GoBlock{baseNode: baseNode{line: open.Line, col: open.Col}, Package: f.PkgName}
		p.advance()
		// Next TokText is the block body.
		if t := p.peek(); t.Kind == TokText {
			f.Go.Raw = t.Value
			f.Go.Props, f.Go.State, f.Go.Methods, f.Go.Imports, f.Go.Mounts, f.Go.Unmounts = ParseGoBody(t.Value)
			p.advance()
		}
		// Expect TokBlockEnd.
		if err := p.consumeBlockEnd(name); err != nil {
			return err
		}
	case "template":
		p.advance()
		// Parse element tokens until TokBlockEnd.
		root, err := p.parseTemplateBody()
		if err != nil {
			return err
		}
		if root != nil {
			f.Root = root
		}
		if err := p.consumeBlockEnd(name); err != nil {
			return err
		}
	case "style":
		f.Style = &StyleBlock{baseNode: baseNode{line: open.Line, col: open.Col}, Lang: "css"}
		p.advance()
		if t := p.peek(); t.Kind == TokText {
			f.Style.Raw = t.Value
			p.advance()
		}
		if err := p.consumeBlockEnd(name); err != nil {
			return err
		}
	case "script":
		f.Script = &ScriptBlock{baseNode: baseNode{line: open.Line, col: open.Col}, Lang: "ts"}
		p.advance()
		// Parse lang= attribute (skip; the lexer dropped attrs).
		// Body is TokText.
		if t := p.peek(); t.Kind == TokText {
			f.Script.Raw = t.Value
			p.advance()
		}
		if err := p.consumeBlockEnd(name); err != nil {
			return err
		}
	default:
		return p.errorf(open, "unknown top-level block: %s", name)
	}
	return nil
}

func (p *Parser) consumeBlockEnd(name string) error {
	for !p.atEnd() {
		t := p.peek()
		if t.Kind == TokBlockEnd && strings.ToLower(t.Value) == name {
			p.advance()
			return nil
		}
		p.advance()
	}
	return p.errorf(Token{Line: 1, Col: 1}, "missing </%s> close", name)
}

// parseTemplateBody parses element tokens until TokBlockEnd.
// We pick the first root element; siblings are nested under it.
func (p *Parser) parseTemplateBody() (*Element, error) {
	var root *Element
	for {
		t := p.peek()
		switch t.Kind {
		case TokBlockEnd, TokEOF:
			return root, nil
		case TokTagOpen:
			el, err := p.parseElement()
			if err != nil {
				return nil, err
			}
			if root == nil {
				root = el
			} else if root.Children == nil {
				root.Children = []Node{el}
			} else {
				root.Children = append(root.Children, el)
			}
		case TokText:
			// Stray text between tags — collect as element text.
			if root != nil {
				root.Text += p.peek().Value
			}
			p.advance()
		default:
			p.advance()
		}
	}
}

// parseElement reads one open/close element (or self-close) and its
// children. Returns an Element AST node.
func (p *Parser) parseElement() (*Element, error) {
	// Expect TokTagOpen, TokTagName.
	open := p.advance()
	if open.Kind != TokTagOpen {
		return nil, p.errorf(open, "expected '<'")
	}
	nameTok := p.advance()
	if nameTok.Kind != TokTagName {
		return nil, p.errorf(nameTok, "expected tag name")
	}
	el := &Element{
		baseNode:  baseNode{line: nameTok.Line, col: nameTok.Col},
		Tag:       nameTok.Value,
		Attrs:     map[string]string{},
		SelfClose: false,
	}
	// Attributes / directives.
	for {
		t := p.peek()
		switch t.Kind {
		case TokTagSelf:
			el.SelfClose = true
			p.advance()
			return el, nil
		case TokAttrName:
			attr := p.advance()
			// ogon:* directives are collected separately.
			if strings.HasPrefix(attr.Value, "ogon:") {
				dir, err := p.parseDirective(attr)
				if err != nil {
					return nil, err
				}
				el.Directives = append(el.Directives, dir)
				continue
			}
			// Regular attribute may have a value.
			val := ""
			if p.peek().Kind == TokAttrValue {
				val = p.advance().Value
			}
			el.Attrs[attr.Value] = val
			el.Order = append(el.Order, attr.Value)
		case TokTagClose:
			// A closing tag at this position means the open tag was
			// already implicitly closed by lexAttrs. The body parser
			// will handle the matching close for the right element.
			return p.parseElementBody(el)
		case TokTagOpen, TokText, TokEOF, TokBlockEnd:
			// lexAttrs consumed the '>' already; we're in the
			// element body now.
			return p.parseElementBody(el)
		default:
			// Anything else is unexpected — bail to body parser.
			return p.parseElementBody(el)
		}
	}
}

// parseElementBody reads text/children until the close tag for `el`.
func (p *Parser) parseElementBody(el *Element) (*Element, error) {
	// Void elements (no children).
	if isVoid(el.Tag) {
		return el, nil
	}
	for {
		t := p.peek()
		switch t.Kind {
		case TokEOF, TokBlockEnd:
			return el, nil
		case TokTagClose:
			if strings.EqualFold(t.Value, el.Tag) {
				p.advance()
				return el, nil
			}
			// Closing tag for a parent element — leave it for the
			// outer caller and return the (potentially empty)
			// element we've collected so far.
			return el, nil
		case TokTagOpen:
			child, err := p.parseElement()
			if err != nil {
				return nil, err
			}
			el.Children = append(el.Children, child)
		case TokText:
			// Process {{ expr }} interpolations.
			el.Children = append(el.Children, parseInterp(p.advance())...)
		case TokTagSelf:
			p.advance()
		default:
			p.advance()
		}
	}
}

// parseInterp splits text content into Interp nodes and literal text.
// `{{ expr }}` becomes an Interp; surrounding text becomes Elements
// with .Tag="" and .Text set.
func parseInterp(t Token) []Node {
	out := []Node{}
	text := t.Value
	for {
		idx := strings.Index(text, "{{")
		if idx < 0 {
			if text != "" {
				out = append(out, &Element{baseNode: baseNode{line: t.Line, col: t.Col}, Tag: "", Text: text})
			}
			return out
		}
		if idx > 0 {
			out = append(out, &Element{baseNode: baseNode{line: t.Line, col: t.Col}, Tag: "", Text: text[:idx]})
		}
		text = text[idx+2:]
		end := strings.Index(text, "}}")
		if end < 0 {
			out = append(out, &Interp{baseNode: baseNode{line: t.Line, col: t.Col + idx + 2}, Expr: strings.TrimSpace(text)})
			return out
		}
		expr := strings.TrimSpace(text[:end])
		out = append(out, &Interp{baseNode: baseNode{line: t.Line, col: t.Col + idx + 2}, Expr: expr})
		text = text[end+2:]
	}
}

// ParseError carries line:col diagnostics (UI-081).
type ParseError struct {
	Line int
	Col  int
	Msg  string
}

func (e *ParseError) Error() string {
	return "ogon/ui parser " + itoa(e.Line) + ":" + itoa(e.Col) + ": " + e.Msg
}

func (p *Parser) errorf(t Token, format string, args ...any) error {
	return &ParseError{Line: t.Line, Col: t.Col, Msg: sprintf(format, args...)}
}

// Lexer primitives.
func (p *Parser) peek() Token {
	if p.pos >= len(p.toks) {
		return Token{Kind: TokEOF}
	}
	return p.toks[p.pos]
}
func (p *Parser) advance() Token {
	if p.pos >= len(p.toks) {
		return Token{Kind: TokEOF}
	}
	t := p.toks[p.pos]
	p.pos++
	return t
}
func (p *Parser) atEnd() bool { return p.pos >= len(p.toks) || p.toks[p.pos].Kind == TokEOF }

// isVoid reports whether a tag is a void HTML element (no close tag).
func isVoid(tag string) bool {
	switch strings.ToLower(tag) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input",
		"link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}
