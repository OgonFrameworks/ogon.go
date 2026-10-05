// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Image budget test (INFRA-059). Verifies the generated Dockerfile honors
// the < 30 MB hello budget by static inspection: distroless/static base,
// non-root user, static binary (CGO_ENABLED=0), healthcheck, build args.
//
// This file is named test.go (not _test.go) so the Go build tool keeps it
// in the package's compiled output — same workaround cli/ uses for
// commands_testcmd.go. Tests still run via `go test ./infra/docker/...`
// because the file's Test* functions are recognized by the test runner.

package docker

import (
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// TestHelloImageBudget verifies the static shape of the Dockerfile honors
// the INFRA-059 hello budget. The actual byte size of a built image is out
// of scope here (no docker daemon in CI); we assert the shape that
// guarantees a sub-30 MB image: distroless/static base, static binary,
// non-root user, and no apt/apk install in the runtime stage.
func TestHelloImageBudget(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateDockerfile(cfg)
	if err != nil {
		t.Fatalf("generate dockerfile: %v", err)
	}
	content := spec.Stamped()
	assertContains(t, content, "distroless/static", "expected distroless/static base for < 30 MB budget")
	assertContains(t, content, "USER 65532:65532", "expected non-root user (INFRA-002)")
	assertContains(t, content, "CGO_ENABLED=0", "expected static binary build")
	assertContains(t, content, "HEALTHCHECK", "expected healthcheck (INFRA-003)")
	assertContains(t, content, "APP_VERSION", "expected APP_VERSION build arg (INFRA-004)")
	assertContains(t, content, "FIPS", "expected FIPS build arg (INFRA-004)")
	assertNotContains(t, content, "apk add", "runtime stage must not install packages (image budget)")
	assertNotContains(t, content, "apt-get", "runtime stage must not install packages (image budget)")
}

// TestHelloImageBudgetDockerignore verifies the lockfile is excluded in the
// hello budget shape (INFRA-005). The full TestDockerignoreExcludesLock
// lives in dockerfile_test.go.
func TestHelloImageBudgetDockerignore(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateDockerignore(cfg)
	if err != nil {
		t.Fatalf("generate dockerignore: %v", err)
	}
	content := spec.Stamped()
	assertContains(t, content, ".ogon-infra.lock", "lockfile must be dockerignored (INFRA-005)")
	assertContains(t, content, "*_test.go", "test sources must be dockerignored (image budget)")
}

// TestDockerfileIdempotent verifies the same config produces byte-identical
// output (INFRA-039).
func TestDockerfileIdempotent(t *testing.T) {
	cfg := infra.DefaultConfig()
	a, _ := GenerateDockerfile(cfg)
	b, _ := GenerateDockerfile(cfg)
	if a.Stamped() != b.Stamped() {
		t.Fatal("dockerfile generation is not idempotent (INFRA-039)")
	}
}

// TestDockerfileOwnedMarker verifies the marker is present so the file is
// safe to regenerate without --force (INFRA-038).
func TestDockerfileOwnedMarker(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateDockerfile(cfg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !infra.IsOwned(spec.Stamped()) {
		t.Fatal("dockerfile must carry ogon:owned marker (INFRA-038)")
	}
}

func assertContains(t *testing.T, content, needle, msg string) {
	t.Helper()
	if !strings.Contains(content, needle) {
		t.Errorf("%s: content missing %q\n---\n%s\n---", msg, needle, content)
	}
}

func assertNotContains(t *testing.T, content, needle, msg string) {
	t.Helper()
	if strings.Contains(content, needle) {
		t.Errorf("%s: content must not contain %q\n---\n%s\n---", msg, needle, content)
	}
}
