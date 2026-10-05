// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// modules/manifest.go: ogon.module.yaml parser (MOD-001/002/017). Each
// manifest declares a module's name, version, ogon-framework constraint,
// provides, requires, license, repo, and maintainers.

package modules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Contribution enumerates the contribution surfaces a module can declare
// (MOD-006..013). Strings are normative — they appear in ogon.module.yaml.
const (
	ContributionRoutes     = "routes"
	ContributionMiddleware = "middleware"
	ContributionConfig     = "config"
	ContributionMigrations = "migrations"
	ContributionCLI        = "cli"
	ContributionGenerators = "generators"
	ContributionUI         = "ui"
	ContributionAssets     = "assets"
	ContributionObs        = "obs"
	ContributionLifecycle  = "lifecycle"
)

// allContributions is the allow-list for manifest validation.
var allContributions = map[string]bool{
	ContributionRoutes:     true,
	ContributionMiddleware: true,
	ContributionConfig:     true,
	ContributionMigrations: true,
	ContributionCLI:        true,
	ContributionGenerators: true,
	ContributionUI:         true,
	ContributionAssets:     true,
	ContributionObs:        true,
	ContributionLifecycle:  true,
}

// Maintainer is a human-readable contact recorded in the manifest (MOD-017).
type Maintainer struct {
	Name  string `json:"name" yaml:"name"`
	Email string `json:"email,omitempty" yaml:"email,omitempty"`
	URL   string `json:"url,omitempty" yaml:"url,omitempty"`
}

// Manifest is the parsed form of ogon.module.yaml (MOD-001).
type Manifest struct {
	// Name is the unique module identifier (lowercase, dotted). Required.
	Name string `json:"name" yaml:"name"`
	// Version is the module's semver version. Required.
	Version string `json:"version" yaml:"version"`
	// Ogon is the framework version constraint (e.g. ">=1.8,<2"). Required.
	Ogon string `json:"ogon" yaml:"ogon"`
	// Provides lists the contribution surfaces (routes, middleware, ...).
	Provides []string `json:"provides,omitempty" yaml:"provides,omitempty"`
	// Requires lists other module names this module depends on (MOD-002).
	Requires []string `json:"requires,omitempty" yaml:"requires,omitempty"`
	// License is the SPDX license identifier (MIT, Apache-2.0, ...).
	License string `json:"license,omitempty" yaml:"license,omitempty"`
	// Repo is the source repository URL.
	Repo string `json:"repo,omitempty" yaml:"repo,omitempty"`
	// Maintainers is the contact list.
	Maintainers []Maintainer `json:"maintainers,omitempty" yaml:"maintainers,omitempty"`
	// Trust is the trust level (first-party | community | untrusted). Used
	// by MOD-024 to gate codegen hooks.
	Trust string `json:"trust,omitempty" yaml:"trust,omitempty"`
	// Path is the on-disk directory the manifest was loaded from. Set by
	// LoadManifest; not part of the YAML.
	Path string `json:"-" yaml:"-"`
}

// Validate checks the manifest for required fields and contribution
// allow-list membership. Returns nil on success.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("modules: nil manifest")
	}
	if m.Name == "" {
		return errors.New("modules: manifest missing name")
	}
	if m.Version == "" {
		return errors.New("modules: manifest missing version")
	}
	if m.Ogon == "" {
		return errors.New("modules: manifest missing ogon constraint")
	}
	if !isValidName(m.Name) {
		return fmt.Errorf("modules: invalid name %q (lowercase, alnum, dots, dashes only)", m.Name)
	}
	if !isSemverish(m.Version) {
		return fmt.Errorf("modules: invalid version %q (expected semver)", m.Version)
	}
	if !isValidConstraint(m.Ogon) {
		return fmt.Errorf("modules: invalid ogon constraint %q", m.Ogon)
	}
	for _, c := range m.Provides {
		if !allContributions[c] {
			return fmt.Errorf("modules: unknown contribution %q (allowed: %s)", c, allContributionNames())
		}
	}
	for _, r := range m.Requires {
		if !isValidName(r) {
			return fmt.Errorf("modules: invalid require name %q", r)
		}
		if r == m.Name {
			return fmt.Errorf("modules: module %q cannot require itself", m.Name)
		}
	}
	if m.Trust != "" {
		if !isValidTrust(m.Trust) {
			return fmt.Errorf("modules: invalid trust %q (allowed: first-party, community, untrusted)", m.Trust)
		}
	}
	return nil
}

// HasContribution reports whether the manifest declares c in Provides.
func (m *Manifest) HasContribution(c string) bool {
	for _, p := range m.Provides {
		if p == c {
			return true
		}
	}
	return false
}

// CanShipCodegen reports whether an untrusted module is allowed to ship
// codegen hooks (MOD-024). Only first-party and community (verified) modules
// may.
func (m *Manifest) CanShipCodegen() bool {
	switch m.Trust {
	case "first-party", "community", "":
		return true
	case "untrusted":
		return false
	}
	return false
}

// LoadManifest parses an ogon.module.yaml file from disk and validates it.
// The Path field is set to the directory containing the file.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("modules: read %s: %w", path, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("modules: parse %s: %w", path, err)
	}
	m.Path = filepath.Dir(path)
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Marshal encodes a manifest back to YAML (used by `ogon modules add`).
func (m *Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return yaml.Marshal(m)
}

