// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Middleware chain builder. The chain is the contract: the order in which
// middlewares wrap the handler is observable via `ogon explain route`
// (PROMPT.md Part VI.3).

package http

import (
	"context"
	"net/http"
	"strings"
)

// Middleware wraps an http.Handler. OgonGo uses net/http's native signature
// so any stdlib middleware is interoperable. The router's per-request *Ctx
// is retrieved via CtxFromRequest.
type Middleware func(http.Handler) http.Handler

// Chain composes middlewares in order: the first element wraps the second,
// which wraps the third, etc. Chain returns a single http.Handler that runs
// middlewares[0] → middlewares[1] → ... → terminal.
//
// Hot-path invariant: the closure cost is one alloc per middleware at
// build time (amortized across all requests). Each request pays one extra
// stack frame per middleware hop; the budget is ≤ 200 ns per hop.
func Chain(middlewares []Middleware, terminal http.Handler) http.Handler {
	if terminal == nil {
		terminal = http.NotFoundHandler()
	}
	h := terminal
	// Walk in reverse so the first element is the outermost wrapper.
	for i := len(middlewares) - 1; i >= 0; i-- {
		if middlewares[i] == nil {
			continue
		}
		h = middlewares[i](h)
	}
	return h
}

// ChainHandler is a convenience that wraps a typed HandlerFunc with the
// supplied middleware chain. Used internally by the server for route-group
// middleware insertion.
func ChainHandler(middlewares []Middleware, h HandlerFunc) HandlerFunc {
	if len(middlewares) == 0 {
		return h
	}
	// Adapt: convert HandlerFunc → http.Handler, chain, then re-adapt back.
	httpH := HandlerToHTTP(h)
	chained := Chain(middlewares, httpH)
	return HTTPToHandler(chained)
}

// HandlerToHTTP adapts an OgonGo HandlerFunc to a stdlib http.Handler.
// The dispatcher retrieves the *Ctx from the request context (set by the
// server's dispatcher) and calls h.
func HandlerToHTTP(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := CtxFromRequest(r)
		if c == nil {
			// No Ctx in context: this is a stdlib handler being served
			// outside the dispatcher (e.g., via ogon.Handler escape hatch).
			// Construct a transient one and release.
			c = AcquireCtx(w, r, nil)
			defer ReleaseCtx(c)
		} else {
			// Re-bind w/r to the current values in case a middleware wrapped
			// them (e.g., gzip ResponseWriter).
			c.w = w
			c.r = r
		}
		_ = h(c)
	})
}

// HTTPToHandler adapts an http.Handler back to an OgonGo HandlerFunc.
// Used when composing stdlib handlers (httprouter, promhttp, etc.) into
// the typed chain.
func HTTPToHandler(h http.Handler) HandlerFunc {
	return func(c *Ctx) error {
		h.ServeHTTP(c.w, c.r)
		return nil
	}
}

// ctxKey is the unexported context key for *Ctx retrieval.
type ctxKey struct{}

// CtxFromRequest returns the *Ctx associated with the request, or nil if
// the request was not dispatched by the OgonGo server.
func CtxFromRequest(r *http.Request) *Ctx {
	if v := r.Context().Value(ctxKey{}); v != nil {
		if c, ok := v.(*Ctx); ok {
			return c
		}
	}
	return nil
}

// WithCtx stores c in r's context. Framework use; app code never calls this.
func WithCtx(r *http.Request, c *Ctx) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, c))
}

// InitCtxMiddleware is the implicit outermost middleware. It acquires a *Ctx
// from the pool, stores it in the request context for downstream middlewares
// and the dispatcher to retrieve, and releases it when the request completes.
//
// This middleware is ALWAYS first in the chain; it is added by NewServer and
// cannot be reordered or removed via ServerOptions.Middlewares.
func InitCtxMiddleware(router *Router) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := AcquireCtx(w, r, router)
			defer ReleaseCtx(c)
			r = WithCtx(r, c)
			next.ServeHTTP(w, r)
		})
	}
}

// RouterMatchMiddleware runs immediately after InitCtx. It performs the
// route lookup and populates c.route / c.params so downstream middlewares
// (CSRF, rate-limit, auth) can consult route metadata. On a miss, it writes
// the 404 / 405 response and short-circuits the chain.
//
// This middleware is ALWAYS second in the chain; added by NewServer.
func RouterMatchMiddleware(router *Router, codecs *CodecRegistry) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := CtxFromRequest(r)
			if c == nil {
				next.ServeHTTP(w, r)
				return
			}
			c.codec = codecs.Negotiate(r.Header.Get("Accept"))
			match, outcome, allow := router.Match(r.Method, r.URL.Path)
			switch outcome {
			case MatchNone:
				_ = c.Problem(NotFoundProblem(r.URL.Path))
				return
			case MatchMethodNotAllowed:
				if len(allow) > 0 {
					w.Header().Set("Allow", joinComma(allow))
				}
				_ = c.Problem(MethodNotAllowedProblem(r.Method, r.URL.Path, joinComma(allow)))
				return
			case MatchOK:
				c.route = match.Route
				c.params = match.Params
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MountMiddleware is a marker function used in explain output to render
// the chain order. Each middleware file calls this in init() so `ogon
// explain route` can enumerate the catalog without reflection.
type mountInfo struct {
	name        string
	defaultPos  int // 0-based position in the default chain
	description string
}

// registry of mounted middlewares for explain output.
var middlewareCatalog = []mountInfo{}

func registerMiddleware(name, description string, defaultPos int) {
	middlewareCatalog = append(middlewareCatalog, mountInfo{
		name:        name,
		description: description,
		defaultPos:  defaultPos,
	})
}

// ExplainChain renders the default middleware chain as text.
func ExplainChain() string {
	out := make([]string, 0, len(middlewareCatalog))
	for _, m := range middlewareCatalog {
		out = append(out, m.name+" — "+m.description)
	}
	return strings.Join(out, "\n")
}
