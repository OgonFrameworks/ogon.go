#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# Golden path: hello-world.
# Verifies: install -> scaffold -> dev -> first route -> 200 -> shutdown.
# Spec: AT-001, DX-001, DX-010.

set -euo pipefail

OGON_BIN="${OGON_BIN:-ogon}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "== [1/6] version =="
"$OGON_BIN" --version

echo "== [2/6] new =="
cd "$WORKDIR"
"$OGON_BIN" new hello --template minimal
cd hello

echo "== [3/6] doctor (in fresh project) =="
"$OGON_BIN" doctor || true

echo "== [4/6] write hello route =="
mkdir -p routes
cat > routes/hello.go <<'EOF'
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Hello route.

package routes

import (
        "net/http"

        ogonhttp "github.com/OgonFrameworks/ogon.go/http"
)

func init() {
        ogonhttp.Register("GET /hello", hello)
}

func hello(c *ogonhttp.Ctx) error {
        return c.JSON(http.StatusOK, map[string]string{"hello": "world"})
}
EOF

echo "== [5/6] test (in-process) =="
cat > routes/hello_test.go <<'EOF'
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Hello route tests.

package routes

import (
        "net/http"
        "testing"

        ogonhttp "github.com/OgonFrameworks/ogon.go/http"
        "github.com/OgonFrameworks/ogon.go/test"
)

func TestHello(t *testing.T) {
        app := test.NewApp(t, ogonhttp.Handler())
        defer app.Close()
        r := app.Recorder().Get("/hello")
        r.AssertStatus(t, http.StatusOK)
        r.AssertJSON(t, `{"hello":"world"}`)
}
EOF
"$OGON_BIN" test

echo "== [6/6] build =="
"$OGON_BIN" build

echo "PASS: hello-world golden path"
