// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// AST node types for `.ogon` components (UI-001, UI-081).
//
// A `.ogon` file has up to four top-level blocks: <go>, <template>,
// <style>, <script lang="ts">. Each block becomes its own AST node,
// then a codegen pass turns each into Go, JS, or CSS output. The
// reactivity analysis pass walks the AST to build a primitive →
// derived → region → event dependency graph (UI-091/096).

package compiler

// Node is the common interface implemented by every AST node. Pos is
// the lexer position (line:col) used for diagnostics (UI-081).
type Node interface {
	nodeSpan() (line, col int)
}

// baseNode embeds a source position so every node gets one for free.
type baseNode struct {
	line int
	col  int
}

func (b baseNode) nodeSpan() (line, col int) { return b.line, b.col }

// File is the root AST node for a single `.ogon` source file.
type File struct {
	baseNode
	Name    string       // logical component name (e.g. "Chat")
	PkgName string       // Go package emitted from <go> block (default: "components")
	Go      *GoBlock     // optional <go> block
	Style   *StyleBlock  // optional <style> block
	Script  *ScriptBlock // optional <script lang="ts"> block
	Root    *Element     // root <template> element (may have children)
	Imports []string     // raw import declarations from <go>
	Props   []Field      // typed props declared in <go>
	State   []Field      // state fields declared in <go>
	Methods []Method     // handler methods declared in <go>
}

// Field represents a typed Go struct field used for Props/State.
type Field struct {
	Name string
	Type string
	Tag  string // struct tag, e.g. `json:"title"`
}

// Method represents a Go handler method declared in <go>.
type Method struct {
	Name    string
	Recv    string
	Params  []Field
	Returns []string
	Body    string // raw Go source
}

// GoBlock is the <go> block — typed Go code that lives server-side.
// It is the only block that mutates State, Shared, or runs Effects.
type GoBlock struct {
	baseNode
	Package  string
	Raw      string
	Imports  []string
	Props    []Field
	State    []Field
	Methods  []Method
	Mounts   []Method // Mount(ctx) handlers
	Unmounts []Method
}

// TemplateBlock (UI-003) holds the parsed template tree. Codegen
// splits it into typed render functions keyed by reactive region.
type TemplateBlock struct {
	baseNode
	Root *Element
}

// StyleBlock (UI-004) is the scoped <style> block. Codegen rewrites
// class names into hashed forms to scope them per-component.
type StyleBlock struct {
	baseNode
	Lang  string // "css" default; "scss"/"pcss" reserved
	Raw   string
	Scope string // hashed scope class (filled by codegen)
}

// ScriptBlock (UI-005/043) is the optional <script lang="ts">
// escape hatch. Esbuild bundles it; the typed Ogon.Call() bridge
// is wired into the generated runtime glue.
type ScriptBlock struct {
	baseNode
	Lang string // "ts" expected, "js" allowed
	Raw  string
}

// Directive is a compile-recognized attribute (UI-063/092). The
// directive parser maps `ogon:submit="HandleSend"` to a Binding
// whose Handler is "HandleSend" and Source is the State path or
// expression that flows into the event payload.
type Directive struct {
	baseNode
	Name    string // e.g. "submit", "model", "click", "if", "for"
	Suffix  string // e.g. "active" for ogon:class:active
	Value   string // raw attribute value
	Binding *Binding
}

// Binding captures the data-binding relationship between a directive
// and a state primitive / event handler.
type Binding struct {
	Handler string // for ogon:click, ogon:submit
	State   string // for ogon:model, ogon:class:
	Item    string // for ogon:for (iteration var name)
	Of      string // for ogon:for (collection state path)
	Key     string // for ogon:for keyed iteration
	Expr    string // for ogon:if, ogon:class:, ogon:loading
}

// Element is an HTML element in the template. It may carry regular
// attributes, directives, text content, and child Elements.
type Element struct {
	baseNode
	Tag        string
	Attrs      map[string]string
	Order      []string // attribute insertion order (stable)
	Directives []*Directive
	Children   []Node
	Text       string // direct text content if no children
	SelfClose  bool
}

// Interp is a `{{ expr }}` interpolation inside element text.
type Interp struct {
	baseNode
	Expr string
}

// If is the structural directive `ogon:if="expr"` rendered as a
// reactive region that conditionally emits its subtree.
type If struct {
	baseNode
	Expr     string
	Children []Node
}

// For is the keyed `ogon:for="item in State.Items"` structural
// directive (UI-022). Keyed iteration enables list-diff patching.
type For struct {
	baseNode
	Item string
	Of   string
	Key  string // path to the per-item key field
	Body []Node
}

// PosOf returns the line:col span of any AST node as a diagnostic
// Where field (UI-081).
func PosOf(n Node) string {
	if n == nil {
		return ""
	}
	l, c := n.nodeSpan()
	return formatPos(l, c)
}

func formatPos(line, col int) string {
	if line == 0 {
		return ""
	}
	if col == 0 {
		col = 1
	}
	// Render like other Go toolchains: "file.ogon:line:col".
	return itoa(line) + ":" + itoa(col)
}

// itoa is a tiny dependency-free int→string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
