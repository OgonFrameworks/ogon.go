// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package compiler

import (
	"strings"
	"testing"
)

func TestReactivity_DetectsStateAndLocal(t *testing.T) {
	src := `<go>
count := ogon.State(0)
open := ogon.Local(false)
</go>
<template><div>{{ count }}</div></template>`
	f, err := ParseFile("Chat", "components", src)
	if err != nil {
		t.Fatalf("parser: %v", err)
	}
	g, diags := AnalyzeReactivity(f)
	if g == nil {
		t.Fatal("expected graph")
	}
	if len(g.Primitives) < 2 {
		t.Fatalf("expected at least 2 primitives; got %d", len(g.Primitives))
	}
	kinds := map[string]bool{}
	for _, p := range g.Primitives {
		kinds[p.Kind.String()] = true
	}
	if !kinds["State"] {
		t.Errorf("expected State primitive; got %v", kinds)
	}
	if !kinds["Local"] {
		t.Errorf("expected Local primitive; got %v", kinds)
	}
	_ = diags // may be empty in this simple case
}

func TestReactivity_RegionsInheritReads(t *testing.T) {
	src := `<go>
title := ogon.State("hello")
</go>
<template><h1 ogon:click="Send">{{ title }}</h1></template>`
	f, err := ParseFile("Chat", "components", src)
	if err != nil {
		t.Fatalf("parser: %v", err)
	}
	g, _ := AnalyzeReactivity(f)
	if len(g.Regions) == 0 {
		t.Fatalf("expected regions; got 0")
	}
	r := g.Regions[0]
	if r.Handler != "Send" {
		t.Errorf("expected handler=Send; got %s", r.Handler)
	}
}

func TestReactivity_ExplainComponent(t *testing.T) {
	src := `<go>
count := ogon.State(0)
</go>
<template><div>{{ count }}</div></template>`
	f, _ := ParseFile("Chat", "components", src)
	g, _ := AnalyzeReactivity(f)
	out := ExplainComponent(g)
	if !strings.Contains(out, "Chat") {
		t.Errorf("expected component name; got %s", out)
	}
	if !strings.Contains(out, "State") {
		t.Errorf("expected State kind in output; got %s", out)
	}
}

func TestReactivity_ExplainJSON(t *testing.T) {
	src := `<go>
count := ogon.State(0)
</go>
<template><div>{{ count }}</div></template>`
	f, _ := ParseFile("Chat", "components", src)
	g, _ := AnalyzeReactivity(f)
	out := ExplainJSON(g)
	if !strings.Contains(out, `"component":"Chat"`) {
		t.Errorf("expected component field; got %s", out)
	}
	if !strings.Contains(out, `"primitives":[`) {
		t.Errorf("expected primitives array; got %s", out)
	}
}
