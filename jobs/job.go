// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — core types: Job, Handler, Args, Envelope (JOBS-001/002/023).
//
// A Job is a unit of deferred work identified by a stable string name.
// Arguments are typed via generics so the worker dispatch layer can
// marshal/unmarshal JSON payloads without reflection on the call site.
// An Envelope carries the runtime metadata (id, attempts, payload,
// idempotency key, tenant) and is what drivers persist and workers
// dispatch to the registered Handler.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/google/uuid"
)

// JobName uniquely identifies a job type. Stable forever; renaming a job
// orphanates its queued envelopes. Convention: snake_case, e.g.
// "welcome_email", "invoice.reconcile".
type JobName string

// Args is the marker interface every typed argument struct satisfies.
// Implementations MUST be JSON-marshallable. The empty-method form lets
// generic handlers constrain Args to user-defined structs only.
// Args is JSON-encoded into Envelope.Payload on enqueue and decoded
// before dispatch (JOBS-023 typed args codegen).
type Args interface {
	// jobsArgs is intentionally unexported: only types declared in the
	// user's app package can satisfy it via embedding jobs.ArgsBase, so
	// we get nominal typing for free.
	jobsArgs()
}

// ArgsBase is the recommended embed for every generated/handwritten
// args struct. It carries no fields; it exists only to seal Args.
//
//	type WelcomeEmailArgs struct {
//	    jobs.ArgsBase
//	    UserID string `json:"user_id"`
//	}
type ArgsBase struct{}

func (ArgsBase) jobsArgs() {}

// Handler is the function the worker calls for one envelope. The
// supplied Args is the decoded payload. Implementations MUST be safe
// for concurrent use by N workers (one process, many goroutines).
//
// Invariants:
//   - Returning a non-nil error schedules a retry (subject to max attempts).
//   - Returning ErrPoison quarantines the envelope (JOBS-025).
//   - Returning ErrFatal aborts retries immediately (DLQ-direct).
//   - Panic is recovered and converted to a redacted error (JOBS-047).
//   - Handlers MUST observe ctx for cancellation/shutdown.
type Handler[A Args] func(ctx context.Context, args A) error

// HandlerFunc is the type-erased dispatch form used by workers after
// resolving the envelope's job name to its handler. The dispatcher
// receives the full Envelope (so middleware can stamp the stack on
// it). Decode-from-payload is done inside the closure (e.g. via
// DecodeDispatcher) so the typed Handler[A] surface is preserved.
type HandlerFunc func(ctx context.Context, env *Envelope) error

// Envelope is the wire/runtime representation of a queued job.
// Drivers persist a subset of these fields; the in-memory form may
// carry derived state (e.g. NextAttempt computed by retry.go).
type Envelope struct {
	// ID is the unique envelope id. Generated on Enqueue if empty.
	ID string `json:"id"`
	// Name is the JobName the worker dispatches to.
	Name JobName `json:"name"`
	// Payload is the JSON-encoded Args.
	Payload json.RawMessage `json:"payload"`
	// Attempts is the 1-based count of dispatches that have occurred
	// (including the in-flight one). 0 means "never dispatched yet".
	Attempts int `json:"attempts"`
	// MaxAttempts caps retries. Once Attempts > MaxAttempts the
	// envelope is moved to the DLQ (JOBS-008). 0 = use queue default.
	MaxAttempts int `json:"max_attempts,omitempty"`
	// Priority is 0 (lowest) .. 9 (highest). Drivers SHOULD dequeue
	// higher priority first (JOBS-018).
	Priority int `json:"priority,omitempty"`
	// IdempotencyKey deduplicates enqueues within the configured
	// window (JOBS-013/014). Empty disables dedup.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// TenantID carries the originating tenant for RLS-aware dispatch
	// (JOBS-044/045). Empty means non-tenant-scoped (system job).
	TenantID string `json:"tenant_id,omitempty"`
	// EnqueuedAt is when the envelope was first queued (UTC).
	EnqueuedAt time.Time `json:"enqueued_at"`
	// VisibleAt is the earliest time the envelope may be dequeued.
	// Used to backoff retries; for a fresh enqueue this equals EnqueuedAt.
	VisibleAt time.Time `json:"visible_at"`
	// LastError is the most recent failure's error string (if any).
	LastError string `json:"last_error,omitempty"`
	// Stack is a redacted stack trace captured from the most recent
	// panic (JOBS-047). Empty for non-panic failures.
	Stack string `json:"stack,omitempty"`
	// Metadata is opaque, driver-specific data (e.g. Redis stream id,
	// DB rowid). Not marshalled across drivers.
	Metadata map[string]string `json:"-"`
}

// NewID returns a fresh ULID-like id. We use UUIDv4 (already a dep)
// for simplicity; the spec permits any opaque unique string.
func NewID() string {
	return uuid.NewString()
}

// EnqueueOptions tunes per-call enqueue behaviour. Zero values fall
// back to queue defaults (set by config.go).
type EnqueueOptions struct {
	// Priority overrides queue default (0..9).
	Priority int
	// IdempotencyKey deduplicates (JOBS-013).
	IdempotencyKey string
	// MaxAttempts overrides queue default.
	MaxAttempts int
	// VisibleAt sets a delayed-dispatch time (UTC). Zero = now.
	VisibleAt time.Time
	// TenantID stamps tenant scoping (JOBS-044).
	TenantID string
}

// MarshalPayload JSON-encodes args. Convenience used by enqueue helpers.
func MarshalPayload(args Args) (json.RawMessage, error) {
	b, err := json.Marshal(args)
	if err != nil {
		return nil, diag.Wrap(err, diag.Diag{
			Code:  "OGON-J0010",
			Title: "jobs: encode payload",
			What:  err.Error(),
		})
	}
	return b, nil
}

// UnmarshalPayload decodes raw into out (a *A). Errors are wrapped as
// diag so the worker reports a structured error path (JOBS-047).
func UnmarshalPayload[A Args](raw json.RawMessage) (A, error) {
	var zero A
	var a A
	if err := json.Unmarshal(raw, &a); err != nil {
		return zero, diag.Wrap(err, diag.Diag{
			Code:  "OGON-J0011",
			Title: "jobs: decode payload",
			What:  err.Error(),
		})
	}
	return a, nil
}

// MarshalEnv is a thin wrapper used by helpers that need to encode an
// Args struct before enqueue. Convenience for callers that don't have
// a direct json.Marshal handy.
func MarshalEnv[A Args](a A) (json.RawMessage, error) { return MarshalPayload(a) }

// ErrFatal aborts retries immediately and routes to DLQ.
var ErrFatal = errors.New("jobs: fatal, no retry")

// ErrPoison quarantines the envelope (JOBS-025).
var ErrPoison = errors.New("jobs: poison message")

// ErrEmpty is returned by Dequeue when no envelope is available.
var ErrEmpty = errors.New("jobs: queue empty")

// ErrAlreadyExists is returned by Enqueue when an idempotency key
// collides within the dedup window (JOBS-013).
var ErrAlreadyExists = errors.New("jobs: idempotency key in flight")
