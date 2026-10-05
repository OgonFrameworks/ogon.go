// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Wrapper around debug.SetMemoryLimit to keep limits package free of
// importing runtime/debug at the public API surface.

package limits

import "runtime/debug"

// SetMemoryLimit passes through to debug.SetMemoryLimit. Isolated here so
// tests can stub it. The argument is in bytes (Go 1.27+).
func SetMemoryLimit(bytes int64) {
	debug.SetMemoryLimit(bytes)
}
