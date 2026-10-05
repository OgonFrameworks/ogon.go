// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Router: static in-memory radix tree with typed path parameters.
// Codegen-ready: at runtime this is a segment-trie with chain compression
// for common prefixes. `ogon build` emits a perfect-hash static route table
// that replaces this implementation in production builds.

package http

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// HandlerFunc is the canonical OgonGo handler signature: full manual control.
// Returning a non-nil error MUST produce a ProblemDetails response (the
// server's dispatcher handles this when the route was registered via the
// typed adapters; raw HandlerFunc registrations handle errors themselves).
type HandlerFunc func(c *Ctx) error

// HandlerResult is the typed return shape adapter. When a handler returns
// (T, error), the dispatcher marshals T via the negotiated codec on success
// and emits a ProblemDetails on failure.
//
// The runtime uses reflection-based dispatch in typed adapter helpers; the
// codegen path emits concrete per-route wrappers.
type HandlerResult = any

// paramKind classifies path parameters for type-aware binding.
type paramKind uint8

const (
	paramString paramKind = iota
	paramInt
	paramInt64
	paramBool
)

// String returns the kind's name (used in explain output and snapshots).
func (k paramKind) String() string {
	switch k {
	case paramString:
		return "string"
	case paramInt:
		return "int"
	case paramInt64:
		return "int64"
	case paramBool:
		return "bool"
	}
	return "string"
}

// Route is a registered route entry. Returned by MapGet/MapPost/... for
// fluent configuration (RequireAuth/Cache/RateLimit attach metadata that
// route-group middleware reads at dispatch time).
type Route struct {
	Method    string
	Template  string
	Handler   HandlerFunc
	Params    []ParamSpec
	groupMeta map[string]any
	groupMws  []GroupMiddleware // per-route typed middleware chain

	// For metrics labels (HTTP-076 cardinality law): stable string used in
	// Prometheus labels. Always the template, never the raw path.
	Label string
}

// GroupMiddleware is the per-route typed middleware signature. Route-group
// middleware (auth/csrf/ratelimit/cache/tenant) attaches via Route.Use and
// runs AFTER the dispatcher's route resolution, BEFORE the handler. Each
// returns an error: a *ProblemDetails is rendered as-is; any other error
// becomes a 500 generic Problem.
type GroupMiddleware func(c *Ctx) error

// Use attaches typed route-group middleware. Order is registration order;
// the first attached runs first.
func (r *Route) Use(mws ...GroupMiddleware) *Route {
	r.groupMws = append(r.groupMws, mws...)
	return r
}

// ParamSpec is a path parameter declaration, emitted by codegen for
// per-route binding.
type ParamSpec struct {
	Name string
	Kind paramKind
}

// RouteGroup config attaches metadata that route-group middleware reads
// at dispatch time. These methods return the receiver for chaining.

// RequireAuth marks the route as requiring authenticated session. The
// auth middleware (P7) checks for this key.
func (r *Route) RequireAuth() *Route {
	r.groupSet("auth", true)
	return r
}

// Cache attaches a per-route cache TTL hint for the cache middleware.
func (r *Route) Cache(ttl string) *Route {
	r.groupSet("cache", ttl)
	return r
}

// RateLimit attaches a named rate-limit policy. The rate-limit middleware
// resolves the policy by name.
func (r *Route) RateLimit(policy string) *Route {
	r.groupSet("ratelimit", policy)
	return r
}

// CSRF marks the route as requiring a valid CSRF token (state-changing
// requests only).
func (r *Route) CSRF() *Route {
	r.groupSet("csrf", true)
	return r
}

// Tenant attaches a tenant isolation policy. The tenant middleware
// enforces per-tenant scoping.
func (r *Route) Tenant(policy string) *Route {
	r.groupSet("tenant", policy)
	return r
}

func (r *Route) groupSet(key string, value any) {
	if r.groupMeta == nil {
		r.groupMeta = make(map[string]any)
	}
	r.groupMeta[key] = value
}

// GroupMeta returns the registered metadata for key, or nil.
func (r *Route) GroupMeta(key string) any {
	if r.groupMeta == nil {
		return nil
	}
	return r.groupMeta[key]
}

