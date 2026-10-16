// Command tack is the single binary for tack's CLI, TUI, daemon and ACP adapter (DESIGN §4.1, §10). P0 has `tack run`
// with --no-daemon only.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "0.0.0-dev"

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
}

func dispatch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "run":
		return runCmd(args[1:], stdout, stderr)
	case "eval":
		return evalCmd(args[1:], stdout, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, "tack", version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "tack: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  tack run [flags] "<question>"   answer a question about a repository (read-only tools)
  tack eval models -manifest FILE -out DIR [-arm NAME] [-fit-only]
                                  E0, the model survey (docs/measurements/e0-preregistration-2026-10-16.md)
  tack version
`)
}
