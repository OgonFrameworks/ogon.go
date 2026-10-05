#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# DX-008 / DX-014: doctor completeness + error-message review.
# Verifies every well-known diagnostic Code constant in diag/codes.go
# has a remedy entry in diag/codes_remedy_test.go.
# Spec: DX-008, DX-014, AT-006.
#
# Usage: ./error-remedy-check.sh [repo-root]

set -euo pipefail

ROOT="${1:-.}"
cd "$ROOT"

CODES_FILE="diag/codes.go"
REMEDY_FILE="diag/codes_remedy_test.go"

if [[ ! -f "$CODES_FILE" ]]; then
	echo "FAIL: $CODES_FILE not found"
	exit 1
fi
if [[ ! -f "$REMEDY_FILE" ]]; then
	echo "FAIL: $REMEDY_FILE not found"
	exit 1
fi

# Extract every Code constant NAME (e.g. CodeRouteConflict) from codes.go.
# Pattern: `\s+<Name>\s+Code\s+=\s+"OGON-..."`.
NAMES=()
while IFS= read -r line; do
	# Split on tab; field 1 is the constant name.
	name="${line%%	*}"
	code="${line#*	}"
	if [[ -n "$name" && -n "$code" ]]; then
		NAMES+=("$name|$code")
	fi
done < <(grep -oE '^\s+[A-Z][A-Za-z0-9]+\s+Code\s+=\s+"OGON-[A-Z][0-9]+"' "$CODES_FILE" \
	| sed -E 's/^\s+([A-Za-z0-9]+)\s+Code\s+=\s+"(OGON-[A-Z][0-9]+)".*/\1\t\2/')

if [[ ${#NAMES[@]} -eq 0 ]]; then
	echo "FAIL: no Code constants found in $CODES_FILE"
	exit 1
fi

FAIL=0
OK=0
for entry in "${NAMES[@]}"; do
	name="${entry%|*}"
	code="${entry#*|}"
	# The remedy file uses `Name: "..."` as the map key.
	if grep -qE "^\s+${name}:" "$REMEDY_FILE" 2>/dev/null; then
		OK=$((OK+1))
	else
		echo "FAIL: $code ($name) has no remedy entry in $REMEDY_FILE"
		FAIL=$((FAIL+1))
	fi
done

echo "_codes: ${#NAMES[@]}"
echo "ok:      $OK"
echo "fail:    $FAIL"

if [[ $FAIL -eq 0 ]]; then
	echo "PASS: every diagnostic code has a remedy (DX-008, DX-014)"
	exit 0
else
	echo "FAIL: $FAIL code(s) missing remedy"
	exit 1
fi
