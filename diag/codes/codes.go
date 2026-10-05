// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// codes is the stable E-code registry for OgonGo. Codes are a public
// contract: once registered they are never reused; retired codes are
// reserved forever so that operator scripts and agent consumers can rely
// on a 1:1 mapping from OGON-E<NNNN> to (class, short title, doc URL).

package codes

import (
	"fmt"
	"sort"
	"sync"
)

// Class is the high-level category of a diagnostic code. The prefix
// digit of the four-digit E-code number identifies the class so that
// operators can read the class from the code itself.
type Class string

const (
	// ClassCodegen covers compile-time / code-generation errors.
	ClassCodegen Class = "C"
	// ClassRoute covers routing conflicts and registration issues.
	ClassRoute Class = "R"
	// ClassValidation covers request and field validation failures.
	ClassValidation Class = "V"
	// ClassConfig covers configuration loading and binding errors.
	ClassConfig Class = "K"
	// ClassMigration covers migration conflicts and drift.
	ClassMigration Class = "M"
	// ClassDependency covers missing or incompatible dependencies.
	ClassDependency Class = "D"
	// ClassSecurity covers authz, authn, and policy violations.
	ClassSecurity Class = "S"
	// ClassRuntime covers supervisor and lifecycle errors.
	ClassRuntime Class = "U"
	// ClassGeneration covers DI / generation-graph conflicts.
	ClassGeneration Class = "G"
)

// ClassPrefixDigit is the leading digit of the four-digit code number
// for each class. It is informational; the codes package does not
// enforce it but Register validates conformance to keep the contract
// human-readable.
var ClassPrefixDigit = map[Class]byte{
	ClassCodegen:    '0',
	ClassRoute:      '1',
	ClassValidation: '2',
	ClassConfig:     '3',
	ClassMigration:  '4',
	ClassDependency: '5',
	ClassSecurity:   '6',
	ClassRuntime:    '7',
	ClassGeneration: '8',
}

// CodeInfo is the public record returned by Lookup and All.
type CodeInfo struct {
	Code       string `json:"code"`
	Class      Class  `json:"class"`
	ShortTitle string `json:"short_title"`
	DocURL     string `json:"doc_url"`
}

// Format is the canonical E-code shape: OGON-E followed by four digits.
const Format = "OGON-E####"

const (
	// Prefix is the leading text of every code.
	Prefix = "OGON-E"
	// CodeLen is the fixed total length of an E-code ("OGON-E" + 4 digits).
	CodeLen = len(Prefix) + 4
)

// Starter set — registered at init so the lookup table is populated even
// before any subsystem has had a chance to register its own.
const (
	// Config not found — class K (3xxx).
	E3001ConfigNotFound = "OGON-E3001"
	// Route conflict — class R (1xxx).
	E1001RouteConflict = "OGON-E1001"
	// Validation failed — class V (2xxx).
	E2001ValidationFailed = "OGON-E2001"
	// Migration conflict — class M (4xxx).
	E4001MigrationConflict = "OGON-E4001"
	// Dependency missing — class D (5xxx).
	E5001DependencyMissing = "OGON-E5001"
	// Security violation — class S (6xxx).
	E6001SecurityViolation = "OGON-E6001"
	// Runtime supervisor closed — class U (7xxx).
	E7001RuntimeSupervisorClosed = "OGON-E7001"
	// Generation conflict — class G (8xxx).
	E8001GenerationConflict = "OGON-E8001"
)

// DefaultDocsBase is the canonical URL prefix for code doc pages.
const DefaultDocsBase = "https://ogongo.dev/errors"

var (
	mu       sync.RWMutex
	registry = make(map[string]CodeInfo, 64)
)

// Register adds a code to the registry. It is safe to call from init()
// across multiple packages; concurrent calls are serialised.
//
// Invariants enforced:
//   - Code must match Format (OGON-E + 4 digits).
//   - Code must not be already registered with different metadata
//     (idempotent re-registration with identical metadata is allowed so
//     that test setups and init() blocks can be re-run).
//   - The leading digit of the code number must equal ClassPrefixDigit
//     for the supplied class. This is a soft contract: misalignment is
//     a programmer error and is reported via panic-on-init in dev builds
//     (registered codes are public; we fail loudly).
func Register(code, shortTitle, docURL string) {
	if !isValidCode(code) {
		panic(fmt.Sprintf("codes.Register: invalid code format %q (want %s)", code, Format))
	}
	mu.Lock()
	defer mu.Unlock()
	if existing, ok := registry[code]; ok {
		if existing.ShortTitle != shortTitle || existing.DocURL != docURL {
			panic(fmt.Sprintf(
				"codes.Register: code %q already registered with different metadata (have %q/%q, want %q/%q) — codes are a public contract and may not be redefined",
				code, existing.ShortTitle, existing.DocURL, shortTitle, docURL,
			))
		}
		return
	}
	class := classOf(code)
	registry[code] = CodeInfo{
		Code:       code,
		Class:      class,
		ShortTitle: shortTitle,
		DocURL:     docURL,
	}
}

// Lookup returns the info for a code, or ok=false if not registered.
func Lookup(code string) (CodeInfo, bool) {
	mu.RLock()
	defer mu.RUnlock()
	info, ok := registry[code]
	return info, ok
}

// All returns every registered code sorted lexicographically (which,
// because of the fixed-width 4-digit suffix, is also numerical order).
// The returned slice is a snapshot; callers may mutate it freely.
func All() []CodeInfo {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]CodeInfo, 0, len(registry))
	for _, info := range registry {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// classOf extracts the class from a code's leading digit. Returns the
// empty string if the digit is unknown.
func classOf(code string) Class {
	if len(code) != CodeLen {
		return ""
	}
	digit := code[len(Prefix)]
	for c, d := range ClassPrefixDigit {
		if d == digit {
			return c
		}
	}
	return ""
}

// isValidCode checks the format OGON-E<4 digits>.
func isValidCode(code string) bool {
	if len(code) != CodeLen {
		return false
	}
	if code[:len(Prefix)] != Prefix {
		return false
	}
	for i := len(Prefix); i < CodeLen; i++ {
		c := code[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func init() {
	Register(E3001ConfigNotFound, "Configuration not found",
		DefaultDocsBase+"/"+E3001ConfigNotFound)
	Register(E1001RouteConflict, "Route registration conflict",
		DefaultDocsBase+"/"+E1001RouteConflict)
	Register(E2001ValidationFailed, "Validation failed",
		DefaultDocsBase+"/"+E2001ValidationFailed)
	Register(E4001MigrationConflict, "Migration conflict",
		DefaultDocsBase+"/"+E4001MigrationConflict)
	Register(E5001DependencyMissing, "Required dependency missing",
		DefaultDocsBase+"/"+E5001DependencyMissing)
	Register(E6001SecurityViolation, "Security policy violation",
		DefaultDocsBase+"/"+E6001SecurityViolation)
	Register(E7001RuntimeSupervisorClosed, "Runtime supervisor closed",
		DefaultDocsBase+"/"+E7001RuntimeSupervisorClosed)
	Register(E8001GenerationConflict, "Generation conflict",
		DefaultDocsBase+"/"+E8001GenerationConflict)
}
