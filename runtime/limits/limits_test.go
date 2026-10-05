// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tests for the limits package.

package limits

import (
	"runtime"
	"testing"
)

func TestDetectNonLinuxNoError(t *testing.T) {
	t.Parallel()
	l, err := Detect()
	if err != nil {
		t.Fatalf("Detect() err = %v", err)
	}
	if runtime.GOOS != "linux" {
		if l.CgroupVersion != 0 {
			t.Fatalf("expected 0 on non-Linux, got %d", l.CgroupVersion)
		}
	}
	_ = l // On linux it should be best-effort; either way no error.
}

func TestApplyNoOpWhenZero(t *testing.T) {
	t.Parallel()
	procs, mem, err := Apply(Limits{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if procs != 0 || mem != 0 {
		t.Fatalf("Apply should be no-op with zero limits, got procs=%d mem=%d", procs, mem)
	}
}