// LoadDir scans dir for `ogon.module.yaml` files (one per subdirectory) and
// returns the parsed manifests sorted by Name. Subdirectories without a
// manifest are skipped silently.
func LoadDir(dir string) ([]*Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("modules: read %s: %w", dir, err)
	}
	var out []*Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manifestPath := filepath.Join(dir, e.Name(), "ogon.module.yaml")
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}
		m, err := LoadManifest(manifestPath)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// allContributionNames returns the sorted allow-list of contribution names.
func allContributionNames() string {
	names := make([]string, 0, len(allContributions))
	for k := range allContributions {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// isValidName checks the module-name grammar: lowercase letters, digits,
// dots, dashes; starts with a letter.
func isValidName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r == '.' || r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isSemverish checks that a version string looks like semver (1-3 numeric
// segments with optional pre-release / build metadata). Not a full semver
// validator. Single-segment versions (e.g. "2") are accepted because they
// are common in version constraints (e.g. "<2" meaning "<2.0.0").
func isSemverish(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.SplitN(s, "+", 2)
	pre := strings.SplitN(parts[0], "-", 2)
	core := pre[0]
	segments := strings.Split(core, ".")
	if len(segments) < 1 || len(segments) > 3 {
		return false
	}
	for _, seg := range segments {
		if seg == "" {
			return false
		}
		for _, r := range seg {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// isValidConstraint checks that an ogon constraint string looks like one or
// more comma-separated comparators (e.g. ">=1.8,<2"). Empty is invalid.
func isValidConstraint(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}
		ok := false
		for _, op := range []string{">=", "<=", ">", "<", "=", "~", "^"} {
			if strings.HasPrefix(part, op) {
				rest := strings.TrimSpace(strings.TrimPrefix(part, op))
				if rest != "" && isSemverish(rest) {
					ok = true
				}
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// isValidTrust reports whether s is one of the allowed trust levels.
func isValidTrust(s string) bool {
	switch s {
	case "first-party", "community", "untrusted":
		return true
	}
	return false
}

// CheckOgonConstraint evaluates whether the running framework version
// satisfies the manifest's ogon constraint. Returns an error naming the
// unsatisfied comparator (or nil if all pass).
func CheckOgonConstraint(manifestOgon, frameworkVersion string) error {
	parts := strings.Split(manifestOgon, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ok, err := evalComparator(part, frameworkVersion)
		if err != nil {
			return fmt.Errorf("modules: bad constraint %q: %w", part, err)
		}
		if !ok {
			return fmt.Errorf("modules: ogon %q does not satisfy %q", frameworkVersion, part)
		}
	}
	return nil
}

// evalComparator evaluates a single comparator like ">=1.8" against a
// concrete version like "1.8.0".
func evalComparator(comparator, version string) (bool, error) {
	for _, op := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(comparator, op) {
			rest := strings.TrimSpace(strings.TrimPrefix(comparator, op))
			cmp := compareSemver(version, rest)
			switch op {
			case ">=":
				return cmp >= 0, nil
			case "<=":
				return cmp <= 0, nil
			case ">":
				return cmp > 0, nil
			case "<":
				return cmp < 0, nil
			case "=":
				return cmp == 0, nil
			case "^":
				// ^1.8 = >=1.8.0, <2.0.0
				if cmp < 0 {
					return false, nil
				}
				return bumpMajorForCaret(rest, version), nil
			case "~":
				// ~1.8 = >=1.8.0, <1.9.0
				if cmp < 0 {
					return false, nil
				}
				return bumpMinorForTilde(rest, version), nil
			}
		}
	}
	return false, fmt.Errorf("unrecognised operator in %q", comparator)
}

// bumpMajorForCaret returns true if `version` is below the next major after
// `target`. Used by ^ semantics.
func bumpMajorForCaret(target, version string) bool {
	t := splitSemver(target)
	v := splitSemver(version)
	if len(t) == 0 || len(v) == 0 {
		return false
	}
	if t[0] != v[0] {
		// Different major: caret disallows.
		return false
	}
	return true
}

// bumpMinorForTilde returns true if `version` is within the same minor as
// `target`. Used by ~ semantics.
func bumpMinorForTilde(target, version string) bool {
	t := splitSemver(target)
	v := splitSemver(version)
	if len(t) < 2 || len(v) < 2 {
		return false
	}
	return t[0] == v[0] && t[1] == v[1]
}

// compareSemver returns -1/0/+1 comparing two semver strings.
func compareSemver(a, b string) int {
	aa := splitSemver(a)
	bb := splitSemver(b)
	for i := 0; i < 3; i++ {
		av, bv := 0, 0
		if i < len(aa) {
			av = aa[i]
		}
		if i < len(bb) {
			bv = bb[i]
		}
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	}
	return 0
}

// splitSemver returns the numeric major/minor/patch segments of a version.
// Non-numeric or missing segments are 0.
func splitSemver(s string) []int {
	// Strip pre-release / build metadata.
	s = strings.SplitN(s, "-", 2)[0]
	s = strings.SplitN(s, "+", 2)[0]
	parts := strings.Split(s, ".")
	out := make([]int, 0, 3)
	for _, p := range parts {
		n := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out = append(out, n)
	}
	return out
}
