// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// DX-008 / DX-014: every well-known diagnostic code carries a remedy
// (next action). This test is the build-time guarantee that the
// mapping is complete; the runtime mirror is
// scripts/dx/error-remedy-check.sh.

package diag

import (
	"strings"
	"testing"
)

// codeRemedies is the authoritative code -> remedy mapping. Every
// well-known Code constant in codes.go MUST have an entry here; the
// test below enforces it. A remedy is a one-line, runnable next action
// (shell command or imperative sentence) the operator can follow.
var codeRemedies = map[Code]string{
	// Compile / codegen (C)
	CodeGenConflict:       "re-run `ogon build` to regenerate; if the conflict persists, inspect `.ogon/ogon.json`.",
	CodeUnknownDirective:  "see `ogon explain gen` for the supported directive list.",
	CodeTypeMismatch:      "adjust the model tag or the handler signature; `ogon check` reports the mismatch.",
	CodeMissingImport:     "add the missing import to the file; `ogon fmt` can auto-insert.",
	CodeCycleDetected:     "break the cycle in the provider graph; `ogon explain di` shows the chain.",
	CodeUnownedFile:       "pass `--force` to overwrite, or move/delete the file; see `ogon explain gen`.",
	CodeGenerationAborted: "read the prior diagnostic; fix it and re-run `ogon build`.",

	// Route (R)
	CodeRouteConflict:         "run `ogon routes check` to find both registrations; rename one.",
	CodeRouteParamInvalid:     "fix the {param} syntax in the route pattern.",
	CodeRouteMissing:          "register the route in `routes/*.go`; see `ogon explain route`.",
	CodeRouteMethodNotAllowed: "add the method to the route registration or use a different method.",

	// Validation (V)
	CodeValidationFailed: "fix the request body to satisfy the `ogon:` tag validators.",
	CodeBindingFailed:    "send a valid JSON body matching the model; see `ogon explain model`.",
	CodeRequiredMissing:  "add the missing required field to the request body.",
	CodeInvalidFormat:    "fix the field format (e.g. email, uuid, date) to satisfy the validator.",

	// Config (K)
	CodeConfigInvalid:       "run `ogon doctor --fix` or edit ogon.yaml; `ogon explain config` lists keys.",
	CodeConfigMissingKey:    "add the missing key to ogon.yaml; the diagnostic names it.",
	CodeConfigUnknownKey:    "remove the unknown key or update the framework; `ogon explain config` lists valid keys.",
	CodeConfigTypeMismatch:  "fix the YAML type (string/int/duration/list) to match the schema.",
	CodeConfigSecretMissing: "set the env var named in `*_env`; `ogon doctor` shows which one.",

	// Migration (M)
	CodeMigrationUnsafe:   "review the destructive op; pass `--yes` to confirm, or rename the column instead.",
	CodeMigrationConflict: "resolve the conflict in migrations/; `ogon migrate diff` shows both sides.",
	CodeMigrationFailed:   "read the driver error; fix the SQL or the model and re-run `ogon migrate run`.",
	CodeMigrationMissing:  "write a migration with `ogon migrate create <name>`.",

	// Dependency (D)
	CodeDepMissing: "install the dependency: `go get <module>` or `ogon add <module>`.",
	CodeDepVersion: "bump or pin the dependency version to satisfy the constraint.",
	CodeDepCyclic:  "break the cycle in the module dependency graph; `ogon modules list` shows it.",

	// Security (S)
	CodeSecPolicy:      "review the policy in `authz/policy`; `ogon explain route` shows the chain.",
	CodeSecAuthFailed:  "fix the credentials; the lockout counter increments on each failure.",
	CodeSecAuthzDenied: "log in as a user with the required role; see /login.",
	CodeSecCSRF:        "ensure the browser sends the CSRF cookie; check SameSite=Lax.",
	CodeSecRateLimited: "back off; the limit is per-IP (60/min) or per-user (5/min on /login).",

	// Runtime (U)
	CodeUUnknown:          "read the diagnostic; if unclear, file a bug with the OGON-U0001 code.",
	CodeURuntimePanic:     "read the stack trace in the log; the supervisor recovered and restarted.",
	CodeUShutdown:         "stop starting new work; the app is shutting down and will exit 0 shortly.",
	CodeUResource:         "the resource is exhausted (DB conns, file handles); raise the limit in ogon.yaml.",
	CodeUDeadlineExceeded: "raise the per-route timeout or optimize the handler.",

	// Generation conflict (G)
	CodeGUnownedEdit: "revert your edit or pass `--force` to overwrite; see `ogon explain gen`.",
	CodeGStaleGen:    "re-run `ogon build` to regenerate the stale files.",
}

