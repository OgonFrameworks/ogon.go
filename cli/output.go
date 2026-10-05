// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Output contract: stable JSON envelope + human table/tree with color when
// TTY. Parity rule (DX-025): JSON must carry 100% of the information the
// human view carries. Enforced by construction — both views render from the
// same typed Data value.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

// Envelope is the stable JSON output contract. See PROMPT.md Part III.2.
//
// Field order is fixed and is part of the public contract — consumers may
// parse with streaming decoders that rely on this order. Do not reorder.
type Envelope struct {
	Command     string      `json:"command"`
	Status      string      `json:"status"`
	Data        any         `json:"data,omitempty"`
	Diagnostics []diag.Diag `json:"diagnostics,omitempty"`
}

// CLI carries the resolved global flags and IO sinks for a single run. A
// fresh CLI is constructed per invocation; never share across runs.
type CLI struct {
	// IO sinks. stdout carries data (and JSON envelopes); stderr carries
	// diagnostics and progress. stdin is used only for interactive prompts.
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader

	// Resolved persistent flags.
	json    bool
	noColor bool
	yes     bool
	verbose bool
	quiet   bool
	version bool
	project string

	// Cached device checks. Computed once at construction.
	stdoutIsTTY bool
	stdinIsTTY  bool
}

// newCLI builds a CLI from the supplied IO sinks and runs the device probes.
func newCLI(stdout, stderr io.Writer, stdin io.Reader) *CLI {
	return &CLI{
		stdout:      stdout,
		stderr:      stderr,
		stdin:       stdin,
		stdoutIsTTY: isCharDevice(stdout),
		stdinIsTTY:  isCharReader(stdin),
	}
}

// ---- flag accessors (used by command handlers) ----

func (c *CLI) JSON() bool        { return c.json }
func (c *CLI) NoColor() bool     { return c.noColor }
func (c *CLI) Yes() bool         { return c.yes }
func (c *CLI) Verbose() bool     { return c.verbose }
func (c *CLI) Quiet() bool       { return c.quiet }
func (c *CLI) Project() string   { return c.project }
func (c *CLI) IsTTY() bool       { return c.stdoutIsTTY }
func (c *CLI) Stdout() io.Writer { return c.stdout }
func (c *CLI) Stderr() io.Writer { return c.stderr }

// DryRun reads the command-local --dry-run flag. Returns false if the flag
// is absent on the command (read-only commands do not define it).
func (c *CLI) DryRun(cmd *cobra.Command) bool {
	if cmd.Flags().Lookup("dry-run") == nil {
		return false
	}
	v, _ := cmd.Flags().GetBool("dry-run")
	return v
}

// ---- emission ----

// emit writes the result envelope in the chosen format. status is derived
// from the diagnostics: any SeverityError ⇒ "error", else "ok".
func (c *CLI) emit(cmd *cobra.Command, data any, diags []diag.Diag) {
	if c.json {
		c.writeJSON(cmd, data, diags)
	} else {
		c.writeHuman(cmd, data, diags)
	}
}

func (c *CLI) writeJSON(cmd *cobra.Command, data any, diags []diag.Diag) {
	c.writeEnvelope(cmd.CommandPath(), data, diags)
}

// writeEnvelope emits the JSON envelope to stdout with a stable field order
// and no HTML escaping. Trailing newline included. Used by both command
// handlers (via emit) and the root error renderer.
func (c *CLI) writeEnvelope(command string, data any, diags []diag.Diag) {
	env := Envelope{
		Command:     command,
		Status:      statusFor(diags),
		Data:        data,
		Diagnostics: diags,
	}
	enc := json.NewEncoder(c.stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(env) // Encode appends a trailing newline
}

func (c *CLI) writeHuman(cmd *cobra.Command, data any, diags []diag.Diag) {
	if data != nil {
		c.renderHuman(data)
	}
	// Diagnostics go to stderr so stdout stays parseable.
	for i := range diags {
		fmt.Fprintln(c.stderr, c.colorizeDiag(&diags[i]))
	}
}

// renderHuman dispatches on the data type to a human renderer. Unknown types
// fall back to indented JSON so no information is silently dropped.
func (c *CLI) renderHuman(data any) {
	switch v := data.(type) {
	case nil:
		// nothing
	case string:
		if v != "" {
			fmt.Fprintln(c.stdout, v)
		}
	case HumanRenderable:
		s := v.RenderHuman(c)
		if s != "" {
			fmt.Fprint(c.stdout, s)
		}
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			fmt.Fprintln(c.stdout, fmt.Sprintf("%v", v))
			return
		}
		fmt.Fprintln(c.stdout, string(b))
	}
}

// HumanRenderable is implemented by data types that carry a custom human
// view. The same value is also marshaled to JSON via its struct tags,
// guaranteeing DX-025 parity by construction.
type HumanRenderable interface {
	RenderHuman(c *CLI) string
}

// statusFor derives "ok"/"error" from diagnostics.
func statusFor(diags []diag.Diag) string {
	for i := range diags {
		if diags[i].Severity >= diag.SeverityError {
			return "error"
		}
	}
	return "ok"
}

