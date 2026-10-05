// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Preload — explicit relations. There is no implicit lazy load in
// OgonRecord. The Preload registry maps a relation name to a resolver
// function that runs after the parent rows are fetched, batching
// queries to avoid N+1.
//
// The framework emits a dev-mode N+1 warning when callers fetch a
// relation outside of Preload (DATA-056/057).

package record

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// Resolver loads a side relation for a slice of parent rows. It MUST
// batch — issue exactly one query per call, never one per parent.
type Resolver func(ctx context.Context, parents []any) error

var (
	preloadMu sync.RWMutex
	resolvers = make(map[string]Resolver)
)

// RegisterPreload registers a relation resolver keyed by "Model.Relation"
// (e.g. "User.Posts"). Re-registering replaces the prior resolver. The
// codegen phase will populate this at init() time; runtime callers may
// register ad-hoc resolvers for tests or escape hatches.
func RegisterPreload(key string, fn Resolver) {
	if key == "" {
		return
	}
	preloadMu.Lock()
	defer preloadMu.Unlock()
	resolvers[key] = fn
}

// ResolvePreload runs all resolvers matching the supplied relations for
// the supplied parent slice. Missing relations produce a diagnostic —
// silent skips would mask typos (DATA-057).
func ResolvePreload(ctx context.Context, modelName string, parents []any, relations []string) error {
	preloadMu.RLock()
	defer preloadMu.RUnlock()
	for _, r := range relations {
		key := modelName + "." + r
		fn, ok := resolvers[key]
		if !ok {
			return diag.New("OGON-D0050", "preload relation not registered",
				"no resolver for "+key+"; add Preload resolver or drop the relation")
		}
		if err := fn(ctx, parents); err != nil {
			return diag.Wrap(err, diag.Diag{
				Code:  "OGON-D0051",
				Title: "preload resolver failed for " + r,
			})
		}
	}
	return nil
}

// WarnPotentialN1 emits a dev-mode warning when it detects a likely N+1
// pattern. The detector is intentionally simple: it counts how many
// times a relation column has been accessed via LazyLookup within a
// single request context. Threshold is 3.
func WarnPotentialN1(ctx context.Context, log *slog.Logger, op string, n int) {
	if log == nil {
		return
	}
	if n >= 3 {
		log.Warn("record: potential N+1 detected",
			"op", op,
			"count", n,
			"hint", "use Preload() to batch-load relations")
	}
}

// LazyLookup is the ONLY permitted per-row relation accessor. It exists
// so callers can opt in to lazy access explicitly (a yellow warning vs
// a red error). Implicit lazy load — defining a struct method that
// queries under the hood — is forbidden by the framework contract.
func LazyLookup(ctx context.Context, parent any, relation string, into any) error {
	if parent == nil {
		return fmt.Errorf("record: LazyLookup nil parent")
	}
	// This is the explicit escape hatch surface; the actual resolver
	// lookup is via ResolvePreload with a slice-of-one.
	return ResolvePreload(ctx, fmt.Sprintf("%T", parent), []any{parent}, []string{relation})
}
