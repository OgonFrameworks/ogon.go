#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# DX-003: file/line counters per feature.
# Counts the developer-written files and lines for each golden path.
# Spec: Part II.4 (complexity budgets), DX-003.
#
# Usage: ./file-line-counters.sh [repo-root]
# Output: TSV of feature, dev_files, dev_lines, gen_files, gen_lines.

set -euo pipefail

ROOT="${1:-.}"
cd "$ROOT"

printf "feature\tdev_files\tdev_lines\tgen_files\tgen_lines\n"

count() {
	local dir="$1"
	local feature="$2"
	if [[ ! -d "$dir" ]]; then
		printf "%s\t0\t0\t0\t0\n" "$feature"
		return
	fi
	# developer-written: no "Code generated" or "ogon:owned" marker
	local dev_files dev_lines gen_files gen_lines
	dev_files=$(find "$dir" -name '*.go' ! -name '*_test.go' \
		-exec grep -L 'Code generated\|ogon:owned\|ogon:generate' {} + 2>/dev/null | wc -l)
	dev_lines=$(find "$dir" -name '*.go' ! -name '*_test.go' \
		-exec grep -L 'Code generated\|ogon:owned\|ogon:generate' {} + 2>/dev/null \
		-exec cat {} + 2>/dev/null | wc -l)
	gen_files=$(find "$dir" -name '*.go' ! -name '*_test.go' \
		-exec grep -l 'Code generated\|ogon:owned\|ogon:generate' {} + 2>/dev/null | wc -l)
	gen_lines=$(find "$dir" -name '*.go' ! -name '*_test.go' \
		-exec grep -l 'Code generated\|ogon:owned\|ogon:generate' {} + 2>/dev/null \
		-exec cat {} + 2>/dev/null | wc -l)
	printf "%s\t%s\t%s\t%s\t%s\n" "$feature" "$dev_files" "$dev_lines" "$gen_files" "$gen_lines"
}

count "examples/hello-world" "hello-world"
count "examples/crud"        "crud"
count "examples/auth"        "auth"
count "examples/jobs"        "jobs"
count "examples/realtime"    "realtime"
count "examples/deploy"      "deploy"

# Complexity budgets (Part II.4):
#   - hello-world: dev <= 1 file, <= 20 lines
#   - crud:        dev <= 1 file, <= 100 lines
#   - auth:        dev <= 1 file, <= 50 lines  (the role-gate)
#   - jobs:        dev <= 1 file, <= 30 lines  (the handler)
#   - realtime:    dev <= 1 file, <= 30 lines
#   - deploy:      dev <= 0 files (infra is generated)
