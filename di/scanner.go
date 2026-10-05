// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// di/scanner.go: AST constructor scanner (DI-001). Finds `func NewX(deps) X`
// constructors under app/services and emits Provider descriptors used by the
// graph builder and code generator. AST-only — no reflection (DI-022).

package di

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Lifetime is the lifecycle category of a provider (DI-005).
type Lifetime int

const (
	// LifetimeSingleton = one instance per container (app).
	LifetimeSingleton Lifetime = iota
	// LifetimeScoped = one instance per request/job/live-event.
	LifetimeScoped
	// LifetimeTransient = fresh instance per call.
	LifetimeTransient
)

// String returns the lowercase name used in diagnostics and explain output.
func (l Lifetime) String() string {
	switch l {
	case LifetimeSingleton:
		return "singleton"
	case LifetimeScoped:
		return "scoped"
	case LifetimeTransient:
		return "transient"
	}
	return "unknown"
}

// ParseLifetime maps a directive token to a Lifetime. Unknown values return
// LifetimeSingleton and the bool is false.
func ParseLifetime(s string) (Lifetime, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "singleton", "":
		return LifetimeSingleton, true
	case "scoped":
		return LifetimeScoped, true
	case "transient":
		return LifetimeTransient, true
	}
	return LifetimeSingleton, false
}

// TypeRef describes a Go type referenced by a constructor signature. The
// scanner records the import path and the type expression as written so the
// codegen can re-emit the correct selector form.
type TypeRef struct {
	// PkgPath is the import path of the package the type belongs to. Empty
	// for builtins or types defined in the same package as the constructor.
	PkgPath string
	// PkgName is the package name (last path component) or the alias used
	// in the source file. Used by codegen to emit a selector expression.
	PkgName string
	// Name is the bare type identifier (e.g. "UserService", "Pool").
	Name string
	// Expr is the source-level type expression rendered via go/printer
	// (e.g. "*services.UserService", "Config", "[]byte").
	Expr string
}

// IsSamePackage reports whether the type lives in the same package as the
// constructor (i.e. no selector in source).
func (t TypeRef) IsSamePackage() bool { return t.PkgPath == "" }

// Provider is a constructor descriptor produced by the scanner.
type Provider struct {
	// Name is the logical provider name (e.g. "UserService" derived from
	// NewUserService).
	Name string
	// FuncName is the constructor function name (e.g. "NewUserService").
	FuncName string
	// PkgPath is the import path of the package containing the constructor.
	PkgPath string
	// PkgName is the package name (last path component) of the constructor.
	PkgName string
	// Provides is the type the constructor returns (its primary product).
	Provides TypeRef
	// Requires are the parameter types the constructor needs.
	Requires []TypeRef
	// Lifetime is the provider lifetime (default singleton).
	Lifetime Lifetime
	// File is the absolute path of the source file.
	File string
	// Line is the 1-based line of the constructor declaration.
	Line int
	// Cleanup is true when the constructor returns a cleanup function
	// (signature `(T, func(), error)` or `(T, func())`).
	Cleanup bool
	// CleanupReturnsError is true when the cleanup function returns an
	// error (signature `func() error`). When false, cleanup is `func()`.
	CleanupReturnsError bool
	// ReturnsError is true when the constructor returns an error as the
	// last result.
	ReturnsError bool
	// Named marks providers explicitly registered via ogon.Provide (DI-007)
	// or test-builder overrides (DI-019). Generated providers have Named=false.
	Named bool
}

// Position returns a "file:line" string for diagnostics.
func (p Provider) Position() string {
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// constructorNameRe matches `New<X>` where X starts with an uppercase letter.
var constructorNameRe = regexp.MustCompile(`^New([A-Z][A-Za-z0-9_]*)$`)

// lifetimeDirectiveRe matches the `//ogon:di lifetime=scoped` comment.
var lifetimeDirectiveRe = regexp.MustCompile(`ogon:di\s+lifetime=(\w+)`)

// ScanDir walks dir recursively, parses every non-test Go file, and returns
// the Provider descriptors for every `func NewX(...) ...` constructor found.
// Subdirectories are scanned as well, allowing multi-package services trees.
//
// The constructor pattern (DI-001) is `func NewX(deps...) X` where X starts
// with an uppercase letter. Constructors may also return `(X, error)` or
// `(X, func(), error)` (cleanup, DI-011).
func ScanDir(dir string) ([]Provider, error) {
	fset := token.NewFileSet()
	var providers []Provider
	visitErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			base := d.Name()
			if path != dir && (base == "testdata" || base == "vendor" || strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileProviders, err := scanFile(fset, path)
		if err != nil {
			return err
		}
		providers = append(providers, fileProviders...)
		return nil
	})
	if visitErr != nil {
		return nil, visitErr
	}
	sortProviders(providers)
	return providers, nil
}

