// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Container-aware runtime limits: read cgroup CPU/memory budgets.

package limits

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Limits is the resolved resource budget for the process.
//
// Memory is in bytes (0 = unset); CPUQuota is the GOMAXPROCS-equivalent
// count derived from the cgroup cpu quota (0 = unset). Source records the
// origin of each value for `ogon explain runtime`.
type Limits struct {
	MemoryBytes   int64
	CPUQuota      int
	MemorySource  string
	CPUSource     string
	CgroupVersion int // 1, 2, or 0 (none)
}

// Detect reads cgroup v2 first, then v1, and returns the resolved Limits.
// On unsupported platforms (non-Linux, no cgroup mounted), it returns
// Limits{} with a nil error — callers should fall back to runtime defaults.
func Detect() (Limits, error) {
	if runtime.GOOS != "linux" {
		return Limits{}, nil
	}
	if v, ok, err := readV2(); ok {
		v.CgroupVersion = 2
		return v, err
	}
	if v, ok, err := readV1(); ok {
		v.CgroupVersion = 1
		return v, err
	}
	return Limits{}, nil
}

// readV2 reads /sys/fs/cgroup/memory.max and /sys/fs/cgroup/cpu.max.
func readV2() (Limits, bool, error) {
	const memPath = "/sys/fs/cgroup/memory.max"
	const cpuPath = "/sys/fs/cgroup/cpu.max"
	out := Limits{}

	memData, err := os.ReadFile(memPath)
	if err != nil {
		return out, false, nil
	}
	memStr := strings.TrimSpace(string(memData))
	if memStr == "max" {
		// no limit set
	} else if n, err := strconv.ParseInt(memStr, 10, 64); err == nil {
		out.MemoryBytes = n
		out.MemorySource = memPath
	} else {
		return out, true, fmt.Errorf("limits: parse %s: %w", memPath, err)
	}

	cpuData, err := os.ReadFile(cpuPath)
	if err != nil {
		// memory is enough; return what we have
		return out, true, nil
	}
	cpuParts := strings.Fields(strings.TrimSpace(string(cpuData)))
	if len(cpuParts) >= 2 && cpuParts[0] != "max" {
		quota, qerr := strconv.ParseInt(cpuParts[0], 10, 64)
		period, perr := strconv.ParseInt(cpuParts[1], 10, 64)
		if qerr == nil && perr == nil && period > 0 {
			out.CPUQuota = int(quota / period)
			if out.CPUQuota < 1 {
				out.CPUQuota = 1
			}
			out.CPUSource = cpuPath
		}
	}
	return out, true, nil
}

// readV1 reads the cgroup v1 memory/cpu files.
func readV1() (Limits, bool, error) {
	const memPath = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	const cpuPath = "/sys/fs/cgroup/cpu/cpu.cfs_quota_us"
	const periodPath = "/sys/fs/cgroup/cpu/cpu.cfs_period_us"
	out := Limits{}

	if data, err := os.ReadFile(memPath); err == nil {
		if n, perr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); perr == nil && n > 0 && n < 1<<62 {
			out.MemoryBytes = n
			out.MemorySource = memPath
		}
	} else {
		return out, false, nil
	}

	quotaData, err := os.ReadFile(cpuPath)
	if err != nil {
		return out, true, nil
	}
	periodData, _ := os.ReadFile(periodPath)
	quota, _ := strconv.ParseInt(strings.TrimSpace(string(quotaData)), 10, 64)
	period, _ := strconv.ParseInt(strings.TrimSpace(string(periodData)), 10, 64)
	if quota > 0 && period > 0 {
		out.CPUQuota = int(quota / period)
		if out.CPUQuota < 1 {
			out.CPUQuota = 1
		}
		out.CPUSource = cpuPath
	}
	return out, true, nil
}

// Apply applies detected limits to the Go runtime: GOMAXPROCS and
// GOMEMLIMIT (via debug.SetMemoryLimit, in MiB). The safety margin keeps
// the runtime below the cgroup limit to avoid OOMKill.
// Returns the applied values for `ogon explain runtime`.
func Apply(l Limits) (maxProcs, memLimitMiB int, err error) {
	if l.CPUQuota > 0 {
		maxProcs = l.CPUQuota
		current := runtime.GOMAXPROCS(0)
		if maxProcs != current {
			runtime.GOMAXPROCS(maxProcs)
		}
	}
	if l.MemoryBytes > 0 {
		memLimitMiB = int(float64(l.MemoryBytes) / 1024 / 1024 * 0.9)
		if memLimitMiB < 1 {
			return maxProcs, 0, errors.New("limits: memory too small to apply safely")
		}
		// debug.SetMemoryLimit in Go 1.27 takes int64 bytes.
		SetMemoryLimit(int64(memLimitMiB) << 20)
	}
	return maxProcs, memLimitMiB, nil
}
