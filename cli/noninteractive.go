// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Non-interactive mode. CLI-050: interactive prompts must never block in
// non-TTY or --yes mode. This file centralizes prompt behavior so every
// mutating command honors the same contract.

package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Confirm asks a yes/no question and returns the answer. Behavior:
//   - --yes mode: returns def immediately, never blocks (CLI-050).
//   - non-TTY stdin: returns def immediately, never blocks (CLI-050).
//   - TTY stdin without --yes: reads one line; empty Enter ⇒ def.
//
// The prompt is written to stdout (so JSON consumers see nothing; prompts
// are a human affordance). In JSON mode, Confirm always returns def without
// prompting — JSON runs are inherently non-interactive.
func (c *CLI) Confirm(prompt string, def bool) bool {
	// JSON mode is non-interactive by definition.
	if c.json {
		return def
	}
	if c.yes {
		return def
	}
	if !c.stdinIsTTY {
		return def
	}
	label := "[y/N]"
	if def {
		label = "[Y/n]"
	}
	fmt.Fprintf(c.stdout, "%s %s ", prompt, label)
	reader := bufio.NewReader(c.stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return def
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return def
	}
	return line[0] == 'y' || line[0] == '1' || line == "true"
}

// ConfirmDestructive is Confirm with a louder prompt for irreversible
// actions (e.g. migration DROP). The def defaults to false so a stray
// Enter never destroys data.
func (c *CLI) ConfirmDestructive(prompt string) bool {
	return c.Confirm(prompt+" — type y to proceed", false)
}

// PromptString asks for a free-text value. In --yes / non-TTY / JSON mode it
// returns def immediately without blocking.
func (c *CLI) PromptString(prompt, def string) string {
	if c.json || c.yes || !c.stdinIsTTY {
		return def
	}
	fmt.Fprintf(c.stdout, "%s [%s]: ", prompt, def)
	reader := bufio.NewReader(c.stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// Interactive reports whether the current run can interact with a human.
// Mirrors the gating used by Confirm; exposed for command logic that needs
// to branch on interactivity.
func (c *CLI) Interactive() bool {
	return !c.json && !c.yes && c.stdinIsTTY
}
