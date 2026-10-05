// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Snapshot tests (TEST-012). The fixture reads/writes golden files from
// testdata/ and supports both JSON and HTML payloads. Update mode rewrites
// the golden file on first run or when OGON_TEST_UPDATE=1 is set.

package test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Snapshot is the per-test golden-file holder. One snapshot = one golden
// file under testdata/golden/<name>. Tests call AssertJSON or AssertHTML.
type Snapshot struct {
	t      *testing.T
	name   string
	dir    string
	update bool
}

var (
	snapshotDirOnce sync.Once
	snapshotDir     = "testdata/golden"
)

// SetSnapshotDir overrides the default testdata/golden location. Tests
// rarely need this; the default matches the spec convention.
func SetSnapshotDir(dir string) {
	snapshotDirOnce.Do(func() {})
	snapshotDir = dir
}

// NewSnapshot constructs a Snapshot bound to t. The name should be unique
// within the package; t.Name() is a safe default.
func NewSnapshot(t *testing.T, name string) *Snapshot {
	t.Helper()
	if name == "" {
		name = t.Name()
	}
	return &Snapshot{
		t:      t,
		name:   name,
		dir:    snapshotDir,
		update: os.Getenv("OGON_TEST_UPDATE") == "1",
	}
}

// AssertJSON compares v (marshalled canonically) to the golden file. On
// update mode, the file is rewritten.
func (s *Snapshot) AssertJSON(v any) {
	s.t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		s.t.Fatalf("ogontest: snapshot marshal: %v", err)
	}
	data = append(data, '\n')
	s.assertBytes(data, ".json")
}

// AssertHTML compares raw HTML to the golden file.
func (s *Snapshot) AssertHTML(html []byte) {
	s.t.Helper()
	s.assertBytes(html, ".html")
}

// AssertText compares raw text to the golden file.
func (s *Snapshot) AssertText(text []byte) {
	s.t.Helper()
	s.assertBytes(text, ".txt")
}

func (s *Snapshot) assertBytes(got []byte, ext string) {
	s.t.Helper()
	path := filepath.Join(s.dir, s.name+ext)
	if s.update {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			s.t.Fatalf("ogontest: snapshot mkdir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			s.t.Fatalf("ogontest: snapshot write: %v", err)
		}
		s.t.Logf("ogontest: snapshot updated: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.t.Fatalf("ogontest: snapshot missing: %s\nrun with OGON_TEST_UPDATE=1 to create", path)
	}
	if err != nil {
		s.t.Fatalf("ogontest: snapshot read: %v", err)
	}
	if !bytesEqual(want, got) {
		s.t.Fatalf("ogontest: snapshot mismatch: %s\nwant: %q\ngot:  %q", path, want, got)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
