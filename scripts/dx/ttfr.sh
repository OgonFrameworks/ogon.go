#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# DX-002: time-to-first-run measurement.
# Measures the wall-clock time from `ogon new` to a 200 on /healthz.
# Spec: Part II.5, DX-002. Target: <= 5 minutes (DOC-002).

set -euo pipefail

OGON_BIN="${OGON_BIN:-ogon}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

T0=$(date +%s.%N)

echo "== T+0s: ogon new =="
cd "$WORKDIR"
"$OGON_BIN" new ttfr --template minimal --no-git >/dev/null
cd ttfr

T1=$(date +%s.%N)
echo "== T+$(awk "BEGIN{printf "%.2f", $T1-$T0}")s: scaffold done"

echo "== dev (background) =="
"$OGON_BIN" dev >/tmp/ttfr-dev.log 2>&1 &
DEV_PID=$!
trap 'kill $DEV_PID 2>/dev/null || true; rm -rf "$WORKDIR"' EXIT

# wait up to 30s for the server to come up
for i in $(seq 1 30); do
	if curl -sf http://localhost:3000/healthz >/dev/null 2>&1; then
		T2=$(date +%s.%N)
		echo "== T+$(awk "BEGIN{printf \"%.2f\", $T2-$T0}")s: 200 on /healthz"
		echo "PASS: time-to-first-run = $(awk "BEGIN{printf \"%.2f\", $T2-$T0}")s"
		exit 0
	fi
	sleep 1
done

echo "FAIL: server did not come up in 30s"
cat /tmp/ttfr-dev.log
exit 1
