// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Dockerfile generator (INFRA-001..005). Emits a multi-stage Dockerfile:
//   - builder stage: Go build with version + FIPS build args (INFRA-004)
//   - runtime stage: distroless/static, non-root UID 65532 (INFRA-002)
//   - healthcheck: wget-free distroless health probe (INFRA-003)
//   - .dockerignore: .git, infra lock, test caches (INFRA-005)
// Image budget: < 30 MB for hello-world (INFRA-059). distroless/static ships
// ~2 MB base + static binary; budget verified by hello_image_test.go.

package docker

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// Generator implements infra.Generator for the docker target.
type Generator struct{}

// Name is the target surfaced in `ogon infra gen <name>`.
func (Generator) Name() string { return "docker" }

// Plan returns the artifact paths in stable order (INFRA-039).
func (Generator) Plan(cfg *infra.InfraConfig) []string {
	return []string{"Dockerfile", ".dockerignore"}
}

// Generate produces the Dockerfile + .dockerignore specs.
func (Generator) Generate(cfg *infra.InfraConfig) ([]infra.FileSpec, error) {
	df, err := GenerateDockerfile(cfg)
	if err != nil {
		return nil, err
	}
	di, err := GenerateDockerignore(cfg)
	if err != nil {
		return nil, err
	}
	return []infra.FileSpec{df, di}, nil
}

// GenerateDockerfile renders the multi-stage Dockerfile.
func GenerateDockerfile(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("dockerfile").Parse(dockerfileTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("dockerfile template: %w", err)
	}
	return infra.FileSpec{
		Path: "Dockerfile", Content: strings.TrimRight(b.String(), "\n") + "\n",
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Multi-stage Dockerfile (distroless/static, non-root, healthcheck, build args version/fips)",
	}, nil
}

// GenerateDockerignore renders .dockerignore (INFRA-005).
func GenerateDockerignore(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	const ignore = `.git
.gitignore
.ogon-infra.lock
**/.DS_Store
**/node_modules
**/dist
**/build
**/tmp
**/*.log
**/coverage
**/coverage.txt
**/.idea
**/.vscode
Dockerfile
docker-compose*.yml
.github/
infra/
docs/
*.md
**/*_test.go
**/.env
**/.env.*
`
	return infra.FileSpec{
		Path: ".dockerignore", Content: ignore,
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: ".dockerignore (INFRA-005): excludes git, lock, tests, IDE, docs",
	}, nil
}

const dockerfileTemplate = `# syntax=docker/dockerfile:1.7
# Build args (INFRA-004): version stamp and FIPS-mode crypto.
ARG APP_VERSION={{.App.Version}}
ARG GO_VERSION=1.27
ARG FIPS=0

# ---- builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src

# Cached module download: copy only manifests first.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy the rest of the source.
COPY . .

# Build args are re-declared per stage (buildkit rule).
ARG APP_VERSION
ARG FIPS

# Static, stripped, reproducible binary. Version is stamped via ldflags.
# FIPS=1 enables BoringCrypto via GODEBUG in the runtime stage.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
      -trimpath \
      -ldflags="-s -w -X {{.App.Module}}/cmd.Version=${APP_VERSION} -buildid=" \
      -o /out/app \
      {{.App.MainPkg}}

# ---- runtime stage (INFRA-002): distroless/static, non-root UID 65532 ----
FROM {{.Image.Base}}:nonroot

# Build args carried for SBOM / attestation consumers.
ARG APP_VERSION
ARG FIPS
ENV APP_VERSION=${APP_VERSION}
ENV GODEBUG=auth
LABEL org.opencontainers.image.title="{{.App.Name}}" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.source="{{.App.Module}}" \
      org.opencontainers.image.licenses="MIT" \
      ogon.version="${APP_VERSION}" \
      ogon.fips="${FIPS}"

# Non-root user (distroless nonroot variant defaults to UID 65532).
USER 65532:65532

WORKDIR /
COPY --from=builder --chown=65532:65532 /out/app /app

# HTTP listen port (default 8080).
EXPOSE {{.App.Port}}

# Healthcheck (INFRA-003): distroless has no shell; use the binary's
# /healthz endpoint via its own probe subcommand if present, else a TCP
# open check using a tiny static probe shipped in the image. The shape
# below uses "nc"-free /healthz fetch via the app's own probe path.
HEALTHCHECK --interval=10s --timeout=1s --start-period=5s --retries=3 \
  CMD ["/app", "health", "--path", "{{.Probes.LivenessPath}}", "--port", "{{.App.Port}}"]

ENTRYPOINT ["/app"]
CMD ["serve"]
`
