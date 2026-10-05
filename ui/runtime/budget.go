// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Budget tests (UI-048/049). The initial-JS bundle must stay under
// 15 KB gzipped (UI-048). SSR must render a 10k-item list under a
// generous time budget (UI-049). The runtime checks these budgets
// during `ogon build --check` and `ogon dev`.

package runtime

import (
	"bytes"
	"compress/gzip"
	"errors"
	"time"
)

// JSGzipBudget is the maximum allowed gzipped JS payload the
// runtime ships to a no-JS-free client (UI-048).
const JSGzipBudget = 15 * 1024

// SSRItemBudget is the list size the SSR renderer must support.
const SSRItemBudget = 10_000

// SSRTimelinessBudget is the time the SSR 10k-item test must fit.
const SSRTimelinessBudget = 500 * time.Millisecond

// CheckJSBudget reports whether the supplied JS bundle fits the
// 15 KB gzip budget (UI-048). Returns the gzipped size and an error
// when the budget is exceeded.
func CheckJSBudget(js []byte) (int, error) {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(js)
	_ = zw.Close()
	gzipped := buf.Len()
	if gzipped > JSGzipBudget {
		return gzipped, ErrJSGzipBudgetHit
	}
	return gzipped, nil
}

// ErrJSGzipBudgetHit is returned when the initial JS exceeds the
// 15 KB gzipped budget (UI-048).
var ErrJSGzipBudgetHit = errors.New("ogon/ui: initial JS exceeds 15 KB gzipped budget (UI-048)")

// CheckSSRList renders `n` list items via the supplied render
// function and asserts it completes within the SSR time budget
// (UI-049). The render function returns the HTML for one item.
func CheckSSRList(n int, render func(i int) string) (string, error) {
	if n <= 0 {
		n = SSRItemBudget
	}
	start := time.Now()
	var out bytes.Buffer
	out.WriteString("<ul>")
	for i := 0; i < n; i++ {
		out.WriteString("<li>")
		out.WriteString(render(i))
		out.WriteString("</li>")
	}
	out.WriteString("</ul>")
	elapsed := time.Since(start)
	if elapsed > SSRTimelinessBudget {
		return out.String(), ErrSSRTimeBudgetHit
	}
	return out.String(), nil
}

// ErrSSRTimeBudgetHit is returned when SSR exceeds the time budget
// for the 10k-item list (UI-049).
var ErrSSRTimeBudgetHit = errors.New("ogon/ui: SSR list budget exceeded (UI-049)")

// BudgetReport summarises both budget tests.
type BudgetReport struct {
	JSGzipBytes int
	JSGzipOK    bool
	SSRMs       int64
	SSROK       bool
}

// RunBudgetTests runs both UI-048 and UI-049 against the supplied
// bundle and SSR render fn. Returns a report and a combined error.
func RunBudgetTests(js []byte, render func(i int) string) BudgetReport {
	rep := BudgetReport{}
	if gz, err := CheckJSBudget(js); err != nil {
		rep.JSGzipBytes = gz
		rep.JSGzipOK = false
	} else {
		rep.JSGzipBytes = gz
		rep.JSGzipOK = true
	}
	start := time.Now()
	_, err := CheckSSRList(SSRItemBudget, render)
	rep.SSRMs = time.Since(start).Milliseconds()
	rep.SSROK = err == nil
	return rep
}
