// Command touched is the day-loop checker: gofmt, build and vet (GOOS darwin and linux, cgo off) of what the diff
// touches, and the tests of the touched packages. It ends with one VERDICT line and exits 0 (PASS), 1 (FAIL) or
// 2 (INCONCLUSIVE).
//
//	go run ./tools/touched              # changes since the merge base with origin/main (or main), plus the worktree
//	go run ./tools/touched -base HEAD~3
//	go run ./tools/touched -all         # every package: what CI runs
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/townsendmerino/tack/tools/gate"
)

func main() {
	var o Options
	flag.StringVar(&o.Base, "base", "", "git ref to diff against through its merge base (default origin/main, else main)")
	flag.BoolVar(&o.All, "all", false, "check every package and Go file in the module")
	flag.StringVar(&o.Timeout, "timeout", "", "go test -timeout (default 3m, 10m where the sandbox escape tests run)")
	flag.Parse()
	o.Log = os.Stdout

	root, err := run(".", nil, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Println(gate.Verdict(gate.Inconclusive, "touched: not in a git repository: "+root))
		os.Exit(2)
	}
	o.Root = strings.TrimSpace(root)
	cells, _, err := Run(o)
	if err != nil {
		fmt.Println(gate.Verdict(gate.Inconclusive, "touched: "+err.Error()))
		os.Exit(2)
	}
	os.Exit(gate.Print(os.Stdout, "touched", cells).Exit)
}
