// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Env matrix generator (INFRA-026/056). Emits three env files
// (ogon.local.yaml / ogon.dev.yaml / ogon.prod.yaml) plus a parity
// checklist (dev→prod) that the CI gen-check enforces.

package infra

import (
	"fmt"
	"strings"
	"text/template"
)

// GenerateEnvMatrix emits per-env config files + parity checklist.
// Files are MarkSeeded — they are one-time seeds. Users edit them; the
// generator refuses to overwrite without --force (INFRA-040).
func GenerateEnvMatrix(cfg *InfraConfig) ([]FileSpec, error) {
	tpl := template.Must(template.New("env").Parse(envTemplate))
	var specs []FileSpec
	for _, env := range cfg.Envs() {
		view := struct {
			Env string
			Cfg *InfraConfig
		}{Env: env, Cfg: cfg}
		var b strings.Builder
		if err := tpl.Execute(&b, view); err != nil {
			return nil, fmt.Errorf("env matrix template: %w", err)
		}
		specs = append(specs, FileSpec{
			Path:    fmt.Sprintf("ogon.%s.yaml", env),
			Content: b.String(),
			Marker:  MarkSeeded, CommentPrefix: "#",
			Description: fmt.Sprintf("Env matrix entry: %s (INFRA-026)", env),
		})
	}
	specs = append(specs, GenerateParityChecklist(cfg))
	return specs, nil
}

const envTemplate = `# ogon.{{.Env}}.yaml — per-env override (INFRA-026).
# Seeded by ogon infra gen; edit by hand. --force required to regenerate.
env: {{.Env}}
app:
  name: {{.Cfg.App.Name}}
  version: {{.Cfg.App.Version}}
  port: {{.Cfg.App.Port}}
http:
  listen: ":{{.Cfg.App.Port}}"
db:
  engine: {{.Cfg.Database.Engine}}
  name: {{.Cfg.Database.Name}}
  url: ${DATABASE_URL}
cache:
  engine: {{.Cfg.Cache.Engine}}
  url: ${REDIS_URL}
log:
  format: {{if eq .Env "prod"}}json{{else}}text{{end}}
  level: {{if eq .Env "prod"}}info{{else}}debug{{end}}
obs:
  otel_exporter: {{if eq .Env "prod"}}on{{else}}off{{end}}
  pprof: {{if eq .Env "prod"}}off{{else}}on{{end}}
`

// GenerateParityChecklist emits dev→prod parity checklist (INFRA-056).
func GenerateParityChecklist(cfg *InfraConfig) FileSpec {
	const doc = `# dev → prod parity checklist (INFRA-056)
#
# A short list of things that must match between dev and prod. The CI
# gen-check job surfaces drift on these fields — mismatches block deploys.

- [ ] DB engine matches (postgres vs sqlite is allowed in local; dev must
      match prod's engine and major version).
- [ ] Cache engine matches (redis in dev == redis in prod; memcached in one
      and not the other is a parity violation).
- [ ] Env var names match (DATABASE_URL, REDIS_URL, SMTP_URL, etc.) — values
      may differ; names must not.
- [ ] Migration runner identical: ogon migrate up --non-interactive runs the
      same path in dev and prod.
- [ ] Health probe paths identical: /healthz, /readyz, /healthz/startup.
- [ ] Drain timeout identical: 30s (web), 300s (worker). Mismatches cause
      rolling deploys to drop in-flight work.
- [ ] Feature flags: any flag enabled in prod is also enabled in dev.
- [ ] Resource shape (250m/512Mi baseline — INFRA-043): same proportions,
      scaled. dev = 1×; prod = N× the baseline.
- [ ] Image base identical: distroless/static in both.
- [ ] Log format: dev may be human-readable; prod must be JSON. Same fields,
      same redaction (OBS-001..005).
`
	return FileSpec{
		Path: "infra/PARITY.md", Content: doc,
		Marker: MarkSeeded, CommentPrefix: "#",
		Description: "dev→prod parity checklist (INFRA-056)",
	}
}
