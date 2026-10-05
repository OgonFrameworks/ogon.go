// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Cloudflare provider stubs. Emits a minimal Cloudflare Terraform snippet
// for projects that front their app with Cloudflare (DNS, WAF, R2, Workers).
// These are stubs because the canonical provider choice is determined per
// project; the generator emits a working-but-minimal starting point.

package cloudflare

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// Generator implements infra.Generator for the cloudflare target.
type Generator struct{}

func (Generator) Name() string { return "cloudflare" }

func (Generator) Plan(cfg *infra.InfraConfig) []string {
	return []string{
		"infra/cloudflare/main.tf",
		"infra/cloudflare/variables.tf",
		"infra/cloudflare/waf.tf",
	}
}

func (Generator) Generate(cfg *infra.InfraConfig) ([]infra.FileSpec, error) {
	main, err := GenerateMain(cfg)
	if err != nil {
		return nil, err
	}
	vars, err := GenerateVariables(cfg)
	if err != nil {
		return nil, err
	}
	waf, err := GenerateWAF(cfg)
	if err != nil {
		return nil, err
	}
	return []infra.FileSpec{main, vars, waf}, nil
}

// GenerateMain emits infra/cloudflare/main.tf.
func GenerateMain(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("cf-main").Parse(mainTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("cf main template: %w", err)
	}
	return infra.FileSpec{
		Path: "infra/cloudflare/main.tf", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Cloudflare DNS + R2 + Workers stub",
	}, nil
}

// GenerateVariables emits infra/cloudflare/variables.tf.
func GenerateVariables(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	const vars = `variable "cloudflare_account_id" {
  type      = string
  sensitive = true
}

variable "zone_id" {
  description = "Cloudflare zone id for the apex domain"
  type        = string
}

variable "app_hostname" {
  description = "Hostname for the app (CNAME → origin)"
  type        = string
  default     = "app.example.com"
}

variable "origin_address" {
  description = "Origin address (ALB DNS, Cloud Run, etc.)"
  type        = string
}
`
	return infra.FileSpec{
		Path: "infra/cloudflare/variables.tf", Content: vars,
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Cloudflare variables (account, zone, hostname, origin)",
	}, nil
}

// GenerateWAF emits infra/cloudflare/waf.tf.
func GenerateWAF(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	const waf = `# Cloudflare WAF custom rules + rate-limiting. Cloudflare-managed
# rulesets are enabled via the dashboard; this file holds project-specific
# overrides.

resource "cloudflare_ruleset" "rate_limit" {
  account_id  = var.cloudflare_account_id
  name        = "ogon_rate_limit"
  description = "Project-specific rate limit"
  kind        = "zone"
  phase       = "http_ratelimit"
  rules {
    expression  = "(http.request.uri.path starts with \"/api/\")"
    action      = "block"
    description = "Block >100 req/min per IP on /api/"
    ratelimit {
      characteristics        = ["ip.src"]
      period                 = 60
      requests_per_period    = 100
      mitigation_timeout     = 300
    }
  }
}

resource "cloudflare_record" "app" {
  zone_id = var.zone_id
  name    = var.app_hostname
  value   = var.origin_address
  type    = "CNAME"
  ttl     = 60
  proxied = true
}
`
	return infra.FileSpec{
		Path: "infra/cloudflare/waf.tf", Content: waf,
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Cloudflare WAF rate-limit + DNS CNAME proxied",
	}, nil
}

const mainTemplate = `terraform {
  required_version = ">= 1.7.0"
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 4.0" }
  }
}

provider "cloudflare" {
  # Reads CF_API_TOKEN from env. Use a fine-grained token scoped to this zone.
}

# R2 bucket for static assets / uploads (optional).
resource "cloudflare_r2_bucket" "assets" {
  account_id = var.cloudflare_account_id
  name       = "{{.App.Name}}-assets"
  location   = "apac"
}

# Workers binding for the app (optional — used when the app's edge logic
# is in a Worker). Worker source is committed alongside this file.
resource "cloudflare_worker_script" "edge" {
  name    = "{{.App.Name}}-edge"
  content = file("infra/cloudflare/worker.js")
  r2_bucket_binding {
    name        = "ASSETS"
    bucket_name = cloudflare_r2_bucket.assets.name
  }
}
`
