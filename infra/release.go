// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Release stamping (INFRA-029), image tagging (sha+semver — INFRA-030),
// registry push helper (INFRA-031), GHCR/ECR/GAR auth docs (INFRA-032).

package infra

import (
	"fmt"
	"strings"
	"text/template"
)

// Release bundles the per-deploy release identifiers. Stamped via ldflags at
// `ogon build` time and used by the deploy pipeline for image tagging.
type Release struct {
	SemVer string // e.g. 1.2.3
	GitSHA string // 7-char short sha (or full)
	Image  string // fully-qualified registry/repo:tag
	Tag    string // primary tag (semver if available, else sha-<sha>)
}

// StampRelease computes the canonical Release object from cfg + sha + semver.
// Priority: explicit semver > git tag > sha. Tag is `<semver>` when semver is
// set, else `sha-<sha>`. Both tags are pushed (INFRA-030).
func StampRelease(cfg *InfraConfig, sha, semver string) Release {
	r := Release{GitSHA: sha, SemVer: semver}
	host := cfg.Registry.Host
	acct := cfg.Registry.Account
	repo := cfg.Image.Repository
	if host == "" {
		host = "ghcr.io"
	}
	if acct == "" {
		acct = "acme"
	}
	if repo == "" {
		repo = cfg.App.Name
	}
	r.Image = fmt.Sprintf("%s/%s/%s", host, acct, repo)
	if semver != "" {
		r.Tag = semver
	} else if sha != "" {
		r.Tag = "sha-" + sha
	} else {
		r.Tag = "latest"
	}
	return r
}

// ImageTag returns the primary image reference for a release (INFRA-030).
func (r Release) ImageTag() string {
	if r.Image == "" || r.Tag == "" {
		return ""
	}
	return r.Image + ":" + r.Tag
}

// AllTags returns the full set of tags to push. INFRA-030: always push both
// sha and semver tags.
func (r Release) AllTags() []string {
	var tags []string
	if r.SemVer != "" {
		tags = append(tags, r.SemVer)
	}
	if r.GitSHA != "" {
		tags = append(tags, "sha-"+r.GitSHA)
	}
	if len(tags) == 0 {
		tags = append(tags, "latest")
	}
	return tags
}

// GenerateRegistryPushHelper emits a Makefile target / shell helper that
// handles registry login + multi-tag push (INFRA-031).
func GenerateRegistryPushHelper(cfg *InfraConfig) (FileSpec, error) {
	tpl := template.Must(template.New("push").Parse(pushTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("push helper template: %w", err)
	}
	return FileSpec{
		Path: "infra/scripts/push.sh", Content: b.String(),
		Marker: MarkOwned, CommentPrefix: "#",
		Description: "Registry push helper (INFRA-031): multi-tag push, sha+semver",
	}, nil
}

const pushTemplate = `#!/usr/bin/env bash
# Registry push helper (INFRA-031). Pushes an image with both sha and semver
# tags. Detects registry provider from cfg.Registry.Provider and configures
# the appropriate login (GHCR, ECR, GAR).
set -euo pipefail

REGISTRY_PROVIDER="{{.Registry.Provider}}"   # ghcr|ecr|gar
REGISTRY_HOST="{{.Registry.Host}}"
REGISTRY_ACCOUNT="{{.Registry.Account}}"
REPO="{{.Image.Repository}}"
SHA="${GIT_SHA:-$(git rev-parse --short HEAD)}"
SEMVER="${APP_VERSION:-}"
IMAGE="${REGISTRY_HOST}/${REGISTRY_ACCOUNT}/${REPO}"

case "${REGISTRY_PROVIDER}" in
  ghcr)
    echo "${GHCR_TOKEN}" | docker login "${REGISTRY_HOST}" -u "${GHCR_USER}" --password-stdin
    ;;
  ecr)
    aws ecr get-login-password --region {{.Terraform.Region}} | \
      docker login --username AWS --password-stdin "${REGISTRY_HOST}"
    ;;
  gar)
    gcloud auth print-access-token | \
      docker login "${REGISTRY_HOST}" -u oauth2accesstoken --password-stdin
    ;;
  *)
    echo "no registry login for provider=${REGISTRY_PROVIDER}" >&2
    exit 1
    ;;
esac

# Build tags (INFRA-030): always both sha and semver when available.
TAGS=()
TAGS+=("sha-${SHA}")
if [ -n "${SEMVER}" ]; then
  TAGS+=("${SEMVER}")
fi

for t in "${TAGS[@]}"; do
  docker tag "${REPO}:latest" "${IMAGE}:${t}"
  docker push "${IMAGE}:${t}"
done

# Emit an SBOM (INFRA-057) for the released image.
if command -v syft >/dev/null 2>&1; then
  syft "${IMAGE}:${SEMVER:-${TAGS[0]}}" -o spdx-json > infra/sbom.spdx.json
fi

echo "pushed ${IMAGE} with tags: ${TAGS[*]}"
`

// GenerateRegistryAuthDocs emits the GHCR/ECR/GAR auth reference (INFRA-032).
func GenerateRegistryAuthDocs(cfg *InfraConfig) (FileSpec, error) {
	const doc = `# Registry Auth Reference (INFRA-032)

How to authenticate the push helper for each supported registry.

## GHCR (GitHub Container Registry)

- Provider: ghcr
- Host: ghcr.io
- Account: GitHub user/org (lowercase)
- Auth: Personal Access Token (PAT) with ` + "`write:packages`" + ` scope, or
  ` + "`GITHUB_TOKEN`" + ` in CI.
- Env vars:
  - ` + "`GHCR_USER`" + ` — GitHub username
  - ` + "`GHCR_TOKEN`" + ` — PAT or GITHUB_TOKEN

## ECR (AWS Elastic Container Registry)

- Provider: ecr
- Host: <account-id>.dkr.ecr.<region>.amazonaws.com
- Account: AWS account ID (numeric)
- Auth: IAM user access key, or IRSA in EKS, or OIDC in GitHub Actions
  (recommended).
- Env vars:
  - ` + "`AWS_ACCESS_KEY_ID`" + ` / ` + "`AWS_SECRET_ACCESS_KEY`" + ` (or assumed role)
- Pre-req: create the repository once:
  ` + "`aws ecr create-repository --repository-name app --region us-east-1`" + `

## GAR (Google Artifact Registry)

- Provider: gar
- Host: <region>-docker.pkg.dev
- Account: GCP project ID
- Auth: Service account key JSON, or Workload Identity in GKE, or WIF in
  GitHub Actions (recommended).
- Env vars:
  - ` + "`GOOGLE_APPLICATION_CREDENTIALS`" + ` — path to SA key JSON
- Pre-req: create the repository once:
  ` + "`gcloud artifacts repositories create app --repository-format=docker --location=us-east1`" + `

## Signing (SEC-074)

For keyless signing via cosign + OIDC, the release workflow already has
` + "`id-token: write`" + ` permission. cosign will use the GitHub OIDC token to
sign; verification is done at deploy time via the cosign-installed verifier.
`
	return FileSpec{
		Path: "infra/REGISTRY_AUTH.md", Content: doc,
		Marker: MarkSeeded, CommentPrefix: "#",
		Description: "GHCR/ECR/GAR auth docs (INFRA-032)",
	}, nil
}
