//go:build darwin

package main

import "github.com/townsendmerino/tack/internal/sandbox"

// sweepTarget sweeps one session's sandbox.
func sweepTarget(t sandbox.Target, dryRun bool) (sandbox.Report, error) {
	return sandbox.Sweep(t, dryRun)
}
