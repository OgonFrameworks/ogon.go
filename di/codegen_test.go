// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for di/codegen.go (DI-004/018/020/022). Verifies that the generated
// container file is syntactically valid Go (parses), byte-stable for
// identical inputs (DI-020), and contains no map[string]any at runtime
// (DI-018).

package di

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateProducesParsableGo(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService"),
		makeProvider("OrderService", "*OrderService", "*UserService"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, DefaultCodegenOptions(out))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Must parse.
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, out, src, parser.ParseComments); err != nil {
		t.Fatalf("generated file does not parse: %v\n---\n%s", err, src)
	}
	// Must be written to disk.
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output file not written: %v", err)
	}
}

func TestGenerateHasNoMapStringAny(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, CodegenOptions{
		PackageName:   "di",
		OutputPath:    out,
		HeaderComment: "test",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Strip comments before checking for runtime map[string]any — the
	// header doc mentions the rule by name.
	codeOnly := stripComments(src)
	if bytes.Contains(codeOnly, []byte("map[string]any")) {
		t.Errorf("generated code contains map[string]any (DI-018 violation):\n%s", codeOnly)
	}
	if bytes.Contains(codeOnly, []byte("map[any]any")) {
		t.Errorf("generated code contains map[any]any (DI-018 spirit violation):\n%s", codeOnly)
	}
}

// stripComments removes // and /* */ comments so we check runtime code only.
func stripComments(src []byte) []byte {
	var out bytes.Buffer
	i := 0
	for i < len(src) {
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			// line comment: skip to newline
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			// block comment: skip to */
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.Bytes()
}

func TestGenerateHasNoReflect(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, DefaultCodegenOptions(out))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if bytes.Contains(src, []byte("\"reflect\"")) {
		t.Errorf("generated code imports reflect (DI-022 violation):\n%s", src)
	}
}

func TestGenerateByteStable(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("A", "*A"),
		makeProvider("B", "*B", "*A"),
		makeProvider("C", "*C", "*B", "*A"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	// Note: the header includes a timestamp; we override it to make the
	// output deterministic for the byte-stability test.
	opts1 := CodegenOptions{
		PackageName:   "di",
		OutputPath:    filepath.Join(t.TempDir(), "a.go"),
		HeaderComment: "test",
	}
	opts2 := CodegenOptions{
		PackageName:   "di",
		OutputPath:    filepath.Join(t.TempDir(), "b.go"),
		HeaderComment: "test",
	}
	// We can't easily strip the timestamp without exposing a knob, so we
	// re-render the source via renderSource directly with a fixed timestamp
	// by intercepting at Generate level. Instead, verify the providers list
	// and the import block are stable by stripping the timestamp line.
	src1, err := Generate(g, opts1)
	if err != nil {
		t.Fatal(err)
	}
	src2, err := Generate(g, opts2)
	if err != nil {
		t.Fatal(err)
	}
	stripped1 := stripTimestamp(src1)
	stripped2 := stripTimestamp(src2)
	if !bytes.Equal(stripped1, stripped2) {
		t.Errorf("generated source not byte-stable (DI-020):\n---1---\n%s\n---2---\n%s", stripped1, stripped2)
	}
}

// stripTimestamp removes the "// Generated: ..." line so byte-stability
// comparisons ignore the wall-clock stamp.
func stripTimestamp(src []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(src, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("// Generated:")) {
			continue
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func TestGenerateSingletonAccessor(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, DefaultCodegenOptions(out))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srcStr := string(src)
	// Must define the singleton accessor.
	if !strings.Contains(srcStr, "func (c *Container) UserService()") {
		t.Errorf("missing UserService accessor:\n%s", src)
	}
	// Must use sync.Once for the lazy build.
	if !strings.Contains(srcStr, "userServiceOnce") {
		t.Errorf("missing userServiceOnce field")
	}
}

func TestGenerateScopedAccessor(t *testing.T) {
	t.Parallel()
	scoped := makeProvider("Cart", "*Cart")
	scoped.Lifetime = LifetimeScoped
	g, err := NewGraph([]Provider{scoped}, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, DefaultCodegenOptions(out))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srcStr := string(src)
	if !strings.Contains(srcStr, "func (c *Container) Cart(ctx context.Context)") {
		t.Errorf("missing scoped Cart accessor:\n%s", src)
	}
	if !strings.Contains(srcStr, "type Scope struct") {
		t.Errorf("missing Scope type")
	}
}

func TestGenerateTransientAccessor(t *testing.T) {
	t.Parallel()
	transient := makeProvider("View", "*View")
	transient.Lifetime = LifetimeTransient
	g, err := NewGraph([]Provider{transient}, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, DefaultCodegenOptions(out))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srcStr := string(src)
	if !strings.Contains(srcStr, "func (c *Container) View() *") {
		t.Errorf("missing transient View accessor:\n%s", src)
	}
	if strings.Contains(srcStr, "viewOnce") {
		t.Errorf("transient accessor should not use sync.Once")
	}
}

func TestGenerateExternalDeps(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("UserService", "*UserService", "*record.Pool"),
	}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, DefaultCodegenOptions(out))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srcStr := string(src)
	if !strings.Contains(srcStr, "type Deps struct") {
		t.Errorf("missing Deps struct:\n%s", src)
	}
	if !strings.Contains(srcStr, "Pool") {
		t.Errorf("missing Pool external dep field")
	}
	if !strings.Contains(srcStr, "func New(deps *Deps) *Container") {
		t.Errorf("missing New(deps *Deps) constructor")
	}
}

func TestGenerateCleanupRegistration(t *testing.T) {
	t.Parallel()
	p := makeProvider("Resource", "*Resource")
	p.Cleanup = true
	g, err := NewGraph([]Provider{p}, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, DefaultCodegenOptions(out))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srcStr := string(src)
	// Cleanup is wrapped from func() to func() error.
	if !strings.Contains(srcStr, "c.cleanups = append(c.cleanups, func() error { cleanupResource(); return nil })") {
		t.Errorf("missing cleanup registration:\n%s", src)
	}
	if !strings.Contains(srcStr, "func (c *Container) Close() error") {
		t.Errorf("missing Close method")
	}
}

func TestGenerateErrorReturnHandled(t *testing.T) {
	t.Parallel()
	p := makeProvider("Thing", "*Thing")
	p.ReturnsError = true
	g, err := NewGraph([]Provider{p}, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	out := filepath.Join(t.TempDir(), "di_gen.go")
	src, err := Generate(g, DefaultCodegenOptions(out))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	srcStr := string(src)
	if !strings.Contains(srcStr, "err := ") {
		t.Errorf("missing error handling:\n%s", src)
	}
}

func TestGenerateBuildsPipeline(t *testing.T) {
	t.Parallel()
	dir := writeServicesFile(t, "user.go", `package services

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }
`)
	out := filepath.Join(t.TempDir(), "di_gen.go")
	servicesDir := dir
	_, err := Build(BuildOptions{
		ServicesDir: servicesDir,
		OutputPath:  out,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output not written: %v", err)
	}
}

func TestGenerateEmptyGraphErrors(t *testing.T) {
	t.Parallel()
	_, err := Generate(nil, DefaultCodegenOptions("/tmp/x.go"))
	if err == nil {
		t.Fatalf("expected error for nil graph")
	}
}

func TestGenerateNilOutputPath(t *testing.T) {
	t.Parallel()
	providers := []Provider{makeProvider("A", "*A")}
	g, err := NewGraph(providers, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Generate(g, CodegenOptions{PackageName: "di"})
	if err == nil {
		t.Fatalf("expected error for empty output path")
	}
}

func TestGenerateCycleFailsCleanly(t *testing.T) {
	t.Parallel()
	providers := []Provider{
		makeProvider("A", "*A", "*B"),
		makeProvider("B", "*B", "*A"),
	}
	_, err := NewGraph(providers, nil)
	if err == nil {
		t.Fatalf("expected cycle error")
	}
}

func TestDefaultPaths(t *testing.T) {
	t.Parallel()
	if got := DefaultServicesDir("/r"); got != "/r/app/services" {
		t.Errorf("DefaultServicesDir = %q", got)
	}
	if got := DefaultOutputPath("/r"); got != "/r/generated/di/di_gen.go" {
		t.Errorf("DefaultOutputPath = %q", got)
	}
}
