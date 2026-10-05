#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# Golden path: auth.
# Verifies: gen auth -> login/logout/passkey -> role-gated route -> 403.
# Spec: AT-007, AT-008, DX-001.

set -euo pipefail

OGON_BIN="${OGON_BIN:-ogon}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "== [1/6] new =="
cd "$WORKDIR"
"$OGON_BIN" new app --template standard --db sqlite
cd app

echo "== [2/6] gen resource User =="
"$OGON_BIN" gen resource User

echo "== [3/6] gen auth (session + passkey) =="
export OGON_SESSION_SECRET=$(openssl rand -hex 32)
"$OGON_BIN" gen auth --flows session,passkey

echo "== [4/6] migrate run =="
"$OGON_BIN" migrate run

echo "== [5/6] test (auth matrix) =="
"$OGON_BIN" test -run TestAuth

echo "== [6/6] doctor (prod checks) =="
"$OGON_BIN" doctor

echo "PASS: auth golden path"
