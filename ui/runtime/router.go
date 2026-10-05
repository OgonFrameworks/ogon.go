// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Client router (UI-031, UI-032). The router intercepts clicks on
// `<a ogon:link>` elements and dispatches to the page loader
// without a full document reload. Prefetch on hover is opt-in via
// `<a ogon:prefetch>`.

package runtime

import (
	"errors"
	"net/url"
	"path"
	"strings"
	"sync"
)

// ClientRouter is the server-side registry that matches intercepted
// link clicks to routes. The client runtime (15 KB JS) sends a
// `navigate` event with the path; the server returns the rendered
// page body and any new metadata for the head.
type ClientRouter struct {
	mu       sync.RWMutex
	prefetch map[string]bool
}

// NewClientRouter constructs an empty router.
func NewClientRouter() *ClientRouter {
	return &ClientRouter{prefetch: map[string]bool{}}
}

// RegisterPrefetch marks a path as prefetchable (UI-032).
func (c *ClientRouter) RegisterPrefetch(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prefetch[p] = true
}

// ShouldPrefetch reports whether the supplied path is registered
// for hover-prefetch.
func (c *ClientRouter) ShouldPrefetch(p string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prefetch[p]
}

// InterceptLink reports whether the supplied anchor attributes
// represent an internal link that the client router should
// intercept (UI-031). Internal = same-origin, has `ogon:link` or
// no `target="_blank"`.
func InterceptLink(href, target string) bool {
	if href == "" || strings.HasPrefix(href, "#") {
		return false
	}
	if target == "_blank" {
		return false
	}
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	// External links (different host or scheme) bypass the router.
	if u.IsAbs() {
		return false
	}
	return true
}

// NormalizePath cleans the path so the page lookup is stable.
// E.g. /a/../b → /b. Empty paths become "/".
func NormalizePath(p string) string {
	if p == "" {
		return "/"
	}
	cleaned := path.Clean(p)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	if !strings.Contains(cleaned, ".") {
		return cleaned
	}
	// Strip file-extension if present (e.g. /dashboard.html → /dashboard).
	dir := path.Dir(cleaned)
	base := path.Base(cleaned)
	if i := strings.Index(base, "."); i > 0 {
		base = base[:i]
	}
	return path.Join(dir, base)
}

// ErrNotIntercepted is returned by the client router when a click
// should not be intercepted (external or anchored link).
var ErrNotIntercepted = errors.New("ogon/ui: link not intercepted")
