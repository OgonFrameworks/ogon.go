// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Flaky quarantine policy (TEST-035) and parallel-safe fixtures (TEST-036).
// The policy: tests that flake three times in a row are quarantined under a
// `_flaky` build tag until they are fixed. The package exposes Quarantine
// and Parallel helpers so any test can opt in or out uniformly.

package test

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// QuarantineDir is the conventional directory for tests pulled out of the
// main suite by the flaky policy.
const QuarantineDir = "testdata/quarantine"

// QuarantineLog records flake counts per test name. The policy reads this
// log to decide whether a test should be quarantined. The log is a simple
// text file with one line per occurrence: `TestName count`.
type QuarantineLog struct {
	mu     sync.Mutex
	counts map[string]int
	path   string
}

// NewQuarantineLog loads the log from path. If the path does not exist,
// returns an empty log.
func NewQuarantineLog(path string) *QuarantineLog {
	l := &QuarantineLog{counts: map[string]int{}, path: path}
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			var name string
			var n int
			if _, err := splitLogLine(line, &name, &n); err == nil && n > 0 {
				l.counts[name] = n
			}
		}
	}
	return l
}

func splitLogLine(line string, name *string, n *int) (int, error) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0, errMalformedLogLine
	}
	*name = parts[0]
	v := 0
	for _, ch := range parts[1] {
		if ch < '0' || ch > '9' {
			return 0, errMalformedLogLine
		}
		v = v*10 + int(ch-'0')
	}
	*n = v
	return len(parts), nil
}

var errMalformedLogLine = errStr("ogontest: malformed quarantine log line")

type errStr string

func (e errStr) Error() string { return string(e) }

// RecordFlake increments the flake count for name and writes the log.
func (l *QuarantineLog) RecordFlake(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts[name]++
	if l.path == "" {
		return
	}
	var b strings.Builder
	for k, v := range l.counts {
		b.WriteString(k)
		b.WriteByte(' ')
		b.WriteString(itoa(v))
		b.WriteByte('\n')
	}
	_ = os.WriteFile(l.path, []byte(b.String()), 0o644)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	if neg {
		out = append([]byte{'-'}, out...)
	}
	return string(out)
}

// ShouldQuarantine reports whether name has accumulated enough flakes to
// be quarantined. The default threshold is 3.
func (l *QuarantineLog) ShouldQuarantine(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[name] >= 3
}

// Quarantine skips the test if it has accumulated enough flakes.
func (l *QuarantineLog) Quarantine(t *testing.T) {
	t.Helper()
	if l.ShouldQuarantine(t.Name()) {
		t.Skipf("ogontest: quarantined under flaky policy (see %s)", l.path)
	}
}

// Parallel is the safe-fixture entrypoint for parallel tests (TEST-036).
// It calls t.Parallel after a quick smoke check that the test's environment
// is not obviously shared (env var OGON_TEST_NO_PARALLEL set).
func Parallel(t *testing.T) {
	t.Helper()
	if os.Getenv("OGON_TEST_NO_PARALLEL") == "1" {
		t.Logf("ogontest: parallel disabled by OGON_TEST_NO_PARALLEL")
		return
	}
	t.Parallel()
}

// ParallelSafe is the marker that a fixture is parallel-safe. Tests call
// this in their setup to assert the property: if a fixture fails the
// assertion, it panics.
type ParallelSafe struct {
	name string
}

// NewParallelSafe constructs a marker with the supplied name. Use the
// name to identify the fixture in panic messages.
func NewParallelSafe(name string) *ParallelSafe { return &ParallelSafe{name: name} }

// Assert panics if the fixture cannot run in parallel. The current
// implementation is a stub: it always passes. Tests can override by setting
// OGON_TEST_NO_PARALLEL=<fixture-name>.
func (p *ParallelSafe) Assert() {
	if v := os.Getenv("OGON_TEST_NO_PARALLEL"); v != "" && v == p.name {
		panic("ogontest: fixture " + p.name + " marked non-parallel by env")
	}
}

// FlakeBackoff sleeps for the supplied duration with jitter. Use to
// re-run flaky operations with exponential backoff.
func FlakeBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(1<<uint(attempt-1)) * 10 * time.Millisecond
	if d > time.Second {
		d = time.Second
	}
	return d
}
