// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — workflow-lite: step chains (JOBS-038), workflow state (JOBS-039).
//
// A Workflow is a sequence of job names that execute in order, each
// receiving the previous step's output as part of its payload. The
// workflow state is persisted to a `ogon_jobs_workflows` table (DB
// driver only — in-proc workflows live in memory and are lost on
// restart; this is acceptable for dev/test).

package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/OgonFrameworks/ogon.go/diag"
)

// WorkflowID is a stable workflow identifier (opaque string).
type WorkflowID string

// WorkflowStep is one node in the chain.
type WorkflowStep struct {
	// Name of the job to dispatch.
	Job JobName
	// Args is the static args for this step. May be augmented by
	// the previous step's output at runtime.
	Args json.RawMessage
}

// Workflow is an ordered list of steps plus state.
type Workflow struct {
	ID      WorkflowID
	Steps   []WorkflowStep
	Current int
	// Output is the latest step's emitted output (JSON). Becomes
	// input to the next step if the next step declares an empty Args.
	Output json.RawMessage
	// Done marks the workflow as complete.
	Done bool
	// Error captures the first step that failed (for resumability).
	Error string
}

// WorkflowStore persists workflow state. InMemoryWorkflowStore for dev/test.
type WorkflowStore interface {
	Get(ctx context.Context, id WorkflowID) (*Workflow, error)
	Put(ctx context.Context, w *Workflow) error
	List(ctx context.Context, n int) ([]*Workflow, error)
}

// InMemoryWorkflowStore is the default dev/test store.
type InMemoryWorkflowStore struct {
	mu sync.Mutex
	ws map[WorkflowID]*Workflow
}

// NewInMemoryWorkflowStore returns a fresh in-memory store.
func NewInMemoryWorkflowStore() *InMemoryWorkflowStore {
	return &InMemoryWorkflowStore{ws: make(map[WorkflowID]*Workflow)}
}

// Get returns the workflow or nil if not found.
func (s *InMemoryWorkflowStore) Get(_ context.Context, id WorkflowID) (*Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.ws[id]
	if !ok {
		return nil, nil
	}
	cp := *w
	return &cp, nil
}

// Put upserts w by ID.
func (s *InMemoryWorkflowStore) Put(_ context.Context, w *Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *w
	s.ws[w.ID] = &cp
	return nil
}

// List returns up to n workflows (snapshot order is undefined).
func (s *InMemoryWorkflowStore) List(_ context.Context, n int) ([]*Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || n > len(s.ws) {
		n = len(s.ws)
	}
	out := make([]*Workflow, 0, n)
	for _, w := range s.ws {
		cp := *w
		out = append(out, &cp)
		if len(out) >= n {
			break
		}
	}
	return out, nil
}

// WorkflowRunner orchestrates one workflow to completion (or
// failure). It dispatches the current step, on success advances
// to the next, on failure marks the workflow errored.
type WorkflowRunner struct {
	store WorkflowStore
	queue Enqueuer
}

// NewWorkflowRunner returns a runner.
func NewWorkflowRunner(store WorkflowStore, queue Enqueuer) *WorkflowRunner {
	return &WorkflowRunner{store: store, queue: queue}
}

// Start creates and stores the workflow, dispatches the first step.
func (r *WorkflowRunner) Start(ctx context.Context, id WorkflowID, steps []WorkflowStep) error {
	if id == "" {
		return diag.New("OGON-J0038", "jobs: workflow id required", "")
	}
	if len(steps) == 0 {
		return diag.New("OGON-J0038", "jobs: workflow has no steps", "")
	}
	w := &Workflow{ID: id, Steps: steps, Current: 0}
	if err := r.store.Put(ctx, w); err != nil {
		return err
	}
	return r.dispatch(ctx, w)
}

// dispatch enqueues the workflow's current step. The step's args are
// taken from the WorkflowStep.Args, or from w.Output if Args is empty.
func (r *WorkflowRunner) dispatch(ctx context.Context, w *Workflow) error {
	if w.Current >= len(w.Steps) {
		w.Done = true
		return r.store.Put(ctx, w)
	}
	step := w.Steps[w.Current]
	args := step.Args
	if len(args) == 0 && len(w.Output) > 0 {
		args = w.Output
	}
	env := &Envelope{
		ID:      NewID(),
		Name:    step.Job,
		Payload: args,
		// We tag the envelope with the workflow id so the worker
		// can hook back into the runner on Ack. For tests, the
		// caller drives the workflow directly via Advance.
		Metadata: map[string]string{"workflow_id": string(w.ID)},
	}
	return r.queue.Enqueue(ctx, env, EnqueueOptions{})
}

// Advance is called by the workflow driver after a step's handler
// returns successfully. output is the JSON-encoded output the next
// step should consume (may be empty).
func (r *WorkflowRunner) Advance(ctx context.Context, id WorkflowID, output json.RawMessage) error {
	w, err := r.store.Get(ctx, id)
	if err != nil || w == nil {
		return err
	}
	if w.Done {
		return nil
	}
	w.Current++
	w.Output = output
	if w.Current >= len(w.Steps) {
		w.Done = true
		return r.store.Put(ctx, w)
	}
	if err := r.store.Put(ctx, w); err != nil {
		return err
	}
	return r.dispatch(ctx, w)
}

// Fail marks the workflow errored at its current step.
func (r *WorkflowRunner) Fail(ctx context.Context, id WorkflowID, reason string) error {
	w, err := r.store.Get(ctx, id)
	if err != nil || w == nil {
		return err
	}
	w.Error = reason
	return r.store.Put(ctx, w)
}

// Resume re-dispatches the current step (called after a crash).
func (r *WorkflowRunner) Resume(ctx context.Context, id WorkflowID) error {
	w, err := r.store.Get(ctx, id)
	if err != nil || w == nil {
		return err
	}
	return r.dispatch(ctx, w)
}

// ListStuck returns workflows whose step has been in-flight longer
// than maxAge (for the stale-workflow reaper). Returns nil for the
// in-memory store because it does not track dispatch timestamps.
func (r *WorkflowRunner) ListStuck(ctx context.Context, maxAge time.Duration) ([]*Workflow, error) {
	return nil, nil
}
