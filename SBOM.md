# SPDX-License-Identifier: MIT
# Copyright (c) 2026 OgonFrameworks. All rights reserved.
#
# SBOM (Software Bill of Materials) — instructions for `ogon build --sbom`.
#
# OgonGo generates an SBOM in SPDX-JSON format using syft. The SBOM is
# attached to every release artefact and is the authoritative manifest
# for downstream supply-chain verification (SEC-073).
#
# ## Prereqs
#
# Install syft:
#
#     go install github.com/anchore/syft/cmd/syft@latest
#
# or download a prebuilt binary from
# https://github.com/anchore/syft/releases.
#
# ## Generate
#
#     ogon build --sbom
#
# The CLI subcommand runs syft over the freshly-built binary (or the
# module if no binary was built yet) and writes `ogon-sbom.spdx.json`
# to the dist/ directory. The output format is SPDX-JSON (the spec
# anchor is https://spdx.github.io/spdx-spec/).
#
# Equivalent raw invocation:
#
#     syft -o spdx-json=ogon-sbom.spdx.json .
#
# ## Verify
#
# To verify the SBOM matches the build, use cosign to attach it to the
# release artefact:
#
#     cosign attach sbom --artifact-type spdxjson ogon-sbom.spdx.json
#
# Then downstream consumers verify with:
#
#     cosign verify-attestation --type spdxjson <release-tag>
#
# ## What's in the SBOM
#
# The SBOM enumerates every Go module in go.mod (direct + transitive),
# their versions, their source repo URLs, and the hash of their source
# code. It also includes the Go toolchain version (go1.27.1).
#
# The SBOM does NOT include:
#   - Test-only dependencies (gofuzz, testify) — these do not ship in
#     the binary.
#   - Build-only dependencies (esbuild for the ui subsystem) — these
#     are emitted in a separate `ogon-build-sbom.spdx.json` for the
#     build pipeline.
#
# ## Cron
#
# `.github/workflows/security.yml` regenerates the SBOM on every release
# tag and uploads it as a release asset. The nightly job also regenerates
# and stores the SBOM as a CI artefact (30-day retention) so the security
# team can diff module drift over time.
#
# ## Integration with the supply-chain audit
#
# The `auth/supply_chain_audit.go` programmatic check reads
# `.github/workflows/security.yml` and verifies govulncheck + gitleaks
# are wired in. The SBOM is the third leg of the supply-chain stool —
# it answers "what's in the binary?" even after the build.

# End of SBOM.md
