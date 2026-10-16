// Command lint runs staticcheck over the module for GOOS darwin and linux, after proving staticcheck is analysing:
// the binary must be the pinned version, and the canary package (tools/lint/canary) must produce its U1000 finding for
// each target. It ends with one VERDICT line and exits 0 (PASS), 1 (FAIL) or 2 (INCONCLUSIVE).
//
//	go run ./tools/lint                          # staticcheck from PATH
//	go run ./tools/lint -staticcheck /path/to/bin
//
// Run it with an installed native staticcheck: `go run …staticcheck` combined with GOOS=linux builds a binary the Mac
// cannot exec (CLAUDE.md).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/townsendmerino/tack/tools/gate"
)

func main() {
	bin := flag.String("staticcheck", "staticcheck", "staticcheck binary")
	flag.Parse()
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		fmt.Println(gate.Verdict(gate.Inconclusive, "lint: not in a git repository"))
		os.Exit(2)
	}
	os.Exit(gate.Print(os.Stdout, "lint", checks(strings.TrimSpace(string(root)), *bin)).Exit)
}
