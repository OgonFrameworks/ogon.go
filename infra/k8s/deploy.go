// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon deploy status` / `ogon deploy --rollback` (INFRA-068/069). These
// helpers poll a Deployment's rollout status (kubectl-free, using the same
// k8s client surface the runtime would use). They emit human-readable
// progress and exit cleanly when the rollout is complete or has failed.
//
// This file ships the contract surface and a polling state machine. The
// actual k8s client wiring lands in the runtime phase; here we expose the
// logic that is testable without a live cluster: rollout-state inference
// from ReplicaSet conditions, and rollback decision logic.

package k8s

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// RolloutStatus is the high-level result of polling a Deployment's rollout.
type RolloutStatus string

const (
	RolloutComplete   RolloutStatus = "complete"
	RolloutInProgress RolloutStatus = "in_progress"
	RolloutTimedOut   RolloutStatus = "timed_out"
	RolloutFailed     RolloutStatus = "failed"
	RolloutRolledBack RolloutStatus = "rolled_back"
)

// ReplicaSetSummary is the minimal shape we infer rollout status from.
type ReplicaSetSummary struct {
	Name       string
	Replicas   int
	Available  int
	Ready      int
	Updated    bool
	Generation int64
	Revision   int64
	FailedPods int
}

// DeploymentSnapshot is the input to the polling state machine. The runtime
// fills this from k8s API responses; tests fill it directly.
type DeploymentSnapshot struct {
	Name              string
	Namespace         string
	Generation        int64
	ObservedGen       int64
	UpdatedReplicas   int
	ReadyReplicas     int
	AvailableReplicas int
	Replicas          int
	Conditions        []DeploymentCondition
	ReplicaSets       []ReplicaSetSummary
}

// DeploymentCondition is the minimal k8s condition we look at.
type DeploymentCondition struct {
	Type    string
	Status  string // "True"|"False"|"Unknown"
	Reason  string
	Message string
}

// PollOptions configures the rollout poll loop.
type PollOptions struct {
	Interval   time.Duration // default 2s
	Timeout    time.Duration // default 5m
	OnProgress func(DeploymentSnapshot, RolloutStatus)
}

// withDefaults fills zero poll fields with sane defaults.
func (o PollOptions) withDefaults() PollOptions {
	if o.Interval == 0 {
		o.Interval = 2 * time.Second
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Minute
	}
	return o
}

// PollRollout iterates fetch+infer until the rollout completes, fails, or
// times out. fetch returns the next DeploymentSnapshot; it is called once
// per Interval. The state machine is pure (no k8s API) so it is unit-tested
// without a cluster.
func PollRollout(fetch func() (DeploymentSnapshot, error), opts PollOptions) (RolloutStatus, DeploymentSnapshot, error) {
	opts = opts.withDefaults()
	deadline := time.Now().Add(opts.Timeout)
	var last DeploymentSnapshot
	for {
		now := time.Now()
		if now.After(deadline) {
			return RolloutTimedOut, last, errors.New("ogon deploy status: timed out waiting for rollout")
		}
		snap, err := fetch()
		if err != nil {
			return RolloutFailed, last, fmt.Errorf("ogon deploy status: fetch: %w", err)
		}
		last = snap
		status := InferRolloutStatus(snap)
		if opts.OnProgress != nil {
			opts.OnProgress(snap, status)
		}
		switch status {
		case RolloutComplete, RolloutFailed:
			return status, snap, nil
		case RolloutRolledBack:
			return status, snap, nil
		}
		time.Sleep(opts.Interval)
	}
}

