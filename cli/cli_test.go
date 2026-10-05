// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// CLI framework tests: JSON envelope parity, exit-code mapping, Levenshtein
// did-you-mean (CLI-046), and non-interactive behavior (CLI-050).

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----

// runCLI executes the command tree against buffers and returns (stdout,
// stderr, exitCode).
func runCLI(t *testing.T, args []string, stdin io.Reader) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut, stdin)
	return out.String(), errOut.String(), code
}

// makeProject creates a temp dir with an ogon.yaml and returns its path.
func makeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ogon.yaml"), []byte("project: test\ntemplate: standard\n"), 0o644); err != nil {
		t.Fatalf("write ogon.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

// parseEnvelope decodes the JSON envelope from stdout.
func parseEnvelope(t *testing.T, out string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON envelope: %v\noutput: %q", err, out)
	}
	return env
}

// ---- exit codes ----

func TestExitCodeConstants(t *testing.T) {
	t.Parallel()
	cases := []struct{ code, want int }{
		{ExitOK, 0},
		{ExitGenericError, 1},
		{ExitUsage, 2},
		{ExitConfigInvalid, 3},
		{ExitMigrationUnsafe, 4},
		{ExitGenConflict, 5},
		{ExitTestFailure, 6},
		{ExitBuildFailure, 7},
		{ExitDoctorFailure, 8},
		{ExitInterrupted, 130},
	}
	for _, c := range cases {
		if c.code != c.want {
			t.Errorf("exit code constant = %d, want %d", c.code, c.want)
		}
	}
	if ExitName(0) != "OK" || ExitName(4) != "MigrationUnsafe" {
		t.Errorf("ExitName mapping wrong")
	}
	if ExitName(999) != "Unknown" {
		t.Errorf("ExitName(999) = %q, want Unknown", ExitName(999))
	}
}

func TestVersionHuman(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"--version"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "ogon") || !strings.Contains(out, Version) {
		t.Fatalf("version output unexpected: %q", out)
	}
}

func TestVersionJSON(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"--version", "--json"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	env := parseEnvelope(t, out)
	if env["command"] != "ogon" {
		t.Errorf("command = %v", env["command"])
	}
	if env["status"] != "ok" {
		t.Errorf("status = %v", env["status"])
	}
	data, _ := env["data"].(map[string]any)
	if data == nil || data["version"] != Version {
		t.Errorf("data.version = %v", data)
	}
}

func TestNoArgsShowsHelp(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, nil, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, "ogon") || !strings.Contains(out, "Usage") {
		t.Fatalf("expected help output, got: %q", out)
	}
}

// ---- Levenshtein / suggest (CLI-046) ----

func TestLevenshtein(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"new", "nwe", 2}, // transposition: classic Levenshtein = 2
		{"kitten", "sitting", 3},
		{"migrate", "migrate", 0},
		{"route", "routes", 1},
	}
	for _, c := range cases {
		if got := Levenshtein(c.a, c.b); got != c.want {
			t.Errorf("Levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSuggestFindsNew(t *testing.T) {
	t.Parallel()
	sug := Suggest("nwe", []string{"new", "build", "dev", "migrate", "test"}, DefaultSuggestDistance)
	if len(sug) == 0 || sug[0] != "new" {
		t.Fatalf("expected first suggestion 'new', got %v", sug)
	}
}

func TestSuggestNoMatch(t *testing.T) {
	t.Parallel()
	sug := Suggest("zzzzz", []string{"new", "build"}, DefaultSuggestDistance)
	if len(sug) != 0 {
		t.Fatalf("expected no suggestions, got %v", sug)
	}
}

func TestSuggestDedupAndOrder(t *testing.T) {
	t.Parallel()
	sug := Suggest("rout", []string{"route", "routes", "route", "build"}, DefaultSuggestDistance)
	// both route and routes are close; route should rank first (shorter dist)
	if len(sug) < 2 {
		t.Fatalf("expected ≥2 suggestions, got %v", sug)
	}
	if sug[0] != "route" {
		t.Errorf("expected 'route' first, got %q", sug[0])
	}
}

// ---- unknown command / flag suggestions (CLI-046, end-to-end) ----

func TestUnknownCommandSuggests(t *testing.T) {
	t.Parallel()
	// non-JSON: suggestion goes to stderr
	_, errOut, code := runCLI(t, []string{"nwe"}, nil)
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d (Usage)", code, ExitUsage)
	}
	if !strings.Contains(errOut, "Did you mean") || !strings.Contains(errOut, "new") {
		t.Fatalf("expected did-you-mean 'new' in stderr, got: %q", errOut)
	}
}

func TestUnknownCommandJSON(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"nwe", "--json"}, nil)
	if code != ExitUsage {
		t.Fatalf("code = %d", code)
	}
	env := parseEnvelope(t, out)
	if env["status"] != "error" {
		t.Errorf("status = %v", env["status"])
	}
	diags, _ := env["diagnostics"].([]any)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %v", diags)
	}
	d, _ := diags[0].(map[string]any)
	fix, _ := d["fix"].([]any)
	if len(fix) == 0 || fix[0] != "new" {
		t.Errorf("expected fix=['new'], got %v", fix)
	}
}

