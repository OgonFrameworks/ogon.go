// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Supply-chain security: SBOM in release pipeline (INFRA-057), trivy scan in
// CI (INFRA-058), signed container images (SEC-074). These emitters return
// reusable workflow snippets that infra/actions/ci.go includes.

package infra

import (
	"fmt"
	"strings"
	"text/template"
)

// GenerateSBOMSnippet returns a reusable workflow snippet that produces an
// SPDX SBOM for the released image (INFRA-057).
func GenerateSBOMSnippet(cfg *InfraConfig) (FileSpec, error) {
	tpl := template.Must(template.New("sbom").Parse(sbomTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("sbom template: %w", err)
	}
	return FileSpec{
		Path: ".github/workflows/_sbom.yml", Content: b.String(),
		Marker: MarkOwned, CommentPrefix: "#",
		Description: "Reusable SBOM job (INFRA-057): syft SPDX JSON",
	}, nil
}

const sbomTemplate = `# Reusable workflow snippet: SBOM generation (INFRA-057).
on:
  workflow_call:
    inputs:
      image:
        required: true
        type: string
jobs:
  sbom:
    runs-on: {{.GitHub.Runner}}
    steps:
      - uses: actions/checkout@v4
      - name: generate SBOM (SPDX JSON)
        uses: anchore/sbom-action@v0
        with:
          image: ${{ inputs.image }}
          format: spdx-json
          output-file: sbom.spdx.json
      - uses: actions/upload-artifact@v4
        with:
          name: sbom
          path: sbom.spdx.json
          retention-days: 90
`

// GenerateTrivySnippet returns a reusable trivy scan job (INFRA-058).
// HIGH+CRITICAL findings exit non-zero.
func GenerateTrivySnippet(cfg *InfraConfig) (FileSpec, error) {
	tpl := template.Must(template.New("trivy").Parse(trivyTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("trivy template: %w", err)
	}
	return FileSpec{
		Path: ".github/workflows/_trivy.yml", Content: b.String(),
		Marker: MarkOwned, CommentPrefix: "#",
		Description: "Reusable trivy scan job (INFRA-058): HIGH/CRITICAL gate",
	}, nil
}

const trivyTemplate = `# Reusable workflow snippet: trivy scan (INFRA-058).
on:
  workflow_call:
    inputs:
      image:
        required: true
        type: string
jobs:
  trivy:
    runs-on: {{.GitHub.Runner}}
    steps:
      - uses: actions/checkout@v4
      - name: trivy scan
        uses: aquasecurity/trivy-action@master
        with:
          image-ref: ${{ inputs.image }}
          format: sarif
          output: trivy-results.sarif
          severity: HIGH,CRITICAL
          exit-code: '1'
          ignore-unfixed: true
      - uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: trivy-results.sarif
`

// GenerateSigningSnippet returns a reusable cosign keyless signing snippet
// (SEC-074).
func GenerateSigningSnippet(cfg *InfraConfig) (FileSpec, error) {
	tpl := template.Must(template.New("sign").Parse(signTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("sign template: %w", err)
	}
	return FileSpec{
		Path: ".github/workflows/_sign.yml", Content: b.String(),
		Marker: MarkOwned, CommentPrefix: "#",
		Description: "Reusable cosign keyless signing job (SEC-074)",
	}, nil
}

const signTemplate = `# Reusable workflow snippet: cosign keyless signing (SEC-074).
on:
  workflow_call:
    inputs:
      image:
        required: true
        type: string
      digest:
        required: true
        type: string
jobs:
  sign:
    runs-on: {{.GitHub.Runner}}
    permissions:
      contents: read
      id-token: write   # required for keyless signing
    steps:
      - uses: actions/checkout@v4
      - uses: sigstore/cosign-installer@v3
      - name: cosign sign (keyless)
        env:
          COSIGN_EXPERIMENTAL: '1'
        run: |
          cosign sign --yes \
            {{.Registry.Host}}/{{.Registry.Account}}/{{.Image.Repository}}@${{ inputs.digest }}
      - name: cosign verify
        env:
          COSIGN_EXPERIMENTAL: '1'
        run: |
          cosign verify \
            --certificate-identity-regexp "https://github.com/{{.GitHub.Repo}}/.+" \
            --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
            {{.Registry.Host}}/{{.Registry.Account}}/{{.Image.Repository}}@${{ inputs.digest }}
`

var _ = strings.TrimSpace
