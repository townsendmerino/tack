package main

import (
	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/aikit/chunk/treesitter"

	"github.com/townsendmerino/tack/internal/kensearch"
)

// ken's engine never imports the tree-sitter chunker; the binary does (ken ADR-023, DESIGN §12), as ken and ken-mcp
// do. Importing aikit/chunk/treesitter registers it with aikit's defaults; registering again here states the tier, as
// ken-mcp does: a 1 s per-parse timeout, so one pathological file cannot stall a watch flush in a long-lived process.
// tack does not need the byte-identical rebuilds the ken CLI turns the timeout off for. TestRunWithKen fails without
// this file.
func init() {
	chunk.Register(kensearch.Chunker, treesitter.New(treesitter.WithParseTimeoutMicros(1_000_000)))
}
