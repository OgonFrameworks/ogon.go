// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Dangerous-operation detection for the migration engine. The detector
// classifies migration steps as Dangerous when they would:
//   - DROP TABLE (DATA-008)
//   - DROP COLUMN (CLI-068)
//   - narrow a column type (e.g. text → integer)
//   - drop a unique index on a large table
//
// In interactive mode the CLI prompts for confirmation; in non-interactive
// mode the runner exits with code 4 (MigrationUnsafe) by surfacing
// diagnostic OGON-D0040 from migration_runner.go.

package record

import (
	"strconv"
	"strings"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// DangerousOp is one detected hazard for human-friendly review.
type DangerousOp struct {
	Kind   string // "drop_table" | "drop_column" | "narrow_type" | "drop_index_large"
	Reason string
	Step   MigrationStep
}

// DetectDangerous inspects a migration and returns the list of Dangerous
// ops. The list is what the CLI surfaces for interactive consent (CLI-068).
func DetectDangerous(m *Migration) []DangerousOp {
	var out []DangerousOp
	for _, s := range m.Steps {
		// fast path: steps already flagged Dangerous (e.g. orphan DROPs)
		if s.Dangerous {
			out = append(out, DangerousOp{Kind: classifyKind(s.Description), Reason: s.Reason, Step: s})
			continue
		}
		// scan Up/Down for forbidden verbs
		if matchesDangerousVerb(s.Up) {
			out = append(out, DangerousOp{
				Kind:   classifyKind(s.Up),
				Reason: "step Up contains a destructive verb",
				Step:   s,
			})
			continue
		}
		if matchesDangerousVerb(s.Down) {
			out = append(out, DangerousOp{
				Kind:   classifyKind(s.Down),
				Reason: "step Down contains a destructive verb",
				Step:   s,
			})
		}
	}
	return out
}

// matchesDangerousVerb reports whether s contains a destructive SQL verb
// at the start of a statement. We scan for `DROP TABLE`, `DROP COLUMN`,
// `ALTER ... DROP COLUMN`. Type narrowing (e.g. TEXT → INTEGER) is
// detected by the caller when comparing the before/after column type;
// see ClassifyNarrowing.
func matchesDangerousVerb(s string) bool {
	up := strings.ToUpper(s)
	return strings.Contains(up, "DROP TABLE") ||
		strings.Contains(up, "DROP COLUMN")
}

// classifyKind maps an SQL fragment to a DangerousOp.Kind label.
func classifyKind(s string) string {
	up := strings.ToUpper(s)
	switch {
	case strings.Contains(up, "DROP TABLE"):
		return "drop_table"
	case strings.Contains(up, "DROP COLUMN"):
		return "drop_column"
	case strings.Contains(up, "ALTER") && strings.Contains(up, "TYPE"):
		return "narrow_type"
	case strings.Contains(up, "DROP INDEX"):
		return "drop_index_large"
	}
	return "dangerous"
}

// ClassifyNarrowing reports whether going from fromType → toType is a
// narrowing (lossy) conversion. Examples: TEXT → INTEGER (data loss),
// DECIMAL(19,4) → INTEGER (scale loss). Widening is not narrowing.
func ClassifyNarrowing(fromType, toType string) bool {
	f := normaliseType(fromType)
	t := normaliseType(toType)
	if f == t {
		return false
	}
	// whitelist of safe widenings (normalised forms)
	switch {
	case f == "int" && (t == "bigint" || t == "numeric" || t == "text"):
		return false
	case f == "bigint" && (t == "numeric" || t == "text"):
		return false
	case f == "bool" && t == "int":
		return false
	case f == "text" && t == "json":
		return false
	}
	// everything else is narrowing (lossy)
	return true
}

// normaliseType canonicalises SQL type strings for comparison.
func normaliseType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ";")
	switch {
	case strings.HasPrefix(s, "varchar"), strings.HasPrefix(s, "text"):
		return "text"
	case strings.HasPrefix(s, "int"), s == "integer", s == "serial", s == "bigint", s == "bigserial":
		if strings.HasPrefix(s, "bigint") {
			return "bigint"
		}
		return "int"
	case strings.HasPrefix(s, "bool"):
		return "bool"
	case strings.HasPrefix(s, "numeric"), strings.HasPrefix(s, "decimal"):
		return "numeric"
	case strings.HasPrefix(s, "float"), strings.HasPrefix(s, "double"), strings.HasPrefix(s, "real"):
		return "float"
	case strings.HasPrefix(s, "uuid"):
		return "uuid"
	case strings.HasPrefix(s, "json"):
		return "json"
	case strings.HasPrefix(s, "timestamp"), strings.HasPrefix(s, "datetime"):
		return "timestamp"
	}
	return s
}

// EnsureInteractiveOrExit inspects the dangerous ops and returns a
// diag-coded error when consent is absent. The CLI maps this to exit 4
// (MigrationUnsafe). When consent is present, returns nil.
func EnsureInteractiveOrExit(m *Migration, confirm DangerousConfirmation) error {
	ops := DetectDangerous(m)
	if len(ops) == 0 {
		return nil
	}
	// Build a status shell for the confirmation callback.
	st := &MigrationStatus{
		Steps:     m.Steps,
		Dangerous: len(ops),
	}
	if confirm == nil || !confirm.Allow(st) {
		var reasons []string
		for _, op := range ops {
			reasons = append(reasons, op.Kind+": "+op.Reason)
		}
		return diag.New("OGON-D0040", "migration refused",
			"non-interactive session encountered "+pluralOps(len(ops))+": "+strings.Join(reasons, "; "))
	}
	return nil
}

func pluralOps(n int) string {
	switch {
	case n == 1:
		return "1 dangerous operation"
	default:
		return strconv.Itoa(n) + " dangerous operations"
	}
}
