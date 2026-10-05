#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# DX-009: explain coverage 100% check.
# Verifies every framework behavior in the spec has an `ogon explain`
# topic. Spec: AT-004, DX-002, DX-009.
#
# Usage: ./explain-coverage.sh [ogon-binary]

set -euo pipefail

OGON_BIN="${1:-ogon}"

# Framework behaviors that must have an explain topic (from spec Part III).
EXPECTED=(
        route
        model
        config
        di
        module
        component
        feature
        exit-codes
        gen
        diag
        runtime
)

# Get the actual list of topics from `ogon explain` (no arg).
# Parse the JSON envelope's data.details array; fall back to human output.
ACTUAL="$("$OGON_BIN" explain --json 2>/dev/null \
        | tr ',[]{}":' '\n\n\n\n\n\n\n' \
        | sed 's/^ *//; s/ *$//' \
        | grep -vE '^(command|status|data|topic|summary|details|available|pass|of|to|the|below|is|ogon|explain|ok|error|)$' \
        || true)"

if [[ -z "$ACTUAL" ]]; then
        # Fallback: parse the human output (lines after the summary).
        ACTUAL="$("$OGON_BIN" explain 2>&1 | sed -n 's/^  • //p' || true)"
fi

FAIL=0
for topic in "${EXPECTED[@]}"; do
        if echo "$ACTUAL" | grep -q "^$topic$"; then
                echo "ok   $topic"
        else
                echo "FAIL $topic (missing)"
                FAIL=1
        fi
done

if [[ $FAIL -eq 0 ]]; then
        echo "PASS: explain coverage 100% (${#EXPECTED[@]}/${#EXPECTED[@]})"
else
        echo "FAIL: explain coverage incomplete"
        exit 1
fi
