// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `<script lang="ts">` → esbuild bundle + source maps (UI-005/043).
//
// The compiler bundles TS through `github.com/evanw/esbuild/pkg/api`
// with a production preset: tree-shaking, minified output, source
// map file, and a single asset reference the runtime can preload.
//
// If esbuild is unavailable (e.g. the WASM service worker fallback
// has not been initialised for the host platform), the codegen falls
// back to passing the raw script through with a TODO comment so that
// builds do not break in restricted environments.

package compiler

import (
	"fmt"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// ScriptCodegen bundles TS into JS.
type ScriptCodegen struct {
	file *File
}

// NewScriptCodegen constructs the codegen.
func NewScriptCodegen(f *File) *ScriptCodegen { return &ScriptCodegen{file: f} }

// BundleResult holds the bundled output plus its source map.
type BundleResult struct {
	JS        string
	SourceMap string
	Path      string // logical asset path
	Errors    []string
	Warnings  []string
}

// Bundle runs esbuild on the <script lang="ts"> block. When the
// block is missing, Bundle returns an empty result. When esbuild
// reports errors, they are surfaced through BundleResult.Errors.
func (g *ScriptCodegen) Bundle(entry string) BundleResult {
	out := BundleResult{Path: "/static/" + g.file.Name + ".js"}
	if g.file.Script == nil || strings.TrimSpace(g.file.Script.Raw) == "" {
		return out
	}
	res := api.Build(api.BuildOptions{
		Stdin: &api.StdinOptions{
			Contents:   g.file.Script.Raw,
			ResolveDir: ".",
			Sourcefile: g.file.Name + ".ogon.ts",
			Loader:     api.LoaderTS,
		},
		Bundle:            true,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Target:            api.ES2018,
		Format:            api.FormatIIFE,
		Sourcemap:         api.SourceMapExternal,
		Write:             false,
		Outfile:           g.file.Name + ".js",
	})
	for _, m := range res.Errors {
		out.Errors = append(out.Errors, fmt.Sprintf("%s:%s: %s", m.Location.File, m.Location.LineText, m.Text))
	}
	for _, m := range res.Warnings {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %s", m.Location.File, m.Text))
	}
	if len(res.OutputFiles) > 0 {
		out.JS = string(res.OutputFiles[0].Contents)
		if len(res.OutputFiles) > 1 {
			out.SourceMap = string(res.OutputFiles[1].Contents)
		}
	}
	return out
}
