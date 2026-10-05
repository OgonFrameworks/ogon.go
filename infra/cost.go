// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Cost / rightsize hints (INFRA-042) and the 250m/512Mi baseline reference
// (INFRA-043). Emits a markdown doc with concrete cost-vs-throughput
// heuristics and resource shape recommendations.

package infra

import (
	"fmt"
	"strings"
	"text/template"
)

// GenerateCostHints emits the cost/rightsize hints doc (INFRA-042).
func GenerateCostHints(cfg *InfraConfig) (FileSpec, error) {
	tpl := template.Must(template.New("cost").Parse(costTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return FileSpec{}, fmt.Errorf("cost hints template: %w", err)
	}
	return FileSpec{
		Path: "infra/COST.md", Content: b.String(),
		Marker: MarkSeeded, CommentPrefix: "#",
		Description: "Cost / rightsize hints (INFRA-042); resource defaults (INFRA-043)",
	}, nil
}

const costTemplate = `# Cost and Right-Sizing Hints (INFRA-042)

> Seeded by ogon infra gen. Edit by hand. --force required to regenerate.

## Baseline resource shape (INFRA-043)

The generator emits these defaults — they are deliberately conservative so
the first deploy is below all provider soft limits (INFRA-054):

| Role | CPU request | Mem request | CPU limit | Mem limit |
|---|---|---|---|---|
| Web   | 250m | 512Mi | 500m  | 1Gi |
| Worker| 250m | 512Mi | 500m  | 1Gi |
| Migrate Job | 500m | 256Mi | 1 | 512Mi |
| Backup Job | 500m | 512Mi | 1 | 1Gi |

## When to right-size

- **CPU throttling sustained >5% over 1h** → bump CPU request, not limit.
  Limits cause throttling-with-limits behavior that is harder to reason about.
- **Memory working set > 70% of request** → bump request. Avoid memory limits
  unless you understand Go's GC interaction (a tight limit can cause OOMKills
  under bursty traffic).
- **p95 latency regression** → investigate the framework overhead budget
  (PERF-XV.1) before adding resources. The router match budget is <1µs;
  middleware hop ≤200ns. If those are breached, more replicas is the wrong
  fix.
- **HPA flapping** → widen scaleDown.stabilizationWindowSeconds to 300+.

## Per-provider cost notes

### AWS

- Fargate pricing is per vCPU-second + per GB-second. A 0.25 vCPU / 0.5 GB
  task running 24/7 is roughly $3.50/month in us-east-1 (varies).
- RDS db.t4g.micro is ~$12/month; multi-AZ doubles it.
- ElastiCache cache.t4g.micro is ~$11/month.
- NAT Gateway is $0.045/hour + $0.045/GB processed — VPC private-by-default
  (INFRA-064) avoids this entirely for back-computer workloads.

### GCP

- Cloud Run bills per vCPU-second + per GB-second of memory. Free tier
  covers ~2M requests/month.
- Cloud SQL db-custom-1-3840 (1 vCPU, 3.75 GB) is ~$30-50/month.
- Memorystore Basic 1GB is ~$35/month.

### Azure

- AKS is free; you pay for the underlying VMSS.
- Azure DB for Postgres Burstable B1ms is ~$25/month.

## Right-sizing checklist

- [ ] CPU throttling % measured over a representative 24h window.
- [ ] Memory working set measured; p99 not average.
- [ ] HPA target utilization not exceeding 75% CPU / 75% memory.
- [ ] PDB minAvailable reviewed quarterly.
- [ ] Spot/preemptible in use for stateless workloads (cfg.K8s.SpotToleration).
- [ ] Cross-AZ traffic cost reviewed (DB→app is intra-AZ by default).

## Right-sizing anti-patterns

- ❌ "Set limits to 4 CPU" — limits cause CFS throttling and lie about
  available capacity.
- ❌ "Memory limit = memory request" — Go GC needs headroom under bursts.
- ❌ "More replicas is always cheaper" — at some point you pay more for
  network/load-balancer/observability than for the marginal capacity.
`
