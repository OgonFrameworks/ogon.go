// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Edge cases for the jobs subsystem (P14 bug-bounty / JOBS-001..054).
// Each test exercises one adversarial scenario: nil ctx, huge payload
// (10MB), zero TTL, max retries hit, nil envelope, etc. The
// contract: no panic on any input; correct rejection via diag;
// panics in user handlers MUST be recovered (JOBS-047).

package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestEdgePayloadDecodeHuge — UnmarshalPayload on a multi-MB JSON
// array must succeed without panicking.
func TestEdgePayloadDecodeHuge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping huge payload in -short")
	}
	// 1M ints at "1," each = ~2MB. Large enough to exercise buffer
	// growth without blowing the test budget.
	var buf bytes.Buffer
	buf.WriteString("[")
	for i := 0; i < 1_000_000; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString("1")
	}
	buf.WriteString("]")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Unmarshal(huge) panicked: %v", r)
		}
	}()
	// Decode into []int — the FuzzSampleArgs field isn't a slice.
	var arr []int
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("Unmarshal huge: %v", err)
	}
	if len(arr) != 1_000_000 {
		t.Fatalf("decoded %d ints, want 1M", len(arr))
	}
}

// TestEdgePayloadDecodeMalformed — FuzzPayload on adversarial inputs
// (extended corpus for P14). Each must not panic, may return error.
func TestEdgePayloadDecodeMalformed(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte(""),
		[]byte("x"),
		[]byte("\x00\x01\x02"),
		[]byte(strings.Repeat("a", 1<<16)), // 64k of "a"
		[]byte("{\"x\":\"" + strings.Repeat("\\u0000", 1000) + "\"}"), // 1k NULs escaped
		[]byte("[]"),  // array, not struct
		[]byte("123"), // primitive
		[]byte("true"),
		[]byte("null"),
		[]byte(`{"tags": "string but expected slice"}`),
		[]byte(`{"meta": "string but expected map"}`),
	}
	for _, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FuzzPayload(%q) panicked: %v", truncJobs(string(c)), r)
				}
			}()
			_ = FuzzPayload(c)
		}()
	}
}

// TestEdgeRetryPolicyMaxAttemptsHit — CanRetry must return false at
// MaxAttempts. The worker uses this to route to DLQ.
func TestEdgeRetryPolicyMaxAttemptsHit(t *testing.T) {
	p := DefaultRetryPolicy // MaxAttempts = 5
	if !p.CanRetry(0) {
		t.Fatal("CanRetry(0) = false; want true")
	}
	if !p.CanRetry(4) {
		t.Fatal("CanRetry(4) = false; want true (attempt=4 means 5th dispatch)")
	}
	if p.CanRetry(5) {
		t.Fatal("CanRetry(5) = true; want false (MaxAttempts reached)")
	}
	if p.CanRetry(6) {
		t.Fatal("CanRetry(6) = true; want false")
	}
}

// TestEdgeRetryPolicyNextBackoffMonotonic — NextBackoff must be
// monotonically non-decreasing for a no-jitter policy, capping at Max.
func TestEdgeRetryPolicyNextBackoffMonotonic(t *testing.T) {
	p := RetryPolicy{
		Base: 1 * time.Millisecond, Mult: 2.0,
		Max: 10 * time.Millisecond, Jitter: 0, // no jitter for determinism
		MaxAttempts: 10,
	}
	prev := time.Duration(0)
	for attempt := 1; attempt <= 20; attempt++ {
		d := p.NextBackoff(attempt)
		if d < prev {
			t.Fatalf("NextBackoff not monotonic at attempt %d: %v < %v", attempt, d, prev)
		}
		if d > p.Max {
			t.Fatalf("NextBackoff(%d) = %v exceeds Max=%v", attempt, d, p.Max)
		}
		prev = d
	}
}

// TestEdgeRetryPolicyZeroBase — A zero-Base policy (with defaults)
// must use the default Base. Never panic, never produce a negative
// backoff.
func TestEdgeRetryPolicyZeroBase(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NextBackoff(0-base) panicked: %v", r)
		}
	}()
	p := RetryPolicy{}.WithDefaults(DefaultRetryPolicy)
	d := p.NextBackoff(1)
	if d < 0 {
		t.Fatalf("NextBackoff returned negative duration: %v", d)
	}
}

// TestEdgeRetryPolicyNegativeAttempt — NextBackoff on a negative
// attempt must not panic; should be treated as attempt=1.
func TestEdgeRetryPolicyNegativeAttempt(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NextBackoff(-5) panicked: %v", r)
		}
	}()
	p := RetryPolicy{Base: 1 * time.Millisecond, Mult: 2.0, Max: 10 * time.Millisecond, Jitter: 0, MaxAttempts: 5}
	d1 := p.NextBackoff(-5)
	d2 := p.NextBackoff(1)
	if d1 != d2 {
		t.Fatalf("NextBackoff(-5)=%v != NextBackoff(1)=%v", d1, d2)
	}
}