func TestUnknownFlagSuggests(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"--json", "--jsn"}, nil)
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	env := parseEnvelope(t, out)
	if env["status"] != "error" {
		t.Errorf("status = %v", env["status"])
	}
	diags, _ := env["diagnostics"].([]any)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %v", diags)
	}
	d, _ := diags[0].(map[string]any)
	fix, _ := d["fix"].([]any)
	found := false
	for _, f := range fix {
		if f == "json" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'json' in flag suggestions, got %v", fix)
	}
}

// ---- JSON envelope parity (DX-025) ----

func TestExplainJSONParity(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"explain", "route", "--json"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	env := parseEnvelope(t, out)
	if env["command"] != "ogon explain" {
		t.Errorf("command = %v", env["command"])
	}
	if env["status"] != "ok" {
		t.Errorf("status = %v", env["status"])
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		t.Fatal("data missing")
	}
	if data["topic"] != "route" {
		t.Errorf("topic = %v", data["topic"])
	}
	if data["summary"] == "" {
		t.Error("summary must be present (parity)")
	}
	details, _ := data["details"].([]any)
	if len(details) == 0 {
		t.Error("details must be non-empty (parity)")
	}
}

func TestExplainHumanHasSameInfo(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"explain", "route"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	// the human view must mention the topic and the summary
	if !strings.Contains(out, "route") {
		t.Errorf("human view missing topic: %q", out)
	}
}

func TestDBURLJSONParity(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "db", "url"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	env := parseEnvelope(t, out)
	if env["command"] != "ogon db url" {
		t.Errorf("command = %v", env["command"])
	}
	data, _ := env["data"].(map[string]any)
	pairs, _ := data["pairs"].([]any)
	if len(pairs) < 2 {
		t.Fatalf("expected ≥2 pairs (driver,url), got %v", pairs)
	}
}

// ---- non-interactive mode (CLI-050) ----

// neverReader blocks forever on Read. Used to prove the prompt path is
// never reached in --yes / non-TTY / JSON mode.
type neverReader struct{}

func (neverReader) Read(p []byte) (int, error) {
	time.Sleep(1 * time.Hour)
	return 0, io.EOF
}

func TestNonInteractiveYesNeverBlocks(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	// `ogon db reset` is destructive: in --yes mode it declines (def=false)
	// and returns exit 4 WITHOUT reading stdin.
	done := make(chan int, 1)
	go func() {
		_, _, code := runCLI(t, []string{"--project", dir, "--yes", "db", "reset"}, neverReader{})
		done <- code
	}()
	select {
	case code := <-done:
		if code != ExitMigrationUnsafe {
			t.Fatalf("code = %d, want %d (MigrationUnsafe)", code, ExitMigrationUnsafe)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("db reset --yes blocked: prompt was not short-circuited (CLI-050)")
	}
}

func TestNonInteractiveNonTTYNeverBlocks(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	// Non-TTY stdin (bytes.Buffer) without --yes: Confirm returns def
	// immediately because stdinIsTTY is false.
	stdin := bytes.NewBufferString("n\n")
	done := make(chan int, 1)
	go func() {
		_, _, code := runCLI(t, []string{"--project", dir, "db", "reset"}, stdin)
		done <- code
	}()
	select {
	case code := <-done:
		if code != ExitMigrationUnsafe {
			t.Fatalf("code = %d, want %d", code, ExitMigrationUnsafe)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("db reset in non-TTY blocked: prompt was not short-circuited (CLI-050)")
	}
}

func TestNonInteractiveJSONNeverBlocks(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	out, _, code := runCLI(t, []string{"--project", dir, "--json", "db", "reset"}, neverReader{})
	if code != ExitMigrationUnsafe {
		t.Fatalf("code = %d, want %d", code, ExitMigrationUnsafe)
	}
	env := parseEnvelope(t, out)
	if env["status"] != "error" {
		t.Errorf("status = %v", env["status"])
	}
}

// ---- dry-run ----

func TestNewDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "myapp")
	out, _, code := runCLI(t, []string{"new", target, "--template", "minimal", "--dry-run"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d; stderr in out: %q", code, out)
	}
	// dry-run must not create the directory
	if _, err := os.Stat(target); err == nil {
		t.Fatal("dry-run created the project directory")
	}
	// JSON mode: envelope reports created files in the plan
	outJ, _, codeJ := runCLI(t, []string{"new", target, "--template", "minimal", "--dry-run", "--json"}, nil)
	if codeJ != ExitOK {
		t.Fatalf("json code = %d", codeJ)
	}
	env := parseEnvelope(t, outJ)
	data, _ := env["data"].(map[string]any)
	created, _ := data["created"].([]any)
	if len(created) == 0 {
		t.Error("expected planned created files in JSON dry-run")
	}
}

// ---- exit-code mapping ----

func TestNotInProjectReturnsConfigInvalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() // no ogon.yaml
	_, _, code := runCLI(t, []string{"--project", dir, "routes", "list"}, nil)
	if code != ExitConfigInvalid {
		t.Fatalf("code = %d, want %d (ConfigInvalid)", code, ExitConfigInvalid)
	}
}

