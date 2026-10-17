//go:build !darwin

package main

// sweepAll has nothing to do: without a sandbox the permission engine runs no commands (internal/perms, noSandbox).
func sweepAll() (int, error) { return 0, nil }