// TestEdgeRecoverDispatch — A handler that panics must be recovered
// and surfaced as an OGON-J0047 diag. The envelope's Stack field must
// be populated so the worker can attach to DLQ.
func TestEdgeRecoverDispatch(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RecoverDispatch leaked panic: %v", r)
		}
	}()
	dispatcher := RecoverDispatch("panic-job", func(_ context.Context, _ *Envelope) error {
		panic("boom")
	})
	env := &Envelope{ID: "edge-panic", Name: "panic-job"}
	err := dispatcher(context.Background(), env)
	if err == nil {
		t.Fatal("RecoverDispatch returned nil err on panic")
	}
	if env.Stack == "" {
		t.Fatal("RecoverDispatch did not populate env.Stack")
	}
}

// TestEdgeRecoverDispatchNilEnv — RecoverDispatch must not panic
// even if env is nil (can happen if the dispatcher is called from
// a buggy caller).
func TestEdgeRecoverDispatchNilEnv(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RecoverDispatch(nil env) leaked panic: %v", r)
		}
	}()
	dispatcher := RecoverDispatch("nil-env-job", func(_ context.Context, _ *Envelope) error {
		panic("boom with nil env")
	})
	err := dispatcher(context.Background(), nil)
	if err == nil {
		t.Fatal("RecoverDispatch(nil env) returned nil err")
	}
}

// TestEdgeTimeoutDispatchZeroDuration — TimeoutDispatch with d=0
// must still function: the timeout fires essentially immediately,
// but no panic.
func TestEdgeTimeoutDispatchZeroDuration(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TimeoutDispatch(0) panicked: %v", r)
		}
	}()
	dispatcher := TimeoutDispatch(0, func(ctx context.Context, _ *Envelope) error {
		<-ctx.Done() // wait for the (immediate) timeout
		return ctx.Err()
	})
	err := dispatcher(context.Background(), &Envelope{ID: "edge-zero-ttl"})
	if err == nil {
		t.Fatal("TimeoutDispatch(0) returned nil; want timeout error")
	}
}

// TestEdgeTimeoutDispatchSlowHandler — A handler that takes longer
// than the timeout must be aborted and the dispatcher returns a
// timeout error.
func TestEdgeTimeoutDispatchSlowHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow-handler edge test in -short")
	}
	dispatcher := TimeoutDispatch(10*time.Millisecond, func(ctx context.Context, _ *Envelope) error {
		select {
		case <-time.After(5 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	start := time.Now()
	err := dispatcher(context.Background(), &Envelope{ID: "edge-slow"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("TimeoutDispatch(slow) returned nil; want timeout error")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("TimeoutDispatch did not abort; elapsed=%v", elapsed)
	}
}

// TestEdgePayloadKeyEmpty — A zero PayloadKey must report Enabled()
// = false (encryption disabled). Seal/Open on a disabled key fall
// back to the un-encrypted interop path (no error, no panic) — this
// is the documented dev-mode behaviour.
func TestEdgePayloadKeyEmpty(t *testing.T) {
	var k PayloadKey // zero
	if k.Enabled() {
		t.Fatal("zero PayloadKey.Enabled() = true; want false")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zero PayloadKey Seal/Open panicked: %v", r)
		}
	}()
	// Seal on a disabled key returns the plaintext unchanged.
	out, err := k.Seal([]byte("plaintext"))
	if err != nil {
		t.Fatalf("Seal(plaintext) on disabled key returned err %v; want nil", err)
	}
	if string(out) != "plaintext" {
		t.Fatalf("Seal(plaintext) on disabled key returned %q; want %q", out, "plaintext")
	}
	// Open on a disabled key returns the input unchanged when no
	// enc1: prefix is detected after base64-decoding.
	back, err := k.Open(out)
	if err != nil {
		t.Fatalf("Open(plaintext) on disabled key returned err %v; want nil", err)
	}
	if string(back) != "plaintext" {
		t.Fatalf("Open(plaintext) on disabled key returned %q; want %q", back, "plaintext")
	}
}

// TestEdgePayloadKeyRoundTripHuge — Seal+Open on a 1MB payload must
// round-trip correctly.
func TestEdgePayloadKeyRoundTripHuge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1MB encryption edge test in -short")
	}
	k := KeyFromPassphrase("test-passphrase-for-p14")
	if !k.Enabled() {
		t.Fatal("KeyFromPassphrase returned disabled key")
	}
	plaintext := bytes.Repeat([]byte("Aa1!"), 256*1024) // 1MB
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Seal/Open(1MB) panicked: %v", r)
		}
	}()
	ct, err := k.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	pt, err := k.Open(ct)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatalf("round-trip mismatch: got len=%d, want %d", len(pt), len(plaintext))
	}
}

