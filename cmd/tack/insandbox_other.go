//go:build !darwin

package main

// inSandbox reports whether this process runs inside tack's sandbox, which exists on macOS only.
func inSandbox() bool { return false }