// sortProviders returns a deterministic order regardless of filesystem walk
// order — important for byte-stable codegen (DI-020).
func sortProviders(ps []Provider) {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].PkgPath != ps[j].PkgPath {
			return ps[i].PkgPath < ps[j].PkgPath
		}
		if ps[i].File != ps[j].File {
			return ps[i].File < ps[j].File
		}
		if ps[i].Line != ps[j].Line {
			return ps[i].Line < ps[j].Line
		}
		return ps[i].FuncName < ps[j].FuncName
	})
}

// scanFile parses a single Go file and returns the providers declared in it.
func scanFile(fset *token.FileSet, path string) ([]Provider, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("di: read %s: %w", path, err)
	}
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("di: parse %s: %w", path, err)
	}
	pkgPath := packagePath(file, path)
	imports := importMap(file)
	var providers []Provider
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue // skip methods
		}
		if fn.Body == nil {
			continue // stub declaration (e.g. assembly)
		}
		m := constructorNameRe.FindStringSubmatch(fn.Name.Name)
		if m == nil {
			continue
		}
		p, err := buildProvider(fset, fn, m[1], path, pkgPath, imports)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, nil
}

// packagePath derives a Go import path for the file from its directory and the
// module root. We don't run `go list` (codegen stays offline); the path is
// only used for diagnostics and stable sort. When the file lives inside a
// `go.mod` directory, the module path is resolved by walking up.
func packagePath(file *ast.File, absPath string) string {
	if file.Name != nil && file.Name.Name != "" {
		// Best-effort: use the directory path relative to the nearest go.mod.
		if modPath, ok := moduleRootDir(absPath); ok {
			rel, err := filepath.Rel(modPath.dir, filepath.Dir(absPath))
			if err == nil && rel != "." {
				return modPath.module + "/" + filepath.ToSlash(rel)
			}
			return modPath.module
		}
		// Fall back to directory base name (still useful for diagnostics).
		return filepath.Base(filepath.Dir(absPath))
	}
	return ""
}

// moduleRoot walks up from a file path looking for go.mod; returns the module
// path declared there and the directory that contains it.
func moduleRootDir(p string) (struct{ module, dir string }, bool) {
	dir := filepath.Dir(p)
	for i := 0; i < 32; i++ {
		mod := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(mod); err == nil {
			return struct{ module, dir string }{module: parseModulePath(data), dir: dir}, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return struct{ module, dir string }{}, false
}

// parseModulePath reads the `module <path>` line from go.mod contents. Returns
// empty string if not found.
func parseModulePath(data []byte) string {
	for _, line := range bytes.Split(data, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if strings.HasPrefix(s, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(s, "module"))
		}
	}
	return ""
}

// importMap builds alias → import path lookup from the file's import block.
// Imports without an explicit alias use their package name as the alias.
func importMap(file *ast.File) map[string]string {
	out := make(map[string]string, len(file.Imports))
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		} else {
			alias = filepath.Base(path) // default alias = package name
		}
		out[alias] = path
	}
	return out
}

// buildProvider constructs a Provider descriptor from an *ast.FuncDecl.
func buildProvider(fset *token.FileSet, fn *ast.FuncDecl, shortName, file, pkgPath string, imports map[string]string) (Provider, error) {
	if fn.Type == nil || fn.Type.Results == nil {
		return Provider{}, fmt.Errorf("di: constructor %s has no results at %s", fn.Name.Name, fset.Position(fn.Pos()))
	}
	results := fn.Type.Results.List
	provides, cleanup, cleanupErrRet, returnsErr, err := extractProvides(fset, fn.Name.Name, results, imports, pkgPath)
	if err != nil {
		return Provider{}, err
	}
	requires, err := extractRequires(fset, fn.Name.Name, fn.Type.Params, imports, pkgPath)
	if err != nil {
		return Provider{}, err
	}
	pos := fset.Position(fn.Pos())
	p := Provider{
		Name:                shortName,
		FuncName:            fn.Name.Name,
		PkgPath:             pkgPath,
		PkgName:             pkgNameOf(pkgPath),
		Provides:            provides,
		Requires:            requires,
		Lifetime:            LifetimeSingleton,
		File:                file,
		Line:                pos.Line,
		Cleanup:             cleanup,
		CleanupReturnsError: cleanupErrRet,
		ReturnsError:        returnsErr,
	}
	// Lifetime directive from the doc comment.
	if fn.Doc != nil {
		for _, c := range fn.Doc.List {
			if m := lifetimeDirectiveRe.FindStringSubmatch(c.Text); m != nil {
				if lt, ok := ParseLifetime(m[1]); ok {
					p.Lifetime = lt
				}
			}
		}
	}
	return p, nil
}

