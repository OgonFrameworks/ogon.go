// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Normative CLI exit codes. See PROMPT.md Part III.2.
//
// Exit codes are a public contract. Never reuse a retired code. Adding new
// codes is permitted only by spec amendment; the values below mirror the
// normative table and must not drift.

package cli

// Exit codes (normative, Part III.2).
const (
	// ExitOK: command succeeded.
	ExitOK = 0
	// ExitGenericError: generic / unclassified failure.
	ExitGenericError = 1
	// ExitUsage: usage error (unknown command/flag, bad args).
	ExitUsage = 2
	// ExitConfigInvalid: configuration invalid (ogon.yaml bad/missing).
	ExitConfigInvalid = 3
	// ExitMigrationUnsafe: migration is unsafe or requires confirmation.
	ExitMigrationUnsafe = 4
	// ExitGenConflict: generation conflict (would overwrite an unowned file).
	ExitGenConflict = 5
	// ExitTestFailure: one or more tests failed.
	ExitTestFailure = 6
	// ExitBuildFailure: build failed.
	ExitBuildFailure = 7
	// ExitDoctorFailure: doctor detected an unfixable problem.
	ExitDoctorFailure = 8
	// ExitInterrupted: process interrupted (SIGINT).
	ExitInterrupted = 130
)

// exitNames maps each code to a stable symbolic name used in diagnostics and
// `ogon explain`. Order is not significant; codes are stable forever.
var exitNames = map[int]string{
	ExitOK:              "OK",
	ExitGenericError:    "GenericError",
	ExitUsage:           "Usage",
	ExitConfigInvalid:   "ConfigInvalid",
	ExitMigrationUnsafe: "MigrationUnsafe",
	ExitGenConflict:     "GenConflict",
	ExitTestFailure:     "TestFailure",
	ExitBuildFailure:    "BuildFailure",
	ExitDoctorFailure:   "DoctorFailure",
	ExitInterrupted:     "Interrupted",
}

// ExitName returns the stable symbolic name for a code, or "Unknown".
func ExitName(code int) string {
	if n, ok := exitNames[code]; ok {
		return n
	}
	return "Unknown"
}

// ExitDescription returns a short human description for a code.
func ExitDescription(code int) string {
	switch code {
	case ExitOK:
		return "ok"
	case ExitGenericError:
		return "generic error"
	case ExitUsage:
		return "usage error"
	case ExitConfigInvalid:
		return "configuration invalid"
	case ExitMigrationUnsafe:
		return "migration unsafe / needs confirm"
	case ExitGenConflict:
		return "generation conflict (unowned file)"
	case ExitTestFailure:
		return "test failure"
	case ExitBuildFailure:
		return "build failure"
	case ExitDoctorFailure:
		return "doctor failure"
	case ExitInterrupted:
		return "interrupted"
	}
	return "unknown"
}

// AllExitCodes returns every normative code, in numeric order. Useful for
// `ogon explain exit-codes` and parity tests.
func AllExitCodes() []int {
	return []int{
		ExitOK,
		ExitGenericError,
		ExitUsage,
		ExitConfigInvalid,
		ExitMigrationUnsafe,
		ExitGenConflict,
		ExitTestFailure,
		ExitBuildFailure,
		ExitDoctorFailure,
		ExitInterrupted,
	}
}
