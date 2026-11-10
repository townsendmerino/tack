// Command relbuild is the release build of the daemon binary and its gate. For darwin/arm64 and linux/amd64 it runs
// the one pinned build,
//
//	CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> GOWORK=off GOFLAGS=-mod=readonly go build -trimpath -o tack-<os>-<arch> ./cmd/tack
//
// and then reads each binary: its recorded build settings must be that command's and go.mod's toolchain, the linux
// binary must be static, and the darwin binary may load only the system libraries Go itself uses (darwinLibs). It ends
// with one VERDICT line and exits 0 (PASS), 1 (FAIL) or 2 (INCONCLUSIVE).
//
//	go run ./tools/relbuild            # build into a temporary directory, judge, discard
//	go run ./tools/relbuild -out dist  # keep the binaries in dist/
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
	out := flag.String("out", "", "directory to keep the binaries in (default: a temporary one, removed)")
	flag.Parse()
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		fmt.Println(gate.Verdict(gate.Inconclusive, "relbuild: not in a git repository"))
		os.Exit(2)
	}
	dir := *out
	if dir == "" {
		if dir, err = os.MkdirTemp("", "tack-relbuild-"); err == nil {
			defer os.RemoveAll(dir)
		}
	} else {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fmt.Println(gate.Verdict(gate.Inconclusive, "relbuild: "+err.Error()))
		os.Exit(2)
	}
	exit := gate.Print(os.Stdout, "relbuild", check(strings.TrimSpace(string(root)), "./cmd/tack", dir)).Exit
	if *out == "" {
		os.RemoveAll(dir) // os.Exit skips the defer
	}
	os.Exit(exit)
}
