// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// modules/lockfile.go: ogon.lock writer/reader (MOD-021). The lockfile
// records every installed module and the resolved version, so builds are
// reproducible. The format is YAML for parity with ogon.module.yaml.

package modules

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/goccy/go-yaml"
)

// LockEntry is one row in ogon.lock.
type LockEntry struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
	Source  string `json:"source,omitempty" yaml:"source,omitempty"`
	Digest  string `json:"digest,omitempty" yaml:"digest,omitempty"`
}

// Lockfile is the parsed form of ogon.lock.
type Lockfile struct {
	Entries []LockEntry `json:"entries" yaml:"entries"`
}

// LockfilePath returns "<root>/ogon.lock".
func LockfilePath(root string) string {
	return filepath.Join(root, "ogon.lock")
}

// LoadLock reads ogon.lock from path. Returns an empty lockfile if missing.
func LoadLock(path string) (*Lockfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Lockfile{}, nil
		}
		return nil, fmt.Errorf("modules: read %s: %w", path, err)
	}
	var l Lockfile
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("modules: parse %s: %w", path, err)
	}
	return &l, nil
}

// Save writes the lockfile to path.
func (l *Lockfile) Save(path string) error {
	l.Sort()
	data, err := yaml.Marshal(l)
	if err != nil {
		return fmt.Errorf("modules: marshal lockfile: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("modules: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("modules: write %s: %w", path, err)
	}
	return nil
}

// Sort sorts entries by name for byte-stable output (DI-020).
func (l *Lockfile) Sort() {
	sort.Slice(l.Entries, func(i, j int) bool { return l.Entries[i].Name < l.Entries[j].Name })
}

// Add inserts or replaces an entry.
func (l *Lockfile) Add(e LockEntry) {
	for i, ex := range l.Entries {
		if ex.Name == e.Name {
			l.Entries[i] = e
			return
		}
	}
	l.Entries = append(l.Entries, e)
}

// Remove deletes an entry by name. No-op if absent.
func (l *Lockfile) Remove(name string) {
	out := l.Entries[:0]
	for _, e := range l.Entries {
		if e.Name != name {
			out = append(out, e)
		}
	}
	l.Entries = out
}

// Get returns the entry for name, or nil.
func (l *Lockfile) Get(name string) (LockEntry, bool) {
	for _, e := range l.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return LockEntry{}, false
}
