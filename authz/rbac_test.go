// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHasPermission(t *testing.T) {
	p := NewPolicy()
	p.GrantRole(Role("editor"), Permission("posts:write"))
	p.GrantRole(Role("admin"), Permission("billing:write"))
	p.ImplantRole(Role("admin"), Role("editor"))

	admin := Subjects{UserID: "u", TenantID: "t", Roles: []Role{Role("admin")}}
	editor := Subjects{UserID: "u", TenantID: "t", Roles: []Role{Role("editor")}}
	anon := Subjects{UserID: "u", TenantID: "t", Roles: nil}

	if !p.HasPermission(admin, Permission("posts:write")) {
		t.Fatal("admin should inherit editor: posts:write")
	}
	if !p.HasPermission(admin, Permission("billing:write")) {
		t.Fatal("admin should have billing:write")
	}
	if !p.HasPermission(editor, Permission("posts:write")) {
		t.Fatal("editor should have posts:write")
	}
	if p.HasPermission(editor, Permission("billing:write")) {
		t.Fatal("editor should NOT have billing:write")
	}
	if p.HasPermission(anon, Permission("anything")) {
		t.Fatal("anon should have no permissions")
	}
}

func TestRequirePermissionMiddleware(t *testing.T) {
	p := NewPolicy()
	p.GrantRole(Role("admin"), Permission("billing:write"))

	mw := p.RequirePermission(Permission("billing:write"), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// no subject → 401
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// admin → 200
	r2 := r.WithContext(WithSubject(context.Background(), Subjects{
		UserID: "u", TenantID: "t", Roles: []Role{Role("admin")},
	}))
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	// editor (no billing) → 403
	r3 := r.WithContext(WithSubject(context.Background(), Subjects{
		UserID: "u", TenantID: "t", Roles: []Role{Role("editor")},
	}))
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, r3)
	if w3.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w3.Code)
	}
}

func TestCheckResource(t *testing.T) {
	p := NewPolicy()
	p.GrantResource(Role("owner"), "doc:1", Permission("read"))
	sub := Subjects{UserID: "u", TenantID: "t", Roles: []Role{Role("owner")}}
	if !p.CheckResource(sub, "doc:1", Permission("read")) {
		t.Fatal("owner should read doc:1")
	}
	if p.CheckResource(sub, "doc:2", Permission("read")) {
		t.Fatal("owner should NOT read doc:2")
	}
}

func TestTenantGuard(t *testing.T) {
	g := NewTenantGuard()
	sub := Subjects{UserID: "u", TenantID: "t1", Roles: nil}
	if err := g.Check(sub, "t1"); err != nil {
		t.Fatalf("same tenant should pass: %v", err)
	}
	if err := g.Check(sub, "t2"); err == nil {
		t.Fatal("cross-tenant should fail")
	}
}
