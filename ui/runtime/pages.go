// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Page routing + nested layouts (UI-009, UI-010). Pages live under
// `app/pages/<name>.ogon`; layouts nest by directory depth.

package runtime

import (
	"errors"
	"net/http"
	"path"
	"strings"
	"sync"
)

// Page is a registered route. Pattern is a path pattern with
// `{param}` placeholders; Handler renders the page.
type Page struct {
	Name    string
	Pattern string
	Layouts []string // layout component names, outermost first
	Handler http.HandlerFunc
}

// Router is the runtime page router. It dispatches by path pattern
// and falls back to the 404 handler when no page matches.
type Router struct {
	mu       sync.RWMutex
	pages    []*Page
	notFound http.HandlerFunc
}

// NewRouter constructs a router with a default 404 handler.
func NewRouter() *Router {
	return &Router{notFound: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	})}
}

// Register adds a page route.
func (r *Router) Register(p *Page) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pages = append(r.pages, p)
}

// ServeHTTP dispatches by path.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.pages {
		if matchPage(p.Pattern, req.URL.Path) {
			p.Handler.ServeHTTP(w, req)
			return
		}
	}
	r.notFound.ServeHTTP(w, req)
}

// NotFound sets the 404 handler (UI-069).
func (r *Router) NotFound(h http.HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notFound = h
}

// matchPage reports whether `pattern` matches `p`. Patterns support
// `{name}` placeholders that match a single segment. `*` matches
// anything including slashes.
func matchPage(pattern, p string) bool {
	if pattern == "" || pattern == "/" {
		return p == "/" || p == ""
	}
	if strings.HasSuffix(pattern, "/*") {
		base := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(p, base)
	}
	patSeg := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	urlSeg := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(patSeg) != len(urlSeg) {
		return false
	}
	for i, seg := range patSeg {
		if seg == urlSeg[i] {
			continue
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			continue
		}
		if seg == "*" {
			continue
		}
		return false
	}
	return true
}

// PathParams extracts `{param}` values from the URL.
func PathParams(pattern, p string) map[string]string {
	out := map[string]string{}
	patSeg := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	urlSeg := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i := 0; i < len(patSeg) && i < len(urlSeg); i++ {
		seg := patSeg[i]
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
			out[name] = urlSeg[i]
		}
	}
	return out
}

// DiscoverLayouts returns the layout component chain for a page path.
// Layouts are derived from directory depth: every `layout.ogon` from
// the page's directory up to the root becomes a layout in the chain
// (outermost first). When no layouts are found, the page renders
// standalone.
func DiscoverLayouts(pagePath string) []string {
	out := []string{}
	dir := path.Dir(pagePath)
	for dir != "." && dir != "/" {
		layoutPath := path.Join(dir, "layout.ogon")
		out = append([]string{layoutBaseName(layoutPath)}, out...)
		dir = path.Dir(dir)
	}
	return out
}

// layoutBaseName turns a layout file path into a component name.
// E.g. "app/pages/admin/layout.ogon" → "AdminLayout".
func layoutBaseName(p string) string {
	base := path.Base(p)
	base = strings.TrimSuffix(base, ".ogon")
	if base == "layout" {
		base = "Layout"
	} else {
		base = strings.ToUpper(base[:1]) + base[1:]
	}
	return base
}

// ErrLayoutMissing is returned when a layout referenced by DiscoverLayouts
// is not registered with the registry at render time.
var ErrLayoutMissing = errors.New("ogon/ui: missing layout component")
