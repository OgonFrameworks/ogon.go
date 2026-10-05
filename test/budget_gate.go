// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Budget regression gate (TEST-051). The gate reads the latest benchmark
// baseline from a JSON file and compares against the candidate. If any
// metric regresses more than the configured threshold (default 10% per
// PERF-009), the gate fails the build. The threshold is per-area so
// cheap code (router match < 1 µs) and expensive code (codegen ≤ 2 s)
// share the same gate.

package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

// BudgetThreshold is a per-area regression bound. Area is the bench name
// (e.g., "RouterMatch"); MaxPctRegress is the maximum allowed regression
// in percent (10 == 10% slower is OK, more is a fail).
type BudgetThreshold struct {
	Area          string
	MaxPctRegress float64
}

// BudgetReport is one bench run's results: name -> value (ns/op, B/op, etc).
// The JSON shape is intentionally the same as the standard testing.B
// benchmark output so the gate works on the project's existing reports.
type BudgetReport struct {
	Source     string                  `json:"source"`
	RecordedAt string                  `json:"recorded_at"`
	Results    map[string]BudgetResult `json:"results"`
}

// BudgetResult is a single bench result.
type BudgetResult struct {
	NsPerOp     float64 `json:"ns_per_op"`
	BytesPerOp  int64   `json:"bytes_per_op"`
	AllocsPerOp int64   `json:"allocs_per_op"`
}

// BudgetGate is the per-test gate. Construct from a baseline file and a
// candidate report; call Check to fail the test on regression.
type BudgetGate struct {
	Baseline   *BudgetReport
	Candidate  *BudgetReport
	Thresholds []BudgetThreshold
	Default    float64
}

// LoadBudgetReport reads a JSON bench report from path.
func LoadBudgetReport(path string) (*BudgetReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ogontest: read %s: %w", path, err)
	}
	var r BudgetReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("ogontest: parse %s: %w", path, err)
	}
	return &r, nil
}

// WriteBudgetReport serialises a report to path.
func WriteBudgetReport(path string, r *BudgetReport) error {
	if r == nil {
		return errors.New("ogontest: nil report")
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// NewBudgetGate constructs a gate with the supplied thresholds.
func NewBudgetGate(baseline, candidate *BudgetReport, thresholds []BudgetThreshold, defaultPct float64) *BudgetGate {
	if defaultPct <= 0 {
		defaultPct = 10.0
	}
	return &BudgetGate{
		Baseline:   baseline,
		Candidate:  candidate,
		Thresholds: thresholds,
		Default:    defaultPct,
	}
}

// Regression is a single per-area delta above the threshold.
type Regression struct {
	Area         string
	BaselineNs   float64
	CandidateNs  float64
	PctChange    float64
	ThresholdPct float64
}

// Check computes the regressions. Returns nil if no area regressed beyond
// its threshold.
func (g *BudgetGate) Check() []Regression {
	if g.Baseline == nil || g.Candidate == nil {
		return nil
	}
	var out []Regression
	for name, cand := range g.Candidate.Results {
		base, ok := g.Baseline.Results[name]
		if !ok {
			continue // new bench, no baseline
		}
		if base.NsPerOp <= 0 {
			continue
		}
		delta := (cand.NsPerOp - base.NsPerOp) / base.NsPerOp * 100
		threshold := g.thresholdFor(name)
		if delta > threshold {
			out = append(out, Regression{
				Area:         name,
				BaselineNs:   base.NsPerOp,
				CandidateNs:  cand.NsPerOp,
				PctChange:    delta,
				ThresholdPct: threshold,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PctChange > out[j].PctChange })
	return out
}

func (g *BudgetGate) thresholdFor(area string) float64 {
	for _, t := range g.Thresholds {
		if t.Area == area {
			return t.MaxPctRegress
		}
	}
	return g.Default
}

// AssertGate is the test entrypoint: fails the test if any regression is
// detected. Prints a focused report so CI logs are actionable.
func AssertGate(t interface{ Fatalf(string, ...any) }, g *BudgetGate) {
	if g == nil {
		t.Fatalf("ogontest: nil budget gate")
	}
	regressions := g.Check()
	if len(regressions) == 0 {
		return
	}
	var msg string
	for _, r := range regressions {
		msg += fmt.Sprintf("  %s: %g → %g ns/op (%.2f%% > %.0f%%)\n",
			r.Area, r.BaselineNs, r.CandidateNs, r.PctChange, r.ThresholdPct)
	}
	t.Fatalf("ogontest: budget regression detected:\n%s", msg)
}
