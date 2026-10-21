//go:build !darwin

package main

import (
	"errors"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/sandbox"
)

// editKit refuses: tack's sandbox is macOS's Seatbelt, and without it no command may run (internal/perms, noSandbox).
func editKit(string, string, bool) func(string, sandbox.Target) (core.EditKit, error) {
	return func(string, sandbox.Target) (core.EditKit, error) {
		return core.EditKit{}, errors.New("edit mode needs tack's sandbox, which is macOS-only")
	}
}
