// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tenant-isolation guard (SEC-027).
//
// Every request that touches tenant-scoped data MUST carry a verified
// tenant context. The guard enforces that:
//   1. The session's TenantID == resource TenantID.
//   2. Cross-tenant access attempts are logged to the audit chain
//      (SEC-028) and rejected with 403 (not 404 — leakage of resource
//      existence is a separate concern; we err on the side of fail-loud
//      for now and recommend the operator downgrade to 404 via the
//      audit sink if they want strict non-disclosure).

package authz

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// TenantGuard enforces that the request's subject tenant matches the
// resource tenant. It is used as both a middleware (HTTP) and a
// programmatic checker (for DI/RPC layers).
type TenantGuard struct {
	// denyMissingTenant controls behaviour when the subject or resource
	// has no tenant. Default true (single-tenant deployments flip this
	// to false via WithAllowMissing).
	denyMissing bool
}

// NewTenantGuard returns the production default (fail-closed).
func NewTenantGuard() *TenantGuard { return &TenantGuard{denyMissing: true} }

// WithAllowMissing returns a guard that allows requests where one
// side has no tenant (useful for single-tenant or platform-internal
// flows).
func (g *TenantGuard) WithAllowMissing() *TenantGuard {
	out := *g
	out.denyMissing = false
	return &out
}

// Check returns nil if subject and resource tenants match.
func (g *TenantGuard) Check(sub Subjects, resourceTenantID string) error {
	if sub.TenantID == "" && resourceTenantID == "" {
		if g.denyMissing {
			return diag.New("OGON-SEC-027", "tenant: missing", "both subject and resource lack tenant id")
		}
		return nil
	}
	if sub.TenantID == "" || resourceTenantID == "" {
		if g.denyMissing {
			return diag.New("OGON-SEC-027", "tenant: asymmetric", "one side missing tenant id")
		}
		return nil
	}
	if sub.TenantID != resourceTenantID {
		return diag.New("OGON-SEC-027", "tenant: mismatch",
			"subject tenant != resource tenant (cross-tenant attempt)")
	}
	return nil
}

// Middleware wraps next with a tenant guard that checks the subject
// against the tenant ID resolved from the request (resolver returns
// the resource's tenant). The resolver SHOULD come from the route
// (e.g. URL param like /orgs/{org}/...).
func (g *TenantGuard) Middleware(resolve func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := SubjectFromContext(r.Context())
		if sub == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		resourceTenant := resolve(r)
		if err := g.Check(*sub, resourceTenant); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ScopedContext carries the request's verified tenant ID deeper into
// the call stack (DI, jobs, etc.). Use TenantFromContext to read it.
type scopedCtxKey struct{}

// WithTenant stamps the verified tenant into the context. The value is
// stored as a string (not *string) so TenantFromContext's type
// assertion succeeds (P4-A discovery: previously WithTenant stored a
// string but TenantFromContext asserted to *string, which always
// failed and returned "" — silently breaking tenant propagation).
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, scopedCtxKey{}, tenantID)
}

// TenantFromContext returns the verified tenant or empty string.
func TenantFromContext(ctx context.Context) string {
	v, _ := ctx.Value(scopedCtxKey{}).(string)
	return v
}

// ErrTenantMismatch is the programmatic sentinel for cross-tenant.
var ErrTenantMismatch = errors.New("authz: tenant mismatch")

// ResolveTenantFromPath pulls the tenant from a path segment by name.
// e.g. resolveTenantFromPath("/orgs/{org}/...", "org")
func ResolveTenantFromPath(pattern, segmentName, requestPath string) string {
	// crude segment matcher; real routers use the framework's
	// path-parameter extraction.
	parts := strings.Split(pattern, "/")
	want := "{" + segmentName + "}"
	reqParts := strings.Split(requestPath, "/")
	if len(parts) != len(reqParts) {
		return ""
	}
	for i, p := range parts {
		if p == want {
			return reqParts[i]
		}
	}
	return ""
}
