package main

import (
	"os"
	"testing"

	"github.com/townsendmerino/tack/internal/shell"
)

// TestMain lets this test binary stand in for tack's own: an edit kit starts `<exe> shell` for each command, and under
// test the executable is this binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "shell" {
		os.Exit(shell.Child(os.Stdin, os.Stdout, os.NewFile(3, "reply")))
	}
	os.Exit(m.Run())
}
