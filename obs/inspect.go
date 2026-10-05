// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon inspect runtime` live snapshot. Implements OBS-024.
//
// The CLI's `ogon inspect runtime` command prints a one-screen snapshot
// of the live process: version, uptime, goroutine count, memstats, GC
// pause, DB pool, queue depth, live conns. The data is gathered here so
// the CLI and the http/inspect endpoint share one renderer.

package obs

import (
	"encoding/json"
	"fmt"
	"runtime"
	"time"
)

// RuntimeSnapshotFull is the data structure returned by `ogon inspect
// runtime` and the http/inspect endpoint. (OBS-024)
type RuntimeSnapshotFull struct {
	Time         time.Time     `json:"time"`
	Host         string        `json:"host,omitempty"`
	Version      string        `json:"version,omitempty"`
	Revision     string        `json:"revision,omitempty"`
	Environment  string        `json:"environment,omitempty"`
	Uptime       time.Duration `json:"uptime_ms"`
	State        string        `json:"state,omitempty"`
	Goroutines   int           `json:"goroutines"`
	CgoCalls     int64         `json:"cgo_calls"`
	AllocBytes   uint64        `json:"alloc_bytes"`
	SysBytes     uint64        `json:"sys_bytes"`
	HeapObjects  uint64        `json:"heap_objects"`
	NumGC        uint32        `json:"num_gc"`
	PauseTotalNs uint64        `json:"gc_pause_total_ns"`
	NextGCBytes  uint64        `json:"gc_next_bytes"`
}

// CollectRuntimeSnapshot returns a live snapshot of the runtime. (OBS-024)
func CollectRuntimeSnapshot(version, revision, env string) RuntimeSnapshotFull {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return RuntimeSnapshotFull{
		Time:         now(),
		Host:         Hostname(),
		Version:      version,
		Revision:     revision,
		Environment:  env,
		Uptime:       now().Sub(processStart),
		Goroutines:   runtime.NumGoroutine(),
		CgoCalls:     int64(runtime.NumCgoCall()),
		AllocBytes:   ms.Alloc,
		SysBytes:     ms.Sys,
		HeapObjects:  ms.HeapObjects,
		NumGC:        ms.NumGC,
		PauseTotalNs: ms.PauseTotalNs,
		NextGCBytes:  ms.NextGC,
	}
}

// RenderSnapshotJSON serialises a snapshot as pretty-printed JSON. (OBS-024)
func RenderSnapshotJSON(s RuntimeSnapshotFull) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// RenderSnapshotHuman renders a one-screen human-friendly snapshot. (OBS-024)
func RenderSnapshotHuman(s RuntimeSnapshotFull) string {
	var b []byte
	add := func(format string, args ...any) {
		b = append(b, []byte(fmt.Sprintf(format, args...))...)
	}
	add("OgonGo runtime snapshot (%s)\n", s.Time.Format(time.RFC3339))
	if s.Host != "" {
		add("  host:          %s\n", s.Host)
	}
	add("  version:       %s\n", s.Version)
	add("  revision:      %s\n", s.Revision)
	add("  environment:   %s\n", s.Environment)
	add("  uptime:        %s\n", s.Uptime)
	add("  goroutines:    %d\n", s.Goroutines)
	add("  cgo calls:     %d\n", s.CgoCalls)
	add("  alloc bytes:   %d (%d MiB)\n", s.AllocBytes, s.AllocBytes/1024/1024)
	add("  sys bytes:     %d (%d MiB)\n", s.SysBytes, s.SysBytes/1024/1024)
	add("  heap objects:  %d\n", s.HeapObjects)
	add("  num GC:        %d\n", s.NumGC)
	add("  GC pause:      %s\n", time.Duration(s.PauseTotalNs))
	add("  GC target:     %d (%d MiB)\n", s.NextGCBytes, s.NextGCBytes/1024/1024)
	return string(b)
}

// processStart is the package-level start time, captured at init. (OBS-024)
var processStart = time.Now()
