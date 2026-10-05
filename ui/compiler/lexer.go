// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `.ogon` lexer: tokenises the four top-level blocks (UI-001/081).
//
// The lexer is intentionally small and deterministic. It scans the
// source into one of three top-level regions: code (the bare <go>
// block body), tag (template markup), or style/script (raw text
// blocks). Inside tag regions it produces tokens for open/close
// tags, attributes, and text. Diagnostics carry line:col (UI-081).

package compiler

import (
	"strings"
	"unicode/utf8"
)

// TokenKind enumerates lexer token types.
type TokenKind int

const (
	TokEOF TokenKind = iota
	TokError
	TokText      // raw character data (text node or block body)
	TokTagOpen   // <tag
	TokTagClose  // </tag>
	TokTagSelf   // />
	TokTagName   // identifier after < or </
	TokAttrName  // attribute name (incl. ogon: directives)
	TokAttrValue // quoted attribute value
	TokBlockOpen // <go> <template> <style> <script>
	TokBlockEnd  // closing tag of a top-level block
)

// Token is one lexer output. Pos is byte offset; Line/Col are 1-based
// for diagnostics (UI-081).
type Token struct {
	Kind  TokenKind
	Value string
	Pos   int
	Line  int
	Col   int
}

// Lexer scans a `.ogon` source string.
type Lexer struct {
	src  string
	pos  int
	line int
	col  int
	out  []Token
}

// NewLexer constructs a fresh lexer.
func NewLexer(src string) *Lexer {
	return &Lexer{src: src, line: 1, col: 1}
}

// Tokens returns the lexed token stream (EOF included).
func (l *Lexer) Tokens() []Token { return l.out }

// Lex runs the lexer and returns the token stream.
func (l *Lexer) Lex() ([]Token, error) {
	// A `.ogon` file is one of:
	//   <go>...</go><template>...</template><style>...</style>[<script>...</script>]
	// in any order, each optional, with whitespace between.
	for l.pos < len(l.src) {
		l.skipSpace()
		if l.pos >= len(l.src) {
			break
		}
		if !l.consumeIf('<') {
			break
		}
		// We're at the start of a tag — read its name.
		name, nameLine, nameCol := l.readName()
		if name == "" {
			return nil, l.errf(nameLine, nameCol, "expected tag name after '<'")
		}
		// Dispatch on block type.
		switch strings.ToLower(name) {
		case "go", "template", "style", "script":
			if err := l.lexBlockBody(name); err != nil {
				return nil, err
			}
		default:
			// Not a recognised top-level block; treat the rest as a
			// raw template (useful for page fragments).
			if err := l.lexTemplateTag(name, nameLine, nameCol); err != nil {
				return nil, err
			}
		}
	}
	l.emit(TokEOF, "")
	return l.out, nil
}

// lexBlockBody consumes a top-level block: name="go|template|style|script".
// It scans until the matching </name> close tag. The body is emitted
// verbatim as TokText; for <template> the body is re-tokenised into
// element tags. We keep one shared lexer primitive for simplicity.
func (l *Lexer) lexBlockBody(name string) error {
	openLine, openCol := l.line, l.col
	// Skip attributes (e.g. <script lang="ts">).
	for {
		l.skipSpace()
		if l.pos >= len(l.src) {
			return l.errf(openLine, openCol, "unterminated <"+name+"> block")
		}
		c := l.src[l.pos]
		l.advance()
		if c == '>' {
			break
		}
	}
	closeTag := "</" + name
	body, end, ok := l.scanRawUntil(closeTag)
	if !ok {
		return l.errf(openLine, openCol, "missing </"+name+"> close tag")
	}
	// Emit synthetic tokens for the block header so the parser can
	// distinguish block types.
	l.emitAt(TokBlockOpen, name, openLine, openCol)
	// Specialise: <template> is parsed into element tokens; others
	// remain a single TokText body.
	if name == "template" {
		// Re-lex the body as a tag stream.
		sub := NewLexer(body)
		// Reset line/col to start at 1:1 for the body fragment so
		// diagnostics point inside the template.
		sub.line, sub.col = 1, 1
		subTokens, err := sub.lexTemplate()
		if err != nil {
			return err
		}
		// Drop the sub-lexer's TokEOF so the outer parser sees a
		// contiguous stream (the outer Lex loop appends its own).
		for _, t := range subTokens {
			if t.Kind != TokEOF {
				l.out = append(l.out, t)
			}
		}
	} else {
		l.emit(TokText, body)
	}
	l.emitAt(TokBlockEnd, name, l.lineOf(end), l.colOf(end))
	// Advance past "</name>".
	l.pos = end + len(closeTag)
	for l.pos < len(l.src) && l.src[l.pos] != '>' {
		l.pos++
	}
	if l.pos < len(l.src) {
		l.pos++
	}
	return nil
}

