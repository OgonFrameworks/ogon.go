// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — queue config in ogon.yaml (JOBS-041), per-env concurrency
// defaults (JOBS-042).
//
// The config is a subset of ogon.yaml under `jobs:`:
//
//	jobs:
//	  default_concurrency: 8
//	  default_visibility: 30s
//	  retry:
//	    base: 1s
//	    mult: 2.0
//	    max: 5m
//	    jitter: 1.0
//	    max_attempts: 5
//	  queues:
//	    default:
//	      driver: db            # db | redis | inproc
//	      concurrency: 8
//	      visibility: 30s
//	    mailers:
//	      driver: redis
//	      concurrency: 4
//	      visibility: 60s
//	      max_attempts: 10
//	  cron:
//	    leader_election: pg_advisory
//	    schedules:
//	      - name: nightly_reconcile
//	        spec: "0 2 * * *"
//	        tz: UTC
//	        job: invoice.reconcile

package jobs

import (
	"time"
)

// Config is the top-level jobs config block.
type Config struct {
	DefaultConcurrency int                    `yaml:"default_concurrency,omitempty"`
	DefaultVisibility  time.Duration          `yaml:"default_visibility,omitempty"`
	PerEnvConcurrency  map[string]int         `yaml:"per_env_concurrency,omitempty"`
	Retry              RetryPolicy            `yaml:"retry,omitempty"`
	Queues             map[string]QueueConfig `yaml:"queues,omitempty"`
	Cron               CronConfig             `yaml:"cron,omitempty"`
}

// QueueConfig configures one queue.
type QueueConfig struct {
	Driver         string          `yaml:"driver"` // db | redis | inproc
	Concurrency    int             `yaml:"concurrency,omitempty"`
	Visibility     time.Duration   `yaml:"visibility,omitempty"`
	MaxAttempts    int             `yaml:"max_attempts,omitempty"`
	PriorityLevels int             `yaml:"priority_levels,omitempty"`
	RateLimit      RateLimitConfig `yaml:"rate_limit,omitempty"`
}

// RateLimitConfig per-queue (JOBS-019).
type RateLimitConfig struct {
	Max         int           `yaml:"max,omitempty"`
	RefillEvery time.Duration `yaml:"refill_every,omitempty"`
}

// CronConfig is the cron block.
type CronConfig struct {
	LeaderElection string         `yaml:"leader_election,omitempty"` // pg_advisory | none
	Schedules      []CronSchedule `yaml:"schedules,omitempty"`
}

// CronSchedule is one persisted schedule (JOBS-011).
type CronSchedule struct {
	Name string `yaml:"name"`
	Spec string `yaml:"spec"`
	TZ   string `yaml:"tz,omitempty"`
	Job  string `yaml:"job"`
	Args any    `yaml:"args,omitempty"`
}

// DefaultConfig returns the production defaults.
func DefaultConfig() Config {
	return Config{
		DefaultConcurrency: 8,
		DefaultVisibility:  30 * time.Second,
		PerEnvConcurrency: map[string]int{
			"dev":  2,
			"test": 1,
			"prod": 8,
		},
		Retry: DefaultRetryPolicy,
		Queues: map[string]QueueConfig{
			"default": {
				Driver:      "db",
				Concurrency: 8,
				Visibility:  30 * time.Second,
			},
		},
		Cron: CronConfig{LeaderElection: "pg_advisory"},
	}
}

// ConcurrencyForEnv returns the concurrency for the supplied env,
// falling back to DefaultConcurrency when unset.
func (c Config) ConcurrencyForEnv(env string) int {
	if env != "" {
		if v, ok := c.PerEnvConcurrency[env]; ok && v > 0 {
			return v
		}
	}
	if c.DefaultConcurrency > 0 {
		return c.DefaultConcurrency
	}
	return 8
}

// QueueOrDefault returns the queue config for name or the default
// queue config when name is missing.
func (c Config) QueueOrDefault(name string) QueueConfig {
	if q, ok := c.Queues[name]; ok {
		return q
	}
	if q, ok := c.Queues["default"]; ok {
		return q
	}
	return QueueConfig{
		Driver:      "db",
		Concurrency: 8,
		Visibility:  30 * time.Second,
	}
}
