//go:build !darwin

package main

import "github.com/townsendmerino/tack/internal/sandbox"

// sweepTarget has nothing to sweep: without a sandbox the permission engine runs no commands (internal/perms,
// noSandbox).
func sweepTarget(sandbox.Target, bool) (sandbox.Report, error) { return sandbox.Report{}, nil }
