// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Idempotent stable-order generation (INFRA-039) and --force semantics
// (INFRA-040). The generator never writes a file whose content is identical
// to the last generation; unowned files require --force; owned files are
// safe to overwrite unless the user has locally edited them since the last
// generation, in which case --force is required.
//
// The ledger is a single JSON file at <repo-root>/.ogon-infra.lock. It maps
// repo-relative path → {marker, sha256, generator, generated_at}. The CLI
// updates the ledger atomically after a successful generation pass.

package infra

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// LockEntry is one file's generation state.
type LockEntry struct {
	Path        string    `json:"path"`
	Marker      string    `json:"marker"`
	SHA256      string    `json:"sha256"`
	Generator   string    `json:"generator,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
}

// LockFile is the on-disk ledger. Version bumps when the entry shape changes.
type LockFile struct {
	Version string      `json:"version"`
	Entries []LockEntry `json:"entries"`
}

// LoadLock reads the ledger from <root>/.ogon-infra.lock. A missing ledger
// returns an empty LockFile (zero-value), not an error — the first run has
// nothing to diff against.
func LoadLock(root string) (*LockFile, error) {
	p := filepath.Join(root, ".ogon-infra.lock")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &LockFile{Version: Version, Entries: nil}, nil
		}
		return nil, fmt.Errorf("read lock: %w", err)
	}
	var lf LockFile
	if err := json.Unmarshal(b, &lf); err != nil {
		return nil, fmt.Errorf("parse lock: %w", err)
	}
	if lf.Version == "" {
		lf.Version = Version
	}
	return &lf, nil
}

// SaveLock writes the ledger atomically (write-temp + rename).
func SaveLock(root string, lf *LockFile) error {
	if lf.Version == "" {
		lf.Version = Version
	}
	sort.Slice(lf.Entries, func(i, j int) bool { return lf.Entries[i].Path < lf.Entries[j].Path })
	b, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lock: %w", err)
	}
	p := filepath.Join(root, ".ogon-infra.lock")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write lock: %w", err)
	}
	return os.Rename(tmp, p)
}

// Lookup returns the ledger entry for path, or nil if absent.
func (l *LockFile) Lookup(path string) *LockEntry {
	for i := range l.Entries {
		if l.Entries[i].Path == path {
			return &l.Entries[i]
		}
	}
	return nil
}

// Action is the disposition of a write attempt.
type Action string

const (
	ActionCreated          Action = "created"
	ActionUpdated          Action = "updated"
	ActionSkippedIdentical Action = "skipped-identical"
	ActionSkippedUnowned   Action = "skipped-unowned"
	ActionSkippedEdited    Action = "skipped-edited"
	ActionForcedOverwrite  Action = "forced-overwrite"
)

// WriteResult is what IdempotentWrite returns. Caller surfaces this in the
// --dry-run plan or the actual gen report.
type WriteResult struct {
	Path   string
	Action Action
	Reason string
}

// SHA256Hex returns the hex SHA-256 of content.
func SHA256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// FileSHA256 reads a file and returns its hex SHA-256. Missing file → "".
func FileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// IdempotentWrite applies one FileSpec to disk under root, honouring
// ownership rules (INFRA-038/040). The lock is consulted to detect manual
// edits: an existing file whose current hash differs from the ledger's
// recorded hash is considered "edited" and requires --force to overwrite.
//
// Write decision matrix (content = Stamped spec content; sha = SHA256(content)):
//   - target missing → write (ActionCreated)
//   - target exists, hash matches sha → skip (ActionSkippedIdentical)
//   - target exists, has MarkOwned marker, hash differs → write (ActionUpdated)
//   - target exists, has MarkSeeded marker → refuse without force (ActionSkippedEdited)
//   - target exists, no marker (user-owned) → refuse without force (ActionSkippedUnowned)
//   - target exists, has MarkOwned marker but ledger says user edited → refuse without force
//   - --force → write regardless, except when the file is user-owned AND the
//     ledger records no prior generation (truly a user file)
func IdempotentWrite(root string, spec FileSpec, force bool, lock *LockFile) (WriteResult, error) {
	path := filepath.Join(root, spec.Path)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return WriteResult{}, fmt.Errorf("stat %s: %w", spec.Path, err)
	}
	content := spec.Stamped()
	sha := SHA256Hex(content)

	if existing == nil {
		// target missing → write
		if err := mkdirpWrite(path, content); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{Path: spec.Path, Action: ActionCreated}, nil
	}

	// Same content? skip
	if SHA256Hex(string(existing)) == sha {
		return WriteResult{Path: spec.Path, Action: ActionSkippedIdentical, Reason: "content unchanged"}, nil
	}

	marker := MarkerOf(string(existing))
	ledger := lock.Lookup(spec.Path)

	// Edited detection (INFRA-040): the file carries the owned marker but
	// its current hash does not match the ledger → user edited it.
	edited := false
	if ledger != nil && SHA256Hex(string(existing)) != ledger.SHA256 {
		edited = true
	}

	switch {
	case force:
		// --force writes regardless. We refuse only if the file is genuinely
		// user-owned (no marker, no ledger entry) — that's a destructive
		// overwrite of the user's source.
		if marker == "" && ledger == nil {
			return WriteResult{Path: spec.Path, Action: ActionSkippedUnowned,
				Reason: "file is user-owned (no marker, no ledger); refusing even with --force"}, nil
		}
		if err := mkdirpWrite(path, content); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{Path: spec.Path, Action: ActionForcedOverwrite,
			Reason: "force-overwrote " + string(marker)}, nil

	case marker == MarkOwned && !edited:
		// safe to regenerate
		if err := mkdirpWrite(path, content); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{Path: spec.Path, Action: ActionUpdated}, nil

	case marker == MarkOwned && edited:
		return WriteResult{Path: spec.Path, Action: ActionSkippedEdited,
			Reason: "owned file has local edits; use --force"}, nil

	case marker == MarkSeeded:
		return WriteResult{Path: spec.Path, Action: ActionSkippedEdited,
			Reason: "seeded file; use --force to regenerate"}, nil

	default: // marker == "" → user-owned
		return WriteResult{Path: spec.Path, Action: ActionSkippedUnowned,
			Reason: "file is unowned; use --force"}, nil
	}
}

// mkdirpWrite writes content to path, creating parent dirs as needed.
func mkdirpWrite(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// SortSpecs returns specs in stable lexicographic order by path (INFRA-039).
// The CLI runs generators through this before writing so the on-disk plan is
// deterministic regardless of generator registration order.
func SortSpecs(specs []FileSpec) []FileSpec {
	out := make([]FileSpec, len(specs))
	copy(out, specs)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ApplyLock updates the ledger to record a successful generation pass. Specs
// not in the lock are added; specs present in the lock but not in this run are
// left untouched (they may belong to another generator).
func ApplyLock(lock *LockFile, specs []FileSpec, generator string) {
	now := time.Now().UTC()
	for _, s := range specs {
		content := s.Stamped()
		entry := LockEntry{
			Path: s.Path, Marker: string(s.Marker),
			SHA256: SHA256Hex(content), Generator: generator, GeneratedAt: now,
		}
		if existing := lock.Lookup(s.Path); existing != nil {
			*existing = entry
		} else {
			lock.Entries = append(lock.Entries, entry)
		}
	}
}
