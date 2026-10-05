// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// No-JS graceful degradation (UI-044/045). Forms post normally
// without JS; the live transport layer is a progressive enhancement.
// This file declares the convention the compiler enforces: every
// interactive element MUST have a working fallback via form POST.

package runtime

import (
	"net/http"
	"net/url"
	"strings"
)

// NoJSEnhancement is a flag set by the runtime when the request is
// detected to have JavaScript disabled (UI-044). When true, the
// render path emits form `action` attributes and disables inline
// directives that depend on the live transport.
type NoJSEnhancement struct {
	Disabled bool
}

// DetectNoJS inspects the request for no-JS markers. The two signals
// we look at:
//   - `?ogon:nojs` query param (set by the meta refresh fallback)
//   - Missing `ogon-sid` cookie (the live transport sets it on join)
func DetectNoJS(r *http.Request) NoJSEnhancement {
	if _, ok := r.URL.Query()["ogon:nojs"]; ok {
		return NoJSEnhancement{Disabled: true}
	}
	if _, err := r.Cookie("ogon-sid"); err != nil {
		return NoJSEnhancement{Disabled: true}
	}
	return NoJSEnhancement{Disabled: false}
}

// FormFallbackAction rewrites a `ogon:click` directive into a
// form POST so the action works without JS (UI-045). The compiler
// emits `<form action="<action>" method="post"><button>` markup
// whenever the directive is set.
func FormFallbackAction(action, handler string) string {
	if action == "" {
		action = "/" + handler
	}
	return action
}

// InlineForm wraps a button in a form so no-JS clients can submit.
// Used by the compiler when emitting button markup that targets
// an `ogon:click` handler.
func InlineForm(action, buttonHTML string) string {
	if !strings.HasPrefix(action, "/") {
		action = "/" + action
	}
	return `<form action="` + action + `" method="post">` + buttonHTML + `</form>`
}

// NProgressIndicator emits a CSS-only "loading" indicator that
// shows during a no-JS form POST (UI-044). When the live transport
// is active, the indicator is hidden; without JS, the browser
// shows it until the POST completes.
func NProgressIndicator() string {
	return `<style>.ogon-progress{position:fixed;top:0;left:0;right:0;height:2px;background:#0a0;opacity:0;animation:ogon-pulse 1s ease infinite}@keyframes ogon-pulse{0%{opacity:0}50%{opacity:.9}100%{opacity:0}}</style><div class="ogon-progress"></div>`
}

// LinkAsButton wraps an anchor styled as a button so it POSTs to an
// action handler. This is the no-JS fallback for ogon:click on `<a>`.
func LinkAsButton(action, label string) string {
	return InlineForm(action, `<button type="submit">`+label+`</button>`)
}

// QueryFallback helps components that bind a state path to the URL
// query string (UI-026) — without JS, a plain anchor with a query
// param is enough to mutate the URL state.
func QueryFallback(baseURL, key, val string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	q := u.Query()
	q.Set(key, val)
	u.RawQuery = q.Encode()
	return u.String()
}

// EnforceMethodForm ensures a fallback form supports GET (for
// ogon:click that does not mutate state) or POST (for mutating
// handlers). Default is POST.
func EnforceMethodForm(method string) string {
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		return "POST"
	}
	if m != "GET" && m != "POST" {
		return "POST"
	}
	return m
}
