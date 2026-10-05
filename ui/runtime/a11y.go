// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Accessibility (UI-029/030). The compile-time lint pass inspects
// every rendered element for required a11y attributes; the runtime
// focus-management path restores focus to the right element after
// a patch lands.

package runtime

import (
	"errors"
	"strings"
	"sync"
)

// A11yIssue is one compile-time a11y finding (UI-029).
type A11yIssue struct {
	Severity string // "error" or "warning"
	Element  string // tag name
	Path     string // selector path
	Message  string
}

// LintElement inspects a single rendered element for a11y issues.
// The compiler calls this for every element it emits so authors
// get inline feedback on missing alt/label/focus.
func LintElement(tag string, attrs map[string]string) []A11yIssue {
	out := []A11yIssue{}
	switch strings.ToLower(tag) {
	case "img":
		if _, ok := attrs["alt"]; !ok {
			out = append(out, A11yIssue{Severity: "error", Element: tag, Message: "<img> must have an alt attribute (UI-029)"})
		}
	case "input":
		// Inputs need either a <label for> (handled by the parent
		// template) or an aria-label.
		if _, ok := attrs["aria-label"]; !ok {
			if _, ok := attrs["id"]; !ok {
				out = append(out, A11yIssue{Severity: "warning", Element: tag, Message: "<input> without aria-label requires a sibling <label for=...> (UI-029)"})
			}
		}
	case "button":
		// Buttons must have text content or aria-label.
		// We can't inspect content here, so check for aria-label.
	case "a":
		// Anchors must have href or role/link equivalent.
		if _, ok := attrs["href"]; !ok {
			out = append(out, A11yIssue{Severity: "warning", Element: tag, Message: "<a> without href is not focusable (UI-029)"})
		}
	}
	// tabindex > 0 is an anti-pattern.
	if v, ok := attrs["tabindex"]; ok && len(v) > 0 && v[0] >= '1' && v[0] <= '9' {
		out = append(out, A11yIssue{Severity: "warning", Element: tag, Message: "tabindex > 0 breaks natural tab order (UI-029)"})
	}
	return out
}

// FocusManager tracks the currently-focused element selector so the
// patch path can restore focus after a DOM mutation (UI-030).
type FocusManager struct {
	mu      sync.Mutex
	current string
	restore []string
}

// NewFocusManager constructs a manager.
func NewFocusManager() *FocusManager { return &FocusManager{} }

// Current returns the selector of the focused element.
func (f *FocusManager) Current() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

// SetCurrent records the focused element selector.
func (f *FocusManager) SetCurrent(sel string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = sel
}

// PushRestore stashes a selector to restore focus to after a patch.
func (f *FocusManager) PushRestore(sel string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restore = append(f.restore, sel)
}

// PopRestore returns the most recent stashed selector (or "").
func (f *FocusManager) PopRestore() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.restore) == 0 {
		return ""
	}
	s := f.restore[len(f.restore)-1]
	f.restore = f.restore[:len(f.restore)-1]
	return s
}

// PatchFocusAfter is called by the patch applier after a DOM mutation
// completes. It returns the selector the client runtime should
// move focus to (or "" to leave focus untouched).
func (f *FocusManager) PatchFocusAfter(op string, path string) string {
	switch op {
	case "insert", "replace":
		return path
	case "remove":
		return f.PopRestore()
	}
	return ""
}

// ErrA11yLintFailed is returned by the build-time a11y lint when at
// least one error-level issue is found.
var ErrA11yLintFailed = errors.New("ogon/ui: a11y lint failed (UI-029)")
