// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the config package: precedence, interpolation, unknown keys,
// strict mode, redaction, Explain, env mapping, secrets, .env precedence.

package config

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// memFS is an in-memory file system for deterministic tests.
type memFS struct {
	files map[string][]byte
}

func (m memFS) ReadFile(name string) ([]byte, error) {
	if b, ok := m.files[name]; ok {
		return b, nil
	}
	// Match os.ReadFile's error type so errors.Is(os.ErrNotExist) works.
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// writeTempFile writes content to a uniquely-named file in t.TempDir() and
// returns its path. Used for file:// secret tests where the real fs is
// required (the secret path comes from a YAML value, not a Loader opt).
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// ---------- Precedence ----------

func TestPrecedenceDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader().Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.GetString("http.addr"), ":3000"; got != want {
		t.Fatalf("http.addr default = %q, want %q", got, want)
	}
	if got, want := cfg.GetString("obs.log"), "info"; got != want {
		t.Fatalf("obs.log default = %q, want %q", got, want)
	}
}

func TestPrecedenceYAMLOverridesDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`http: { addr: ":8080" }`)),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.GetString("http.addr"), ":8080"; got != want {
		t.Fatalf("http.addr = %q, want %q (YAML should override defaults)", got, want)
	}
}

func TestPrecedenceEnvYAMLOverridesYAML(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`http: { addr: ":8080" }`)),
		WithEnvYAMLBytes([]byte(`http: { addr: ":4000" }`)),
		WithEnvYAML("dev"),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.GetString("http.addr"), ":4000"; got != want {
		t.Fatalf("http.addr = %q, want %q (env YAML should override base YAML)", got, want)
	}
}

func TestPrecedenceEnvOverridesYAML(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`http: { addr: ":8080" }`)),
		WithEnvMap(map[string]string{
			"OGON_HTTP_ADDR": ":9999",
		}),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.GetString("http.addr"), ":9999"; got != want {
		t.Fatalf("http.addr = %q, want %q (env should override YAML)", got, want)
	}
}

func TestPrecedenceExplicitOverridesEnv(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithEnvMap(map[string]string{"OGON_HTTP_ADDR": ":9999"}),
		WithExplicit(map[string]any{"http.addr": ":7777"}),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.GetString("http.addr"), ":7777"; got != want {
		t.Fatalf("http.addr = %q, want %q (explicit should override env)", got, want)
	}
}

func TestPrecedenceCLIFlagsWin(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithExplicit(map[string]any{"http.addr": ":7777"}),
		WithFlags(map[string]string{"http.addr": ":5000"}),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.GetString("http.addr"), ":5000"; got != want {
		t.Fatalf("http.addr = %q, want %q (CLI flags win)", got, want)
	}
}

// ---------- Interpolation ----------

func TestInterpolationFromEnv(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`db: { primary: "postgres://${DB_USER}:${DB_PASS}@host/db" }`)),
		WithEnvMap(map[string]string{
			"DB_USER": "alice",
			"DB_PASS": "s3cret",
		}),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "postgres://alice:s3cret@host/db"
	if got := cfg.GetString("db.primary"); got != want {
		t.Fatalf("db.primary = %q, want %q", got, want)
	}
}

func TestInterpolationFromDotEnv(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`db: { primary: "postgres://${DB_USER}:${DB_PASS}@host/db" }`)),
		// Use a real newline; parseDotEnv splits on \n, not the literal "\\n".
		WithDotEnvBytes([]byte("DB_USER=bob\nDB_PASS=hunter2")),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "postgres://bob:hunter2@host/db"
	if got := cfg.GetString("db.primary"); got != want {
		t.Fatalf("db.primary = %q, want %q (.env should provide interpolation vars)", got, want)
	}
}

func TestInterpolationUnknownVarLeftAsIs(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`db: { primary: "${MISSING_VAR}" }`)),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.GetString("db.primary"), "${MISSING_VAR}"; got != want {
		t.Fatalf("db.primary = %q, want %q (missing var should be left as-is)", got, want)
	}
}

