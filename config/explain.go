// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Explain: resolved value + every source layer that contributed (CFG-030).

package config

import (
	"fmt"
	"strings"
)

// Explain returns the resolved value for key and every source layer that
// contributed to it, in precedence order (defaults first, CLI flags last).
//
// Missing keys return Explanation{Key: key, Value: nil, Sources: nil}.
//
// Output is intended for `ogon explain config http.addr` (Part V.3).
func (c *Config) Explain(key string) Explanation {
	sources := c.trace[key]
	// Return a non-nil slice to keep callers from needing nil checks.
	if sources == nil {
		sources = []Source{}
	}
	return Explanation{
		Key:     key,
		Value:   c.resolved[key],
		Sources: sources,
	}
}

// String renders an Explanation in the human form used by
// `ogon explain config <key>`. URL credentials are redacted (CFG-029).
//
// Example output:
//
//	key:      http.addr
//	resolved: :5000
//	sources:
//	  - defaults → :3000
//	  - ogon.yaml → :8080
//	  - ogon.dev.yaml → :4000
//	  - env:OGON_HTTP_ADDR → :9999
//	  - cli:--addr → :5000
func (e Explanation) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "key:      %s\n", e.Key)
	fmt.Fprintf(&b, "resolved: %s\n", formatExplainValue(e.Value))
	if len(e.Sources) == 0 {
		b.WriteString("sources:  (none)\n")
		return b.String()
	}
	b.WriteString("sources:\n")
	for _, s := range e.Sources {
		line := fmt.Sprintf("  - %s → %s", s.Layer, formatExplainValue(s.Value))
		if s.Path != "" {
			line += fmt.Sprintf(" (file: %s)", s.Path)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// formatExplainValue renders a value for Explain output. Strings have URL
// credentials redacted; other types use fmt.Sprint.
func formatExplainValue(v any) string {
	if v == nil {
		return "<unset>"
	}
	switch x := v.(type) {
	case string:
		return Redact(x)
	default:
		return Redact(fmt.Sprint(x))
	}
}