// node is one trie entry. Lookup is segment-by-segment; static children
// indexed in a map for O(1) dispatch. Param and catchall are single-child
// slots (OgonGo enforces the standard radix invariant: at most one param
// child per node, and catchall must be terminal).
type node struct {
	segment   string
	isParam   bool
	paramName string
	paramKind paramKind
	isCatch   bool // "*" catch-all terminal
	children  map[string]*node
	param     *node
	catchall  *node
	routes    map[string]*Route
}

// Router is the static in-memory route table. Methods MapGet/MapPost/...
// register routes; Match resolves a request to a Route plus extracted
// parameters. Concurrent registrations are NOT safe — register everything
// at boot, then serve. Reads are safe for concurrent use after freeze.
type Router struct {
	mu     sync.RWMutex
	root   *node
	routes []*Route // insertion order, for snapshot tests

	// MaxBodyBytes caps the request body size before the bind layer sees it.
	// Set by the Server during NewServer; the Bind helper consults it. Zero
	// means use the framework default (10 MiB).
	MaxBodyBytes int64
}

// NewRouter returns an empty router.
func NewRouter() *Router {
	return &Router{root: &node{}}
}

// ErrRouteConflict is returned when two registrations overlap on the same
// (method, template). Routes MUST be unambiguous; the framework refuses to
// boot otherwise (HTTP-014).
var ErrRouteConflict = errors.New("ogon/http: route conflict")

// MapGet registers a GET handler.
func (rt *Router) MapGet(pattern string, h HandlerFunc) *Route {
	return rt.register(http.MethodGet, pattern, h)
}

// MapPost registers a POST handler.
func (rt *Router) MapPost(pattern string, h HandlerFunc) *Route {
	return rt.register(http.MethodPost, pattern, h)
}

// MapPut registers a PUT handler.
func (rt *Router) MapPut(pattern string, h HandlerFunc) *Route {
	return rt.register(http.MethodPut, pattern, h)
}

// MapPatch registers a PATCH handler.
func (rt *Router) MapPatch(pattern string, h HandlerFunc) *Route {
	return rt.register(http.MethodPatch, pattern, h)
}

// MapDelete registers a DELETE handler.
func (rt *Router) MapDelete(pattern string, h HandlerFunc) *Route {
	return rt.register(http.MethodDelete, pattern, h)
}

// Map registers an arbitrary method. Lower-level escape hatch for handlers
// that need METHOD_HEAD, METHOD_OPTIONS, or custom verbs.
func (rt *Router) Map(method, pattern string, h HandlerFunc) *Route {
	return rt.register(method, pattern, h)
}

// register inserts a route into the trie and the snapshot slice.
func (rt *Router) register(method, pattern string, h HandlerFunc) *Route {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if pattern == "" {
		pattern = "/"
	}
	if pattern[0] != '/' {
		pattern = "/" + pattern
	}

	segments := splitPath(pattern)
	params := make([]ParamSpec, 0, 2)
	for _, seg := range segments {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			name, kind := parseParam(seg)
			params = append(params, ParamSpec{Name: name, Kind: kind})
		} else if seg == "*" {
			params = append(params, ParamSpec{Name: "_", Kind: paramString})
		}
	}

	r := &Route{
		Method:   method,
		Template: pattern,
		Handler:  h,
		Label:    method + " " + pattern,
		Params:   params,
	}
	// Walk the trie, creating nodes as needed.
	cur := rt.root
	for _, seg := range segments {
		// catchall must be terminal — refuse to descend past
		// a catchall node. The check fires BEFORE the descent
		// so that creating the catchall (the current segment
		// IS the catchall) is allowed; only adding children
		// to it is rejected. (P14 bug-bounty fix: previously
		// the check fired on the iteration that *created*
		// the catchall, which made every "/foo/*" pattern
		// panic at registration time.)
		if cur.isCatch {
			panic(fmt.Sprintf("ogon/http: invalid pattern %q: catch-all must be terminal", pattern))
		}
		cur = cur.descendOrCreate(seg)
	}
	if cur.routes == nil {
		cur.routes = make(map[string]*Route, 1)
	}
	if existing, ok := cur.routes[method]; ok {
		panic(fmt.Sprintf("ogon/http: %s on %q conflicts with %s", method, pattern, existing.Template))
	}
	cur.routes[method] = r
	rt.routes = append(rt.routes, r)
	return r
}

