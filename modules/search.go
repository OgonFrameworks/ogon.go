// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// modules/search.go: built-in module search index (MOD-034). Used by
// `ogon modules search` when the network registry is unavailable.

package modules

import (
	"sort"
	"strings"
)

// IndexEntry is one row in the search index.
type IndexEntry struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	License     string   `json:"license"`
	Repo        string   `json:"repo"`
	Tags        []string `json:"tags,omitempty"`
}

// RegistryIndex is a searchable in-memory module index. The CLI populates it
// from a built-in first-party list; community indexes ship as separate Go
// modules (MOD-016).
type RegistryIndex struct {
	entries []IndexEntry
}

// NewRegistryIndex returns an empty index.
func NewRegistryIndex() *RegistryIndex { return &RegistryIndex{} }

// Add appends entries to the index.
func (i *RegistryIndex) Add(entries ...IndexEntry) {
	i.entries = append(i.entries, entries...)
}

// All returns the entries sorted by name.
func (i *RegistryIndex) All() []IndexEntry {
	out := append([]IndexEntry(nil), i.entries...)
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// Search returns entries that match the query. A match is any of:
//   - name contains query (case-insensitive)
//   - description contains query
//   - any tag equals query (case-insensitive)
//
// Results are sorted by relevance: name-contains first, then tag, then
// description. Stable alphabetical tie-break.
func (i *RegistryIndex) Search(query string) []IndexEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return i.All()
	}
	var nameHits, tagHits, descHits []IndexEntry
	for _, e := range i.entries {
		if strings.Contains(strings.ToLower(e.Name), q) {
			nameHits = append(nameHits, e)
			continue
		}
		matchedTag := false
		for _, t := range e.Tags {
			if strings.EqualFold(t, q) {
				matchedTag = true
				break
			}
		}
		if matchedTag {
			tagHits = append(tagHits, e)
			continue
		}
		if strings.Contains(strings.ToLower(e.Description), q) {
			descHits = append(descHits, e)
		}
	}
	out := append(append(nameHits, tagHits...), descHits...)
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// DefaultFirstPartyIndex returns the built-in index of well-known first-party
// OgonGo modules. These are the modules the CLI ships with documentation for
// (MOD-033: stripe/sentry/s3 are external modules; this index lists the
// known names so users can `ogon modules search` for them offline).
func DefaultFirstPartyIndex() *RegistryIndex {
	idx := NewRegistryIndex()
	idx.Add(
		IndexEntry{Name: "ogon-stripe", Version: "1.0.0", Description: "Stripe payments integration", License: "MIT", Repo: "github.com/ogonframeworks/ogon-stripe", Tags: []string{"payments", "stripe"}},
		IndexEntry{Name: "ogon-sentry", Version: "1.0.0", Description: "Sentry error tracking integration", License: "MIT", Repo: "github.com/ogonframeworks/ogon-sentry", Tags: []string{"observability", "sentry", "errors"}},
		IndexEntry{Name: "ogon-s3", Version: "1.0.0", Description: "S3-compatible object storage integration", License: "MIT", Repo: "github.com/ogonframeworks/ogon-s3", Tags: []string{"storage", "s3", "aws"}},
		IndexEntry{Name: "ogon-auth-session", Version: "1.0.0", Description: "Session-based authentication module", License: "MIT", Repo: "github.com/ogonframeworks/ogon-auth-session", Tags: []string{"auth", "session"}},
		IndexEntry{Name: "ogon-auth-passkey", Version: "1.0.0", Description: "WebAuthn / passkey authentication module", License: "MIT", Repo: "github.com/ogonframeworks/ogon-auth-passkey", Tags: []string{"auth", "passkey", "webauthn"}},
	)
	return idx
}
