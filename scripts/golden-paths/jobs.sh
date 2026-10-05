#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# Golden path: jobs.
# Verifies: enqueue -> process -> retry -> DLQ with <= 5 lines of test.
# Spec: AT-012, DX-001.

set -euo pipefail

OGON_BIN="${OGON_BIN:-ogon}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "== [1/5] new =="
cd "$WORKDIR"
"$OGON_BIN" new jobs --template standard --db sqlite
cd jobs

echo "== [2/5] gen job SendEmail =="
"$OGON_BIN" gen job SendEmail

echo "== [3/5] migrate run (jobs table) =="
"$OGON_BIN" migrate run

echo "== [4/5] test =="
"$OGON_BIN" test -run TestJob

echo "== [5/5] jobs status =="
"$OGON_BIN" jobs status

echo "PASS: jobs golden path"
