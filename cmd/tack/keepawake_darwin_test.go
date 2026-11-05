//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestKeepAwakeHoldsIdleSleepOff: caffeinate, as keepAwakeCmd starts it, holds PreventUserIdleSystemSleep while it
// runs, which pmset reports by its pid.
func TestKeepAwakeHoldsIdleSleepOff(t *testing.T) {
	cmd := keepAwakeCmd(os.Getpid())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	want := fmt.Sprintf("pid %d(caffeinate)", cmd.Process.Pid)
	var out []byte
	for range 50 {
		out, _ = exec.Command("pmset", "-g", "assertions").Output()
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, want) && strings.Contains(l, "PreventUserIdleSystemSleep") {
				return
			}
		}
		exec.Command("sleep", "0.1").Run()
	}
	t.Fatalf("no PreventUserIdleSystemSleep held by %s:\n%s", want, out)
}

// TestEvalPilotKeepsTheMacAwake: eval pilot takes the keep-awake once its flags parse, before anything can fail.
func TestEvalPilotKeepsTheMacAwake(t *testing.T) {
	called := false
	old := startKeepAwake
	startKeepAwake = func(io.Writer) func() { called = true; return func() {} }
	t.Cleanup(func() { startKeepAwake = old })
	var stdout, stderr bytes.Buffer
	dispatch([]string{"eval", "pilot", "-e0-results", "x"}, &stdout, &stderr) // fails after the flags parse
	if !called {
		t.Fatalf("eval pilot did not keep the Mac awake\nstderr: %s", stderr.String())
	}
}
