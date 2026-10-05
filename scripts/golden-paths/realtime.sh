#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# Golden path: realtime.
# Verifies: live.Handle -> two subscribers -> broadcast -> reconnect.
# Spec: AT-013, DX-001.

set -euo pipefail

OGON_BIN="${OGON_BIN:-ogon}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "== [1/4] new =="
cd "$WORKDIR"
"$OGON_BIN" new chat --template standard --db sqlite
cd chat

echo "== [2/4] gen route room (live) =="
"$OGON_BIN" gen route room --live

echo "== [3/4] test (WS + SSE) =="
"$OGON_BIN" test -run TestLive

echo "== [4/4] build =="
"$OGON_BIN" build

echo "PASS: realtime golden path"
