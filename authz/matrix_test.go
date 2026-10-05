// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Authorization matrix test (SEC-023..026). Verifies the role×route
// authorization matrix — every (role, route) cell has the expected
// Allow/Deny decision. This is the canonical matrix test that should
// fail loudly if anyone widens a role's permissions by accident.

package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// matrixRole is the closed set of roles the policy knows about. Adding a
// new role requires adding rows here so the matrix stays audited.
var matrixRoles = []Role{
	Role("admin"),
	Role("editor"),
	Role("viewer"),
	Role("anonymous"),
}

// matrixRoutes is the (permission, route, method) tuple list. Each entry
// is one route the app exposes; the matrix asserts the role can/cannot
// access it.
type matrixRoute struct {
	perm   Permission
	route  string
	method string
}

var matrixRoutes = []matrixRoute{
	{Permission("billing:write"), "/api/billing/charge", http.MethodPost},
	{Permission("users:write"), "/api/users/:id", http.MethodPatch},
	{Permission("users:read"), "/api/users/:id", http.MethodGet},
	{Permission("posts:write"), "/api/posts", http.MethodPost},
	{Permission("posts:read"), "/api/posts", http.MethodGet},
	{Permission("admin:*"), "/api/admin/*", http.MethodGet},
}

// matrixPolicy returns the canonical policy used by the matrix test.
// Adding a permission grant here is a security-relevant change that the
// matrix test will flag.
func matrixPolicy() *Policy {
	p := NewPolicy()
	p.GrantRole(Role("admin"),
		Permission("billing:write"),
		Permission("users:write"),
		Permission("users:read"),
		Permission("posts:write"),
		Permission("posts:read"),
		Permission("admin:*"),
	)
	p.GrantRole(Role("editor"),
		Permission("posts:write"),
		Permission("posts:read"),
		Permission("users:read"),
	)
	p.GrantRole(Role("viewer"),
		Permission("posts:read"),
		Permission("users:read"),
	)
	// anonymous: no grants
	// Role hierarchy: admin inherits editor inherits viewer.
	p.ImplantRole(Role("admin"), Role("editor"))
	p.ImplantRole(Role("editor"), Role("viewer"))
	return p
}

// TestRoleRouteMatrix: the full role×route matrix. Each cell runs as a
// subtest so failures point at the specific (role, route) pair.
func TestRoleRouteMatrix(t *testing.T) {
	p := matrixPolicy()
	// expected[cell] = true (allow) / false (deny)
	expected := map[string]bool{}
	for _, role := range matrixRoles {
		for _, rt := range matrixRoutes {
			key := string(role) + "|" + string(rt.perm)
			switch role {
			case Role("admin"):
				expected[key] = true // admin has everything
			case Role("editor"):
				expected[key] = (rt.perm == Permission("posts:write") || rt.perm == Permission("posts:read") || rt.perm == Permission("users:read"))
			case Role("viewer"):
				expected[key] = (rt.perm == Permission("posts:read") || rt.perm == Permission("users:read"))
			case Role("anonymous"):
				expected[key] = false
			}
		}
	}
	for _, role := range matrixRoles {
		for _, rt := range matrixRoutes {
			role := role
			rt := rt
			t.Run(string(role)+"+"+rt.route+":"+rt.method, func(t *testing.T) {
				sub := Subjects{UserID: "u", TenantID: "t", Roles: []Role{role}}
				got := p.HasPermission(sub, rt.perm)
				key := string(role) + "|" + string(rt.perm)
				want := expected[key]
				if got != want {
					t.Errorf("role %s on %s: HasPermission(%s) = %v, want %v",
						role, rt.route, rt.perm, got, want)
				}
			})
		}
	}
}

// TestAdminCanAccessAdmin: admin must hit /api/admin/* (admin:* perm).
func TestAdminCanAccessAdmin(t *testing.T) {
	p := matrixPolicy()
	mw := p.RequirePermission(Permission("admin:*"), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	r = r.WithContext(WithSubject(context.Background(), Subjects{
		UserID: "u", TenantID: "t", Roles: []Role{Role("admin")},
	}))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin should access /api/admin/*: got %d", w.Code)
	}
}

// TestUserCannotAccessAdmin: non-admin (editor, viewer, anonymous) must
// NOT hit /api/admin/*.
func TestUserCannotAccessAdmin(t *testing.T) {
	p := matrixPolicy()
	mw := p.RequirePermission(Permission("admin:*"), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, role := range []Role{Role("editor"), Role("viewer"), Role("anonymous")} {
		role := role
		t.Run(string(role), func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
			r = r.WithContext(WithSubject(context.Background(), Subjects{
				UserID: "u", TenantID: "t", Roles: []Role{role},
			}))
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s should be denied /api/admin/*: got %d, want 403", role, w.Code)
			}
		})
	}
}

// TestAnonAlwaysDenied: anonymous subject (nil/empty Roles) must be
// denied every permission, including read.
func TestAnonAlwaysDenied(t *testing.T) {
	p := matrixPolicy()
	anon := Subjects{UserID: "", TenantID: "", Roles: nil}
	for _, rt := range matrixRoutes {
		rt := rt
		t.Run(string(rt.perm), func(t *testing.T) {
			if p.HasPermission(anon, rt.perm) {
				t.Errorf("anon must NOT have %s", rt.perm)
			}
		})
	}
}

// TestEditorBoundedToOwnResources: editor has posts:write (a permission
// grant) but a resource-level check on someone else's post must still
// deny unless the editor has a ResourceGrants entry. SEC-026.
func TestEditorBoundedToOwnResources(t *testing.T) {
	p := matrixPolicy()
	// editor has the role-level posts:write grant.
	sub := Subjects{UserID: "editor1", TenantID: "t", Roles: []Role{Role("editor")}}
	if !p.HasPermission(sub, Permission("posts:write")) {
		t.Fatal("editor should have posts:write")
	}
	// But CheckResource on doc:99 (not granted) should fail.
	if p.CheckResource(sub, "post:99", Permission("write")) {
		t.Fatal("editor should NOT have resource-level write on post:99")
	}
	// Grant the resource and confirm CheckResource succeeds.
	p.GrantResource(Role("editor"), "post:99", Permission("write"))
	if !p.CheckResource(sub, "post:99", Permission("write")) {
		t.Fatal("editor should have resource-level write on post:99 after grant")
	}
}
