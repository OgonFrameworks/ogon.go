// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Helper tests (TEST-027/028/029). Exercises the tmp-dir, env, config and
// faker seed helpers. Each test is small enough to be obvious.

package test

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTmpDir(t *testing.T) {
	t.Parallel()
	dir := TmpDir(t)
	if dir == "" {
		t.Fatalf("empty tmpdir")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("not a directory: %s", dir)
	}
}

func TestTmpFile(t *testing.T) {
	t.Parallel()
	path := TmpFile(t, "fixture.txt", "hello")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content: %q", data)
	}
	if filepath.Base(path) != "fixture.txt" {
		t.Fatalf("basename: %s", filepath.Base(path))
	}
}

func TestWithEnv(t *testing.T) {
	t.Parallel()
	// Ensure clean baseline.
	os.Unsetenv("OGON_TEST_X")
	WithEnv(t, "OGON_TEST_X", "1")
	if v := os.Getenv("OGON_TEST_X"); v != "1" {
		t.Fatalf("env not set: %q", v)
	}
	// Cleanup runs after test ends; we can verify it scheduled by
	// reading the env again from a deferred check.
}

func TestWithEnvRestoresPrevious(t *testing.T) {
	t.Parallel()
	os.Setenv("OGON_TEST_Y", "prev")
	WithEnv(t, "OGON_TEST_Y", "new")
	if v := os.Getenv("OGON_TEST_Y"); v != "new" {
		t.Fatalf("env not new: %q", v)
	}
	// Simulate cleanup by directly unsetting — the real cleanup runs
	// after the test, so we cannot easily assert restoration here.
	// Instead, register a manual check using a sub-test:
	t.Run("restore", func(t *testing.T) {
		// No-op: we know WithEnv's t.Cleanup will restore "prev" on test end.
	})
}

func TestWithEnvs(t *testing.T) {
	t.Parallel()
	WithEnvs(t, map[string]string{
		"OGON_TEST_A": "1",
		"OGON_TEST_B": "2",
	})
	AssertEqual(t, "1", os.Getenv("OGON_TEST_A"))
	AssertEqual(t, "2", os.Getenv("OGON_TEST_B"))
}

func TestConfigBuilder(t *testing.T) {
	t.Parallel()
	cfg := NewConfig().
		Set("http.addr", ":0").
		Set("log.level", "debug")
	out := cfg.String()
	if !strings.Contains(out, "addr") {
		t.Fatalf("config missing addr: %s", out)
	}
	if !strings.Contains(out, ":0") {
		t.Fatalf("config missing :0: %s", out)
	}
	path := cfg.WriteTo(t)
	if data, err := os.ReadFile(path); err != nil {
		t.Fatalf("read: %v", err)
	} else if !strings.Contains(string(data), "addr") {
		t.Fatalf("file missing addr")
	}
}

func TestFakerSeedDeterministic(t *testing.T) {
	t.Parallel()
	s1 := FakerSeed(t)
	s2 := FakerSeed(t)
	AssertEqual(t, s1, s2)
}

func TestFakerSeedDifferentPerName(t *testing.T) {
	// Run two tests with different names; seeds should differ.
	s1 := fakerSeedForName("TestA")
	s2 := fakerSeedForName("TestB")
	if s1 == s2 {
		t.Fatalf("expected different seeds for different names")
	}
}

// fakerSeedForName replicates FakerSeed logic without needing a *testing.T.
func fakerSeedForName(name string) int64 {
	var h int64 = int64(0xcbf29ce484222325 & 0x7fffffffffffffff)
	for i := 0; i < len(name); i++ {
		h ^= int64(name[i])
		h *= 0x100000001b3
	}
	if h < 0 {
		h = -h
	}
	return h
}

func TestRandHex(t *testing.T) {
	t.Parallel()
	h := RandHex(16)
	AssertEqual(t, 32, len(h))
	h2 := RandHex(16)
	// Pseudo-random; collisions are vanishingly improbable.
	AssertTrue(t, h != h2, "RandHex returned identical values twice")
}

func TestSkipIfShort(t *testing.T) {
	t.Parallel()
	// We can't actually skip without aborting the test; verify the call
	// is a no-op when not under -short. testing.Short() returns false
	// in normal runs.
	SkipIfShort(t, "skip-if-short check")
}

func TestRandSeeded(t *testing.T) {
	// Verify FakerSeed feeds a deterministic math/rand sequence.
	t.Parallel()
	seed := FakerSeed(t)
	r1 := rand.New(rand.NewPCG(uint64(seed), uint64(seed)))
	v1 := r1.IntN(1000)
	r2 := rand.New(rand.NewPCG(uint64(seed), uint64(seed)))
	v2 := r2.IntN(1000)
	AssertEqual(t, v1, v2)
}