func TestInterpolateFunction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		env  map[string]string
		want string
	}{
		{"${X}", map[string]string{"X": "1"}, "1"},
		{"pre-${X}-post", map[string]string{"X": "MID"}, "pre-MID-post"},
		{"${X}${Y}", map[string]string{"X": "a", "Y": "b"}, "ab"},
		{"${X}", map[string]string{}, "${X}"},
		{"no vars", map[string]string{"X": "1"}, "no vars"},
		{"${lower}", map[string]string{"lower": "1"}, "${lower}"}, // lowercase not matched
	}
	for i, c := range cases {
		if got := Interpolate(c.in, c.env); got != c.want {
			t.Errorf("case %d: Interpolate(%q) = %q, want %q", i, c.in, got, c.want)
		}
	}
}

// ---------- Unknown key warning ----------

func TestUnknownKeyWarning(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`htp: { addr: ":3000" }`)), // typo: "htp" not "http"
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	unknown := cfg.UnknownKeys()
	if len(unknown) == 0 {
		t.Fatalf("expected unknown key warning for 'htp.addr'")
	}
	var found UnknownKey
	for _, u := range unknown {
		if u.Key == "htp.addr" {
			found = u
			break
		}
	}
	if found.Key == "" {
		t.Fatalf("unknown keys = %v, want one with Key='htp.addr'", unknown)
	}
	if found.Suggestion != "http.addr" {
		t.Fatalf("suggestion = %q, want 'http.addr' (Levenshtein ≤ 2)", found.Suggestion)
	}
}

func TestUnknownKeyNoSuggestionWhenFar(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`completely_unrelated_key: "x"`)),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// No known key within distance 2 of "completely_unrelated_key".
	for _, u := range cfg.UnknownKeys() {
		if u.Key == "completely_unrelated_key" && u.Suggestion != "" {
			t.Fatalf("suggestion = %q, want empty (distance > 2)", u.Suggestion)
		}
	}
}

// ---------- --strict ----------

func TestStrictModeFailsOnUnknownKey(t *testing.T) {
	t.Parallel()
	_, err := NewLoader(
		WithYAMLBytes([]byte(`htp: { addr: ":3000" }`)),
		WithStrict(),
	).Load(context.Background())
	if err == nil {
		t.Fatalf("expected error in strict mode with unknown key")
	}
	var d *diag.Diag
	if !errors.As(err, &d) {
		t.Fatalf("expected *diag.Diag, got %T: %v", err, err)
	}
	if d.Code != string(diag.CodeConfigUnknownKey) {
		t.Fatalf("code = %q, want %q", d.Code, diag.CodeConfigUnknownKey)
	}
}

func TestStrictModePassesWithKnownKeys(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`http: { addr: ":3000" }`)),
		WithStrict(),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load with strict + known keys: %v", err)
	}
	if cfg.GetString("http.addr") != ":3000" {
		t.Fatalf("http.addr = %q, want :3000", cfg.GetString("http.addr"))
	}
}

// ---------- Redaction ----------

func TestRedactURLCredentials(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"postgres://alice:s3cr3t@db.internal:5432/app", "postgres://***:***@db.internal:5432/app"},
		{"https://user:pass@api.example.com/path", "https://***:***@api.example.com/path"},
		{"https://api.example.com/path", "https://api.example.com/path"},
		{"file:///etc/secrets/db_url", "file:///etc/secrets/db_url"},
		{"postgres://alice@host/db", "postgres://alice@host/db"}, // no password, no redaction
		{"plain text without url", "plain text without url"},
	}
	for i, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("case %d: Redact(%q) = %q, want %q", i, c.in, got, c.want)
		}
	}
}

func TestRedactValueRecursive(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"db": "postgres://u:p@host/db",
		"nested": map[string]any{
			"url": "redis://ruser:rpass@cache:6379",
		},
		"list": []any{"https://a:b@x.com", "plain"},
	}
	out := RedactValue(in).(map[string]any)
	if got := out["db"].(string); !strings.Contains(got, "***:***@") {
		t.Fatalf("db not redacted: %q", got)
	}
	nested := out["nested"].(map[string]any)
	if got := nested["url"].(string); !strings.Contains(got, "***:***@") {
		t.Fatalf("nested url not redacted: %q", got)
	}
	list := out["list"].([]any)
	if got := list[0].(string); !strings.Contains(got, "***:***@") {
		t.Fatalf("list[0] not redacted: %q", got)
	}
	if got := list[1].(string); got != "plain" {
		t.Fatalf("list[1] changed unexpectedly: %q", got)
	}
}