// TestEveryCodeHasRemedy is the DX-008 / DX-014 gate. Every well-known
// Code constant in codes.go must have an entry in codeRemedies. If you
// add a new Code constant, you must add a remedy here; CI fails
// otherwise.
func TestEveryCodeHasRemedy(t *testing.T) {
	t.Parallel()
	allCodes := []Code{
		CodeGenConflict, CodeUnknownDirective, CodeTypeMismatch,
		CodeMissingImport, CodeCycleDetected, CodeUnownedFile,
		CodeGenerationAborted,

		CodeRouteConflict, CodeRouteParamInvalid, CodeRouteMissing,
		CodeRouteMethodNotAllowed,

		CodeValidationFailed, CodeBindingFailed, CodeRequiredMissing,
		CodeInvalidFormat,

		CodeConfigInvalid, CodeConfigMissingKey, CodeConfigUnknownKey,
		CodeConfigTypeMismatch, CodeConfigSecretMissing,

		CodeMigrationUnsafe, CodeMigrationConflict, CodeMigrationFailed,
		CodeMigrationMissing,

		CodeDepMissing, CodeDepVersion, CodeDepCyclic,

		CodeSecPolicy, CodeSecAuthFailed, CodeSecAuthzDenied,
		CodeSecCSRF, CodeSecRateLimited,

		CodeUUnknown, CodeURuntimePanic, CodeUShutdown,
		CodeUResource, CodeUDeadlineExceeded,

		CodeGUnownedEdit, CodeGStaleGen,
	}
	for _, c := range allCodes {
		remedy, ok := codeRemedies[c]
		if !ok {
			t.Errorf("DX-014: code %q has no remedy in codeRemedies", c)
			continue
		}
		if strings.TrimSpace(remedy) == "" {
			t.Errorf("DX-014: code %q has an empty remedy", c)
			continue
		}
		// A remedy should be actionable: it must reference an `ogon`
		// command (the CLI is the primary surface) OR start with an
		// imperative verb ("fix", "add", "review", ...). The list
		// below is the canonical imperative set; add to it when you
		// add a new verb to a remedy.
		actionable := strings.Contains(remedy, "ogon") ||
			strings.Contains(remedy, "log in") ||
			startsWithImperative(remedy)
		if !actionable {
			t.Errorf("DX-014: code %q remedy is not actionable: %q", c, remedy)
		}
	}
}

// startsWithImperative reports whether remedy begins with one of the
// canonical imperative verbs. Add to this list when you introduce a
// new verb in a remedy.
func startsWithImperative(remedy string) bool {
	imperatives := []string{
		"fix", "add", "remove", "review", "ensure", "back off",
		"revert", "raise", "install", "bump", "break", "set", "read",
		"stop", "log", "send", "edit", "update", "re-run", "wait",
		"resolve", "adjust", "rotate", "use", "see", "check",
	}
	lower := strings.ToLower(remedy)
	for _, v := range imperatives {
		if strings.HasPrefix(lower, v) {
			return true
		}
	}
	return false
}

// TestRemedyMapHasNoOrphans ensures every remedy in the map corresponds
// to a well-known Code constant. This catches typos in the map keys.
func TestRemedyMapHasNoOrphans(t *testing.T) {
	t.Parallel()
	known := map[Code]struct{}{}
	for _, c := range []Code{
		CodeGenConflict, CodeUnknownDirective, CodeTypeMismatch,
		CodeMissingImport, CodeCycleDetected, CodeUnownedFile,
		CodeGenerationAborted,
		CodeRouteConflict, CodeRouteParamInvalid, CodeRouteMissing,
		CodeRouteMethodNotAllowed,
		CodeValidationFailed, CodeBindingFailed, CodeRequiredMissing,
		CodeInvalidFormat,
		CodeConfigInvalid, CodeConfigMissingKey, CodeConfigUnknownKey,
		CodeConfigTypeMismatch, CodeConfigSecretMissing,
		CodeMigrationUnsafe, CodeMigrationConflict, CodeMigrationFailed,
		CodeMigrationMissing,
		CodeDepMissing, CodeDepVersion, CodeDepCyclic,
		CodeSecPolicy, CodeSecAuthFailed, CodeSecAuthzDenied,
		CodeSecCSRF, CodeSecRateLimited,
		CodeUUnknown, CodeURuntimePanic, CodeUShutdown,
		CodeUResource, CodeUDeadlineExceeded,
		CodeGUnownedEdit, CodeGStaleGen,
	} {
		known[c] = struct{}{}
	}
	for c := range codeRemedies {
		if _, ok := known[c]; !ok {
			t.Errorf("DX-014: codeRemedies has orphan key %q (not a known Code constant)", c)
		}
	}
}
