package main

import (
	"os"
	"testing"

	"github.com/townsendmerino/tack/internal/shell"
)

// TestMain lets this test binary stand in for tack's own: an edit kit starts `<exe> shell` for each command, the A1
// arm starts `<exe> run`, and the daemon tests start `<exe> daemon run` (both with TACK_TEST_AS_TACK=1); under test the
// executable is this binary. TACK_TEST_PATH, when
// set, is the PATH tack runs with, apart from the pilot's.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "shell" {
		os.Exit(shell.Child(os.Stdin, os.Stdout, os.NewFile(3, "reply")))
	}
	if len(os.Args) > 1 && (os.Args[1] == "run" || os.Args[1] == "daemon") && os.Getenv("TACK_TEST_AS_TACK") == "1" {
		if p := os.Getenv("TACK_TEST_PATH"); p != "" {
			os.Setenv("PATH", p)
		}
		os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