// extractProvides inspects the result list and returns the provided type plus
// flags for cleanup / error returns. Acceptable shapes:
//   - X
//   - (X, error)
//   - (X, func())
//   - (X, func(), error)
//   - (X, func() error)
//   - (X, func() error, error)
//
// Returns: provides, cleanup, cleanupReturnsError, returnsError, err.
func extractProvides(fset *token.FileSet, fnName string, results []*ast.Field, imports map[string]string, ctorPkg string) (TypeRef, bool, bool, bool, error) {
	// Flatten: a field can have multiple names but for constructors that's
	// unusual; we collect the underlying type expressions.
	var types []ast.Expr
	for _, f := range results {
		if len(f.Names) == 0 {
			types = append(types, f.Type)
			continue
		}
		for range f.Names {
			types = append(types, f.Type)
		}
	}
	if len(types) == 0 {
		return TypeRef{}, false, false, false, fmt.Errorf("di: constructor %s returns no values at %s", fnName, fset.Position(results[0].Pos()))
	}
	provides := refFromExpr(types[0], imports, ctorPkg)
	cleanup := false
	cleanupErr := false
	returnsErr := false
	for _, t := range types[1:] {
		switch {
		case isFuncType(t):
			cleanup = true
			// Inspect the func type's results to see if it returns an error.
			if ft, ok := t.(*ast.FuncType); ok && ft.Results != nil {
				for _, r := range ft.Results.List {
					if isErrorType(r.Type) {
						cleanupErr = true
						break
					}
				}
			}
		case isErrorType(t):
			returnsErr = true
		default:
			return TypeRef{}, false, false, false, fmt.Errorf("di: constructor %s has unsupported return at %s", fnName, fset.Position(t.Pos()))
		}
	}
	return provides, cleanup, cleanupErr, returnsErr, nil
}

// extractRequires returns the parameter types of a constructor.
func extractRequires(fset *token.FileSet, fnName string, params *ast.FieldList, imports map[string]string, ctorPkg string) ([]TypeRef, error) {
	if params == nil {
		return nil, nil
	}
	var out []TypeRef
	for _, p := range params.List {
		if len(p.Names) == 0 {
			out = append(out, refFromExpr(p.Type, imports, ctorPkg))
			continue
		}
		for range p.Names {
			out = append(out, refFromExpr(p.Type, imports, ctorPkg))
		}
	}
	_ = fnName
	_ = fset
	return out, nil
}

// refFromExpr renders an ast.Expr into a TypeRef. Selectors (`pkg.Type`) are
// resolved against the import map to discover PkgPath; bare identifiers are
// treated as same-package types.
func refFromExpr(expr ast.Expr, imports map[string]string, ctorPkg string) TypeRef {
	rendered := renderExpr(expr)
	ref := TypeRef{Expr: rendered}
	switch t := expr.(type) {
	case *ast.Ident:
		ref.Name = t.Name
	case *ast.SelectorExpr:
		if pkg, ok := t.X.(*ast.Ident); ok {
			ref.PkgName = pkg.Name
			ref.PkgPath = imports[pkg.Name]
			ref.Name = t.Sel.Name
		}
	case *ast.StarExpr:
		inner := refFromExpr(t.X, imports, ctorPkg)
		ref.PkgPath = inner.PkgPath
		ref.PkgName = inner.PkgName
		ref.Name = "*" + inner.Name
	case *ast.ArrayType:
		inner := refFromExpr(t.Elt, imports, ctorPkg)
		ref.PkgPath = inner.PkgPath
		ref.PkgName = inner.PkgName
		ref.Name = "[]" + inner.Name
	case *ast.MapType:
		ref.Name = rendered
	case *ast.ChanType:
		ref.Name = rendered
	case *ast.FuncType:
		ref.Name = rendered
	case *ast.IndexExpr:
		inner := refFromExpr(t.X, imports, ctorPkg)
		ref.PkgPath = inner.PkgPath
		ref.PkgName = inner.PkgName
		ref.Name = inner.Name
	}
	// The constructor's own package types have no selector in source. Mark
	// them with the ctor's pkg path so codegen can qualify them.
	if _, ok := expr.(*ast.Ident); ok && ctorPkg != "" {
		ref.PkgPath = ctorPkg
		ref.PkgName = pkgNameOf(ctorPkg)
	}
	return ref
}

// renderExpr returns the source-level text of an AST expression.
func renderExpr(expr ast.Expr) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, token.NewFileSet(), expr)
	return b.String()
}

// isErrorType reports whether an expression is the `error` builtin interface.
func isErrorType(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "error"
}

// isFuncType reports whether an expression is a `func()` (cleanup) type.
func isFuncType(expr ast.Expr) bool {
	_, ok := expr.(*ast.FuncType)
	return ok
}

// pkgNameOf returns the last path component of a Go import path.
func pkgNameOf(pkgPath string) string {
	if pkgPath == "" {
		return ""
	}
	parts := strings.Split(pkgPath, "/")
	return parts[len(parts)-1]
}

// ErrNoProviders is returned by Generate when no providers were found.
var ErrNoProviders = errors.New("di: no providers found")
