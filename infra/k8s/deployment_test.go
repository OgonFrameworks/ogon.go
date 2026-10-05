// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// k8s Deployment generator tests. Asserts INFRA-008/017/043/060/061/062.

package k8s

import (
	"strings"
	"testing"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// TestGeneratorImplementsInterface verifies Generator satisfies infra.Generator.
func TestGeneratorImplementsInterface(t *testing.T) {
	var _ infra.Generator = Generator{}
}

// TestDeploymentProbes verifies INFRA-008 probes (liveness+readiness+startup).
func TestDeploymentProbes(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateDeployment(cfg)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	c := spec.Stamped()
	for _, want := range []string{"livenessProbe:", "readinessProbe:", "startupProbe:"} {
		if !strings.Contains(c, want) {
			t.Errorf("expected %s (INFRA-008)", want)
		}
	}
	for _, want := range []string{cfg.Probes.LivenessPath, cfg.Probes.ReadinessPath, cfg.Probes.StartupPath} {
		if !strings.Contains(c, want) {
			t.Errorf("expected probe path %q", want)
		}
	}
}

// TestDeploymentResources verifies INFRA-043 baseline (250m/512Mi).
func TestDeploymentResources(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateDeployment(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "250m") {
		t.Error("expected 250m CPU request (INFRA-043)")
	}
	if !strings.Contains(c, "512Mi") {
		t.Error("expected 512Mi memory request (INFRA-043)")
	}
}

// TestDeploymentSecurityContext verifies non-root + read-only rootfs.
func TestDeploymentSecurityContext(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateDeployment(cfg)
	c := spec.Stamped()
	for _, want := range []string{"runAsNonRoot: true", "runAsUser: 65532", "readOnlyRootFilesystem: true", `drop: ["ALL"]`} {
		if !strings.Contains(c, want) {
			t.Errorf("expected %q in securityContext (INFRA-008)", want)
		}
	}
}

// TestDeploymentRollingStrategy verifies INFRA-017 zero-downtime rolling.
func TestDeploymentRollingStrategy(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateDeployment(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "type: RollingUpdate") {
		t.Error("expected RollingUpdate strategy (INFRA-017)")
	}
	if !strings.Contains(c, "maxUnavailable: 0") {
		t.Error("expected maxUnavailable: 0 (INFRA-017/062 zero-downtime)")
	}
}

// TestDeploymentStartupProbeBudget verifies INFRA-060 (~5 min budget).
func TestDeploymentStartupProbeBudget(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateDeployment(cfg)
	c := spec.Stamped()
	// failureThreshold: 30 + periodSeconds: 10 = 300s budget.
	if !strings.Contains(c, "failureThreshold: 30") {
		t.Error("expected startup failureThreshold: 30 (INFRA-060)")
	}
	if !strings.Contains(c, "periodSeconds: 10") {
		t.Error("expected startup periodSeconds: 10 (INFRA-060)")
	}
}

// TestDeploymentSIGTERMDrain verifies INFRA-061: terminationGracePeriodSeconds.
func TestDeploymentSIGTERMDrain(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateDeployment(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "terminationGracePeriodSeconds: 30") {
		t.Error("expected terminationGracePeriodSeconds: 30 (INFRA-061 web drain)")
	}
	if !strings.Contains(c, `command: ["/app", "drain"]`) {
		t.Error("expected preStop drain (INFRA-061)")
	}
}

// TestWorkerDrainLonger verifies the worker has a longer drain (INFRA-061).
func TestWorkerDrainLonger(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateWorkerDeployment(cfg)
	if err != nil {
		t.Fatalf("gen worker: %v", err)
	}
	c := spec.Stamped()
	if !strings.Contains(c, "terminationGracePeriodSeconds: 300") {
		t.Error("expected worker terminationGracePeriodSeconds: 300 (INFRA-061)")
	}
	if !strings.Contains(c, `command: ["/app", "drain", "--timeout", "300s"]`) {
		t.Error("expected worker preStop drain with 300s timeout (INFRA-061)")
	}
}

// TestMigrationJob verifies INFRA-028 pre-deploy migration Job.
func TestMigrationJob(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateMigrationJob(cfg)
	if err != nil {
		t.Fatalf("gen migrate: %v", err)
	}
	c := spec.Stamped()
	if !strings.Contains(c, `kind: Job`) {
		t.Error("expected kind: Job (INFRA-028)")
	}
	if !strings.Contains(c, `"migrate"`) || !strings.Contains(c, `"up"`) {
		t.Error("expected migrate up command (INFRA-028)")
	}
	if !strings.Contains(c, "pre-upgrade,pre-install") {
		t.Error("expected helm pre-upgrade hook (INFRA-028)")
	}
}

// TestNetworkPolicyDefaultDeny verifies INFRA-065 default-deny ingress.
func TestNetworkPolicyDefaultDeny(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateNetworkPolicy(cfg)
	if err != nil {
		t.Fatalf("gen np: %v", err)
	}
	c := spec.Stamped()
	if !strings.Contains(c, "default-deny-ingress") {
		t.Error("expected default-deny-ingress (INFRA-065)")
	}
	if !strings.Contains(c, "policyTypes:") {
		t.Error("expected policyTypes (INFRA-013)")
	}
	if !strings.Contains(c, "port: 5432") {
		t.Error("expected scoped egress to postgres:5432 (INFRA-013/064)")
	}
	if !strings.Contains(c, "port: 6379") {
		t.Error("expected scoped egress to redis:6379 (INFRA-013/064)")
	}
}

// TestHPA verifies INFRA-011 (cpu+mem) and INFRA-044 (autoscale on p95).
func TestHPA(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GenerateHPA(cfg)
	if err != nil {
		t.Fatalf("gen hpa: %v", err)
	}
	c := spec.Stamped()
	if !strings.Contains(c, "name: cpu") {
		t.Error("expected cpu metric (INFRA-011)")
	}
	if !strings.Contains(c, "name: memory") {
		t.Error("expected memory metric (INFRA-011)")
	}
	if !strings.Contains(c, "ogonframeworks.dev/autoscale-policy") {
		t.Error("expected autoscale-on-p95 annotation (INFRA-044)")
	}
}

// TestPDB verifies INFRA-012.
func TestPDB(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, err := GeneratePDB(cfg)
	if err != nil {
		t.Fatalf("gen pdb: %v", err)
	}
	if !strings.Contains(spec.Stamped(), "kind: PodDisruptionBudget") {
		t.Error("expected kind: PodDisruptionBudget (INFRA-012)")
	}
}

// TestIngressTLS verifies INFRA-045 cert-manager + INFRA-046 external-dns.
func TestIngressTLS(t *testing.T) {
	cfg := infra.DefaultConfig()
	spec, _ := GenerateIngress(cfg)
	c := spec.Stamped()
	if !strings.Contains(c, "cert-manager.io/cluster-issuer:") {
		t.Error("expected cert-manager annotation (INFRA-045)")
	}
	if !strings.Contains(c, "external-dns.alpha.kubernetes.io/hostname:") {
		t.Error("expected external-dns annotation (INFRA-046)")
	}
}

// TestInferRolloutStatusComplete verifies INFRA-068 status inference.
func TestInferRolloutStatusComplete(t *testing.T) {
	snap := DeploymentSnapshot{
		Generation: 3, ObservedGen: 3, Replicas: 2,
		UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2,
	}
	if got := InferRolloutStatus(snap); got != RolloutComplete {
		t.Errorf("got %q, want complete (INFRA-068)", got)
	}
}

// TestInferRolloutStatusFailed verifies INFRA-068 failure detection.
func TestInferRolloutStatusFailed(t *testing.T) {
	snap := DeploymentSnapshot{
		Conditions: []DeploymentCondition{{
			Type: "Progressing", Status: "False",
			Reason: "ProgressDeadlineExceeded",
		}},
	}
	if got := InferRolloutStatus(snap); got != RolloutFailed {
		t.Errorf("got %q, want failed (INFRA-068)", got)
	}
}

// TestInferRolloutStatusInProgress verifies in-progress detection.
func TestInferRolloutStatusInProgress(t *testing.T) {
	snap := DeploymentSnapshot{
		Generation: 3, ObservedGen: 2, Replicas: 2,
		UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1,
	}
	if got := InferRolloutStatus(snap); got != RolloutInProgress {
		t.Errorf("got %q, want in_progress (INFRA-068)", got)
	}
}

// TestDecideRollbackProgressDeadline verifies INFRA-069 rollback decision.
func TestDecideRollbackProgressDeadline(t *testing.T) {
	snap := DeploymentSnapshot{
		Conditions: []DeploymentCondition{{
			Type: "Progressing", Status: "False",
			Reason: "ProgressDeadlineExceeded",
		}},
	}
	d := DecideRollback(snap, 5)
	if !d.ShouldRollback {
		t.Errorf("expected rollback=true (INFRA-069); reason=%q", d.Reason)
	}
}

// TestDecideRollbackNoCondition verifies no rollback when conditions are fine.
func TestDecideRollbackNoCondition(t *testing.T) {
	snap := DeploymentSnapshot{
		ReplicaSets: []ReplicaSetSummary{{
			Revision: 5, Replicas: 2, FailedPods: 0, Updated: true,
		}},
	}
	d := DecideRollback(snap, 5)
	if d.ShouldRollback {
		t.Errorf("expected rollback=false; reason=%q", d.Reason)
	}
}

// TestPollRolloutComplete verifies the polling state machine returns on
// complete.
func TestPollRolloutComplete(t *testing.T) {
	calls := 0
	fetch := func() (DeploymentSnapshot, error) {
		calls++
		return DeploymentSnapshot{
			Generation: 1, ObservedGen: 1, Replicas: 1,
			UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1,
		}, nil
	}
	status, _, err := PollRollout(fetch, PollOptions{Interval: 0, Timeout: 0})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if status != RolloutComplete {
		t.Errorf("got %q, want complete (INFRA-068)", status)
	}
	if calls != 1 {
		t.Errorf("fetch called %d times, want 1 (immediate complete)", calls)
	}
}

// TestGeneratorPlan verifies the k8s plan is stable (INFRA-039).
func TestGeneratorPlan(t *testing.T) {
	cfg := infra.DefaultConfig()
	plan := Generator{}.Plan(cfg)
	if len(plan) == 0 {
		t.Fatal("k8s plan is empty")
	}
	for i := 1; i < len(plan); i++ {
		if plan[i-1] > plan[i] {
			t.Errorf("plan not sorted: %q before %q (INFRA-039)", plan[i-1], plan[i])
		}
	}
}

// TestGenerateAll verifies the full Generate() surface returns every file.
func TestGenerateAll(t *testing.T) {
	cfg := infra.DefaultConfig()
	specs, err := Generator{}.Generate(cfg)
	if err != nil {
		t.Fatalf("generate all: %v", err)
	}
	if len(specs) != len(Generator{}.Plan(cfg)) {
		t.Errorf("Generate len = %d, Plan len = %d (must match)", len(specs), len(Generator{}.Plan(cfg)))
	}
}
