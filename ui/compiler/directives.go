// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Directive parser for `.ogon` templates (UI-063/092). The compiler
// recognises a fixed set of ogon:* directives; unknown ones surface
// a UI-082 diagnostic with the directive name and source position.

package compiler

import (
	"strings"
)

// knownDirectives is the closed set of compile-recognised directives.
// Adding a directive here is the only way to extend the language —
// the parser is strict so that typos don't silently fall back to
// runtime reflection (UI-082).
var knownDirectives = map[string]directiveSpec{
	"submit":     {MinArgs: 1, MaxArgs: 1, HasHandler: true},
	"model":      {MinArgs: 1, MaxArgs: 1, HasState: true},
	"click":      {MinArgs: 1, MaxArgs: 1, HasHandler: true},
	"if":         {MinArgs: 1, MaxArgs: 1, HasExpr: true},
	"for":        {MinArgs: 1, MaxArgs: 1, HasExpr: true},
	"class":      {MinArgs: 1, MaxArgs: 1, HasSuffix: true, HasExpr: true},
	"loading":    {MinArgs: 1, MaxArgs: 1, HasExpr: true},
	"error":      {MinArgs: 1, MaxArgs: 1, HasExpr: true},
	"on":         {MinArgs: 1, MaxArgs: 2, HasHandler: true},
	"key":        {MinArgs: 1, MaxArgs: 1, HasExpr: true},
	"bind":       {MinArgs: 1, MaxArgs: 1, HasState: true},
	"show":       {MinArgs: 1, MaxArgs: 1, HasExpr: true},
	"transition": {MinArgs: 1, MaxArgs: 1, HasExpr: true},
	"optimistic": {MinArgs: 1, MaxArgs: 1, HasExpr: true},
}

type directiveSpec struct {
	MinArgs, MaxArgs int
	HasHandler       bool
	HasState         bool
	HasExpr          bool
	HasSuffix        bool
}

// parseDirective consumes an ogon:* attribute token and emits a
// Directive AST node. The value token (if present) follows the
// attribute name token; we read it directly from the token stream.
func (p *Parser) parseDirective(attr Token) (*Directive, error) {
	// attr.Value is e.g. "ogon:submit" or "ogon:class:active".
	body := strings.TrimPrefix(attr.Value, "ogon:")
	parts := strings.SplitN(body, ":", 2)
	name := parts[0]
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}
	// Unknown directive → UI-082 error with line:col.
	if _, ok := knownDirectives[name]; !ok {
		return nil, p.errorf(attr, "unknown directive ogon:%s (UI-082)", name)
	}
	spec := knownDirectives[name]
	if spec.HasSuffix && suffix == "" {
		return nil, p.errorf(attr, "directive ogon:%s requires a suffix (e.g. ogon:%s:active)", name, name)
	}
	if !spec.HasSuffix && suffix != "" {
		return nil, p.errorf(attr, "directive ogon:%s does not accept a suffix", name)
	}
	val := ""
	if p.peek().Kind == TokAttrValue {
		val = p.advance().Value
	}
	d := &Directive{
		baseNode: baseNode{line: attr.Line, col: attr.Col},
		Name:     name,
		Suffix:   suffix,
		Value:    val,
		Binding:  &Binding{},
	}
	// Populate binding fields based on directive spec.
	switch {
	case spec.HasHandler:
		d.Binding.Handler = val
	case spec.HasState:
		d.Binding.State = val
	case spec.HasExpr:
		d.Binding.Expr = val
	}
	if name == "for" {
		// ogon:for="item in State.Items" — split into Item/Of/Key.
		item, of, key := parseForExpr(val)
		d.Binding.Item = item
		d.Binding.Of = of
		d.Binding.Key = key
	}
	return d, nil
}

// parseForExpr splits "item in State.Items" into (item, of, key).
// Key defaults to "item.ID" when not provided.
func parseForExpr(val string) (item, of, key string) {
	idx := strings.Index(val, " in ")
	if idx < 0 {
		return "", val, ""
	}
	item = strings.TrimSpace(val[:idx])
	rest := strings.TrimSpace(val[idx+4:])
	of = rest
	// Optional `key item.ID` suffix.
	if kidx := strings.Index(rest, " key "); kidx >= 0 {
		of = strings.TrimSpace(rest[:kidx])
		key = strings.TrimSpace(rest[kidx+5:])
	} else {
		key = item + ".ID" // default to a struct ID field
	}
	return
}

// ParseDirectiveStatic is the package-level entry point used by tests
// and the LSP (UI-086). It accepts the raw directive name + value.
func ParseDirectiveStatic(name, suffix, value string) (*Directive, error) {
	spec, ok := knownDirectives[name]
	if !ok {
		return nil, &ParseError{Line: 1, Col: 1, Msg: "unknown directive ogon:" + name}
	}
	// Enforce suffix rules (UI-082 consistent diagnostics).
	if spec.HasSuffix && suffix == "" {
		return nil, &ParseError{Line: 1, Col: 1, Msg: "directive ogon:" + name + " requires a suffix"}
	}
	if !spec.HasSuffix && suffix != "" {
		return nil, &ParseError{Line: 1, Col: 1, Msg: "directive ogon:" + name + " does not accept a suffix"}
	}
	d := &Directive{Name: name, Suffix: suffix, Value: value, Binding: &Binding{}}
	switch {
	case spec.HasHandler:
		d.Binding.Handler = value
	case spec.HasState:
		d.Binding.State = value
	case spec.HasExpr:
		d.Binding.Expr = value
	}
	if name == "for" {
		item, of, key := parseForExpr(value)
		d.Binding.Item = item
		d.Binding.Of = of
		d.Binding.Key = key
	}
	return d, nil
}

