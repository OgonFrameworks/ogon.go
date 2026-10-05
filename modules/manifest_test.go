// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for modules/manifest.go (MOD-001/002/003/017/024): YAML parsing,
// validation, ogon-constraint checking.

package modules

import (
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

const validManifest = `name: ogon-stripe
version: 1.0.0
ogon: ">=1.8,<2"
provides:
  - routes
  - config
  - migrations
requires:
  - ogon-auth-session
license: MIT
repo: github.com/ogonframeworks/ogon-stripe
maintainers:
  - name: Alice
    email: alice@example.com
trust: first-party
`

func TestLoadManifestValid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeManifest(t, dir, "stripe/ogon.module.yaml", validManifest)
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Name != "ogon-stripe" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.Version != "1.0.0" {
		t.Errorf("Version = %q", m.Version)
	}
	if m.Ogon != ">=1.8,<2" {
		t.Errorf("Ogon = %q", m.Ogon)
	}
	if len(m.Provides) != 3 {
		t.Errorf("Provides = %v", m.Provides)
	}
	if len(m.Requires) != 1 {
		t.Errorf("Requires = %v", m.Requires)
	}
	if m.License != "MIT" {
		t.Errorf("License = %q", m.License)
	}
	if len(m.Maintainers) != 1 {
		t.Errorf("Maintainers = %v", m.Maintainers)
	}
	if m.Maintainers[0].Name != "Alice" {
		t.Errorf("Maintainer.Name = %q", m.Maintainers[0].Name)
	}
	if m.Trust != "first-party" {
		t.Errorf("Trust = %q", m.Trust)
	}
}

func TestLoadManifestMissingName(t *testing.T) {
	t.Parallel()
	bad := `version: 1.0.0
ogon: ">=1.8"
`
	path := writeManifest(t, t.TempDir(), "x/ogon.module.yaml", bad)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatalf("expected error for missing name")
	}
}

func TestLoadManifestMissingVersion(t *testing.T) {
	t.Parallel()
	bad := `name: foo
ogon: ">=1.8"
`
	path := writeManifest(t, t.TempDir(), "x/ogon.module.yaml", bad)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatalf("expected error for missing version")
	}
}

func TestLoadManifestMissingOgon(t *testing.T) {
	t.Parallel()
	bad := `name: foo
version: 1.0.0
`
	path := writeManifest(t, t.TempDir(), "x/ogon.module.yaml", bad)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatalf("expected error for missing ogon constraint")
	}
}

func TestLoadManifestInvalidName(t *testing.T) {
	t.Parallel()
	bad := `name: BAD-NAME
version: 1.0.0
ogon: ">=1.8"
`
	path := writeManifest(t, t.TempDir(), "x/ogon.module.yaml", bad)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatalf("expected error for invalid name")
	}
}

func TestLoadManifestInvalidVersion(t *testing.T) {
	t.Parallel()
	bad := `name: foo
version: not-a-version
ogon: ">=1.8"
`
	path := writeManifest(t, t.TempDir(), "x/ogon.module.yaml", bad)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatalf("expected error for invalid version")
	}
}

func TestLoadManifestUnknownContribution(t *testing.T) {
	t.Parallel()
	bad := `name: foo
version: 1.0.0
ogon: ">=1.8"
provides:
  - bogus
`
	path := writeManifest(t, t.TempDir(), "x/ogon.module.yaml", bad)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatalf("expected error for unknown contribution")
	}
}

func TestLoadManifestSelfRequire(t *testing.T) {
	t.Parallel()
	bad := `name: foo
version: 1.0.0
ogon: ">=1.8"
requires:
  - foo
`
	path := writeManifest(t, t.TempDir(), "x/ogon.module.yaml", bad)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatalf("expected error for self-require")
	}
}

func TestLoadManifestInvalidTrust(t *testing.T) {
	t.Parallel()
	bad := `name: foo
version: 1.0.0
ogon: ">=1.8"
trust: magic
`
	path := writeManifest(t, t.TempDir(), "x/ogon.module.yaml", bad)
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatalf("expected error for invalid trust")
	}
}

