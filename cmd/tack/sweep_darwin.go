//go:build darwin

package main

import "github.com/townsendmerino/tack/internal/sandbox"

// sweepAll kills every process inside any of tack's sandboxes.
func sweepAll() (int, error) { return sandbox.Sweep(sandbox.MarkAll) }
