// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// OpenAPI contract assert (TEST-014/031). The fixture accepts an OpenAPI v3
// document (as bytes or parsed) and offers a small set of assertions:
// paths present, methods present, schema names present, and (critically) a
// drift check against a baseline. The drift check produces an actionable
// diff so a CI failure tells the PR author exactly what changed.

package test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// OpenAPISpec is the parsed surface of an OpenAPI v3 document, narrowed to
// the fields this fixture cares about. The fixture does not validate the
// full spec — it is a contract-drift detector, not a linter.
type OpenAPISpec struct {
	OpenAPI    string                    `json:"openapi"`
	Paths      map[string]map[string]any `json:"paths"`      // path -> method -> op
	Components map[string]map[string]any `json:"components"` // section -> name -> schema
}

// ParseOpenAPI unmarshals an OpenAPI v3 document.
func ParseOpenAPI(data []byte) (*OpenAPISpec, error) {
	var s OpenAPISpec
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("ogontest: parse openapi: %w", err)
	}
	return &s, nil
}

// AssertPathPresent fails the test if the supplied path is not declared.
func AssertPathPresent(t interface{ Fatalf(string, ...any) }, spec *OpenAPISpec, path string) {
	if spec == nil || spec.Paths == nil {
		t.Fatalf("ogontest: openapi: spec has no paths")
	}
	if _, ok := spec.Paths[path]; !ok {
		t.Fatalf("ogontest: openapi: path %q missing", path)
	}
}

// AssertMethodPresent fails the test if the supplied (path, method) is not
// declared.
func AssertMethodPresent(t interface{ Fatalf(string, ...any) }, spec *OpenAPISpec, path, method string) {
	AssertPathPresent(t, spec, path)
	m := strings.ToLower(method)
	if _, ok := spec.Paths[path][m]; !ok {
		t.Fatalf("ogontest: openapi: %s %s missing", strings.ToUpper(m), path)
	}
}

// AssertSchemaPresent fails the test if the supplied schema name is not
// declared under components.schemas.
func AssertSchemaPresent(t interface{ Fatalf(string, ...any) }, spec *OpenAPISpec, name string) {
	if spec == nil || spec.Components == nil || spec.Components["schemas"] == nil {
		t.Fatalf("ogontest: openapi: spec has no schemas")
	}
	if _, ok := spec.Components["schemas"][name]; !ok {
		t.Fatalf("ogontest: openapi: schema %q missing", name)
	}
}

// OpenAPIDrift is the result of a drift check between two specs.
type OpenAPIDrift struct {
	PathsAdded     []string
	PathsRemoved   []string
	MethodsAdded   []string
	MethodsRemoved []string
	SchemasAdded   []string
	SchemasRemoved []string
}

// IsEmpty reports whether the drift object has no changes.
func (d OpenAPIDrift) IsEmpty() bool {
	return len(d.PathsAdded) == 0 && len(d.PathsRemoved) == 0 &&
		len(d.MethodsAdded) == 0 && len(d.MethodsRemoved) == 0 &&
		len(d.SchemasAdded) == 0 && len(d.SchemasRemoved) == 0
}

// DiffOpenAPI computes drift between baseline (checked-in golden) and
// candidate (current build). Tests fail the build when the drift object
// is non-empty.
func DiffOpenAPI(baseline, candidate *OpenAPISpec) OpenAPIDrift {
	d := OpenAPIDrift{}
	if baseline == nil {
		baseline = &OpenAPISpec{Paths: map[string]map[string]any{}, Components: map[string]map[string]any{}}
	}
	if candidate == nil {
		candidate = &OpenAPISpec{Paths: map[string]map[string]any{}, Components: map[string]map[string]any{}}
	}
	basePaths := pathSet(baseline)
	candPaths := pathSet(candidate)
	d.PathsAdded, d.PathsRemoved = setDiff(basePaths, candPaths)

	baseMethods := methodSet(baseline)
	candMethods := methodSet(candidate)
	d.MethodsAdded, d.MethodsRemoved = setDiff(baseMethods, candMethods)

	baseSchemas := schemaSet(baseline)
	candSchemas := schemaSet(candidate)
	d.SchemasAdded, d.SchemasRemoved = setDiff(baseSchemas, candSchemas)
	return d
}

func pathSet(s *OpenAPISpec) []string {
	out := make([]string, 0, len(s.Paths))
	for p := range s.Paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func methodSet(s *OpenAPISpec) []string {
	out := []string{}
	for p, ops := range s.Paths {
		for m := range ops {
			out = append(out, strings.ToUpper(m)+" "+p)
		}
	}
	sort.Strings(out)
	return out
}

func schemaSet(s *OpenAPISpec) []string {
	schemas, ok := s.Components["schemas"]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(schemas))
	for n := range schemas {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func setDiff(a, b []string) (added, removed []string) {
	amap := map[string]bool{}
	for _, x := range a {
		amap[x] = true
	}
	bmap := map[string]bool{}
	for _, x := range b {
		bmap[x] = true
	}
	for _, x := range b {
		if !amap[x] {
			added = append(added, x)
		}
	}
	for _, x := range a {
		if !bmap[x] {
			removed = append(removed, x)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}
