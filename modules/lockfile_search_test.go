// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for modules/lockfile.go (MOD-021) and modules/search.go (MOD-034).

package modules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockfilePath(t *testing.T) {
	t.Parallel()
	if got := LockfilePath("/r"); got != "/r/ogon.lock" {
		t.Errorf("LockfilePath = %q", got)
	}
}

func TestLoadLockMissingReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, err := LoadLock(filepath.Join(dir, "ogon.lock"))
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if l == nil {
		t.Fatal("nil lockfile")
	}
	if len(l.Entries) != 0 {
		t.Errorf("entries = %d, want 0", len(l.Entries))
	}
}

func TestLockfileSaveAndLoadRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ogon.lock")
	l := &Lockfile{}
	l.Add(LockEntry{Name: "b", Version: "1.0.0"})
	l.Add(LockEntry{Name: "a", Version: "2.0.0"})
	if err := l.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadLock(path)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("entries = %d", len(loaded.Entries))
	}
	// Save() sorts by name.
	if loaded.Entries[0].Name != "a" {
		t.Errorf("first = %q", loaded.Entries[0].Name)
	}
	if loaded.Entries[1].Name != "b" {
		t.Errorf("second = %q", loaded.Entries[1].Name)
	}
}

func TestLockfileAddReplaces(t *testing.T) {
	t.Parallel()
	l := &Lockfile{}
	l.Add(LockEntry{Name: "a", Version: "1.0.0"})
	l.Add(LockEntry{Name: "a", Version: "2.0.0"})
	if len(l.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(l.Entries))
	}
	if l.Entries[0].Version != "2.0.0" {
		t.Errorf("version = %q", l.Entries[0].Version)
	}
}

func TestLockfileRemove(t *testing.T) {
	t.Parallel()
	l := &Lockfile{}
	l.Add(LockEntry{Name: "a", Version: "1.0.0"})
	l.Add(LockEntry{Name: "b", Version: "1.0.0"})
	l.Remove("a")
	if len(l.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(l.Entries))
	}
	if l.Entries[0].Name != "b" {
		t.Errorf("entry = %q", l.Entries[0].Name)
	}
	// Remove non-existent is a no-op.
	l.Remove("nonexistent")
	if len(l.Entries) != 1 {
		t.Errorf("entries = %d after noop remove", len(l.Entries))
	}
}

func TestLockfileGet(t *testing.T) {
	t.Parallel()
	l := &Lockfile{}
	l.Add(LockEntry{Name: "a", Version: "1.0.0"})
	e, ok := l.Get("a")
	if !ok {
		t.Fatal("not found")
	}
	if e.Version != "1.0.0" {
		t.Errorf("version = %q", e.Version)
	}
	if _, ok := l.Get("nonexistent"); ok {
		t.Errorf("nonexistent should not be found")
	}
}

func TestLockfileSaveCreatesDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nested := filepath.Join(dir, "subdir", "ogon.lock")
	l := &Lockfile{}
	l.Add(LockEntry{Name: "a", Version: "1.0.0"})
	if err := l.Save(nested); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

// ---- search tests ----

func TestRegistryIndexSearchByName(t *testing.T) {
	t.Parallel()
	idx := NewRegistryIndex()
	idx.Add(
		IndexEntry{Name: "ogon-stripe", Description: "Stripe payments"},
		IndexEntry{Name: "ogon-sentry", Description: "Sentry errors"},
	)
	results := idx.Search("stripe")
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Name != "ogon-stripe" {
		t.Errorf("name = %q", results[0].Name)
	}
}

func TestRegistryIndexSearchByTag(t *testing.T) {
	t.Parallel()
	idx := NewRegistryIndex()
	idx.Add(
		IndexEntry{Name: "ogon-stripe", Tags: []string{"payments"}},
		IndexEntry{Name: "ogon-s3", Tags: []string{"storage"}},
	)
	results := idx.Search("storage")
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Name != "ogon-s3" {
		t.Errorf("name = %q", results[0].Name)
	}
}

func TestRegistryIndexSearchByDescription(t *testing.T) {
	t.Parallel()
	idx := NewRegistryIndex()
	idx.Add(
		IndexEntry{Name: "a", Description: "Object storage"},
		IndexEntry{Name: "b", Description: "Other"},
	)
	results := idx.Search("storage")
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Name != "a" {
		t.Errorf("name = %q", results[0].Name)
	}
}

func TestRegistryIndexSearchEmptyReturnsAll(t *testing.T) {
	t.Parallel()
	idx := NewRegistryIndex()
	idx.Add(
		IndexEntry{Name: "a"},
		IndexEntry{Name: "b"},
	)
	results := idx.Search("")
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
}

func TestRegistryIndexAllSorted(t *testing.T) {
	t.Parallel()
	idx := NewRegistryIndex()
	idx.Add(IndexEntry{Name: "z"})
	idx.Add(IndexEntry{Name: "a"})
	all := idx.All()
	if all[0].Name != "a" || all[1].Name != "z" {
		t.Errorf("order = %v,%v", all[0].Name, all[1].Name)
	}
}

func TestDefaultFirstPartyIndexHasKnownModules(t *testing.T) {
	t.Parallel()
	idx := DefaultFirstPartyIndex()
	all := idx.All()
	if len(all) == 0 {
		t.Fatal("no first-party modules")
	}
	// Stripe, sentry, s3 should all be in the index (MOD-033).
	names := map[string]bool{}
	for _, e := range all {
		names[e.Name] = true
	}
	for _, want := range []string{"ogon-stripe", "ogon-sentry", "ogon-s3"} {
		if !names[want] {
			t.Errorf("missing %q in default index", want)
		}
	}
}

func TestDefaultFirstPartyIndexSearchFindsStripe(t *testing.T) {
	t.Parallel()
	idx := DefaultFirstPartyIndex()
	results := idx.Search("stripe")
	if len(results) == 0 {
		t.Fatal("no stripe results")
	}
	if results[0].Name != "ogon-stripe" {
		t.Errorf("first result = %q", results[0].Name)
	}
}