// ParseGoBody parses a `<go>` block body into props/state/methods.
//
// The body is free-form Go source; we extract struct-field
// declarations, imports, and method declarations through a
// line-based scan. This deliberately avoids a full Go parser to
// keep the compiler dependency-light — full type-checking is left
// to `go vet`/`go build` after codegen.
func ParseGoBody(raw string) (props, state []Field, methods []Method, imports []string, mounts, unmounts []Method) {
	lines := strings.Split(raw, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "import\t"):
			imports = append(imports, parseImports(lines, &i)...)
		case strings.HasPrefix(line, "type Props struct"):
			props = parseStructFields(lines, &i)
		case strings.HasPrefix(line, "type State struct"):
			state = parseStructFields(lines, &i)
		case strings.HasPrefix(line, "func "):
			m := parseMethod(lines, &i)
			if strings.HasPrefix(m.Name, "Mount") {
				mounts = append(mounts, m)
			} else if strings.HasPrefix(m.Name, "Unmount") {
				unmounts = append(unmounts, m)
			} else {
				methods = append(methods, m)
			}
		}
	}
	return
}

// parseImports reads an import block (single or grouped).
func parseImports(lines []string, i *int) []string {
	line := strings.TrimSpace(lines[*i])
	// Single-line import "pkg"
	if strings.HasPrefix(line, `import "`) {
		return []string{strings.Trim(strings.TrimPrefix(line, "import "), `"`)}
	}
	out := []string{}
	// Block: import (\n "a"\n "b"\n)
	if strings.Contains(line, "(") {
		for *i < len(lines) {
			l := strings.TrimSpace(lines[*i])
			if l == ")" {
				break
			}
			if strings.HasPrefix(l, `"`) {
				out = append(out, strings.Trim(l, `"`))
			}
			*i++
		}
	}
	return out
}

// parseStructFields reads `type Foo struct { ... }` body fields.
// Supports both multi-line and single-line forms.
func parseStructFields(lines []string, i *int) []Field {
	out := []Field{}
	line := strings.TrimSpace(lines[*i])
	// Detect single-line form: `type X struct { Field Type }`.
	if openBrace := strings.Index(line, "{"); openBrace >= 0 {
		body := strings.TrimSpace(line[openBrace+1:])
		if closeBrace := strings.LastIndex(body, "}"); closeBrace >= 0 {
			body = strings.TrimSpace(body[:closeBrace])
			out = append(out, parseInlineFields(body)...)
			return out
		}
	}
	// Multi-line: subsequent lines until `}`.
	*i++
	for *i < len(lines) {
		l := strings.TrimSpace(lines[*i])
		if l == "}" {
			break
		}
		if l == "" {
			*i++
			continue
		}
		// First token is field name; rest is type (and tags).
		parts := strings.SplitN(l, " ", 2)
		if len(parts) == 2 {
			out = append(out, Field{Name: parts[0], Type: strings.TrimSpace(parts[1])})
		}
		*i++
	}
	return out
}

// parseInlineFields splits a single-line struct body like
// `Title string Count int` into Field values.
func parseInlineFields(body string) []Field {
	out := []Field{}
	tokens := strings.Fields(body)
	for i := 0; i+1 < len(tokens); i += 2 {
		out = append(out, Field{Name: tokens[i], Type: tokens[i+1]})
	}
	return out
}

// parseMethod reads a `func (recv Type) Method(...) ...` body.
func parseMethod(lines []string, i *int) Method {
	header := strings.TrimSpace(lines[*i])
	body := strings.TrimPrefix(header, "func ")
	body = strings.TrimSpace(body)
	// If the method has a receiver, skip past the receiver group.
	if strings.HasPrefix(body, "(") {
		depth := 0
		closeIdx := -1
		for j, c := range body {
			if c == '(' {
				depth++
			} else if c == ')' {
				depth--
				if depth == 0 {
					closeIdx = j
					break
				}
			}
		}
		if closeIdx >= 0 {
			body = strings.TrimSpace(body[closeIdx+1:])
		}
	}
	// Name is the identifier up to the next '(' (the param list).
	name := body
	if parenIdx := strings.Index(body, "("); parenIdx >= 0 {
		name = strings.TrimSpace(body[:parenIdx])
	}
	// Collect body until the closing brace.
	method := Method{Name: name, Body: header + "\n"}
	*i++
	depth := strings.Count(header, "{") - strings.Count(header, "}")
	for *i < len(lines) && depth > 0 {
		method.Body += lines[*i] + "\n"
		depth += strings.Count(lines[*i], "{") - strings.Count(lines[*i], "}")
		*i++
	}
	return method
}
