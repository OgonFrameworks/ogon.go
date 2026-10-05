// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// jobs — CLI: ogon jobs list/retry/purge/run (JOBS-027/028/029),
// schedule registry snapshot (JOBS-030).
//
// The CLI is a thin wrapper over Queue + DLQDriver + CronRunner.
// It is wired into the framework's CLI dispatcher (Phase 5) by
// the generated `ogon` binary; this file defines the command surface
// so the wire-up is one line per subcommand.

package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// CLI is the dispatcher for `ogon jobs` subcommands. The framework's
// root command tree calls Dispatch with the subcommand name and
// arguments; this type performs the work.
type CLI struct {
	Queue   Queue
	DLQ     DLQDriver
	Cron    *CronRunner
	Metrics *Metrics
	Runner  *SyncRunner
	// RunTimeout caps `ogon jobs run` (the CLI sync run). Default 60s.
	RunTimeout time.Duration
}

// NewCLI constructs a CLI bound to the supplied queue + dlq.
func NewCLI(q Queue, dlq DLQDriver) *CLI {
	return &CLI{Queue: q, DLQ: dlq, RunTimeout: 60 * time.Second}
}

// Dispatch executes a subcommand. args excludes the subcommand name.
// Writes human-readable output to out; returns the error (if any).
func (c *CLI) Dispatch(ctx context.Context, sub string, args []string, out io.Writer) error {
	switch sub {
	case "list":
		return c.cmdList(ctx, args, out)
	case "dlq":
		return c.cmdDLQList(ctx, args, out)
	case "retry":
		return c.cmdDLQRetry(ctx, args, out)
	case "purge":
		return c.cmdDLQPurge(ctx, args, out)
	case "run":
		return c.cmdRun(ctx, args, out)
	case "schedule":
		return c.cmdSchedule(ctx, args, out)
	case "status":
		return c.cmdStatus(ctx, args, out)
	default:
		return fmt.Errorf("jobs: unknown subcommand %q (want: list|dlq|retry|purge|run|schedule|status)", sub)
	}
}

func (c *CLI) cmdList(ctx context.Context, args []string, out io.Writer) error {
	n := 25
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
			n = v
		}
	}
	envs, err := c.Queue.Peek(ctx, n)
	if err != nil {
		return err
	}
	if len(envs) == 0 {
		fmt.Fprintln(out, "queue empty")
		return nil
	}
	for _, e := range envs {
		fmt.Fprintf(out, "%-20s %s attempt=%d prio=%d tenant=%s\n",
			e.Name, e.ID, e.Attempts, e.Priority, e.TenantID)
	}
	return nil
}

func (c *CLI) cmdDLQList(ctx context.Context, args []string, out io.Writer) error {
	if c.DLQ == nil {
		return fmt.Errorf("jobs: no DLQ driver configured")
	}
	n := 25
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
			n = v
		}
	}
	entries, err := c.DLQ.List(ctx, n)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "dlq empty")
		return nil
	}
	for _, e := range entries {
		fmt.Fprintf(out, "%-20s %s attempt=%d reason=%s err=%s\n",
			e.Envelope.Name, e.Envelope.ID, e.Envelope.Attempts, e.Reason, e.Envelope.LastError)
	}
	return nil
}

func (c *CLI) cmdDLQRetry(ctx context.Context, args []string, out io.Writer) error {
	if c.DLQ == nil {
		return fmt.Errorf("jobs: no DLQ driver configured")
	}
	if len(args) == 0 {
		return fmt.Errorf("jobs: retry requires at least one id (or 'all')")
	}
	var ids []string
	if args[0] == "all" {
		entries, err := c.DLQ.List(ctx, 1000)
		if err != nil {
			return err
		}
		for _, e := range entries {
			ids = append(ids, e.Envelope.ID)
		}
	} else {
		ids = args
	}
	n, err := c.DLQ.Replay(ctx, ids)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "replayed %d envelopes\n", n)
	return nil
}

func (c *CLI) cmdDLQPurge(ctx context.Context, args []string, out io.Writer) error {
	if c.DLQ == nil {
		return fmt.Errorf("jobs: no DLQ driver configured")
	}
	ids := args
	n, err := c.DLQ.Purge(ctx, ids)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "purged %d entries\n", n)
	return nil
}

func (c *CLI) cmdRun(ctx context.Context, args []string, out io.Writer) error {
	if c.Runner == nil {
		return fmt.Errorf("jobs: no sync runner configured")
	}
	n := 100
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
			n = v
		}
	}
	to := c.RunTimeout
	if to <= 0 {
		to = 60 * time.Second
	}
	tctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	processed, err := c.Runner.Drain(tctx, n)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "processed %d envelopes\n", processed)
	return nil
}

func (c *CLI) cmdSchedule(ctx context.Context, args []string, out io.Writer) error {
	if c.Cron == nil {
		return fmt.Errorf("jobs: no cron runner configured")
	}
	snaps := c.Cron.Snapshot()
	if len(snaps) == 0 {
		fmt.Fprintln(out, "no schedules")
		return nil
	}
	for _, s := range snaps {
		fmt.Fprintf(out, "%-30s %s tz=%s job=%s\n", s.Name, s.Spec, s.TZ, s.Job)
	}
	return nil
}

func (c *CLI) cmdStatus(ctx context.Context, args []string, out io.Writer) error {
	depth, _ := c.Queue.Depth(ctx)
	snap := map[string]any{
		"depth": depth,
	}
	if c.Metrics != nil {
		snap["metrics"] = c.Metrics.Snap()
	}
	if c.DLQ != nil {
		if d, err := c.DLQ.Len(ctx); err == nil {
			snap["dlq"] = d
		}
	}
	b, _ := json.MarshalIndent(snap, "", "  ")
	_, _ = io.WriteString(out, string(b))
	_, _ = io.WriteString(out, "\n")
	return nil
}

// SplitArgs is a tiny helper that splits "jobs list 25" into
// subcommand="list", args=["25"]. Kept here so the wire-up is one line.
func SplitArgs(input string) (sub string, args []string) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}
