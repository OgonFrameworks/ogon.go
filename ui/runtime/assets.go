// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Asset pipeline + image helper (UI-038/039). The pipeline minifies,
// hashes, and emits a manifest mapping logical asset names to their
// content-hashed URLs so that cache-busting is automatic.

package runtime

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
)

// Asset is a single asset in the pipeline output.
type Asset struct {
	Logical string // logical name, e.g. "chat.js"
	Hashed  string // hashed filename, e.g. "chat-9f1a3.js"
	SHA1    string
	Size    int
}

// Manifest is the build-time asset map. The runtime serves hashed
// filenames; templates reference assets via the logical name.
type Manifest struct {
	mu     sync.RWMutex
	assets map[string]Asset
}

// NewManifest constructs an empty manifest.
func NewManifest() *Manifest { return &Manifest{assets: map[string]Asset{}} }

// Add records an asset and returns its hashed filename.
func (m *Manifest) Add(logical string, body []byte) Asset {
	h := sha1.Sum(body)
	digest := hex.EncodeToString(h[:])[:8]
	ext := path.Ext(logical)
	base := strings.TrimSuffix(path.Base(logical), ext)
	hashed := fmt.Sprintf("%s-%s%s", base, digest, ext)
	a := Asset{Logical: logical, Hashed: hashed, SHA1: digest, Size: len(body)}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assets[logical] = a
	return a
}

// Lookup returns the hashed filename for a logical name.
func (m *Manifest) Lookup(logical string) (Asset, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.assets[logical]
	return a, ok
}

// JSON renders the manifest as the JSON document the dev server
// serves at `/ogon-manifest.json` (UI-038).
func (m *Manifest) JSON() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var b strings.Builder
	b.WriteString(`{`)
	first := true
	for k, v := range m.assets {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, `"%s":{"hashed":"%s","sha1":"%s","size":%d}`, k, v.Hashed, v.SHA1, v.Size)
	}
	b.WriteString(`}`)
	return b.String()
}

// MinifyCSS strips comments and collapses whitespace from a CSS
// source. It's a lossy approximation of a real CSS minifier but
// sufficient for the in-memory cache budget.
func MinifyCSS(src string) string {
	out := strings.Builder{}
	inComment := false
	for i := 0; i < len(src); i++ {
		if !inComment && i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			inComment = true
			i++
			continue
		}
		if inComment {
			if i+1 < len(src) && src[i] == '*' && src[i+1] == '/' {
				inComment = false
				i++
			}
			continue
		}
		c := src[i]
		if c == '\n' || c == '\t' {
			out.WriteByte(' ')
			continue
		}
		out.WriteByte(c)
	}
	s := out.String()
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// MinifyJS strips comments and collapses whitespace from a JS source.
// This is intentionally a low-fidelity minifier — esbuild is the
// canonical bundler in codegen_script.go.
func MinifyJS(src string) string {
	out := strings.Builder{}
	i := 0
	for i < len(src) {
		// Skip line comments.
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		// Skip block comments.
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		if src[i] == '\n' || src[i] == '\t' {
			out.WriteByte(' ')
			i++
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.String()
}

// ImageHelper emits an `<img>` tag with width/height attributes and
// optional loading="lazy" (UI-039). Width/height prevent layout
// shift on load; the lazy attribute defers off-screen loads.
func ImageHelper(src, alt string, w, h int, lazy bool) string {
	tag := `<img src="` + src + `" alt="` + alt + `"`
	if w > 0 {
		tag += fmt.Sprintf(` width="%d"`, w)
	}
	if h > 0 {
		tag += fmt.Sprintf(` height="%d"`, h)
	}
	if lazy {
		tag += ` loading="lazy"`
	}
	return tag + ">"
}

// ErrAssetMissing is returned by Lookup when an asset isn't in the
// manifest (a 404 case the runtime surfaces).
var ErrAssetMissing = errors.New("ogon/ui: asset missing from manifest")