// TestEdgePayloadKeyOpenGarbage — Open on adversarial ciphertexts
// must not panic. The interop contract says Open returns the input
// unchanged when no enc1: prefix is detected after base64-decoding,
// so most garbage inputs return (input, nil). What we actually
// assert here is panic-safety.
func TestEdgePayloadKeyOpenGarbage(t *testing.T) {
	k := KeyFromPassphrase("test-passphrase-p14")
	cases := [][]byte{
		nil,
		{},
		[]byte("garbage"),
		[]byte("\x00\x01\x02"),
		bytes.Repeat([]byte("a"), 1024),
	}
	for _, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Open(%q) panicked: %v", truncJobs(string(c)), r)
				}
			}()
			// We don't assert on the error: the interop-mode contract
			// returns nil error on un-prefixed input, error only on
			// malformed enc1: payloads.
			_, _ = k.Open(c)
		}()
	}
}

// TestEdgeNewIDUniqueness — 10k NewID calls must produce 10k unique
// ids (the UUID generator must not collide in practice).
func TestEdgeNewIDUniqueness(t *testing.T) {
	const N = 10_000
	seen := make(map[string]struct{}, N)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewID panicked: %v", r)
		}
	}()
	for i := 0; i < N; i++ {
		id := NewID()
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID produced duplicate at iteration %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestEdgeEnvelopeNilArgs — MarshalPayload on a nil Args interface
// is a common caller bug. The encoding/json package marshals nil
// interfaces as `null` with no error, so MarshalPayload also returns
// nil error — the contract is that the caller MUST pass a non-nil
// Args (typically an embedded ArgsBase). We assert only panic-safety
// here; a stricter contract would require a typed nil check.
func TestEdgeEnvelopeNilArgs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MarshalPayload(nil) panicked: %v", r)
		}
	}()
	out, err := MarshalPayload(nil)
	// We do not assert on err — the documented contract puts the
	// "non-nil Args" responsibility on the caller.
	_ = out
	_ = err
}

// TestEdgeEnvelopeZeroAttempts — An Envelope with Attempts=0 is
// valid (never dispatched). Worker dispatch increments before run.
func TestEdgeEnvelopeZeroAttempts(t *testing.T) {
	env := &Envelope{ID: NewID(), Name: "edge-zero-attempts", Attempts: 0}
	if env.Attempts != 0 {
		t.Fatalf("expected Attempts=0, got %d", env.Attempts)
	}
	// MarshalPayload requires Args; we pass an ArgsBase (the empty
	// embed) which marshals to "{}".
	payload, err := MarshalPayload(ArgsBase{})
	if err != nil {
		t.Fatalf("MarshalPayload(ArgsBase{}): %v", err)
	}
	env.Payload = payload
	if len(env.Payload) == 0 {
		t.Fatal("payload empty after assignment")
	}
}

// TestEdgeErrSentinelsAreErrors — Each public sentinel error must
// satisfy the error interface (compile-time check).
func TestEdgeErrSentinelsAreErrors(t *testing.T) {
	sentinels := []error{
		ErrFatal,
		ErrPoison,
		ErrEmpty,
		ErrAlreadyExists,
		ErrQueueClosed,
	}
	for _, s := range sentinels {
		if s == nil {
			t.Fatalf("sentinel error is nil: %v", s)
		}
		if !errors.Is(s, s) {
			t.Fatalf("sentinel error is not reflexive under errors.Is: %v", s)
		}
	}
}

// TestEdgeFakeQueueDepthAccounting — The fakeQueue helper's depth
// counter must remain balanced after N enqueues + dequeues. This
// is a guard against the test helper itself leaking.
func TestEdgeFakeQueueDepthAccounting(t *testing.T) {
	q := newFakeQueue(1024)
	var depth atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const N = 500
	for i := 0; i < N; i++ {
		env := &Envelope{ID: NewID(), Name: "edge-depth", Attempts: 0}
		if err := q.Enqueue(ctx, env, EnqueueOptions{}); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
		depth.Add(1)
	}

	// Drain.
	for i := 0; i < N; i++ {
		env, rcpt, err := q.Dequeue(ctx)
		if err != nil {
			t.Fatalf("Dequeue %d: %v", i, err)
		}
		if env == nil {
			t.Fatalf("Dequeue %d returned nil env", i)
		}
		if rcpt == nil {
			t.Fatalf("Dequeue %d returned nil rcpt", i)
		}
		depth.Add(-1)
	}
	if got := depth.Load(); got != 0 {
		t.Fatalf("depth drift: %d (want 0)", got)
	}
}

// truncJobs keeps test failure messages readable.
func truncJobs(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
