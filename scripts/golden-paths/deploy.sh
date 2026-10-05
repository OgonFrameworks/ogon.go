#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# Golden path: deploy.
# Verifies: build -> docker build -> k8s dry-run -> health.
# Spec: AT-009, AT-010, DX-001.
# Note: does not push to a real registry; uses --dry-run.

set -euo pipefail

OGON_BIN="${OGON_BIN:-ogon}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "== [1/6] new =="
cd "$WORKDIR"
"$OGON_BIN" new svc --template standard --db sqlite
cd svc

echo "== [2/6] build =="
"$OGON_BIN" build

echo "== [3/6] infra gen docker =="
"$OGON_BIN" infra gen docker

echo "== [4/6] infra gen k8s =="
"$OGON_BIN" infra gen k8s

echo "== [5/6] deploy --dry-run (k8s) =="
"$OGON_BIN" deploy --cloud k8s --dry-run

echo "== [6/6] infra diff (drift check) =="
"$OGON_BIN" infra diff

echo "PASS: deploy golden path (dry-run only)"