// lexTemplateTag is hit only when the file starts with an arbitrary
// element instead of a known block. Emit the open tag tokens and
// continue with the inner template lexer for the remainder.
func (l *Lexer) lexTemplateTag(name string, ln, cl int) error {
	l.emitAt(TokTagOpen, "", ln, cl)
	l.emitAt(TokTagName, name, ln, cl)
	// Lex attributes (and any further tags) from the current position
	// so the parser sees a well-formed open element.
	if err := l.lexAttrs(); err != nil {
		return err
	}
	toks, err := l.lexTemplate()
	if err != nil {
		return err
	}
	// lexTemplate emits an EOF token; drop it because the outer
	// Lex loop adds its own.
	for _, t := range toks {
		if t.Kind != TokEOF {
			l.out = append(l.out, t)
		}
	}
	return nil
}

// lexTemplate is the inner template lexer. It produces open/close,
// attribute, and text tokens until EOF.
func (l *Lexer) lexTemplate() ([]Token, error) {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '<' {
			// Could be <tag, </tag, or <!-- comment -->.
			if strings.HasPrefix(l.src[l.pos:], "<!--") {
				l.skipComment()
				continue
			}
			if strings.HasPrefix(l.src[l.pos:], "</") {
				l.advance()
				l.advance()
				name, ln, cl := l.readName()
				l.skipSpace()
				if l.pos < len(l.src) && l.src[l.pos] == '>' {
					l.advance()
				}
				l.emitAt(TokTagClose, name, ln, cl)
				continue
			}
			ln, cl := l.line, l.col
			l.advance()
			name, _, _ := l.readName()
			l.emitAt(TokTagOpen, "", ln, cl)
			l.emitAt(TokTagName, name, ln, cl)
			// Parse attributes until we see > or />
			if err := l.lexAttrs(); err != nil {
				return nil, err
			}
			continue
		}
		// Read a text run up to the next '<'.
		start := l.pos
		for l.pos < len(l.src) && l.src[l.pos] != '<' {
			l.advance()
		}
		if l.pos > start {
			l.emit(TokText, l.src[start:l.pos])
		}
	}
	l.emitAt(TokEOF, "", l.line, l.col)
	return l.out, nil
}

// lexAttrs reads attributes (and directives) after an opening tag
// name until the closing '>' or self-close '/>'.
func (l *Lexer) lexAttrs() error {
	for {
		l.skipSpace()
		if l.pos >= len(l.src) {
			return l.errf(l.line, l.col, "unterminated tag (missing '>')")
		}
		c := l.src[l.pos]
		if c == '>' {
			l.advance()
			return nil
		}
		if c == '/' {
			l.advance()
			if l.pos < len(l.src) && l.src[l.pos] == '>' {
				l.advance()
				l.emit(TokTagSelf, "/>")
				return nil
			}
			continue
		}
		// Attribute name. Names may include ':' (for directives such
		// as ogon:submit, ogon:class:active).
		name, ln, cl := l.readAttrName()
		if name == "" {
			return l.errf(ln, cl, "expected attribute name")
		}
		l.emitAt(TokAttrName, name, ln, cl)
		// Optional value.
		l.skipSpace()
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.advance()
			l.skipSpace()
			val, err := l.readQuoted()
			if err != nil {
				return err
			}
			l.emit(TokAttrValue, val)
		}
	}
}

// readQuoted reads a quoted attribute value. Single or double quotes
// accepted; unquoted values are read up to the next whitespace or '>'.
func (l *Lexer) readQuoted() (string, error) {
	if l.pos >= len(l.src) {
		return "", l.errf(l.line, l.col, "expected attribute value")
	}
	q := l.src[l.pos]
	if q != '"' && q != '\'' {
		// Unquoted value.
		start := l.pos
		for l.pos < len(l.src) {
			c := l.src[l.pos]
			if c == ' ' || c == '\t' || c == '\n' || c == '>' || c == '/' {
				break
			}
			l.advance()
		}
		return l.src[start:l.pos], nil
	}
	l.advance()
	start := l.pos
	for l.pos < len(l.src) && l.src[l.pos] != q {
		l.advance()
	}
	if l.pos >= len(l.src) {
		return "", l.errf(l.line, l.col, "unterminated attribute value")
	}
	val := l.src[start:l.pos]
	l.advance() // closing quote
	return val, nil
}

