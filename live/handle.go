// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// live/handle — `live.Handle("/room/{id}", Component)` DSL (LIVE-027).
//
// The Handle DSL is the ergonomic surface for registering a live
// route. Internally it creates a route entry in the registry, binds
// the supplied Component, and applies any HandleOption modifiers
// (delivery mode, join hooks, subscribe auth, rate limits).
//
// Route patterns use the {name} path-param syntax (not glob). At
// upgrade time the request path is matched against all registered
// patterns; the first match wins. The matched params are exposed to
// the Component via the RouteContext.

package live

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
)

// Component is the application-side handler for a live route.
//
// OnJoin is invoked after the connection has subscribed to its
// channel; OnLeave on unsubscribe/eviction; OnMessage for each
// inbound application message addressed to this route's channel.
// All methods must be safe for concurrent use.
type Component interface {
	OnJoin(ctx context.Context, conn *Connection, params map[string]string) error
	OnLeave(ctx context.Context, conn *Connection)
	OnMessage(ctx context.Context, conn *Connection, env *Envelope) error
}

// Route is a single registered live route.
type Route struct {
	Pattern        string
	Component      Component
	Delivery       DeliveryMode
	JoinHook       JoinHook
	LeaveHook      LeaveHook
	SubscribeAuth  SubscribeAuth
	PerMessageAuth PerMessageAuth
	MsgPerSec      int
}

// RouteRegistry holds all registered routes.
type RouteRegistry struct {
	mu     sync.RWMutex
	routes []*compiledRoute
}

type compiledRoute struct {
	route *Route
	regex *regexp.Regexp
	names []string
}

// NewRouteRegistry constructs an empty registry.
func NewRouteRegistry() *RouteRegistry { return &RouteRegistry{} }

// Handle is the DSL entrypoint. It compiles pattern into a regex.
func (r *RouteRegistry) Handle(pattern string, comp Component, opts ...HandleOption) error {
	if pattern == "" {
		return errors.New("ogon/live: empty pattern")
	}
	if comp == nil {
		return errors.New("ogon/live: nil component")
	}
	route := &Route{
		Pattern:   pattern,
		Component: comp,
		Delivery:  DeliveryAtMostOnce,
		MsgPerSec: 64,
	}
	for _, opt := range opts {
		opt(route)
	}
	cr, err := compileRoute(route)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.routes = append(r.routes, cr)
	r.mu.Unlock()
	return nil
}

// Match returns the route matching path, or nil. Path params are
// returned in the map.
func (r *RouteRegistry) Match(path string) (*Route, map[string]string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, cr := range r.routes {
		if m := cr.regex.FindStringSubmatchIndex(path); m != nil {
			params := make(map[string]string, len(cr.names))
			for i, name := range cr.names {
				if m[2*i+2] >= 0 {
					params[name] = path[m[2*i+2]:m[2*i+3]]
				}
			}
			return cr.route, params
		}
	}
	return nil, nil
}

func compileRoute(r *Route) (*compiledRoute, error) {
	pat := r.Pattern
	if !strings.HasPrefix(pat, "/") {
		pat = "/" + pat
	}
	var names []string
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pat) {
		if pat[i] == '{' {
			j := strings.IndexByte(pat[i+1:], '}')
			if j < 0 {
				return nil, errors.New("ogon/live: unterminated {param}")
			}
			name := pat[i+1 : i+1+j]
			names = append(names, name)
			b.WriteString("([^/]+)")
			i += j + 2
		} else {
			ch := pat[i]
			if ch == '/' || ch == '.' || ch == '-' {
				b.WriteByte(ch)
			} else {
				b.WriteString(regexp.QuoteMeta(string(ch)))
			}
			i++
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, err
	}
	return &compiledRoute{route: r, regex: re, names: names}, nil
}

// HandleOption modifies a Route during registration.
type HandleOption func(*Route)

// WithDelivery sets the delivery semantics for the route.
func WithDelivery(m DeliveryMode) HandleOption {
	return func(r *Route) { r.Delivery = m }
}

// WithJoinHook installs a per-channel join authorization hook.
func WithJoinHook(h JoinHook) HandleOption {
	return func(r *Route) { r.JoinHook = h }
}

// WithLeaveHook installs a leave hook.
func WithLeaveHook(h LeaveHook) HandleOption {
	return func(r *Route) { r.LeaveHook = h }
}

// WithSubscribeAuth installs subscribe auth.
func WithSubscribeAuth(h SubscribeAuth) HandleOption {
	return func(r *Route) { r.SubscribeAuth = h }
}

// WithPerMessageAuth installs per-message auth.
func WithPerMessageAuth(h PerMessageAuth) HandleOption {
	return func(r *Route) { r.PerMessageAuth = h }
}

// WithRateLimit sets the per-connection rate limit for this route.
func WithRateLimit(msgPerSec int) HandleOption {
	return func(r *Route) { r.MsgPerSec = msgPerSec }
}

// Stub ctx import for documentation references.
var _ = context.Background
