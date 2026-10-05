// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Levenshtein-based "did you mean" suggestions for unknown commands and
// flags. CLI-046. The algorithm is implemented here (not delegated to
// cobra's built-in SuggestionsFor) so it is independently testable and
// deterministic.

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// DefaultSuggestDistance is the maximum Levenshtein distance considered a
// "close enough" match. Mirrors cobra's default of 2.
const DefaultSuggestDistance = 2

// Levenshtein computes the edit distance between a and b using the classic
// two-row dynamic programming algorithm over runes. Cost is O(len(a)*len(b)).
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// Suggest returns candidate names within maxDist of input (case-insensitive),
// sorted by distance then alphabetically. De-duplicates. Returns nil when no
// candidate qualifies. A non-positive maxDist falls back to
// DefaultSuggestDistance. Shared-prefix matches get a distance bonus.
func Suggest(input string, candidates []string, maxDist int) []string {
	if maxDist <= 0 {
		maxDist = DefaultSuggestDistance
	}
	type cand struct {
		name string
		dist int
	}
	var cs []cand
	seen := map[string]bool{}
	low := strings.ToLower(input)
	for _, cn := range candidates {
		if cn == "" || seen[cn] {
			continue
		}
		seen[cn] = true
		lcn := strings.ToLower(cn)
		d := Levenshtein(low, lcn)
		if strings.HasPrefix(low, lcn) || strings.HasPrefix(lcn, low) {
			d--
		}
		if d <= maxDist {
			cs = append(cs, cand{cn, d})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].dist != cs[j].dist {
			return cs[i].dist < cs[j].dist
		}
		return cs[i].name < cs[j].name
	})
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.name)
	}
	return out
}

// FormatSuggestion renders the cobra-style "Did you mean this?" block.
func FormatSuggestion(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nDid you mean this?\n")
	for _, s := range suggestions {
		fmt.Fprintf(&b, "\t%s\n", s)
	}
	return b.String()
}

// suggestSubcommands returns did-you-mean candidates for input among cmd's
// direct subcommands (excluding the auto-generated help/completion).
func suggestSubcommands(cmd *cobra.Command, input string) []string {
	var cands []string
	for _, sub := range cmd.Commands() {
		n := sub.Name()
		if n == "help" || n == "completion" {
			continue
		}
		cands = append(cands, n)
	}
	return Suggest(input, cands, DefaultSuggestDistance)
}

// suggestFlags returns did-you-mean candidates for an unknown flag among the
// command's local and inherited flags.
func suggestFlags(cmd *cobra.Command, input string) []string {
	var cands []string
	cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		cands = append(cands, f.Name)
	})
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		cands = append(cands, f.Name)
	})
	return Suggest(input, cands, DefaultSuggestDistance)
}