// readName reads an identifier (letters, digits, hyphen).
func (l *Lexer) readName() (string, int, int) {
	ln, cl := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) {
		r, sz := utf8.DecodeRuneInString(l.src[l.pos:])
		if !(isLetter(r) || isDigit(r) || r == '-' || r == '_') {
			break
		}
		l.pos += sz
		l.col++
	}
	return l.src[start:l.pos], ln, cl
}

// readAttrName reads an attribute name including ':' (directives).
func (l *Lexer) readAttrName() (string, int, int) {
	ln, cl := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) {
		r, sz := utf8.DecodeRuneInString(l.src[l.pos:])
		if !(isLetter(r) || isDigit(r) || r == '-' || r == '_' || r == ':') {
			break
		}
		l.pos += sz
		l.col++
	}
	return l.src[start:l.pos], ln, cl
}

// scanRawUntil returns the body between the current position and the
// first occurrence of `close`, plus the byte offset of the close.
func (l *Lexer) scanRawUntil(close string) (string, int, bool) {
	idx := strings.Index(l.src[l.pos:], close)
	if idx < 0 {
		return "", 0, false
	}
	body := l.src[l.pos : l.pos+idx]
	end := l.pos + idx
	// Advance line/col counters across the body so subsequent
	// diagnostics land in the right place.
	for _, c := range body {
		if c == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
	}
	l.pos = end
	return body, end, true
}

// skipComment skips an HTML comment `<!-- ... -->`.
func (l *Lexer) skipComment() {
	idx := strings.Index(l.src[l.pos:], "-->")
	if idx < 0 {
		// Unterminated — consume to EOF.
		for l.pos < len(l.src) {
			l.advance()
		}
		return
	}
	for i := 0; i < idx+len("-->"); i++ {
		l.advance()
	}
}

func (l *Lexer) skipSpace() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		l.advance()
	}
}

func (l *Lexer) consumeIf(want byte) bool {
	if l.pos >= len(l.src) || l.src[l.pos] != want {
		return false
	}
	l.advance()
	return true
}

func (l *Lexer) advance() {
	if l.pos >= len(l.src) {
		return
	}
	if l.src[l.pos] == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	l.pos++
}

func (l *Lexer) emit(k TokenKind, v string) {
	l.emitAt(k, v, l.line, l.col)
}

func (l *Lexer) emitAt(k TokenKind, v string, ln, cl int) {
	l.out = append(l.out, Token{Kind: k, Value: v, Pos: l.pos, Line: ln, Col: cl})
}

func (l *Lexer) errf(line, col int, format string, args ...any) error {
	return &LexError{Line: line, Col: col, Msg: sprintf(format, args...)}
}

// lineOf/colOf return the line/col that *would* apply at byte offset
// `pos`. We pre-compute them by scanning from the current position;
// accurate enough for diagnostics.
func (l *Lexer) lineOf(pos int) int {
	// Count newlines between current pos and target.
	if pos < l.pos {
		pos = l.pos
	}
	line := l.line
	for i := l.pos; i < pos && i < len(l.src); i++ {
		if l.src[i] == '\n' {
			line++
		}
	}
	return line
}

func (l *Lexer) colOf(pos int) int {
	if pos < l.pos {
		pos = l.pos
	}
	// Find last newline at or before pos.
	last := l.pos
	for i := pos - 1; i >= l.pos; i-- {
		if i < len(l.src) && l.src[i] == '\n' {
			last = i + 1
			break
		}
	}
	return pos - last + 1
}

// LexError carries a line:col diagnostic from the lexer.
type LexError struct {
	Line int
	Col  int
	Msg  string
}

func (e *LexError) Error() string {
	return "ogon/ui lexer " + itoa(e.Line) + ":" + itoa(e.Col) + ": " + e.Msg
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80
}
func isDigit(r rune) bool { return r >= '0' && r <= '9' }
