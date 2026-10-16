// Command protogen regenerates the Swift protocol types from internal/proto (DESIGN §9.3). Run it from the repo root
// in the same commit as any protocol change; internal/proto/swiftgen's test fails while the file is stale.
//
//	go run ./tools/protogen
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/townsendmerino/tack/internal/proto/swiftgen"
)

func main() {
	if err := os.MkdirAll(filepath.Dir(swiftgen.OutPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(swiftgen.OutPath, swiftgen.Generate(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "protogen: wrote", swiftgen.OutPath)
}
