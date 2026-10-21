//go:build darwin

package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/shell"
)

// TestEditKitLoopbackOnlyForGoTest: the edit kit's sandbox gives a command loopback only when [run] sandbox_loopback is
// on and the shell says the command runs go test; every other command keeps the profile with no network.
func TestEditKitLoopbackOnlyForGoTest(t *testing.T) {
	for _, setting := range []bool{false, true} {
		kit, err := editKit(t.TempDir(), t.TempDir(), setting)(t.TempDir(), sandbox.Target{Mark: "s-test", Since: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		defer kit.Close()
		sh := kit.Shell.(*shell.Session)
		for _, goTest := range []bool{false, true} {
			cmd := sh.Wrap(exec.Command("/usr/bin/true"), "s-test", "c-test", goTest)
			profile := strings.Join(cmd.Args, " ")
			if got, want := strings.Contains(profile, `(remote tcp "localhost:`), setting && goTest; got != want {
				t.Errorf("sandbox_loopback %v, go test %v: loopback in the profile %v, want %v", setting, goTest, got, want)
			}
		}
	}
}
