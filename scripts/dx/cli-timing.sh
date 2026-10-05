#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# DX-004: CLI timing harness.
# Measures p50/p95/p99 latency of non-generating CLI commands.
# Spec: Part II.5, DX-004. Target: <= 100ms p95 (PERF-013).
#
# Usage: ./cli-timing.sh [ogon-binary]
# Output: TSV of command, p50_ms, p95_ms, p99_ms.

set -euo pipefail

OGON_BIN="${1:-ogon}"
RUNS=20

# time_ms runs the command once and prints elapsed milliseconds.
time_ms() {
	local start end
	start=$(date +%s%N)
	"$@" >/dev/null 2>&1 || true
	end=$(date +%s%N)
	echo $(( (end - start) / 1000000 ))
}

bench() {
	local label="$1"
	shift
	local tmp
	tmp=$(mktemp)
	for _ in $(seq 1 $RUNS); do
		time_ms "$OGON_BIN" "$@" >>"$tmp"
	done
	sort -n "$tmp" -o "$tmp"
	local p50 p95 p99
	p50=$(sed -n "$((RUNS/2))p" "$tmp")
	p95=$(sed -n "$((RUNS*95/100))p" "$tmp")
	p99=$(sed -n "$((RUNS*99/100))p" "$tmp")
	rm -f "$tmp"
	printf "%s\t%s\t%s\t%s\n" "$label" "$p50" "$p95" "$p99"
}

printf "command\tp50_ms\tp95_ms\tp99_ms\n"
bench "version" --version
bench "help" --help
bench "doctor" doctor
bench "explain-route" explain route
bench "explain-exit-codes" explain exit-codes
bench "explain-runtime" explain runtime
bench "explain-jobs" explain jobs
bench "explain-migrate" explain migrate

# Budgets (Part XV.1, PERF-013): all <= 100ms p95.
