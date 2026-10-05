// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Migration helpers — shared utilities for the migration engine. Kept
// separate so migration.go stays focused on the diff/emit logic.

package record

import (
	"reflect"
	"time"
)

// reflectPtrKind reports whether the supplied type's Kind is Pointer.
// Isolated as a helper so migration.go doesn't need a direct reflect import
// (and so generated code in a future phase can replace it cleanly).
func reflectPtrKind() reflect.Kind { return reflect.Ptr }

// migrationTimestamp returns a Unix-seconds migration version. Tests
// can swap it via SetMigrationTimestampForTest.
var migrationTimestamp = func() int64 { return time.Now().UTC().Unix() }

// SetMigrationTimestampForTest overrides the version-stamping function.
// The name carries the ForTest suffix to discourage production use.
func SetMigrationTimestampForTest(f func() int64) { migrationTimestamp = f }