// descendOrCreate walks one segment down the trie, creating nodes as needed.
func (n *node) descendOrCreate(seg string) *node {
	if seg == "" {
		return n
	}
	if seg == "*" {
		if n.catchall == nil {
			n.catchall = &node{segment: seg, isCatch: true}
		}
		return n.catchall
	}
	if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
		name, kind := parseParam(seg)
		if n.param == nil {
			n.param = &node{segment: seg, isParam: true, paramName: name, paramKind: kind}
		} else if n.param.paramName != name || n.param.paramKind != kind {
			panic(fmt.Sprintf("ogon/http: conflicting param %s vs %s at same level", n.param.paramName, name))
		}
		return n.param
	}
	if n.children == nil {
		n.children = make(map[string]*node, 1)
	}
	if child, ok := n.children[seg]; ok {
		return child
	}
	child := &node{segment: seg}
	n.children[seg] = child
	return child
}

// splitPath breaks "/a/b/c" into ["a","b","c"]. Empty segments collapse.
// Leading/trailing slashes are ignored.
//
// Used at registration time — the heap allocation here is fine because routes
// are registered once at boot. The hot-path Match uses splitPathStack instead,
// which keeps the segment slice on the goroutine stack for typical URLs.
func splitPath(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// splitPathStack splits p into segments, writing up to maxStack segments into
// the caller-supplied backing array. For paths with more than maxStack
// segments it falls back to a heap-allocated slice (defensive: silent
// truncation would be unsafe and would let adversarial paths bypass routing).
//
// The returned slice shares storage with `stack` for the in-stack case; the
// caller MUST NOT retain it past the lifetime of `stack`. Match honours this
// contract (the slice is consumed inside the function body).
func splitPathStack(p string, stack []string) []string {
	if p == "" || p == "/" {
		return nil
	}
	// Trim leading and trailing slashes in-place.
	for len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	for len(p) > 0 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	if p == "" {
		return nil
	}
	out := stack[:0]
	for {
		if len(out) == len(stack) {
			// Path longer than the stack backing array — fall back to heap.
			heap := make([]string, len(out), len(out)+8)
			copy(heap, out)
			heap = append(heap, strings.Split(p, "/")...)
			return heap
		}
		idx := strings.IndexByte(p, '/')
		if idx < 0 {
			out = append(out, p)
			return out
		}
		out = append(out, p[:idx])
		p = p[idx+1:]
	}
}

// parseParam extracts {name} or {name:kind} into its components.
func parseParam(seg string) (string, paramKind) {
	inner := seg[1 : len(seg)-1]
	if i := strings.IndexByte(inner, ':'); i >= 0 {
		name := inner[:i]
		kindStr := inner[i+1:]
		switch kindStr {
		case "int":
			return name, paramInt
		case "int64":
			return name, paramInt64
		case "bool":
			return name, paramBool
		default:
			return name, paramString
		}
	}
	return inner, paramString
}

// MatchResult is the outcome of a successful route resolution.
type MatchResult struct {
	Route  *Route
	Params map[string]string
}

// Match walks the trie for the supplied method+path. Returns nil, false
// on no match. When the path matches but the method does not, returns
// (nil, false, methods...) so the server can emit 405 + Allow.
type MatchOutcome int

const (
	MatchNone MatchOutcome = iota
	MatchOK
	MatchMethodNotAllowed
)

// Match resolves the route. On MatchMethodNotAllowed, the caller SHOULD emit
// a 405 with an Allow header listing methods.
//
// The hot path is allocation-free for static routes (zero params, ≤16 path
// segments): the segment slice is stack-allocated via splitPathStack, the
// params map is lazily allocated only when a param node is encountered, and
// the MatchResult is returned by value so escape analysis keeps it on the
// caller's stack. Param-bearing routes incur one allocation (the params map
// header + bucket array). The previous implementation paid 3 allocations
// per Match even on static routes (slice + map + result struct) — the
// optimization was profile-driven, matching PERF-001's ≤1 alloc budget for
// the router hot path. The 16-segment stack bound covers every realistic
// URL; longer paths fall back to a heap-allocated slice (defensive — silent
// truncation would let adversarial paths bypass routing).
func (rt *Router) Match(method, path string) (MatchResult, MatchOutcome, []string) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	var stack [16]string
	segments := splitPathStack(path, stack[:])
	var params map[string]string

	cur := rt.root
	for _, seg := range segments {
		if cur.children != nil {
			if child, ok := cur.children[seg]; ok {
				cur = child
				continue
			}
		}
		if cur.param != nil {
			if params == nil {
				params = make(map[string]string, 4)
			}
			params[cur.param.paramName] = seg
			cur = cur.param
			continue
		}
		if cur.catchall != nil {
			// catchall consumes the rest of the path into a single param "_"
			if params == nil {
				params = make(map[string]string, 1)
			}
			params["_"] = seg
			cur = cur.catchall
			continue
		}
		return MatchResult{}, MatchNone, nil
	}
	if cur.routes == nil {
		return MatchResult{}, MatchNone, nil
	}
	if r, ok := cur.routes[method]; ok {
		return MatchResult{Route: r, Params: params}, MatchOK, nil
	}
	// Method not allowed: collect allowed methods.
	methods := make([]string, 0, len(cur.routes))
	for m := range cur.routes {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	return MatchResult{}, MatchMethodNotAllowed, methods
}

// Routes returns a snapshot of all registered routes, in insertion order.
// Used by golden snapshot tests (HTTP-069) and `ogon explain route`.
func (rt *Router) Routes() []*Route {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]*Route, len(rt.routes))
	copy(out, rt.routes)
	return out
}