func TestExplainRedactsURLCredentials(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`db: { primary: "postgres://alice:s3cr3t@host/db" }`)),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := cfg.Explain("db.primary").String()
	if strings.Contains(out, "s3cr3t") {
		t.Fatalf("Explain output contains secret 's3cr3t':\n%s", out)
	}
	if !strings.Contains(out, "***:***@") {
		t.Fatalf("Explain output does not contain redaction:\n%s", out)
	}
}

// ---------- Explain ----------

func TestExplainReturnsEveryLayer(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`http: { addr: ":8080" }`)),
		WithEnvYAMLBytes([]byte(`http: { addr: ":4000" }`)),
		WithEnvYAML("dev"),
		WithEnvMap(map[string]string{"OGON_HTTP_ADDR": ":9999"}),
		WithExplicit(map[string]any{"http.addr": ":7777"}),
		WithFlags(map[string]string{"http.addr": ":5000"}),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	expl := cfg.Explain("http.addr")
	if got, want := expl.Value, ":5000"; got != want {
		t.Fatalf("resolved = %v, want %v", got, want)
	}

	// Expect 6 sources: defaults, ogon.yaml, ogon.dev.yaml, env, explicit, cli.
	wantLayers := []string{
		"defaults",
		"ogon.yaml",
		"ogon.dev.yaml",
		"env:OGON_HTTP_ADDR",
		"explicit",
		"cli",
	}
	if len(expl.Sources) != len(wantLayers) {
		t.Fatalf("sources = %d, want %d (%v)", len(expl.Sources), len(wantLayers), expl.Sources)
	}
	for i, want := range wantLayers {
		if got := expl.Sources[i].Layer; !strings.HasPrefix(got, want) {
			t.Fatalf("source[%d].Layer = %q, want prefix %q", i, got, want)
		}
	}

	// Verify each source's value matches what that layer set.
	wantValues := []string{":3000", ":8080", ":4000", ":9999", ":7777", ":5000"}
	for i, want := range wantValues {
		got, ok := expl.Sources[i].Value.(string)
		if !ok {
			t.Fatalf("source[%d].Value = %T, want string", i, expl.Sources[i].Value)
		}
		if got != want {
			t.Fatalf("source[%d].Value = %q, want %q", i, got, want)
		}
	}
}

func TestExplainMissingKey(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader().Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	expl := cfg.Explain("does.not.exist")
	if expl.Value != nil {
		t.Fatalf("Value = %v, want nil", expl.Value)
	}
	if len(expl.Sources) != 0 {
		t.Fatalf("Sources = %v, want empty", expl.Sources)
	}
	if !strings.Contains(expl.String(), "<unset>") {
		t.Fatalf("Explain output for missing key should say <unset>:\n%s", expl.String())
	}
}

// ---------- Env var mapping ----------

