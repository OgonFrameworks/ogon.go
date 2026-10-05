// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// CLI integration tests for the DI codegen wiring (ogon build, ogon explain
// di) and the modules subcommands (ogon modules list|add|update|search).

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeDIProject creates a temp project with an app/services/ dir containing
// a single UserService constructor, plus a minimal main.go so `go build .`
// succeeds (the build pipeline runs `go build -o bin/service .` after DI
// codegen).
func makeDIProject(t *testing.T) string {
	t.Helper()
	dir := makeProject(t)
	servicesDir := filepath.Join(dir, "app", "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatalf("mkdir services: %v", err)
	}
	if err := os.WriteFile(filepath.Join(servicesDir, "user.go"), []byte(`package services

type UserService struct{}

func NewUserService() *UserService { return &UserService{} }
`), 0o644); err != nil {
		t.Fatalf("write user.go: %v", err)
	}
	// Minimal main so `go build .` succeeds in the test project.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

func main() {}
`), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return dir
}

func TestBuildEmitsDIGenFile(t *testing.T) {
	t.Parallel()
	dir := makeDIProject(t)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "build"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0; out=%s", code, out)
	}
	diGen := filepath.Join(dir, "generated", "di", "di_gen.go")
	if _, err := os.Stat(diGen); err != nil {
		t.Fatalf("di_gen.go not written: %v", err)
	}
	data, err := os.ReadFile(diGen)
	if err != nil {
		t.Fatalf("read di_gen.go: %v", err)
	}
	if !bytes.Contains(data, []byte("func (c *Container) UserService()")) {
		t.Errorf("di_gen.go missing UserService accessor:\n%s", data)
	}
}

func TestBuildNoServicesSkipsDI(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	// Add a minimal main so the build step succeeds.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "build"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0; out=%s", code, out)
	}
	env := parseEnvelope(t, out)
	data, _ := env["data"].(map[string]any)
	di, _ := data["di"].(map[string]any)
	if di == nil {
		t.Fatalf("missing di summary in build envelope: %v", data)
	}
	if skipped, _ := di["skipped"].(bool); !skipped {
		t.Errorf("di.skipped = %v, want true", di["skipped"])
	}
	// No di_gen.go should exist.
	if _, err := os.Stat(filepath.Join(dir, "generated", "di", "di_gen.go")); err == nil {
		t.Errorf("di_gen.go written despite no services dir")
	}
}

func TestExplainDIRendersGraph(t *testing.T) {
	t.Parallel()
	dir := makeDIProject(t)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "explain", "di"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0; out=%s", code, out)
	}
	env := parseEnvelope(t, out)
	data, _ := env["data"].(map[string]any)
	graph, _ := data["graph"].(string)
	if !strings.Contains(graph, "UserService") {
		t.Errorf("graph missing UserService: %q", graph)
	}
}

func TestExplainDIDotFlag(t *testing.T) {
	t.Parallel()
	dir := makeDIProject(t)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "explain", "di", "--dot"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0; out=%s", code, out)
	}
	env := parseEnvelope(t, out)
	data, _ := env["data"].(map[string]any)
	dot, _ := data["dot"].(string)
	if !strings.HasPrefix(dot, "digraph di {") {
		t.Errorf("dot output missing digraph header: %q", dot)
	}
}

// ---- modules subcommands ----

func TestModulesListEmpty(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "modules", "list"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0; out=%s", code, out)
	}
	env := parseEnvelope(t, out)
	data, _ := env["data"].(map[string]any)
	modules, _ := data["modules"].([]any)
	if len(modules) != 0 {
		t.Errorf("modules = %v, want empty", modules)
	}
}

func TestModulesAddWritesLockfile(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, code := runCLI(t, []string{"--project", dir, "modules", "add", "ogon-stripe", "--version", "1.0.0"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	lockPath := filepath.Join(dir, "ogon.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	if !bytes.Contains(data, []byte("ogon-stripe")) {
		t.Errorf("lockfile missing ogon-stripe:\n%s", data)
	}
}

func TestModulesAddDryRunDoesNotWrite(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, code := runCLI(t, []string{"--project", dir, "modules", "add", "ogon-stripe", "--dry-run"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "ogon.lock")); err == nil {
		t.Errorf("lockfile written in dry-run mode")
	}
}

func TestModulesListAfterAdd(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, _ = runCLI(t, []string{"--project", dir, "modules", "add", "ogon-stripe", "--version", "1.0.0"}, nil)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "modules", "list"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	env := parseEnvelope(t, out)
	data, _ := env["data"].(map[string]any)
	modules, _ := data["modules"].([]any)
	if len(modules) != 1 {
		t.Fatalf("modules = %v, want 1", modules)
	}
	m, _ := modules[0].(map[string]any)
	if m["name"] != "ogon-stripe" {
		t.Errorf("name = %v", m["name"])
	}
}

func TestModulesUpdateSpecificModule(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, _ = runCLI(t, []string{"--project", dir, "modules", "add", "ogon-stripe", "--version", "1.0.0"}, nil)
	_, _, code := runCLI(t, []string{"--project", dir, "modules", "update", "ogon-stripe", "--to", "1.1.0"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	// Verify the lockfile was updated.
	data, _ := os.ReadFile(filepath.Join(dir, "ogon.lock"))
	if !bytes.Contains(data, []byte("1.1.0")) {
		t.Errorf("lockfile not updated to 1.1.0:\n%s", data)
	}
}

func TestModulesUpdateUnknownFails(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, _ = runCLI(t, []string{"--project", dir, "modules", "add", "ogon-stripe"}, nil)
	_, _, code := runCLI(t, []string{"--project", dir, "modules", "update", "nonexistent", "--to", "1.0.0"}, nil)
	if code != ExitConfigInvalid {
		t.Errorf("code = %d, want %d (ConfigInvalid)", code, ExitConfigInvalid)
	}
}

func TestModulesSearchReturnsResults(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"--json", "modules", "search", "stripe"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	env := parseEnvelope(t, out)
	data, _ := env["data"].(map[string]any)
	modules, _ := data["modules"].([]any)
	if len(modules) == 0 {
		t.Errorf("no search results for 'stripe'")
	}
	// Verify the first result has a name field.
	first, _ := modules[0].(map[string]any)
	if _, ok := first["name"].(string); !ok {
		t.Errorf("first result missing name: %v", first)
	}
}

func TestModulesSearchEmptyReturnsAll(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"--json", "modules", "search", ""}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	env := parseEnvelope(t, out)
	data, _ := env["data"].(map[string]any)
	modules, _ := data["modules"].([]any)
	if len(modules) == 0 {
		t.Errorf("expected all modules for empty query")
	}
}

func TestModulesListJSONShape(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, _ = runCLI(t, []string{"--project", dir, "modules", "add", "ogon-stripe"}, nil)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "modules", "list"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	// Verify the envelope shape is stable (command, status, data).
	env := parseEnvelope(t, out)
	if env["command"] != "ogon modules list" {
		t.Errorf("command = %v", env["command"])
	}
	if env["status"] != "ok" {
		t.Errorf("status = %v", env["status"])
	}
	// data.modules is a non-empty array.
	data, _ := env["data"].(map[string]any)
	if _, ok := data["modules"]; !ok {
		t.Errorf("data.modules missing: %v", data)
	}
}

// Verify the structured payload can be JSON-decoded into a typed struct.
func TestModulesListDecodesToStruct(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, _ = runCLI(t, []string{"--project", dir, "modules", "add", "ogon-stripe"}, nil)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "modules", "list"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var env struct {
		Command string `json:"command"`
		Status  string `json:"status"`
		Data    struct {
			Modules []struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				Source  string `json:"source,omitempty"`
			} `json:"modules"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Modules) != 1 {
		t.Fatalf("modules = %d, want 1", len(env.Data.Modules))
	}
	if env.Data.Modules[0].Name != "ogon-stripe" {
		t.Errorf("name = %q", env.Data.Modules[0].Name)
	}
}
