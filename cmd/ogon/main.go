// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// ogon CLI entrypoint. Defers entirely to the cli package; the only
// responsibility here is mapping the returned exit code to os.Exit, including
// the 130 (interrupted) case so SIGINT surfaces as the normative code.

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/OgonFrameworks/ogon.go/cli"
)

func main() {
	// Surface SIGINT as the normative ExitInterrupted (130) code. A second
	// SIGINT forces immediate termination.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() {
		n := <-sigCh
		// On the first interrupt we let the in-flight command finish if it
		// observes ctx cancellation; to keep the binary simple and
		// deterministic we exit immediately with the normative code.
		_ = n
		os.Exit(cli.ExitInterrupted)
	}()

	code := cli.Execute()
	os.Exit(code)
}
