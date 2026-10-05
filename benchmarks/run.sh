#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# Runs all OgonGo benchmarks with -benchmem and writes JSON-encoded results
# to benchmarks/results.json. Used by .github/workflows/bench.yml for
# regression detection (PERF-009).

set -euo pipefail

cd "$(dirname "$0")/.."

mkdir -p benchmarks

# Run benches with short benchtime to keep CI fast.
BENCH_PKAGES=(
  ./diag/...
  ./runtime/...
  ./config/...
  ./cli/...
  ./http/...
  ./record/...
  ./live/...
  ./pubsub/...
  ./jobs/...
  ./obs/...
  ./infra/...
  ./ui/...
)

echo "Running benchmarks across ${#BENCH_PACKAGES[@]} package groups..."

# Capture raw bench output (Go uses a line-oriented format).
RAW=benchmarks/results.raw
go test -bench=. -benchmem -benchtime=100ms -run=^$ \
  "${BENCH_PACKAGES[@]}" 2>&1 | tee "$RAW"

# Emit a minimal JSON document summarizing lines that begin with "Benchmark".
python3 - <<PY
import json, re, sys
results = []
with open("$RAW") as f:
    for line in f:
        m = re.match(r'^(Benchmark\w+)\s+(\d+)\s+(\d+)\s+ns/op\s+([\d.]+)\s+allocs/op', line)
        if not m:
            continue
        results.append({
            "name": m.group(1),
            "iterations": int(m.group(3)),
            "ns_per_op": float(m.group(4)),
            "allocs_per_op": int(m.group(5)) if m.group(5) else 0,
        })
with open("benchmarks/results.json", "w") as f:
    json.dump({"benchmarks": results}, f, indent=2)
print(f"Wrote {len(results)} benchmark results to benchmarks/results.json")
PY
