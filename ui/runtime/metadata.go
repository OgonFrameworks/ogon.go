// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Metadata API + SSR head + canonical + sitemap (UI-013/014/070).

package runtime

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Metadata is the per-page SEO/social surface. The compiler emits
// one Metadata per page from <og:meta> directives in the template.
type Metadata struct {
	Title       string
	Description string
	Canonical   string
	OpenGraph   map[string]string
	Twitter     map[string]string
	Robots      string // noindex,nofollow etc.
}

// MetadataRegistry collects per-page Metadata. SSR head rendering
// (UI-014) consults the registry when emitting the <head> section.
type MetadataRegistry struct {
	mu   sync.RWMutex
	data map[string]*Metadata
}

// NewMetadataRegistry constructs an empty registry.
func NewMetadataRegistry() *MetadataRegistry {
	return &MetadataRegistry{data: map[string]*Metadata{}}
}

// Set stores metadata for a route.
func (r *MetadataRegistry) Set(route string, m *Metadata) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[route] = m
}

// Get returns metadata for a route.
func (r *MetadataRegistry) Get(route string) (*Metadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.data[route]
	return m, ok
}

// RenderHead emits the <head> HTML for a route. Includes title,
// description, canonical, OpenGraph, Twitter, and robots tags.
func (r *MetadataRegistry) RenderHead(route string) string {
	m, ok := r.Get(route)
	if !ok {
		return "<title>Untitled</title>\n"
	}
	var b strings.Builder
	if m.Title != "" {
		fmt.Fprintf(&b, "<title>%s</title>\n", escapeHTML(m.Title))
	}
	if m.Description != "" {
		fmt.Fprintf(&b, "<meta name=\"description\" content=\"%s\">\n", escapeHTML(m.Description))
	}
	if m.Canonical != "" {
		fmt.Fprintf(&b, "<link rel=\"canonical\" href=\"%s\">\n", escapeHTML(m.Canonical))
	}
	for k, v := range m.OpenGraph {
		fmt.Fprintf(&b, "<meta property=\"og:%s\" content=\"%s\">\n", k, escapeHTML(v))
	}
	for k, v := range m.Twitter {
		fmt.Fprintf(&b, "<meta name=\"twitter:%s\" content=\"%s\">\n", k, escapeHTML(v))
	}
	if m.Robots != "" {
		fmt.Fprintf(&b, "<meta name=\"robots\" content=\"%s\">\n", m.Robots)
	}
	return b.String()
}

// SitemapEntry is a single <url> entry in the sitemap.
type SitemapEntry struct {
	Loc      string
	LastMod  string
	Priority string
}

// Sitemap renders the sitemap.xml document for the supplied entries.
// Called by the generated `/sitemap.xml` route (UI-070).
func Sitemap(baseURL string, entries []SitemapEntry) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, e := range entries {
		b.WriteString("\t<url>\n")
		fmt.Fprintf(&b, "\t\t<loc>%s/%s</loc>\n", strings.TrimRight(baseURL, "/"), strings.TrimLeft(e.Loc, "/"))
		if e.LastMod != "" {
			fmt.Fprintf(&b, "\t\t<lastmod>%s</lastmod>\n", e.LastMod)
		}
		if e.Priority != "" {
			fmt.Fprintf(&b, "\t\t<priority>%s</priority>\n", e.Priority)
		}
		b.WriteString("\t</url>\n")
	}
	b.WriteString("</urlset>\n")
	return b.String()
}

// Robots renders the robots.txt body for the supplied rules. The
// generated `/robots.txt` route calls this (UI-070).
func Robots(sitemapURL string, disallow []string) string {
	var b strings.Builder
	for _, d := range disallow {
		fmt.Fprintf(&b, "Disallow: %s\n", d)
	}
	if sitemapURL != "" {
		fmt.Fprintf(&b, "Sitemap: %s\n", sitemapURL)
	}
	return b.String()
}

// escapeHTML replaces the obvious user-controlled characters with
// entities. Used by RenderHead so a metadata value cannot inject
// markup into the document head.
func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

// EnsureHTTPMethod is a small helper used by the generated route
// handlers to enforce GET/POST.
func EnsureHTTPMethod(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, m := range allowed {
		if r.Method == m {
			return true
		}
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}