// InferRolloutStatus is the pure decision function. It encodes the k8s
// rollout contract: a rollout is complete when ObservedGen == Generation and
// UpdatedReplicas == Replicas == AvailableReplicas. It is failed when the
// DeploymentProgressing condition has reason "ProgressDeadlineExceeded".
func InferRolloutStatus(s DeploymentSnapshot) RolloutStatus {
	// Failure: progress deadline exceeded.
	for _, c := range s.Conditions {
		if c.Type == "Progressing" && c.Status == "False" && c.Reason == "ProgressDeadlineExceeded" {
			return RolloutFailed
		}
		if c.Type == "ReplicaFailure" && c.Status == "True" {
			// not fatal by itself; combine with failed pods later
		}
	}
	// Rolled back: the latest ReplicaSet is not the updated one.
	if len(s.ReplicaSets) > 0 {
		// Find the most recent ReplicaSet; if it has Updated=false, the
		// deployment was rolled back.
		newest := s.ReplicaSets[0]
		for _, rs := range s.ReplicaSets {
			if rs.Revision > newest.Revision {
				newest = rs
			}
		}
		if !newest.Updated && newest.Replicas > 0 {
			return RolloutRolledBack
		}
	}
	// Complete: observed == generation, all replicas updated+ready+available.
	if s.ObservedGen >= s.Generation &&
		s.UpdatedReplicas == s.Replicas &&
		s.AvailableReplicas == s.Replicas &&
		s.ReadyReplicas == s.Replicas {
		return RolloutComplete
	}
	return RolloutInProgress
}

// RollbackDecision decides whether to roll back given a failed snapshot.
// We roll back only when the failure is unrecoverable (progress deadline
// exceeded, or >50% of pods have failed in the new ReplicaSet).
type RollbackDecision struct {
	ShouldRollback bool
	Reason         string
}

// DecideRollback is the pure decision function for `ogon deploy --rollback`.
func DecideRollback(s DeploymentSnapshot, currentRev int64) RollbackDecision {
	for _, c := range s.Conditions {
		if c.Type == "Progressing" && c.Status == "False" && c.Reason == "ProgressDeadlineExceeded" {
			return newRollbackDecision(true, "progress deadline exceeded (INFRA-069)")
		}
	}
	// >50% failed pods in the latest ReplicaSet → roll back.
	for _, rs := range s.ReplicaSets {
		if rs.Revision == currentRev && rs.Replicas > 0 {
			if rs.FailedPods*2 > rs.Replicas {
				return RollbackDecision{
					ShouldRollback: true,
					Reason:         fmt.Sprintf(">50%% of pods failed in revision %d (%d/%d)", currentRev, rs.FailedPods, rs.Replicas),
				}
			}
		}
	}
	return RollbackDecision{ShouldRollback: false, Reason: "no rollback condition met"}
}

// newRollbackDecision is the helper constructor (lowercase to avoid
// colliding with the exported RollbackDecision type).
func newRollbackDecision(rollback bool, reason string) RollbackDecision {
	return RollbackDecision{ShouldRollback: rollback, Reason: reason}
}

// errRolloutDevice rolls in from outside the package to keep the surface clean.
var _ = strings.TrimSpace

// DeployStatusReport is the JSON shape returned by `ogon deploy status`.
type DeployStatusReport struct {
	Status   RolloutStatus `json:"status"`
	Revision int64         `json:"revision"`
	Reason   string        `json:"reason,omitempty"`
	Message  string        `json:"message,omitempty"`
	Duration string        `json:"duration,omitempty"`
}

// BuildDeployStatusReport converts a snapshot + status into the CLI envelope.
func BuildDeployStatusReport(s DeploymentSnapshot, status RolloutStatus) DeployStatusReport {
	r := DeployStatusReport{Status: status}
	if len(s.ReplicaSets) > 0 {
		for _, rs := range s.ReplicaSets {
			if rs.Updated {
				r.Revision = rs.Revision
			}
		}
	}
	for _, c := range s.Conditions {
		if c.Type == "Progressing" {
			r.Reason = c.Reason
			r.Message = c.Message
		}
	}
	return r
}

// ErrRolloutTimedOut is the sentinel returned when a rollout poll exceeds
// its budget.
var ErrRolloutTimedOut = errors.New("ogon: rollout timed out")

// infra import marker — package surfaces a `Default` constant so callers
// can introspect the active config without rebuilding it.
var _ = infra.DefaultConfig
