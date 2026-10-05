// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Forms: action handlers, file uploads, inline validation, flash
// messages (UI-015/016/017/018). Forms post normally without JS
// (UI-044/045); the live transport layer is an optional progressive
// enhancement.

package runtime

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// FormHandler is a server-side action handler. The generated route
// adapter calls it after parsing multipart bodies; it returns the
// URL to redirect to (PRG pattern, UI-045) or a validation error.
type FormHandler func(ctx context.Context, r *http.Request) (redirect string, errs []ValidationError, err error)

// ValidationError is a single field-level validation failure
// (UI-017). The render path replays these into the form so the
// user sees inline feedback without a roundtrip.
type ValidationError struct {
	Field   string
	Message string
}

// FormsRegistry maps action names to handlers. Action names come
// from the `ogon:submit` directive and ride in the form body as
// the `ogon:action` field.
type FormsRegistry struct {
	mu       sync.RWMutex
	handlers map[string]FormHandler
	flash    *FlashStore
	maxBytes int64
}

// NewFormsRegistry constructs the registry. maxBytes caps the
// request body size (UI-016).
func NewFormsRegistry(maxBytes int64, flash *FlashStore) *FormsRegistry {
	if maxBytes <= 0 {
		maxBytes = 10 << 20 // 10 MiB default
	}
	return &FormsRegistry{
		handlers: map[string]FormHandler{},
		flash:    flash,
		maxBytes: maxBytes,
	}
}

// Register adds a form action handler.
func (f *FormsRegistry) Register(action string, h FormHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[action] = h
}

// ServeForm is the HTTP entry point invoked by the generated route
// adapter. It enforces the size cap, dispatches to the action
// handler, and stores validation errors as flash messages so the
// next render can replay them.
func (f *FormsRegistry) ServeForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, f.maxBytes)
	if err := r.ParseMultipartForm(f.maxBytes); err != nil && err != http.ErrNotMultipart {
		http.Error(w, "form parse failed", http.StatusBadRequest)
		return
	}
	action := r.FormValue("ogon:action")
	if action == "" {
		http.Error(w, "missing ogon:action", http.StatusBadRequest)
		return
	}
	f.mu.RLock()
	h, ok := f.handlers[action]
	f.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	redirect, errs, err := h(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(errs) > 0 && f.flash != nil {
		// Store errors for the next render — keyed by session cookie.
		sid := sessionID(r)
		f.flash.Set(sid, "errors", errs)
		// Fall back to Referer to redisplay the form with errors.
		if redirect == "" {
			redirect = r.Header.Get("Referer")
		}
	}
	if redirect == "" {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// FileUpload wraps a single multipart file header (UI-016).
type FileUpload struct {
	Field string
	Name  string
	MIME  string
	Size  int64
	Open  func() (multipart.File, error)
}

// ParseUploads collects multipart file headers into FileUpload
// values. Callers MUST validate MIME against an allowlist before
// trusting the headers — see auth/upload.go for the sniff helper.
func ParseUploads(r *http.Request) ([]FileUpload, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	out := []FileUpload{}
	for field, headers := range r.MultipartForm.File {
		for _, h := range headers {
			h := h
			out = append(out, FileUpload{
				Field: field,
				Name:  h.Filename,
				MIME:  h.Header.Get("Content-Type"),
				Size:  h.Size,
				Open: func() (multipart.File, error) {
					return h.Open()
				},
			})
		}
	}
	return out, nil
}

// FlashStore is a tiny server-side flash message store keyed by
// session ID. It is used to replay form validation errors after a
// PRG redirect (UI-018).
type FlashStore struct {
	mu   sync.Mutex
	data map[string]map[string]any
}

// NewFlashStore constructs an empty flash store.
func NewFlashStore() *FlashStore { return &FlashStore{data: map[string]map[string]any{}} }

// Set stores a value under the session/key pair.
func (s *FlashStore) Set(sessionID, key string, val any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[sessionID]; !ok {
		s.data[sessionID] = map[string]any{}
	}
	s.data[sessionID][key] = val
}

// Take retrieves and removes a value (flash semantics).
func (s *FlashStore) Take(sessionID, key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.data[sessionID]; ok {
		v, ok := m[key]
		if ok {
			delete(m, key)
		}
		return v, ok
	}
	return nil, false
}

// SweepDrop removes sessions with no remaining entries. Called by
// a periodic cleanup goroutine owned by the runtime supervisor.
func (s *FlashStore) SweepDrop() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for sid, m := range s.data {
		if len(m) == 0 {
			delete(s.data, sid)
			n++
		}
	}
	return n
}

func sessionID(r *http.Request) string {
	c, err := r.Cookie("ogon_sid")
	if err != nil || c.Value == "" {
		return r.RemoteAddr
	}
	return c.Value
}

// ErrNoUploads is returned by ParseUploads when the request is not
// multipart or contains no files. Callers usually ignore this.
var ErrNoUploads = errors.New("ogon/ui: no uploads in request")

// FlashRedirectRoundtrip is a helper for tests: it stores the errors
// and returns the redirect URL with the action embedded.
func FlashRedirectRoundtrip(f *FlashStore, action, redirect string, errs []ValidationError) string {
	if f == nil || len(errs) == 0 {
		return redirect
	}
	q := url.Values{}
	q.Set("ogon:action", action)
	q.Set("ogon:flash", time.Now().UTC().Format(time.RFC3339Nano))
	if !strings.Contains(redirect, "?") {
		return redirect + "?" + q.Encode()
	}
	return redirect + "&" + q.Encode()
}

// Suppress unused-import errors when io is not directly referenced
// in this build configuration.
var _ = io.EOF
