// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package compiler

import (
	"strings"
	"testing"
)

func TestDirective_StaticSubmit(t *testing.T) {
	d, err := ParseDirectiveStatic("submit", "", "HandleSend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Binding.Handler != "HandleSend" {
		t.Errorf("expected HandleSend; got %s", d.Binding.Handler)
	}
}

func TestDirective_StaticModel(t *testing.T) {
	d, err := ParseDirectiveStatic("model", "", "State.Input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Binding.State != "State.Input" {
		t.Errorf("expected State.Input; got %s", d.Binding.State)
	}
}

func TestDirective_ClassWithSuffix(t *testing.T) {
	d, err := ParseDirectiveStatic("class", "active", "State.On")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Suffix != "active" {
		t.Errorf("expected suffix=active; got %s", d.Suffix)
	}
}

func TestDirective_ForExpression(t *testing.T) {
	d, err := ParseDirectiveStatic("for", "", "item in State.Items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Binding.Item != "item" || d.Binding.Of != "State.Items" {
		t.Errorf("expected item in State.Items; got %+v", d.Binding)
	}
}

func TestDirective_UnknownFails(t *testing.T) {
	_, err := ParseDirectiveStatic("unknown", "", "")
	if err == nil {
		t.Fatal("expected unknown-directive error")
	}
}

func TestDirective_UnknownDirectiveError(t *testing.T) {
	e := NewUnknownDirectiveError("xyz", 5, 10)
	if e == nil {
		t.Fatal("expected non-nil error")
	}
	s := e.Error()
	if !strings.Contains(s, "OGON-UI-082") {
		t.Errorf("expected OGON-UI-082 prefix; got %s", s)
	}
	if !strings.Contains(s, "xyz") {
		t.Errorf("expected directive name; got %s", s)
	}
	if !strings.Contains(s, "5:10") {
		t.Errorf("expected line:col; got %s", s)
	}
}

func TestDirective_ClassWithoutSuffix(t *testing.T) {
	_, err := ParseDirectiveStatic("class", "", "State.On")
	if err == nil {
		t.Fatal("expected suffix-required error for ogon:class")
	}
}

func TestDirective_ClassWithBadSuffix(t *testing.T) {
	_, err := ParseDirectiveStatic("submit", "active", "HandleX")
	if err == nil {
		t.Fatal("expected suffix-not-accepted error for ogon:submit")
	}
}

func TestParseGoBody_SimpleField(t *testing.T) {
	raw := `type Props struct { Title string }`
	props, _, _, _, _, _ := ParseGoBody(raw)
	if len(props) != 1 || props[0].Name != "Title" || props[0].Type != "string" {
		t.Fatalf("unexpected props: %+v", props)
	}
}

func TestParseGoBody_Method(t *testing.T) {
	raw := `func (c *Chat) HandleSend(p map[string]any) error { return nil }`
	_, _, methods, _, _, _ := ParseGoBody(raw)
	if len(methods) != 1 {
		t.Fatalf("expected 1 method; got %+v", methods)
	}
	if methods[0].Name != "HandleSend" {
		t.Errorf("expected HandleSend; got %s", methods[0].Name)
	}
}
