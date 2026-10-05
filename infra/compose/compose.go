// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// compose dev stack generator (INFRA-006/007/055). Emits a single
// docker-compose.yml with: app + postgres + redis + mailpit (+ adminer opt),
// healthchecks on every service, depends_on with condition: service_healthy,
// and a "preview" profile (compose profile) for per-PR preview environments.

package compose

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// Generator implements infra.Generator for the compose target.
type Generator struct{}

func (Generator) Name() string { return "compose" }

func (Generator) Plan(cfg *infra.InfraConfig) []string {
	return []string{"docker-compose.yml"}
}

func (Generator) Generate(cfg *infra.InfraConfig) ([]infra.FileSpec, error) {
	spec, err := GenerateCompose(cfg)
	if err != nil {
		return nil, err
	}
	return []infra.FileSpec{spec}, nil
}

// GenerateCompose renders the dev stack docker-compose.yml.
func GenerateCompose(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("compose").Parse(composeTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("compose template: %w", err)
	}
	return infra.FileSpec{
		Path: "docker-compose.yml", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Dev stack: app + postgres + redis + mailpit (+ adminer opt) with healthchecks/depends_on (INFRA-006/007); PR preview profile (INFRA-055)",
	}, nil
}

const composeTemplate = `name: {{.App.Name}}-dev

services:
  app:
    build:
      context: .
      dockerfile: Dockerfile
    image: {{.App.Name}}:dev
    container_name: {{.App.Name}}-app
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      mailpit:
        condition: service_started
    environment:
      APP_ENV: dev
      HTTP_PORT: "{{.App.Port}}"
      DATABASE_URL: postgres://{{.Database.User}}:dev@postgres:5432/{{.Database.Name}}?sslmode=disable
      REDIS_URL: redis://redis:6379/0
      SMTP_URL: smtp://mailpit:1025
    ports:
      - "{{.App.Port}}:{{.App.Port}}"
    healthcheck:
      test: ["CMD", "/app", "health", "--path", "{{.Probes.LivenessPath}}", "--port", "{{.App.Port}}"]
      interval: 10s
      timeout: 1s
      retries: 3
      start_period: 5s
    restart: unless-stopped

  postgres:
    image: postgres:{{.Compose.PostgresVersion}}-alpine
    container_name: {{.App.Name}}-postgres
    environment:
      POSTGRES_DB: {{.Database.Name}}
      POSTGRES_USER: {{.Database.User}}
      POSTGRES_PASSWORD: dev
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U {{.Database.User}} -d {{.Database.Name}}"]
      interval: 5s
      timeout: 3s
      retries: 10
    restart: unless-stopped

  redis:
    image: redis:{{.Compose.RedisVersion}}-alpine
    container_name: {{.App.Name}}-redis
    command: ["redis-server", "--save", "60", "1", "--loglevel", "warning"]
    ports:
      - "6379:6379"
    volumes:
      - redisdata:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10
    restart: unless-stopped

  mailpit:
    image: axllent/mailpit:{{.Compose.MailpitVersion}}
    container_name: {{.App.Name}}-mailpit
    ports:
      - "1025:1025"  # SMTP
      - "8025:8025"  # web UI
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8025/live"]
      interval: 10s
      timeout: 3s
      retries: 5
    restart: unless-stopped
{{if .Compose.Adminer}}
  adminer:
    image: adminer:latest
    container_name: {{.App.Name}}-adminer
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "8081:8080"
    environment:
      ADMINER_DEFAULT_SERVER: postgres
    restart: unless-stopped
{{end}}
  # PR preview env (INFRA-055): one-off ephemeral stack on the "preview"
  # profile. Booted by CI per PR; torn down on merge.
  preview:
    profiles: ["{{.Compose.PreviewProfile}}"]
    image: {{.App.Name}}:preview
    build:
      context: .
      dockerfile: Dockerfile
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    environment:
      APP_ENV: preview
      HTTP_PORT: "18080"
      DATABASE_URL: postgres://{{.Database.User}}:preview@postgres:5432/{{.Database.Name}}_preview?sslmode=disable
      REDIS_URL: redis://redis:6379/1
    ports:
      - "18080:18080"
    restart: "no"

volumes:
  pgdata:
  redisdata:
`
