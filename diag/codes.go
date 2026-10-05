// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Stable E-code registry. Codes are a public contract; never reused.

package diag

// Class identifies the broad area a diagnostic belongs to.
type Class rune

const (
	ClassCompile       Class = 'C'
	ClassRoute         Class = 'R'
	ClassValidation    Class = 'V'
	ClassConfig        Class = 'K'
	ClassMigration     Class = 'M'
	ClassDependency    Class = 'D'
	ClassSecurity      Class = 'S'
	ClassRuntime       Class = 'U'
	ClassGenerationCfl Class = 'G'
)

// Code is a stable diagnostic identifier of the form OGON-<class><nnnn>.
type Code string

// Well-known codes used across the framework.
const (
	// Compile / codegen problems (C)
	CodeGenConflict       Code = "OGON-C0001"
	CodeUnknownDirective  Code = "OGON-C0002"
	CodeTypeMismatch      Code = "OGON-C0003"
	CodeMissingImport     Code = "OGON-C0004"
	CodeCycleDetected     Code = "OGON-C0005"
	CodeUnownedFile       Code = "OGON-C0006"
	CodeGenerationAborted Code = "OGON-C0007"

	// Route problems (R)
	CodeRouteConflict         Code = "OGON-R0001"
	CodeRouteParamInvalid     Code = "OGON-R0002"
	CodeRouteMissing          Code = "OGON-R0003"
	CodeRouteMethodNotAllowed Code = "OGON-R0004"

	// Validation problems (V)
	CodeValidationFailed Code = "OGON-V0001"
	CodeBindingFailed    Code = "OGON-V0002"
	CodeRequiredMissing  Code = "OGON-V0003"
	CodeInvalidFormat    Code = "OGON-V0004"

	// Config problems (K)
	CodeConfigInvalid       Code = "OGON-K0001"
	CodeConfigMissingKey    Code = "OGON-K0002"
	CodeConfigUnknownKey    Code = "OGON-K0003"
	CodeConfigTypeMismatch  Code = "OGON-K0004"
	CodeConfigSecretMissing Code = "OGON-K0005"

	// Migration problems (M)
	CodeMigrationUnsafe   Code = "OGON-M0001"
	CodeMigrationConflict Code = "OGON-M0002"
	CodeMigrationFailed   Code = "OGON-M0003"
	CodeMigrationMissing  Code = "OGON-M0004"

	// Dependency / environment problems (D)
	CodeDepMissing Code = "OGON-D0001"
	CodeDepVersion Code = "OGON-D0002"
	CodeDepCyclic  Code = "OGON-D0003"

	// Security problems (S)
	CodeSecPolicy      Code = "OGON-S0001"
	CodeSecAuthFailed  Code = "OGON-S0002"
	CodeSecAuthzDenied Code = "OGON-S0003"
	CodeSecCSRF        Code = "OGON-S0004"
	CodeSecRateLimited Code = "OGON-S0005"

	// Runtime problems (U)
	CodeUUnknown          Code = "OGON-U0001"
	CodeURuntimePanic     Code = "OGON-U0002"
	CodeUShutdown         Code = "OGON-U0003"
	CodeUResource         Code = "OGON-U0004"
	CodeUDeadlineExceeded Code = "OGON-U0005"

	// Generation conflict problems (G)
	CodeGUnownedEdit Code = "OGON-G0001"
	CodeGStaleGen    Code = "OGON-G0002"
)

// docsURLPrefix is the canonical docs prefix; appended to the code lowercase.
const docsURLPrefix = "https://ogongo.dev/errors/"

// DocsURL returns the canonical docs URL for a code.
func DocsURL(c Code) string {
	return docsURLPrefix + string(c)
}

// retired prevents reuse of retired codes.
var retired = map[Code]struct{}{}

// Reserve marks a code as retired so it cannot be reused.
func Reserve(c Code) {
	retired[c] = struct{}{}
}

// IsRetired reports whether a code has been retired.
func IsRetired(c Code) bool {
	_, ok := retired[c]
	return ok
}
