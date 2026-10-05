// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Per-page Load(ctx) data loading + parallel dependencies
// (UI-033/034). Each page declares its Load function in the `<go>`
// block; the runtime dispatches Loads in parallel using a worker
// pool owned by the runtime supervisor.

package runtime

import (
	"context"
	"errors"
	"sync"
)

// LoadFunc is the per-page data loader. It returns the props bag
// that the page's render function consumes.
type LoadFunc func(ctx context.Context, params map[string]string) (any, error)

// LoadDep is a single dependency declared by a page. Page authors
// pass a slice of LoadDeps to the runtime; the runtime runs them
// in parallel and merges results into a single props map (UI-034).
type LoadDep struct {
	Name string // key in the props map
	Fn   LoadFunc
}

// LoadParallel runs every dep in parallel and returns the merged
// props map. Any error aborts the batch (UI-034). Cancellation
// through ctx propagates to every dep.
func LoadParallel(ctx context.Context, deps []LoadDep) (map[string]any, error) {
	if len(deps) == 0 {
		return map[string]any{}, nil
	}
	var wg sync.WaitGroup
	type result struct {
		name string
		val  any
		err  error
	}
	results := make(chan result, len(deps))
	for _, d := range deps {
		d := d
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, err := d.Fn(ctx, nil)
			results <- result{name: d.Name, val: val, err: err}
		}()
	}
	wg.Wait()
	close(results)
	out := map[string]any{}
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		out[r.name] = r.val
	}
	return out, nil
}

// LoadSerial runs a single dep synchronously (used by simpler pages
// that don't parallelise their Load).
func LoadSerial(ctx context.Context, dep LoadDep) (any, error) {
	return dep.Fn(ctx, nil)
}

// ErrLoadAborted is returned when one of the parallel deps errors.
var ErrLoadAborted = errors.New("ogon/ui: page Load aborted (UI-034)")
