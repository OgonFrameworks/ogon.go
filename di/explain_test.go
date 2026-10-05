// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for di/explain.go (DI-014/015): the human-readable graph render and
// the Graphviz DOT export.

package di

import (
	"bytes"
	"strings"
	"testing"
)

func TestExplainRendersProviders(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService"),
		makeProvider("OrderService", "*OrderService", "*UserService"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	var buf bytes.Buffer
	if err := Explain(g, &buf, DefaultExplainOptions()); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "UserService") {
		t.Errorf("missing UserService in output:\n%s", out)
	}
	if !strings.Contains(out, "OrderService") {
		t.Errorf("missing OrderService in output:\n%s", out)
	}
	if !strings.Contains(out, "singleton") {
		t.Errorf("missing lifetime annotation:\n%s", out)
	}
}

func TestExplainShowsExternalDeps(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService", "*record.Pool"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	var buf bytes.Buffer
	if err := Explain(g, &buf, DefaultExplainOptions()); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "external deps:") {
		t.Errorf("missing external deps section:\n%s", out)
	}
	if !strings.Contains(out, "Pool") {
		t.Errorf("missing Pool in external deps:\n%s", out)
	}
}

func TestExplainEmptyGraph(t *testing.T) {
	t.Parallel()
	g, err := NewGraph(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Explain(g, &buf, DefaultExplainOptions()); err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if !strings.Contains(buf.String(), "no providers") {
		t.Errorf("expected no providers message, got: %s", buf.String())
	}
}

func TestGraphvizRendersDigraph(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService"),
		makeProvider("OrderService", "*OrderService", "*UserService"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	var buf bytes.Buffer
	if err := Graphviz(g, &buf); err != nil {
		t.Fatalf("Graphviz: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "digraph di {") {
		t.Errorf("missing digraph header:\n%s", out)
	}
	if !strings.Contains(out, "->") {
		t.Errorf("missing edges:\n%s", out)
	}
	if !strings.Contains(out, "rankdir=LR") {
		t.Errorf("missing rankdir:\n%s", out)
	}
}

func TestGraphvizStable(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("B", "*B"),
		makeProvider("A", "*A"),
		makeProvider("C", "*C", "*A", "*B"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b1, b2 bytes.Buffer
	_ = Graphviz(g, &b1)
	_ = Graphviz(g, &b2)
	if !bytes.Equal(b1.Bytes(), b2.Bytes()) {
		t.Errorf("Graphviz output not stable")
	}
}

func TestExplainNilGraphErrors(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := Explain(nil, &buf, DefaultExplainOptions()); err == nil {
		t.Errorf("expected error for nil graph")
	}
	if err := Graphviz(nil, &buf); err == nil {
		t.Errorf("expected error for nil graph")
	}
}

func TestEscapeDot(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{`simple`, `simple`},
		{`with"quote`, `with\"quote`},
		{`with{brace}`, `with\{brace\}`},
		{`with|pipe`, `with\|pipe`},
		{`back\slash`, `back\\slash`},
	}
	for _, c := range cases {
		if got := escapeDot(c.in); got != c.want {
			t.Errorf("escapeDot(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
