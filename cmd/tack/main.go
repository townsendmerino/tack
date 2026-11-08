// Command tack is the single binary for tack's CLI, TUI, daemon and ACP adapter (DESIGN §4.1, §10).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/townsendmerino/tack/internal/shell"
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
	case "daemon":
		return daemonCmd(args[1:], stdout, stderr)
	case "halt":
		return haltCmd(args[1:], stdout, stderr)
	case "resume":
		return resumeCmd(args[1:], stdout, stderr)
	case "rewind":
		return rewindCmd(args[1:], stdout, stderr)
	case "shell":
		// Internal: the child internal/shell.Session starts for each bash command. It reads its request on stdin and
		// writes its reply to fd 3; it is not for people.
		return shell.Child(os.Stdin, stdout, os.NewFile(3, "reply"))
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
  tack daemon run|start|stop|status [-config FILE]
                                  the daemon: in the foreground, started detached, stopped after a drain, or asked how it is
  tack halt [-reason TEXT]        stop every running turn and command; refuse new turns
  tack resume                     lift a halt
  tack rewind [-yes [-no-undo]] <session> [seq]
                                  list a session's checkpoints, or put the files back as they were at one
  tack version
`)
}
