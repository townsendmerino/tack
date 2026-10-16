// Command tack is the single binary for tack's CLI, TUI, daemon and ACP adapter
// (docs/design/DESIGN.md §4.1, §10). Nothing is built yet; P0 starts here.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "tack: nothing is built yet (P0); see docs/design/DESIGN.md")
	os.Exit(1)
}
