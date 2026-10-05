// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Immutable, hash-chained audit log (SEC-028, SEC-029).
//
// Every state-mutating request is appended to this log. Each entry's
// prev-hash links to the prior entry's, so any tampering breaks the
// chain. A periodic verifier recomputes the chain from genesis and
// rejects if a node diverges (SEC-029).

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// AuditEntry is one record in the chain. All fields are immutable
// after Append.
type AuditEntry struct {
	Seq      int64     `json:"seq"`
	At       time.Time `json:"at"`
	Who      string    `json:"who"`   // user ID
	What     string    `json:"what"`  // action, e.g. "billing.charge"
	Where    string    `json:"where"` // route/endpoint
	IP       string    `json:"ip"`
	Tenant   string    `json:"tenant,omitempty"`
	Status   int       `json:"status"` // HTTP status if applicable
	PrevHash string    `json:"prev"`   // hex(sha256(prev entry))
	Hash     string    `json:"hash"`   // hex(sha256(entry-canonical))
	Metadata any       `json:"meta,omitempty"`
}

// AuditStore persists entries. The default is in-memory; production
// uses an append-only table whose row-hash matches Hash.
type AuditStore interface {
	Append(ctx context.Context, e AuditEntry) error
	Verify(ctx context.Context) (int64, error) // returns seq of first divergence, -1 if clean
}

// Audit is the entry-point coordinator.
type Audit struct {
	mu      sync.Mutex
	store   AuditStore
	genesis string // genesis hash (for first-entry linkage)
}

// NewAudit constructs an Audit chain over store.
func NewAudit(store AuditStore) *Audit {
	return &Audit{store: store, genesis: "genesis"}
}

// Record builds+appends an entry, returning its hash. The chain is
// locked during append so concurrent writers serialize.
func (a *Audit) Record(ctx context.Context, who, what, where, ip, tenant string, status int, meta any) (string, error) {
	if a == nil {
		return "", errors.New("audit: nil audit")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// we don't know the seq/hash without first computing the prev link.
	// The store implementations compute prev from the last entry.
	prev := a.genesis
	e := AuditEntry{
		At:  time.Now().UTC(),
		Who: who, What: what, Where: where, IP: ip,
		Tenant: tenant, Status: status,
		PrevHash: prev, Metadata: meta,
	}
	// Seq is assigned by the store; we compute Hash without Seq then
	// ask the store to fill Seq (store does that atomically).
	e.Hash = computeHash(e, 0)
	if err := a.store.Append(ctx, e); err != nil {
		return "", err
	}
	return e.Hash, nil
}

// computeHash canonicalizes (excluding the Seq itself so the hash is
// stable across store-assigned seqs) and returns hex(sha256).
func computeHash(e AuditEntry, seq int64) string {
	// canonical JSON ordering
	canon, _ := json.Marshal(struct {
		At       time.Time `json:"at"`
		Who      string    `json:"who"`
		What     string    `json:"what"`
		Where    string    `json:"where"`
		IP       string    `json:"ip"`
		Tenant   string    `json:"tenant,omitempty"`
		Status   int       `json:"status"`
		PrevHash string    `json:"prev"`
		Seq      int64     `json:"seq"`
		Metadata any       `json:"meta,omitempty"`
	}{
		At: e.At, Who: e.Who, What: e.What, Where: e.Where, IP: e.IP,
		Tenant: e.Tenant, Status: e.Status, PrevHash: e.PrevHash,
		Seq: seq, Metadata: e.Metadata,
	})
	h := sha256.Sum256(canon)
	return hex.EncodeToString(h[:])
}

// ---- in-memory backend ----

// MemoryAuditStore is the default in-memory backend.
type MemoryAuditStore struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// NewMemoryAuditStore returns a ready store.
func NewMemoryAuditStore() *MemoryAuditStore {
	return &MemoryAuditStore{entries: make([]AuditEntry, 0, 1024)}
}

// Append appends an entry, assigning Seq and recomputing PrevHash/Hash.
func (s *MemoryAuditStore) Append(_ context.Context, e AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var prev string
	if len(s.entries) > 0 {
		prev = s.entries[len(s.entries)-1].Hash
	} else {
		prev = "genesis"
	}
	e.PrevHash = prev
	e.Seq = int64(len(s.entries)) + 1
	e.Hash = computeHash(e, e.Seq)
	s.entries = append(s.entries, e)
	return nil
}

// Verify walks the chain and returns the seq of the first divergence
// (-1 if clean). This is the SEC-029 tamper-detection surface.
func (s *MemoryAuditStore) Verify(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := "genesis"
	for i, e := range s.entries {
		if e.PrevHash != prev {
			return e.Seq, nil // divergence at seq e.Seq
		}
		// recompute Hash, excluding Hash itself (computeHash is idempotent)
		want := computeHash(e, e.Seq)
		if want != e.Hash {
			return e.Seq, nil
		}
		prev = e.Hash
		_ = i
	}
	return -1, nil
}

// All returns a copy of all entries (for tests).
func (s *MemoryAuditStore) All() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEntry, len(s.entries))
	copy(out, s.entries)
	return out
}