func TestCanShipCodegen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		trust string
		want  bool
	}{
		{"first-party", true},
		{"community", true},
		{"untrusted", false},
		{"", true}, // default = trusted
	}
	for _, c := range cases {
		m := &Manifest{Name: "x", Version: "1.0.0", Ogon: ">=1.8", Trust: c.trust}
		if got := m.CanShipCodegen(); got != c.want {
			t.Errorf("trust=%q: got %v, want %v", c.trust, got, c.want)
		}
	}
}

func TestHasContribution(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Name:     "x",
		Version:  "1.0.0",
		Ogon:     ">=1.8",
		Provides: []string{ContributionRoutes, ContributionConfig},
	}
	if !m.HasContribution(ContributionRoutes) {
		t.Errorf("HasContribution(routes) = false")
	}
	if m.HasContribution(ContributionUI) {
		t.Errorf("HasContribution(ui) = true")
	}
}

func TestCheckOgonConstraint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		constraint string
		version    string
		wantErr    bool
	}{
		{">=1.8", "1.8.0", false},
		{">=1.8", "1.7.0", true},
		{">=1.8,<2", "1.9.5", false},
		{">=1.8,<2", "2.0.0", true},
		{"^1.8", "1.8.0", false},
		{"^1.8", "1.9.0", false},
		{"^1.8", "2.0.0", true},
		{"~1.8", "1.8.5", false},
		{"~1.8", "1.9.0", true},
		{"=1.8.0", "1.8.0", false},
		{"=1.8.0", "1.8.1", true},
	}
	for _, c := range cases {
		err := CheckOgonConstraint(c.constraint, c.version)
		gotErr := err != nil
		if gotErr != c.wantErr {
			t.Errorf("CheckOgonConstraint(%q, %q) = err=%v, want err=%v", c.constraint, c.version, err, c.wantErr)
		}
	}
}

func TestLoadDirCollectsManifests(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "stripe/ogon.module.yaml", validManifest)
	// Second module
	writeManifest(t, dir, "auth/ogon.module.yaml", `name: ogon-auth-session
version: 1.0.0
ogon: ">=1.8"
`)
	// Subdir without manifest — skipped silently
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifests, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("got %d manifests, want 2", len(manifests))
	}
	// Sorted by name
	if manifests[0].Name != "ogon-auth-session" {
		t.Errorf("first = %q", manifests[0].Name)
	}
	if manifests[1].Name != "ogon-stripe" {
		t.Errorf("second = %q", manifests[1].Name)
	}
}

func TestMarshalRoundtrip(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Name:    "foo",
		Version: "1.0.0",
		Ogon:    ">=1.8",
		License: "MIT",
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty marshal output")
	}
}

func TestMarshalInvalidFails(t *testing.T) {
	t.Parallel()
	m := &Manifest{Name: "", Version: "1.0.0", Ogon: ">=1.8"}
	if _, err := m.Marshal(); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestIsValidName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"foo", true},
		{"foo-bar", true},
		{"foo.baz", true},
		{"a1", true},
		{"-foo", false}, // starts with dash
		{".foo", false}, // starts with dot
		{"1foo", false}, // starts with digit
		{"Foo", false},  // uppercase
		{"foo bar", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isValidName(c.in); got != c.want {
			t.Errorf("isValidName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsSemverish(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"1.0.0", true},
		{"1.0", true},
		{"1.0.0-rc1", true},
		{"1.0.0+build.1", true},
		{"v1.0.0", false}, // no v prefix
		{"not-a-version", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isSemverish(c.in); got != c.want {
			t.Errorf("isSemverish(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsValidConstraint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{">=1.8", true},
		{">=1.8,<2", true},
		{"^1.8", true},
		{"~1.8.0", true},
		{"=1.8.0", true},
		{"", false},
		{"invalid", false},
		{">=", false},
	}
	for _, c := range cases {
		if got := isValidConstraint(c.in); got != c.want {
			t.Errorf("isValidConstraint(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
