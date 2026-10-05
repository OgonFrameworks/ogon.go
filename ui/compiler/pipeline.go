// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Compiler pipeline (UI-003..005). Wires together:
//   parse → codegen go/template/style/script → emit
//
// The pipeline is deterministic and incremental: given the same
// source it produces byte-identical output so the fingerprint
// cache (UI-080) can short-circuit unchanged components.

package compiler

import (
	"errors"
	"strings"
)

// Pipeline is the top-level orchestrator. It runs each codegen pass
// in a fixed order and collects artefacts into a single Output.
type Pipeline struct {
	cache *CompileCache
	name  string
	pkg   string
}

// NewPipeline constructs a pipeline. If cache is nil, no cache is
// used (every compile is fresh).
func NewPipeline(cache *CompileCache, name, pkg string) *Pipeline {
	return &Pipeline{cache: cache, name: name, pkg: pkg}
}

// Output is the per-component artefact bundle.
type Output struct {
	Name        string
	Fingerprint string
	Go          string
	JS          string
	CSS         string
	Manifest    string
	Warnings    []string
	Errors      []error
}

// Compile runs the full pipeline on a source string.
func (p *Pipeline) Compile(src string) (*Output, error) {
	fp := Fingerprint(src)
	if p.cache != nil {
		if e, ok := p.cache.Get(p.name, fp); ok {
			return &Output{
				Name:        p.name,
				Fingerprint: e.Fingerprint,
				Go:          e.Go,
				JS:          e.JS,
				CSS:         e.CSS,
				Manifest:    e.Manifest,
			}, nil
		}
	}
	f, err := ParseFile(p.name, p.pkg, src)
	if err != nil {
		return nil, err
	}
	out := &Output{Name: p.name, Fingerprint: fp}
	// Go codegen (UI-001/002).
	out.Go = NewGoCodegen(f).Emit()
	// Template codegen (UI-003/091).
	out.Go += "\n" + NewTemplateCodegen(f).Emit()
	// Style codegen (UI-004).
	if f.Style != nil {
		css, _ := NewStyleCodegen(f).Emit()
		out.CSS = css
	}
	// Script codegen (UI-005/043).
	if f.Script != nil {
		bun := NewScriptCodegen(f).Bundle(p.name + ".ogon.ts")
		out.JS = bun.JS
		out.Warnings = append(out.Warnings, bun.Warnings...)
		out.Errors = append(out.Errors, errorsFromStrings(bun.Errors)...)
	}
	// Manifest entry.
	out.Manifest = buildManifest(out)
	// Cache.
	if p.cache != nil {
		_ = p.cache.Put(p.name, CacheEntry{
			Fingerprint: fp,
			Go:          out.Go,
			JS:          out.JS,
			CSS:         out.CSS,
			Manifest:    out.Manifest,
		})
	}
	return out, nil
}

// errorsFromStrings converts esbuild's string error list into a slice
// of error values so the caller can append them to Output.Errors.
func errorsFromStrings(ss []string) []error {
	out := make([]error, 0, len(ss))
	for _, s := range ss {
		out = append(out, errors.New(s))
	}
	return out
}

// buildManifest produces the JSON manifest entry the asset pipeline
// (UI-038) consumes. It records the component name, the fingerprint,
// and the artefact file names so the runtime can preload them.
func buildManifest(out *Output) string {
	var b strings.Builder
	b.WriteString(`{"component":"`)
	b.WriteString(out.Name)
	b.WriteString(`","fingerprint":"`)
	b.WriteString(out.Fingerprint)
	b.WriteString(`","assets":[`)
	assets := []string{}
	if out.Go != "" {
		assets = append(assets, `"`+out.Name+`.go"`)
	}
	if out.JS != "" {
		assets = append(assets, `"`+out.Name+`.js"`)
	}
	if out.CSS != "" {
		assets = append(assets, `"`+out.Name+`.css"`)
	}
	b.WriteString(strings.Join(assets, ","))
	b.WriteString(`]}`)
	return b.String()
}
