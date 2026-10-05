// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// GitHub Actions ci.yml generator (INFRA-034 + TEST-040). Emits a ci.yml
// workflow with: lint (golangci-lint), test (-race), gen-check
// (ogon check verifies committed artifacts match last generation),
// coverage gate, pre-commit hook installation (TEST-040).

package actions

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// Generator implements infra.Generator for the actions target.
type Generator struct{}

func (Generator) Name() string { return "actions" }

func (Generator) Plan(cfg *infra.InfraConfig) []string {
	return []string{
		".github/workflows/ci.yml",
		".github/workflows/release.yml",
		".github/workflows/_terraform-validate.yml",
		".pre-commit-config.yaml",
	}
}

func (Generator) Generate(cfg *infra.InfraConfig) ([]infra.FileSpec, error) {
	ci, err := GenerateCIWorkflow(cfg)
	if err != nil {
		return nil, err
	}
	rel, err := GenerateReleaseWorkflow(cfg)
	if err != nil {
		return nil, err
	}
	precommit, err := GeneratePreCommit(cfg)
	if err != nil {
		return nil, err
	}
	return []infra.FileSpec{ci, rel, precommit}, nil
}

// GenerateCIWorkflow emits the ci.yml workflow.
func GenerateCIWorkflow(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("ci").Parse(ciTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("ci template: %w", err)
	}
	return infra.FileSpec{
		Path: ".github/workflows/ci.yml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "GitHub Actions ci.yml: lint/test/race/gen-check (INFRA-034)",
	}, nil
}

// GenerateReleaseWorkflow emits release.yml — pushes the image with SBOM +
// trivy scan + optional signing (INFRA-057/058/SEC-074).
func GenerateReleaseWorkflow(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("rel").Parse(releaseTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("release template: %w", err)
	}
	return infra.FileSpec{
		Path: ".github/workflows/release.yml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "GitHub Actions release.yml: image push + SBOM + trivy + sign (INFRA-057/058/SEC-074)",
	}, nil
}

// GeneratePreCommit emits a pre-commit config (TEST-040).
func GeneratePreCommit(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	const pre = `# Pre-commit hooks (TEST-040). Install once: pre-commit install.
repos:
  - repo: https://github.com/pre-commit/pre-commit-hooks
    rev: v4.6.0
    hooks:
      - id: trailing-whitespace
      - id: end-of-file-fixer
      - id: check-yaml
      - id: check-added-large-files
      - id: check-merge-conflict
      - id: detect-private-key
  - repo: https://github.com/dnephin/pre-commit-golang
    rev: v1.0.8
    hooks:
      - id: go-fmt
      - id: go-imports
      - id: go-vet
      - id: go-mod-tidy
  - repo: local
    hooks:
      - id: ogon-check
        name: ogon check (gen + lint + drift)
        entry: ogon check
        language: system
        pass_filenames: false
        stages: [commit]
      - id: ogon-infra-diff
        name: ogon infra diff (drift detector)
        entry: ogon infra diff
        language: system
        pass_filenames: false
        stages: [push]
`
	return infra.FileSpec{
		Path: ".pre-commit-config.yaml", Content: pre,
		Marker: infra.MarkSeeded, CommentPrefix: "#",
		Description: "Pre-commit hooks (TEST-040): fmt/vet/ogon-check/infra-diff",
	}, nil
}

const ciTemplate = `name: ci
on:
  push:
    branches: [main]
  pull_request:
permissions:
  contents: read
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
jobs:
  lint:
    runs-on: {{.GitHub.Runner}}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: v1.62
          args: --timeout 5m
  test:
    runs-on: {{.GitHub.Runner}}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - name: go test -race
        run: |
          go test -race -coverprofile=coverage.txt -covermode=atomic ./...
          go tool cover -func=coverage.txt | tail -1
      - name: coverage gate (>= 60%)
        run: |
          COV=$(go tool cover -func=coverage.txt | tail -1 | awk '{print $NF}' | tr -d '%')
          if [ "$(echo "$COV < 60" | bc)" = "1" ]; then
            echo "::error::coverage $COV% below 60% gate"
            exit 1
          fi
      - uses: actions/upload-artifact@v4
        with:
          name: coverage
          path: coverage.txt
  gen-check:
    name: gen + drift check
    runs-on: {{.GitHub.Runner}}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - name: build ogon
        run: go build -o /tmp/ogon ./cmd/ogon
      - name: ogon gen --dry-run (verify plan matches committed artifacts)
        run: /tmp/ogon gen --dry-run
      - name: ogon infra diff (drift detector — INFRA-037)
        run: /tmp/ogon infra diff
  fuzz:
    name: fuzz (smoke)
    runs-on: {{.GitHub.Runner}}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - name: fuzz smoke (60s)
        run: go test -fuzz=Fuzz -fuzztime=60s ./...
        continue-on-error: true
`

const releaseTemplate = `name: release
on:
  push:
    tags: ['v*']
permissions:
  contents: write
  packages: write
  id-token: write   # OIDC for keyless signing (SEC-074)
jobs:
  build-push:
    runs-on: {{.GitHub.Runner}}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - name: registry login
        run: echo "${{ secrets.REGISTRY_PASSWORD }}" | docker login {{.Registry.Host}} -u ${{ secrets.REGISTRY_USER }} --password-stdin
      - name: build + push image (sha + semver)
        id: build
        uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          tags: |
            {{.Registry.Host}}/{{.Registry.Account}}/{{.App.Name}}:${{ github.ref_name }}
            {{.Registry.Host}}/{{.Registry.Account}}/{{.App.Name}}:sha-${{ github.sha }}
          build-args: |
            APP_VERSION=${{ github.ref_name }}
            FIPS=0
          cache-from: type=gha
          cache-to: type=gha,mode=max
{{if .Release.SBOM}}
      - name: SBOM (INFRA-057)
        uses: anchore/sbom-action@v0
        with:
          image: {{.Registry.Host}}/{{.Registry.Account}}/{{.App.Name}}:${{ github.ref_name }}
          format: spdx-json
          output-file: sbom.spdx.json
      - uses: actions/upload-artifact@v4
        with:
          name: sbom
          path: sbom.spdx.json
{{end}}
{{if .Release.Trivy}}
      - name: trivy scan (INFRA-058)
        uses: aquasecurity/trivy-action@master
        with:
          image-ref: {{.Registry.Host}}/{{.Registry.Account}}/{{.App.Name}}:${{ github.ref_name }}
          format: sarif
          output: trivy-results.sarif
          severity: HIGH,CRITICAL
          exit-code: '1'
      - uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: trivy-results.sarif
{{end}}
{{if .Release.Sign}}
      - name: sign image (SEC-074)
        uses: sigstore/cosign-installer@v3
      - name: cosign sign (keyless)
        env:
          COSIGN_EXPERIMENTAL: '1'
        run: |
          cosign sign --yes \
            {{.Registry.Host}}/{{.Registry.Account}}/{{.App.Name}}@${{ steps.build.outputs.digest }}
{{end}}
`

var _ = strings.TrimSpace