// ---- tables ----

// Table is a simple text table. Implements HumanRenderable.
type Table struct {
	Title   string
	Headers []string
	Rows    [][]string
	// Empty is the message shown when Rows is empty (e.g. "no routes").
	Empty string
}

func (t Table) RenderHuman(c *CLI) string {
	var b strings.Builder
	if t.Title != "" {
		b.WriteString(c.bold(t.Title))
		b.WriteByte('\n')
	}
	if len(t.Rows) == 0 {
		if t.Empty != "" {
			b.WriteString(c.dim(t.Empty))
			b.WriteByte('\n')
		}
		return b.String()
	}
	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	if len(t.Headers) > 0 {
		h := make([]string, len(t.Headers))
		for i, hd := range t.Headers {
			h[i] = c.bold(hd)
		}
		fmt.Fprintln(tw, strings.Join(h, "\t"))
	}
	for _, row := range t.Rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
	return b.String()
}

// ---- key/value pairs ----

// KVList is an ordered key/value list. Use for inspect/explain output where
// stable ordering matters (maps would alphabetize).
type KVList struct {
	Title string
	Pairs []KV
}

// KV is a single key/value pair.
type KV struct {
	K string
	V string
}

func (l KVList) RenderHuman(c *CLI) string {
	var b strings.Builder
	if l.Title != "" {
		b.WriteString(c.bold(l.Title))
		b.WriteByte('\n')
	}
	pad := 0
	for _, p := range l.Pairs {
		if len(p.K) > pad {
			pad = len(p.K)
		}
	}
	for _, p := range l.Pairs {
		fmt.Fprintf(&b, "%-*s  %s\n", pad, p.K, p.V)
	}
	return b.String()
}

// MarshalJSON renders KVList as a JSON object. Field order follows slice
// order, which is stable; consumers should treat it as ordered.
func (l KVList) MarshalJSON() ([]byte, error) {
	type kvJSON struct {
		K string `json:"key"`
		V string `json:"value"`
	}
	out := struct {
		Title string   `json:"title,omitempty"`
		Pairs []kvJSON `json:"pairs"`
	}{Title: l.Title}
	for _, p := range l.Pairs {
		out.Pairs = append(out.Pairs, kvJSON{K: p.K, V: p.V})
	}
	return json.Marshal(out)
}

// ---- trees ----

// TreeNode is a node in a tree view.
type TreeNode struct {
	Name     string
	Detail   string
	Children []*TreeNode
}

// Tree is a titled tree. Implements HumanRenderable.
type Tree struct {
	Root *TreeNode
}

func (t Tree) RenderHuman(c *CLI) string {
	var b strings.Builder
	t.renderNode(&b, t.Root, "", true, c)
	return b.String()
}

func (t Tree) renderNode(b *strings.Builder, n *TreeNode, prefix string, last bool, c *CLI) {
	if n == nil {
		return
	}
	if prefix == "" {
		b.WriteString(c.bold(n.Name))
	} else {
		marker := "├── "
		if last {
			marker = "└── "
		}
		b.WriteString(prefix)
		b.WriteString(marker)
		b.WriteString(n.Name)
	}
	if n.Detail != "" {
		b.WriteString("  ")
		b.WriteString(c.dim(n.Detail))
	}
	b.WriteByte('\n')
	childPrefix := prefix
	if prefix != "" {
		if last {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}
	for i, ch := range n.Children {
		t.renderNode(b, ch, childPrefix, i == len(n.Children)-1, c)
	}
}

// ---- color ----

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

// useColor reports whether ANSI color codes should be emitted. Honors
// --no-color, the NO_COLOR env convention, and TTY detection.
func (c *CLI) useColor() bool {
	if c == nil {
		return false
	}
	if c.noColor {
		return false
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return c.stdoutIsTTY
}

func (c *CLI) color(code, s string) string {
	if !c.useColor() || s == "" {
		return s
	}
	return code + s + ansiReset
}

func (c *CLI) red(s string) string    { return c.color(ansiRed, s) }
func (c *CLI) green(s string) string  { return c.color(ansiGreen, s) }
func (c *CLI) yellow(s string) string { return c.color(ansiYellow, s) }
func (c *CLI) cyan(s string) string   { return c.color(ansiCyan, s) }
func (c *CLI) bold(s string) string   { return c.color(ansiBold, s) }
func (c *CLI) dim(s string) string    { return c.color(ansiDim, s) }

// colorizeDiag tints a formatted diagnostic by severity.
func (c *CLI) colorizeDiag(d *diag.Diag) string {
	s := d.FormatHuman()
	switch d.Severity {
	case diag.SeverityError:
		return c.red(s)
	case diag.SeverityWarning:
		return c.yellow(s)
	default:
		return c.dim(s)
	}
}

// ---- device detection ----

// isCharDevice reports whether w is a character device (proxy for TTY).
// Uses only stdlib so the binary has no x/term dependency.
func isCharDevice(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// isCharReader is the reader-side analog used by the noninteractive module.
func isCharReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
