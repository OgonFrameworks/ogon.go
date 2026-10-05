// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package jobs

import (
	"context"
	"testing"
	"time"
)

func TestCronRunnerSnapshot(t *testing.T) {
	schedules := []CronSchedule{
		{Name: "nightly", Spec: "0 2 * * *", TZ: "UTC", Job: "invoice.reconcile"},
		{Name: "minutely", Spec: "* * * * *", TZ: "UTC", Job: "tick"},
	}
	r := NewCronRunner(schedules, func(ctx context.Context, name JobName, args []byte) error { return nil })
	got := r.Snapshot()
	if len(got) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(got))
	}
	if got[0].Name != "nightly" || got[1].Name != "minutely" {
		t.Errorf("snapshot order wrong: %+v", got)
	}
}

func TestCronRunnerIsLeaderOnNoPool(t *testing.T) {
	r := NewCronRunner(nil, func(ctx context.Context, name JobName, args []byte) error { return nil })
	if !r.acquireLock(context.Background(), "") {
		t.Error("acquireLock with no pool should treat as leader (dev mode)")
	}
	if !r.IsLeader() {
		t.Error("IsLeader should be true after acquiring lock")
	}
	r.releaseLock(context.Background(), "")
	if r.IsLeader() {
		t.Error("IsLeader should be false after release")
	}
}

func TestCronRunnerStartStopWithNoPool(t *testing.T) {
	r := NewCronRunner([]CronSchedule{{Name: "x", Spec: "* * * * *", Job: "x"}},
		func(ctx context.Context, name JobName, args []byte) error { return nil })
	if err := r.Start(context.Background(), ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	// give the cron loop a beat to start
	time.Sleep(50 * time.Millisecond)
	_ = r.Stop(context.Background(), "")
}

func TestCronSnapshotIsImmutable(t *testing.T) {
	r := NewCronRunner([]CronSchedule{{Name: "x", Spec: "* * * * *", Job: "x"}},
		func(context.Context, JobName, []byte) error { return nil })
	snap := r.Snapshot()
	snap[0].Name = "mutated"
	again := r.Snapshot()
	if again[0].Name != "x" {
		t.Error("snapshot was mutated externally")
	}
}
