//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
)

// keepAwake holds a no-idle-sleep assertion for as long as this process lives, and returns the function that drops
// it. A Mac that idle-sleeps mid-run leaves the model server writing into a connection nobody reads (owner,
// 2026-10-18). caffeinate -w also exits on its own when this process does. A failure is reported and the run goes
// on: the capture's sleep check still voids a run the host slept through.
func keepAwake(log io.Writer) func() {
	cmd := keepAwakeCmd(os.Getpid())
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(log, "tack eval pilot: keeping the Mac awake:", err)
		return func() {}
	}
	return func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
}

// keepAwakeCmd is caffeinate holding PreventUserIdleSystemSleep until pid exits.
func keepAwakeCmd(pid int) *exec.Cmd {
	return exec.Command("/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(pid))
}

// startKeepAwake is keepAwake; a test replaces it to see that eval pilot calls it.
var startKeepAwake = keepAwake
