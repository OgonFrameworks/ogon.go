// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Idempotent write + drift diff tests (INFRA-037/039/040). Round-trips a
// full generate → write → drift → edit → refuse → force cycle.

package infra

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemp creates a temp dir and returns its path + a cleanup fn.
func writeTemp(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	return dir, func() {}
}

// TestIdempotentWriteCreated verifies a fresh file is created.
func TestIdempotentWriteCreated(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	lock := &LockFile{Version: Version}
	spec := FileSpec{
		Path:    "deploy/k8s/deployment.yaml",
		Content: "apiVersion: apps/v1\nkind: Deployment\n",
		Marker:  MarkOwned, CommentPrefix: "#",
	}
	r, err := IdempotentWrite(root, spec, false, lock)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if r.Action != ActionCreated {
		t.Errorf("action = %q, want created", r.Action)
	}
}

// TestIdempotentWriteSkippedUnowned verifies a user-owned file is refused.
func TestIdempotentWriteSkippedUnowned(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	// Pre-create a user-owned file (no marker).
	full := filepath.Join(root, "deploy/k8s/deployment.yaml")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("user content"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := &LockFile{Version: Version}
	spec := FileSpec{
		Path: "deploy/k8s/deployment.yaml", Content: "new",
		Marker: MarkOwned, CommentPrefix: "#",
	}
	r, err := IdempotentWrite(root, spec, false, lock)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if r.Action != ActionSkippedUnowned {
		t.Errorf("action = %q, want skipped-unowned (INFRA-038)", r.Action)
	}
}

// TestIdempotentWriteForceOverwritesUnowned verifies --force overrides.
func TestIdempotentWriteForceOverwritesUnowned(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	full := filepath.Join(root, "deploy/k8s/deployment.yaml")
	os.MkdirAll(filepath.Dir(full), 0o755)
	os.WriteFile(full, []byte("# ogon:owned\nold content"), 0o644)

	lock := &LockFile{Version: Version, Entries: []LockEntry{{
		Path:   "deploy/k8s/deployment.yaml",
		SHA256: SHA256Hex("# ogon:owned\nold content"),
		Marker: string(MarkOwned),
	}}}
	spec := FileSpec{
		Path: "deploy/k8s/deployment.yaml", Content: "new content",
		Marker: MarkOwned, CommentPrefix: "#",
	}
	r, err := IdempotentWrite(root, spec, true, lock)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if r.Action != ActionForcedOverwrite {
		t.Errorf("action = %q, want forced-overwrite (INFRA-040)", r.Action)
	}
}

// TestIdempotentWriteSkipsIdentical verifies no-op on identical content.
func TestIdempotentWriteSkipsIdentical(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	spec := FileSpec{
		Path: "Dockerfile", Content: "FROM scratch\n",
		Marker: MarkOwned, CommentPrefix: "#",
	}
	lock := &LockFile{Version: Version}
	// First write creates.
	if _, err := IdempotentWrite(root, spec, false, lock); err != nil {
		t.Fatal(err)
	}
	// Second write skips.
	r, err := IdempotentWrite(root, spec, false, lock)
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != ActionSkippedIdentical {
		t.Errorf("action = %q, want skipped-identical (INFRA-039)", r.Action)
	}
}

// TestIdempotentWriteEditedRefusesForce verifies a user-edited owned file
// is refused without --force.
func TestIdempotentWriteEditedRefusesForce(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	spec := FileSpec{
		Path: "deploy/k8s/deployment.yaml", Content: "new content",
		Marker: MarkOwned, CommentPrefix: "#",
	}
	originalStamped := spec.Stamped()
	// On-disk: owned marker, but content was hand-edited (different from ledger).
	full := filepath.Join(root, "deploy/k8s/deployment.yaml")
	os.MkdirAll(filepath.Dir(full), 0o755)
	handEdited := Stamp("hand-edited body", MarkOwned, "#")
	os.WriteFile(full, []byte(handEdited), 0o644)

	lock := &LockFile{Version: Version, Entries: []LockEntry{{
		Path:   "deploy/k8s/deployment.yaml",
		SHA256: SHA256Hex(originalStamped),
		Marker: string(MarkOwned),
	}}}
	r, err := IdempotentWrite(root, spec, false, lock)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if r.Action != ActionSkippedEdited {
		t.Errorf("action = %q, want skipped-edited (INFRA-040)", r.Action)
	}
}

// TestLoadLockMissingFile verifies a missing ledger returns an empty LockFile.
func TestLoadLockMissingFile(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	lf, err := LoadLock(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if lf == nil || len(lf.Entries) != 0 {
		t.Errorf("expected empty lock, got %+v", lf)
	}
}

// TestSaveLoadRoundTrip verifies the ledger survives a save → load cycle.
func TestSaveLoadRoundTrip(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	in := &LockFile{Version: Version, Entries: []LockEntry{{
		Path: "Dockerfile", SHA256: "abc123", Marker: string(MarkOwned),
	}}}
	if err := SaveLock(root, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := LoadLock(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Path != "Dockerfile" {
		t.Errorf("round-trip mismatch: %+v", out.Entries)
	}
}

// TestSaveLockSortsEntries verifies the ledger is sorted on save (INFRA-039).
func TestSaveLockSortsEntries(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	in := &LockFile{Version: Version, Entries: []LockEntry{
		{Path: "z"}, {Path: "a"}, {Path: "m"},
	}}
	if err := SaveLock(root, in); err != nil {
		t.Fatal(err)
	}
	out, _ := LoadLock(root)
	want := []string{"a", "m", "z"}
	for i, e := range out.Entries {
		if e.Path != want[i] {
			t.Errorf("entry[%d] = %q, want %q (INFRA-039)", i, e.Path, want[i])
		}
	}
}

// TestSHA256Hex verifies the hash function is stable.
func TestSHA256Hex(t *testing.T) {
	if got := SHA256Hex("hello"); len(got) != 64 {
		t.Errorf("hash len = %d, want 64", len(got))
	}
	if SHA256Hex("hello") != SHA256Hex("hello") {
		t.Error("hash not stable")
	}
	if SHA256Hex("hello") == SHA256Hex("world") {
		t.Error("distinct inputs produced identical hash")
	}
}

// TestSortSpecs verifies stable ordering.
func TestSortSpecs(t *testing.T) {
	in := []FileSpec{
		{Path: "z"}, {Path: "a"}, {Path: "m"},
	}
	out := SortSpecs(in)
	want := []string{"a", "m", "z"}
	for i, s := range out {
		if s.Path != want[i] {
			t.Errorf("out[%d] = %q, want %q", i, s.Path, want[i])
		}
	}
}

// TestApplyLockUpdatesEntries verifies ledger updates in-place.
func TestApplyLockUpdatesEntries(t *testing.T) {
	lock := &LockFile{Version: Version, Entries: []LockEntry{{Path: "a"}}}
	specs := []FileSpec{{Path: "a", Content: "x", Marker: MarkOwned, CommentPrefix: "#"}}
	ApplyLock(lock, specs, "docker")
	if len(lock.Entries) != 1 || lock.Entries[0].Generator != "docker" {
		t.Errorf("ApplyLock did not update: %+v", lock.Entries)
	}
	specs2 := []FileSpec{{Path: "b", Content: "y", Marker: MarkOwned, CommentPrefix: "#"}}
	ApplyLock(lock, specs2, "compose")
	if len(lock.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(lock.Entries))
	}
}

// TestDiffUnchanged verifies the drift report on a fresh generation.
func TestDiffUnchanged(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	spec := FileSpec{Path: "Dockerfile", Content: "FROM scratch", Marker: MarkOwned, CommentPrefix: "#"}
	lock := &LockFile{Version: Version}
	IdempotentWrite(root, spec, false, lock)
	ApplyLock(lock, []FileSpec{spec}, "docker")
	report, err := Diff(root, lock, []string{"Dockerfile"})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if report.HasDrift() {
		t.Errorf("expected no drift on fresh generation, got %+v", report.Entries)
	}
}

// TestDiffMissing verifies the drift report flags absent files.
func TestDiffMissing(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	lock := &LockFile{Version: Version, Entries: []LockEntry{{
		Path: "Dockerfile", SHA256: "abc",
	}}}
	report, err := Diff(root, lock, []string{"Dockerfile"})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(report.Entries) != 1 || report.Entries[0].Status != DriftMissing {
		t.Errorf("expected missing, got %+v", report.Entries)
	}
}

// TestDiffDrift verifies the drift report flags changed files.
func TestDiffDrift(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	// Write a file with original content.
	orig := Stamp("orig", MarkOwned, "#")
	full := filepath.Join(root, "Dockerfile")
	os.WriteFile(full, []byte(orig), 0o644)

	// Ledger records the original hash; on-disk content differs.
	handEdited := Stamp("hand-edited body", MarkOwned, "#")
	os.WriteFile(full, []byte(handEdited), 0o644)
	lock := &LockFile{Version: Version, Entries: []LockEntry{{
		Path: "Dockerfile", SHA256: SHA256Hex(orig), Marker: string(MarkOwned),
	}}}
	report, err := Diff(root, lock, []string{"Dockerfile"})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(report.Entries) != 1 || report.Entries[0].Status != DriftDrift {
		t.Errorf("expected drift, got %+v", report.Entries)
	}
}

// TestDiffExtra verifies extra files in infra/ are flagged.
func TestDiffExtra(t *testing.T) {
	root, cleanup := writeTemp(t)
	defer cleanup()
	// Drop a stray YAML in infra/ that has no ledger entry.
	infraDir := filepath.Join(root, "infra", "terraform", "aws")
	os.MkdirAll(infraDir, 0o755)
	os.WriteFile(filepath.Join(infraDir, "stray.tf"), []byte("# stray"), 0o644)

	lock := &LockFile{Version: Version}
	report, err := Diff(root, lock, nil)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	found := false
	for _, e := range report.Entries {
		if e.Status == DriftExtra && e.Path == "infra/terraform/aws/stray.tf" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected extra entry for stray.tf, got %+v", report.Entries)
	}
}
