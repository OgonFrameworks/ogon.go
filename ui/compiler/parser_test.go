// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package compiler

import (
	"strings"
	"testing"
)

func TestParser_FullComponent(t *testing.T) {
	src := `<go>
type Props struct { Title string }
type State struct { Count int }
func (c *Chat) HandleSend(p map[string]any) error { return nil }
</go>
<template>
<div ogon:click="HandleSend"><h1>{{ Title }}</h1></div>
</template>
<style>.btn{color:red}</style>`
	f, err := ParseFile("Chat", "components", src)
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}
	if f.Go == nil {
		t.Fatal("expected Go block")
	}
	if len(f.Go.Props) == 0 {
		t.Errorf("expected at least one prop; got %v", f.Go.Props)
	}
	if len(f.Go.State) == 0 {
		t.Errorf("expected at least one state field; got %v", f.Go.State)
	}
	if f.Root == nil {
		t.Fatal("expected root element")
	}
	if f.Root.Tag != "div" {
		t.Errorf("expected root div; got %s", f.Root.Tag)
	}
	if f.Style == nil || !strings.Contains(f.Style.Raw, "color") {
		t.Errorf("expected style block with color rule")
	}
}

func TestParser_TopLevelFragment(t *testing.T) {
	// `.ogon` pages may start with an arbitrary element instead of
	// one of the four canonical blocks (UI-009 page fragments).
	src := `<weird>x</weird>`
	f, err := ParseFile("X", "components", src)
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}
	if f.Root == nil || f.Root.Tag != "weird" {
		t.Errorf("expected root <weird>; got %+v", f.Root)
	}
}

func TestParser_InterpolationExtracted(t *testing.T) {
	src := `<template><div>Hello {{ Title }}</div></template>`
	f, err := ParseFile("X", "components", src)
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}
	var found bool
	walkInterp(f.Root, func(ip *Interp) {
		if ip.Expr == "Title" {
			found = true
		}
	})
	if !found {
		t.Fatalf("expected interpolation node with expr=Title; children=%v", f.Root.Children)
	}
}

func TestParser_DirectiveBinding(t *testing.T) {
	src := `<template><input ogon:model="State.Input" /></template>`
	f, err := ParseFile("X", "components", src)
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}
	if len(f.Root.Directives) == 0 {
		t.Fatalf("expected at least one directive")
	}
	d := f.Root.Directives[0]
	if d.Name != "model" {
		t.Errorf("expected directive 'model'; got %s", d.Name)
	}
	if d.Binding == nil || d.Binding.State != "State.Input" {
		t.Errorf("expected State.Input binding; got %+v", d.Binding)
	}
}

func TestParser_KeyedFor(t *testing.T) {
	src := `<template><li ogon:for="item in State.Items">x</li></template>`
	f, err := ParseFile("X", "components", src)
	if err != nil {
		t.Fatalf("parser error: %v", err)
	}
	d := f.Root.Directives[0]
	if d.Binding.Item != "item" || d.Binding.Of != "State.Items" {
		t.Errorf("expected item/State.Items; got %+v", d.Binding)
	}
	if d.Binding.Key == "" {
		t.Errorf("expected default key path")
	}
}

func TestParser_LineColDiagnostics(t *testing.T) {
	src := `<template><div ogon:xyz="bad"></div></template>`
	_, err := ParseFile("X", "components", src)
	if err == nil {
		t.Fatal("expected unknown-directive error")
	}
	if !strings.Contains(err.Error(), "ogon:xyz") {
		t.Errorf("expected error to mention directive name; got %v", err)
	}
}

func walkInterp(n Node, fn func(*Interp)) {
	if n == nil {
		return
	}
	if el, ok := n.(*Element); ok {
		for _, c := range el.Children {
			walkInterp(c, fn)
		}
		return
	}
	if ip, ok := n.(*Interp); ok {
		fn(ip)
	}
}
