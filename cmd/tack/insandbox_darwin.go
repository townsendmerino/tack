//go:build darwin

package main

import (
	"os/exec"

	"github.com/townsendmerino/tack/internal/sandbox"
)

// inSandbox reports whether this process runs inside a Seatbelt sandbox, tack's own or any: macOS refuses sandbox-exec
// inside one, so a permissive profile that cannot start says so. A sandbox-exec that cannot start for another reason reads
// the same, and the caller treats it as inside: what asks is a privileged act, and it fails closed.
func inSandbox() bool {
	return exec.Command(sandbox.Exe, "-p", "(version 1)(allow default)", "/usr/bin/true").Run() != nil
}