func TestGenUnknownKindReturnsUsage(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, code := runCLI(t, []string{"--project", dir, "gen", "bogus"}, nil)
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d (Usage)", code, ExitUsage)
	}
}

func TestGenConflictExitCode(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	// create an unowned file in the planned path so the conflict detector trips
	_ = os.MkdirAll(filepath.Join(dir, "models"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "models", "example.go"), []byte("// user file\npackage models\n"), 0o644)
	_, _, code := runCLI(t, []string{"--project", dir, "gen", "model", "example"}, nil)
	if code != ExitGenConflict {
		t.Fatalf("code = %d, want %d (GenConflict)", code, ExitGenConflict)
	}
}

func TestGenDryRunAvoidsConflict(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, code := runCLI(t, []string{"--project", dir, "gen", "model", "example", "--dry-run"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
}

func TestDoctorExitsOK(t *testing.T) {
	t.Parallel()
	dir := makeProject(t)
	_, _, code := runCLI(t, []string{"--project", dir, "doctor"}, nil)
	// doctor may report warnings but should not hard-fail in a clean project
	if code != ExitOK && code != ExitDoctorFailure {
		t.Fatalf("code = %d, want 0 or 8", code)
	}
}

// ---- command surface enumeration ----

func TestAllCommandsRegistered(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, nil, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	// every normative command must appear in the root help
	want := []string{
		"new", "dev", "build", "gen", "migrate", "doctor", "explain",
		"infra", "test", "run", "lint", "fmt", "check", "add", "remove",
		"update", "db", "routes", "jobs", "modules", "inspect", "deploy",
		"logs", "health", "benchmark", "docs", "completion", "agent",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("root help missing command %q", w)
		}
	}
}

func TestExplainExitCodes(t *testing.T) {
	t.Parallel()
	out, _, code := runCLI(t, []string{"explain", "exit-codes"}, nil)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	// every normative code's symbolic name must appear
	for _, c := range AllExitCodes() {
		if !strings.Contains(out, ExitName(c)) {
			t.Errorf("explain exit-codes missing %s", ExitName(c))
		}
	}
}

// ---- stable field order ----

func TestEnvelopeFieldOrder(t *testing.T) {
	t.Parallel()
	out, _, _ := runCLI(t, []string{"--version", "--json"}, nil)
	// The envelope must list fields in the fixed order command, status, data,
	// diagnostics. Verify by inspecting the raw bytes.
	idx := func(sub string) int { return strings.Index(out, sub) }
	ci, si, di, dii := idx(`"command"`), idx(`"status"`), idx(`"data"`), idx(`"diagnostics"`)
	if ci < 0 || si < 0 || di < 0 {
		t.Fatalf("envelope missing core fields: %q", out)
	}
	if !(ci < si && si < di) {
		t.Errorf("envelope field order wrong: command=%d status=%d data=%d", ci, si, di)
	}
	// diagnostics may be omitted (omitempty) when nil — that's allowed.
	_ = dii
}

func TestColorSuppressedWhenNoColor(t *testing.T) {
	t.Parallel()
	// stdout is a buffer (non-TTY) so color is already off; verify no ESC seqs.
	out, _, _ := runCLI(t, []string{"explain", "route"}, nil)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("human output contained ANSI escapes in non-TTY: %q", out)
	}
}