func TestParseEnvName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"OGON_HTTP_ADDR", "http.addr"},
		{"OGON_HTTP_READ_TIMEOUT", "http.read_timeout"},
		{"OGON_DB_PRIMARY", "db.primary"},
		{"NOT_OGON", ""},
		{"OGON_", ""},
	}
	for _, c := range cases {
		if got := ParseEnvName(c.in); got != c.want {
			t.Errorf("ParseEnvName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseEnvNameWithSchemaSecondary(t *testing.T) {
	t.Parallel()
	// db.pool.max is in the default schema; primary mapping gives
	// db.pool_max (not in schema), so secondary db.pool.max should win.
	schema := DefaultSchema()
	got := ParseEnvNameWithSchema("OGON_DB_POOL_MAX", schema.Known)
	if got != "db.pool.max" {
		t.Fatalf("ParseEnvNameWithSchema(OGON_DB_POOL_MAX) = %q, want 'db.pool.max'", got)
	}
}

func TestEnvOverrideNestedMapping(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithEnvMap(map[string]string{"OGON_DB_POOL_MAX": "30"}),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.GetString("db.pool.max"); got != "30" {
		t.Fatalf("db.pool.max = %q, want 30 (nested env mapping)", got)
	}
}

// ---------- file:// secrets ----------

func TestFileSecretResolution(t *testing.T) {
	t.Parallel()
	secretContent := "postgres://alice:s3cret@db.internal:5432/app"
	secretPath := writeTempFile(t, "db_url.txt", secretContent)
	yaml := `db: { primary: "file://` + secretPath + `" }`

	cfg, err := NewLoader(
		WithYAMLBytes([]byte(yaml)),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.GetString("db.primary"), secretContent; got != want {
		t.Fatalf("db.primary = %q, want %q (file:// should resolve to file content)", got, want)
	}

	// Explain should NOT reveal the secret content; it should show
	// "file://<resolved>" marker instead.
	expl := cfg.Explain("db.primary").String()
	if strings.Contains(expl, "s3cret") {
		t.Fatalf("Explain output reveals secret:\n%s", expl)
	}
	if !strings.Contains(expl, "file://<resolved>") {
		t.Fatalf("Explain should mark resolved file:// secrets:\n%s", expl)
	}
}

func TestFileSecretMissingFile(t *testing.T) {
	t.Parallel()
	yaml := `db: { primary: "file:///nonexistent/path/secret" }`
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(yaml)),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// On read failure, the original file:// reference is kept.
	if got := cfg.GetString("db.primary"); !strings.HasPrefix(got, "file://") {
		t.Fatalf("db.primary = %q, want file://... reference on read failure", got)
	}
}

// ---------- .env never overrides existing env ----------

func TestDotEnvDoesNotOverrideExistingEnv(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithEnvMap(map[string]string{"OGON_HTTP_ADDR": ":9999"}),
		WithDotEnvBytes([]byte("OGON_HTTP_ADDR=:1111\nDB_USER=bob")),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Real env's :9999 should win over .env's :1111.
	if got := cfg.GetString("http.addr"); got != ":9999" {
		t.Fatalf("http.addr = %q, want :9999 (.env must not override existing env)", got)
	}
	// DB_USER (no OGON_ prefix) is not a config override, but it should be
	// available for ${VAR} interpolation. Verify by interpolating in YAML.
	cfg, err = NewLoader(
		WithYAMLBytes([]byte(`db: { primary: "${DB_USER}" }`)),
		WithEnvMap(map[string]string{}),
		WithDotEnvBytes([]byte("DB_USER=bob")),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.GetString("db.primary"); got != "bob" {
		t.Fatalf("db.primary = %q, want 'bob' (.env should provide interpolation vars)", got)
	}
}

// ---------- YAML missing files ----------

func TestMissingYAMLFileIgnored(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLFile("/nonexistent/ogon.yaml"),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load with missing ogon.yaml: %v", err)
	}
	// Defaults should still apply.
	if got := cfg.GetString("http.addr"); got != ":3000" {
		t.Fatalf("http.addr = %q, want :3000 (defaults)", got)
	}
}

func TestYAMLFileRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "ogon.yaml")
	if err := os.WriteFile(yamlPath, []byte("http: { addr: \":9090\" }\n"), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := NewLoader(
		WithYAMLFile(yamlPath),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.GetString("http.addr"); got != ":9090" {
		t.Fatalf("http.addr = %q, want :9090", got)
	}
}

// ---------- Schema defaults flatten ----------

func TestFlatten(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"http": map[string]any{
			"addr": ":3000",
			"nested": map[string]any{
				"deep": "value",
			},
		},
		"flat": "scalar",
	}
	out := flatten(in)
	want := map[string]any{
		"http.addr":        ":3000",
		"http.nested.deep": "value",
		"flat":             "scalar",
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("flatten = %v, want %v", out, want)
	}
}

// ---------- DotEnv parser ----------

func TestParseDotEnv(t *testing.T) {
	t.Parallel()
	data := []byte("# comment\nKEY1=val1\nKEY2=\"quoted value\"\nKEY3='single'\n\nEMPTY=\n")
	m := parseDotEnv(data)
	if m["KEY1"] != "val1" {
		t.Fatalf("KEY1 = %q, want 'val1'", m["KEY1"])
	}
	if m["KEY2"] != "quoted value" {
		t.Fatalf("KEY2 = %q, want 'quoted value'", m["KEY2"])
	}
	if m["KEY3"] != "single" {
		t.Fatalf("KEY3 = %q, want 'single'", m["KEY3"])
	}
	if m["EMPTY"] != "" {
		t.Fatalf("EMPTY = %q, want ''", m["EMPTY"])
	}
}

// ---------- Levenshtein ----------

func TestLevenshtein(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"http", "http", 0},
		{"http", "htp", 1},
		{"http.addr", "htp.addr", 1},
		{"abc", "xyz", 3},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// ---------- Custom schema ----------

func TestCustomSchema(t *testing.T) {
	t.Parallel()
	custom := &Schema{
		Known: map[string]struct{}{
			"app.name": {},
		},
		Defaults: map[string]any{
			"app.name": "default-app",
		},
	}
	_, err := NewLoader(
		WithSchema(custom),
		WithYAMLBytes([]byte(`app: { title: "my app" }`)), // unknown: title, not name
		WithStrict(),
	).Load(context.Background())
	if err == nil {
		t.Fatalf("expected strict-mode failure on unknown key 'app.title'")
	}
}

// ---------- memFS-based file reads ----------

func TestMemFSYAMLRead(t *testing.T) {
	t.Parallel()
	fs := memFS{
		files: map[string][]byte{
			"ogon.yaml": []byte("http: { addr: \":7070\" }\n"),
		},
	}
	cfg, err := NewLoader(
		WithFileSystem(fs),
		WithYAMLFile("ogon.yaml"),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.GetString("http.addr"); got != ":7070" {
		t.Fatalf("http.addr = %q, want :7070", got)
	}
}

// ---------- Complete precedence chain (integration) ----------

func TestFullPrecedenceChain(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		// Layer 1 (defaults) → :3000
		// Layer 2: ogon.yaml → :8080
		WithYAMLBytes([]byte(`http: { addr: ":8080" }`)),
		// Layer 3: ogon.dev.yaml → :4000
		WithEnvYAMLBytes([]byte(`http: { addr: ":4000" }`)),
		WithEnvYAML("dev"),
		// Layer 4: .env (no direct config effect; provides DB_USER)
		WithDotEnvBytes([]byte("DB_USER=interpolated\nOGON_HTTP_ADDR=:9999")),
		// Layer 5: env → :9999 (from .env since real env is empty)
		WithEnvMap(map[string]string{}),
		// Layer 6: explicit → :7777
		WithExplicit(map[string]any{"http.addr": ":7777"}),
		// Layer 7: CLI → :5000
		WithFlags(map[string]string{"http.addr": ":5000"}),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cfg.GetString("http.addr"); got != ":5000" {
		t.Fatalf("http.addr = %q, want :5000 (CLI wins full chain)", got)
	}

	// The .env-sourced env override should be tagged with "(.env)".
	expl := cfg.Explain("http.addr")
	var foundDotEnv bool
	for _, s := range expl.Sources {
		if strings.Contains(s.Layer, "OGON_HTTP_ADDR") && strings.Contains(s.Layer, ".env") {
			foundDotEnv = true
			break
		}
	}
	if !foundDotEnv {
		t.Fatalf("expected OGON_HTTP_ADDR source tagged with (.env):\n%s", expl.String())
	}
}

// ---------- Unknown key attribution ----------

func TestUnknownKeyAttributedToCorrectLayer(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader(
		WithYAMLBytes([]byte(`htp: { addr: ":3000" }`)),
	).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, u := range cfg.UnknownKeys() {
		if u.Key == "htp.addr" && u.Layer != "ogon.yaml" {
			t.Fatalf("unknown key 'htp.addr' attributed to %q, want 'ogon.yaml'", u.Layer)
		}
	}
}

// ---------- Get returns typed values ----------

func TestGetReturnsTypedValue(t *testing.T) {
	t.Parallel()
	cfg, err := NewLoader().Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// jobs.concurrency default is 10 (int), not a string.
	v := cfg.Get("jobs.concurrency")
	if i, ok := v.(int); !ok || i != 10 {
		t.Fatalf("jobs.concurrency = %v (%T), want int 10", v, v)
	}
	// GetString should still format it.
	if got := cfg.GetString("jobs.concurrency"); got != "10" {
		t.Fatalf("GetString(jobs.concurrency) = %q, want '10'", got)
	}
}
