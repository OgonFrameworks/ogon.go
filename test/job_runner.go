// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Job sync runner (TEST-008). Production jobs run async; tests need them to
// complete synchronously so the test can assert on side effects. The runner
// is a queue + drain loop: Enqueue captures a job, Drain runs all queued
// jobs in the order they were enqueued.

package test

import (
	"context"
	"errors"
	"sync"
)

// Job is a single unit of work. Tests pass these to Enqueue.
type Job struct {
	Name string
	Run  func(ctx context.Context) error
}

// JobRunner is the synchronous job executor. Drain is idempotent; calling
// it again returns the first error encountered during execution.
type JobRunner struct {
	mu   sync.Mutex
	jobs []Job
	err  error
	rand int // monotonic counter, used to assert ordering
}

// NewJobRunner constructs an empty runner.
func NewJobRunner() *JobRunner { return &JobRunner{} }

// Enqueue adds a job. Returns the runner so calls compose:
//
//	runner.Enqueue(job).Enqueue(other).Drain(ctx)
func (r *JobRunner) Enqueue(j Job) *JobRunner {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r
	}
	if j.Name == "" {
		j.Name = "unnamed"
	}
	r.jobs = append(r.jobs, j)
	return r
}

// Drain runs every queued job in order. The first error returned is
// recorded; subsequent jobs are still run so that side-effects settle.
func (r *JobRunner) Drain(ctx context.Context) error {
	r.mu.Lock()
	if r.err != nil {
		err := r.err
		r.mu.Unlock()
		return err
	}
	jobs := r.jobs
	r.jobs = nil
	r.mu.Unlock()

	for _, j := range jobs {
		if err := j.Run(ctx); err != nil && r.err == nil {
			r.err = err
		}
	}
	return r.err
}

// Reset clears the queue and any recorded error. Useful when a runner is
// reused across subtests.
func (r *JobRunner) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs = nil
	r.err = nil
}

// Pending returns the number of jobs queued but not yet drained.
func (r *JobRunner) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs)
}

// Err returns the recorded error, if any.
func (r *JobRunner) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// ErrJobFailed is returned when a job returned an error during Drain.
var ErrJobFailed = errors.New("ogontest: job failed")
