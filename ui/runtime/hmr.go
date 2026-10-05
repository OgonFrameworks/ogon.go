// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// HMR: CSS fast path, component swap with state preservation, dev
// error overlay (UI-040/041/042).

package runtime

import (
	"sync"
	"time"
)

// HMRBus is the dev-only message bus the runtime uses to push
// invalidations to connected clients. The CSS fast path sends a
// `patch-css` message so the client runtime can swap the stylesheet
// without a reload (UI-040). Component swaps send a `swap` message
// with the new component fingerprint (UI-041).
type HMRBus struct {
	mu      sync.Mutex
	clients map[string]chan HMREvent
	lastErr string
	updated time.Time
}

// HMREvent is a single invalidation event.
type HMREvent struct {
	Type        string // "patch-css", "swap", "error", "reload"
	Component   string
	Fingerprint string
	CSS         string
	JS          string
	Message     string
}

// NewHMRBus constructs the bus.
func NewHMRBus() *HMRBus {
	return &HMRBus{clients: map[string]chan HMREvent{}}
}

// Subscribe registers a client and returns a receive channel.
// Unsubscribe is called by the client on disconnect.
func (h *HMRBus) Subscribe(clientID string) chan HMREvent {
	ch := make(chan HMREvent, 16)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[clientID] = ch
	return ch
}

// Unsubscribe removes a client.
func (h *HMRBus) Unsubscribe(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.clients[clientID]; ok {
		close(ch)
		delete(h.clients, clientID)
	}
}

// Broadcast sends an event to every connected client. Non-blocking:
// a slow client's events are dropped after the buffer fills.
func (h *HMRBus) Broadcast(ev HMREvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.updated = time.Now()
	for _, ch := range h.clients {
		select {
		case ch <- ev:
		default:
		}
	}
}

// PatchCSS broadcasts a CSS fast-path event (UI-040).
func (h *HMRBus) PatchCSS(component, css string) {
	h.Broadcast(HMREvent{Type: "patch-css", Component: component, CSS: css})
}

// SwapComponent broadcasts a component-swap event preserving the
// client's local UI state (UI-041).
func (h *HMRBus) SwapComponent(component, fingerprint, js string) {
	h.Broadcast(HMREvent{Type: "swap", Component: component, Fingerprint: fingerprint, JS: js})
}

// PushError stores the latest compile error so the dev overlay can
// display it (UI-042).
func (h *HMRBus) PushError(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastErr = msg
	h.updated = time.Now()
	h.Broadcast(HMREvent{Type: "error", Message: msg})
}

// ClearError removes the latest error after a successful recompile.
func (h *HMRBus) ClearError() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastErr = ""
	h.Broadcast(HMREvent{Type: "error", Message: ""})
}

// LastError returns the latest compile error message (or "").
func (h *HMRBus) LastError() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr
}

// ErrorOverlay is the dev-only HTML fragment injected into every SSR
// response when a compile error is present (UI-042). The overlay is
// shown until the error clears; live HMR removes it on recovery.
func ErrorOverlay(msg string) string {
	if msg == "" {
		return ""
	}
	return `<div id="ogon-error" style="position:fixed;bottom:0;left:0;right:0;background:#300;color:#fff;padding:12px;font-family:monospace;font-size:12px;white-space:pre-wrap;max-height:40vh;overflow:auto;z-index:2147483647"><b>ogon compile error</b><br>` + msg + `</div>`
}
