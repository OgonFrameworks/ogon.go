// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Stdout/stderr helpers for the k8s handler. The k8s_handler tests swap
// these via SetStdoutForTest / SetStderrForTest.

package obs

import (
	"io"
	"os"
)

func defaultStdout() io.Writer { return os.Stdout }
func defaultStderr() io.Writer { return os.Stderr }
