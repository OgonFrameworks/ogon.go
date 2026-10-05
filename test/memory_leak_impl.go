// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// defaultReadMemStats implementation for the memory-leak soak template
// (TEST-052). Kept in its own file so a test stub can override the
// readMemStats var without rebuilding the whole fixture.

package test

import "runtime"

// memSnapshot is the minimal subset of runtime.MemStats the soak template
// uses. Kept here so callers do not depend on runtime internals.
type memSnapshot struct {
	HeapAlloc uint64
}

// defaultReadMemStats is the production readMemStats implementation.
func defaultReadMemStats() memSnapshot {
	var s runtime.MemStats
	runtime.ReadMemStats(&s)
	return memSnapshot{HeapAlloc: s.HeapAlloc}
}