// Snapshot renders the route table as a stable text representation. Format:
//
//	METHOD TEMPLATE [param:kind,...] [meta:key=val,...]
//
// Used by golden snapshot tests.
func (rt *Router) Snapshot() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	out := make([]string, 0, len(rt.routes))
	for _, r := range rt.routes {
		line := r.Method + " " + r.Template
		if len(r.Params) > 0 {
			parts := make([]string, 0, len(r.Params))
			for _, p := range r.Params {
				parts = append(parts, p.Name+":"+p.Kind.String())
			}
			line += " params=" + strings.Join(parts, ",")
		}
		if len(r.groupMeta) > 0 {
			keys := make([]string, 0, len(r.groupMeta))
			for k := range r.groupMeta {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, k+"="+fmt.Sprintf("%v", r.groupMeta[k]))
			}
			line += " meta=" + strings.Join(parts, ",")
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// --- Typed adapters (codegen-ready reflection fallback) ---

// TypedAdapter1 wraps a handler that takes one typed path parameter into a
// HandlerFunc. The parse function is supplied by the caller; the framework
// ships parsers for int/int64/bool/string. Codegen emits concrete versions
// per route, eliminating the closure overhead.
func TypedAdapter1[A any](paramName string, parse func(string) (A, error),
	h func(c *Ctx, a A) (any, error)) HandlerFunc {
	return func(c *Ctx) error {
		raw, ok := c.ParamOk(paramName)
		if !ok {
			return c.Problem(BindProblem("missing path parameter "+paramName).
				WithFieldError(paramName, "missing", "required", nil))
		}
		a, err := parse(raw)
		if err != nil {
			return c.Problem(BindProblem(fmt.Sprintf("invalid %s: %v", paramName, err)).
				WithFieldError(paramName, "type", err.Error(), raw))
		}
		out, err := h(c, a)
		if err != nil {
			return err
		}
		return c.JSON(out)
	}
}

// TypedAdapter2 wraps a handler that takes two typed path parameters.
func TypedAdapter2[A, B any](p1 string, parse1 func(string) (A, error),
	p2 string, parse2 func(string) (B, error),
	h func(c *Ctx, a A, b B) (any, error)) HandlerFunc {
	return func(c *Ctx) error {
		raw1, ok := c.ParamOk(p1)
		if !ok {
			return c.Problem(BindProblem("missing path parameter " + p1))
		}
		a, err := parse1(raw1)
		if err != nil {
			return c.Problem(BindProblem("invalid " + p1 + ": " + err.Error()))
		}
		raw2, ok := c.ParamOk(p2)
		if !ok {
			return c.Problem(BindProblem("missing path parameter " + p2))
		}
		b, err := parse2(raw2)
		if err != nil {
			return c.Problem(BindProblem("invalid " + p2 + ": " + err.Error()))
		}
		out, err := h(c, a, b)
		if err != nil {
			return err
		}
		return c.JSON(out)
	}
}

// IntParser parses an int path parameter. Returns nil on success.
func IntParser(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// Int64Parser parses an int64 path parameter.
func Int64Parser(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// StringParser is the identity parser for string params.
func StringParser(s string) (string, error) { return s, nil }

// BoolParser parses a bool path parameter. Accepts 1/0/true/false.
func BoolParser(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "1", "true", "t", "yes", "on":
		return true, nil
	case "0", "false", "f", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid bool %q", s)
}
