// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// RBAC (SEC-023, SEC-025, SEC-026).
//
// Roles and permissions are defined declaratively; `ogon gen authz`
// will emit typed Go wrappers around RequirePermission so routes get
// compile-time-checked authorization.

package authz

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// Permission is a scoped capability like "billing:write".
type Permission string

// Role is a named bundle of permissions.
type Role string

// Policy maps roles → permissions and (optionally) resource-level
// grants. The default policy is loaded from config at boot.
type Policy struct {
	mu        sync.RWMutex
	rolePerms map[Role][]Permission
	// resource grants are optional; used by CheckResource (SEC-026).
	grants      map[grantKey]struct{}
	roleImplies map[Role][]Role // role → roles it transitively inherits
}

type grantKey struct {
	role       Role
	resource   string
	permission Permission
}

// NewPolicy returns an empty Policy.
func NewPolicy() *Policy {
	return &Policy{
		rolePerms:   map[Role][]Permission{},
		grants:      map[grantKey]struct{}{},
		roleImplies: map[Role][]Role{},
	}
}

// GrantRole assigns permissions to a role.
func (p *Policy) GrantRole(role Role, perms ...Permission) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rolePerms[role] = append(p.rolePerms[role], perms...)
}

// ImplantRole records that `role` inherits from `inherits`.
// Used to express role hierarchies ("admin" inherits "editor").
func (p *Policy) ImplantRole(role Role, inherits ...Role) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roleImplies[role] = append(p.roleImplies[role], inherits...)
}

// GrantResource assigns a resource-level permission (SEC-026).
func (p *Policy) GrantResource(role Role, resource string, perm Permission) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grants[grantKey{role, resource, perm}] = struct{}{}
}

// Subjects is the per-request identity bundle: user ID, tenant, roles,
// and any explicit resource grants. The HTTP layer populates this from
// the session/JWT.
type Subjects struct {
	UserID   string
	TenantID string
	Roles    []Role
	// ResourceGrants is an optional map of "resource:perm" → true,
	// filled by the loader when fine-grained grants are session-bound.
	ResourceGrants map[string]bool
}

// HasPermission checks whether any subject role grants perm,
// transitively following role hierarchy.
func (p *Policy) HasPermission(sub Subjects, perm Permission) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	seen := map[Role]bool{}
	var visit func(r Role) bool
	visit = func(r Role) bool {
		if seen[r] {
			return false
		}
		seen[r] = true
		for _, p2 := range p.rolePerms[r] {
			if p2 == perm {
				return true
			}
		}
		for _, child := range p.roleImplies[r] {
			if visit(child) {
				return true
			}
		}
		return false
	}
	for _, r := range sub.Roles {
		if visit(r) {
			return true
		}
	}
	return false
}

// CheckResource tests for a resource-level grant (SEC-026).
// Returns true if (a) any role has the resource grant, or (b) the
// subject has the grant via their ResourceGrants map.
func (p *Policy) CheckResource(sub Subjects, resource string, perm Permission) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, r := range sub.Roles {
		if _, ok := p.grants[grantKey{r, resource, perm}]; ok {
			return true
		}
	}
	if sub.ResourceGrants != nil {
		return sub.ResourceGrants[string(perm)+":"+resource]
	}
	return false
}

// RequirePermission wraps a handler, returning 403 when the subject
// lacks perm. The Subjects is fetched from the request context via
// SubjectFromContext (caller populates it from session/JWT).
func (p *Policy) RequirePermission(perm Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := SubjectFromContext(r.Context())
		if sub == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !p.HasPermission(*sub, perm) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePermissionFunc is the function-adapter form (http.HandlerFunc).
func (p *Policy) RequirePermissionFunc(perm Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := SubjectFromContext(r.Context())
		if sub == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !p.HasPermission(*sub, perm) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

type subjectsCtxKey struct{}

// WithSubject stores the Subjects in the request context. The session
// middleware should call this after authentication.
func WithSubject(ctx context.Context, sub Subjects) context.Context {
	return context.WithValue(ctx, subjectsCtxKey{}, &sub)
}

// SubjectFromContext returns the Subjects pointer or nil.
func SubjectFromContext(ctx context.Context) *Subjects {
	v, _ := ctx.Value(subjectsCtxKey{}).(*Subjects)
	return v
}

// ErrForbidden is the canonical authz failure (programmatic use).
var ErrForbidden = errors.New("authz: forbidden")

// Forbidden is the structured-diagnostic form used by API responses.
func Forbidden(perm Permission, sub Subjects) *diag.Diag {
	return &diag.Diag{
		Code:     "OGON-SEC-025",
		Severity: diag.SeverityError,
		Title:    "forbidden",
		What:     "subject lacks permission " + string(perm),
		Fix:      []string{"request role grant from administrator"},
	}
}
