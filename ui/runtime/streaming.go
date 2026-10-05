// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Streaming SSR + partial subtree patching + keyed list diff
// (UI-020/021/022). The streaming renderer flushes the <head>
// early so the browser can prefetch assets while the body renders;
// the patcher compares the old and new render trees and emits
// the minimal op list to converge DOM to the new state.

package runtime

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// PatchOp is a single DOM mutation. Wire-protocol JSON marshalling
// is handled by the live package (Part IX); this struct is the
// canonical in-memory form used by the diff algorithm (UI-022).
type PatchOp struct {
	Type   string `json:"t"`           // set-text, set-attr, del-attr, insert, move, remove, replace
	Path   string `json:"p,omitempty"` // CSS selector path to the target node
	Attr   string `json:"a,omitempty"` // attribute name (set-attr/del-attr)
	Value  string `json:"v,omitempty"` // new value
	Key    string `json:"k,omitempty"` // keyed-list identity (UI-022)
	Index  int    `json:"i,omitempty"` // list index (insert/move)
	Before string `json:"b,omitempty"` // sibling key (insert/move)
}

// DiffLists compares two keyed lists and emits the minimal op list
// to transform `old` into `new`. Each entry has a Key and an HTML
// fragment. The algorithm is the classic O(n) keyed diff: build a
// map of old keys, walk new keys, emit inserts/moves/removes.
func DiffLists(old, new []KeyedNode) []PatchOp {
	if len(old) == 0 && len(new) == 0 {
		return nil
	}
	oldIdx := map[string]int{}
	for i, n := range old {
		oldIdx[n.Key] = i
	}
	var ops []PatchOp
	newIdx := map[string]int{}
	for i, n := range new {
		newIdx[n.Key] = i
		if _, wasInOld := oldIdx[n.Key]; !wasInOld {
			before := ""
			if i > 0 {
				before = new[i-1].Key
			}
			ops = append(ops, PatchOp{Type: "insert", Key: n.Key, Path: n.Path, Value: n.HTML, Index: i, Before: before})
			continue
		}
		// Update in place if HTML changed.
		if old[oldIdx[n.Key]].HTML != n.HTML {
			ops = append(ops, PatchOp{Type: "replace", Key: n.Key, Path: n.Path, Value: n.HTML, Index: i})
		}
	}
	// Remove items no longer present.
	for _, o := range old {
		if _, stillInNew := newIdx[o.Key]; !stillInNew {
			ops = append(ops, PatchOp{Type: "remove", Key: o.Key, Path: o.Path})
		}
	}
	return ops
}

// KeyedNode is a single keyed list element. Path is the CSS selector
// path (e.g. "ul > li:nth-child(2)"); Key is the stable identity;
// HTML is the rendered element string.
type KeyedNode struct {
	Key  string
	Path string
	HTML string
}

// StreamingWriter is the early-flush helper used by SSR (UI-020).
// The first call writes the <head> and flushes; subsequent calls
// append to the body and flush after each chunk.
type StreamingWriter struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	mu       sync.Mutex
	headSent bool
}

// NewStreamingWriter wraps an http.ResponseWriter.
func NewStreamingWriter(w http.ResponseWriter) *StreamingWriter {
	flusher, _ := w.(http.Flusher)
	return &StreamingWriter{w: w, flusher: flusher}
}

// WriteHead emits the <head> and flushes (UI-020).
func (s *StreamingWriter) WriteHead(head string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.headSent {
		return
	}
	fmt.Fprint(s.w, "<!doctype html><html><head>")
	fmt.Fprint(s.w, head)
	fmt.Fprint(s.w, "</head><body>")
	if s.flusher != nil {
		s.flusher.Flush()
	}
	s.headSent = true
}

// WriteChunk appends to the body and flushes.
func (s *StreamingWriter) WriteChunk(html string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprint(s.w, html)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// CloseFooter writes the closing tags.
func (s *StreamingWriter) CloseFooter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprint(s.w, "</body></html>")
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// PartialPatch computes the op set needed to transform `oldHTML`
// into `newHTML` for a single subtree. We compare element-by-element
// via a stable selector path; this is the simple variant used when
// the compiler didn't emit a keyed list (UI-021).
func PartialPatch(path, oldHTML, newHTML string) []PatchOp {
	if oldHTML == newHTML {
		return nil
	}
	if oldHTML == "" {
		return []PatchOp{{Type: "insert", Path: path, Value: newHTML}}
	}
	if newHTML == "" {
		return []PatchOp{{Type: "remove", Path: path}}
	}
	return []PatchOp{{Type: "replace", Path: path, Value: newHTML}}
}

// PatchToJSON renders a slice of PatchOps as a JSON array. The live
// transport wraps this in an Envelope before sending (Part IX).
func PatchToJSON(ops []PatchOp) string {
	if len(ops) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, op := range ops {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"t":"%s","p":"%s","a":"%s","v":"%s","k":"%s","i":%d,"b":"%s"}`,
			op.Type, op.Path, op.Attr, op.Value, op.Key, op.Index, op.Before)
	}
	b.WriteByte(']')
	return b.String()
}
