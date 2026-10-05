// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// modules/conflict.go: module contribution conflict detection (MOD-014).
// Two modules conflict when they declare the same route path, the same config
// namespace key, or the same CLI command name.

package modules

import (
	"fmt"
	"sort"
	"strings"
)

// ConflictKind classifies a conflict.
type ConflictKind string

const (
	ConflictRoute    ConflictKind = "route"
	ConflictConfig   ConflictKind = "config"
	ConflictCLI      ConflictKind = "cli"
	ConflictCmdAlias ConflictKind = "command-alias"
)

// Conflict describes a single contribution collision (MOD-014).
type Conflict struct {
	Kind    ConflictKind `json:"kind"`
	Key     string       `json:"key"`
	Modules []string     `json:"modules"`
	Detail  string       `json:"detail,omitempty"`
}

// String renders a conflict for human output.
func (c Conflict) String() string {
	return fmt.Sprintf("%s %q claimed by: %s", c.Kind, c.Key, strings.Join(c.Modules, ", "))
}

// ConflictSet aggregates all conflicts across a registry's modules.
type ConflictSet struct {
	Route  []Conflict
	Config []Conflict
	CLI    []Conflict
	Alias  []Conflict
}

// HasConflicts reports whether any conflicts were detected.
func (c *ConflictSet) HasConflicts() bool {
	return len(c.Route) > 0 || len(c.Config) > 0 || len(c.CLI) > 0 || len(c.Alias) > 0
}

// All returns the conflicts as a flat slice (sorted by Kind, then Key).
func (c *ConflictSet) All() []Conflict {
	out := append([]Conflict(nil), c.Route...)
	out = append(out, c.Config...)
	out = append(out, c.CLI...)
	out = append(out, c.Alias...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return string(out[i].Kind) < string(out[j].Kind)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Contribution is a single contribution claim from a module. Used as input
// to DetectConflicts. Real-world code would populate this from the module's
// declared routes/config-keys/CLI commands at load time.
type Contribution struct {
	Module string
	Kind   ConflictKind
	Key    string
	Detail string
}

// DetectConflicts scans a slice of Contributions and returns the conflict set.
// Two contributions conflict when they share (Kind, Key) across different
// modules. Same-module duplicates are ignored.
func DetectConflicts(contribs []Contribution) *ConflictSet {
	type k struct {
		kind ConflictKind
		key  string
	}
	owners := map[k]map[string]string{} // k → module → detail

	for _, c := range contribs {
		key := k{kind: c.Kind, key: c.Key}
		if owners[key] == nil {
			owners[key] = make(map[string]string)
		}
		owners[key][c.Module] = c.Detail
	}
	cs := &ConflictSet{}
	for key, mods := range owners {
		if len(mods) < 2 {
			continue
		}
		// Sort module names for stable output.
		names := make([]string, 0, len(mods))
		for n := range mods {
			names = append(names, n)
		}
		sort.Strings(names)
		// Use the first non-empty detail.
		detail := ""
		for _, n := range names {
			if mods[n] != "" {
				detail = mods[n]
				break
			}
		}
		conf := Conflict{Kind: key.kind, Key: key.key, Modules: names, Detail: detail}
		switch key.kind {
		case ConflictRoute:
			cs.Route = append(cs.Route, conf)
		case ConflictConfig:
			cs.Config = append(cs.Config, conf)
		case ConflictCLI:
			cs.CLI = append(cs.CLI, conf)
		case ConflictCmdAlias:
			cs.Alias = append(cs.Alias, conf)
		}
	}
	return cs
}

// DetectConflictsFromRegistry walks a registry's manifests and detects
// config-namespace conflicts. (Route and CLI conflicts need full
// contribution data; for now we surface only config-namespace overlaps
// derived from manifest provides.) Returns the conflict set.
func DetectConflictsFromRegistry(r *Registry) *ConflictSet {
	var contribs []Contribution
	for _, m := range r.All() {
		// Each module that contributes config owns the namespace `m.Name.*`.
		// That can't conflict by construction (names are unique). However, if
		// two modules both declare config but with overlapping subkeys, that
		// would be detected here. Without a per-key schema, we mark the
		// namespace itself.
		if m.HasContribution(ContributionConfig) {
			contribs = append(contribs, Contribution{
				Module: m.Name,
				Kind:   ConflictConfig,
				Key:    m.Name + ".",
				Detail: "config namespace root",
			})
		}
	}
	return DetectConflicts(contribs)
}
