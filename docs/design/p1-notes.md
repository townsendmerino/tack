# P1 notes

<!-- citations-at: 5a1e79b -->

Findings for P1 (DESIGN §16), recorded as they arrive from P0's runs and measurements. Each one is an input to P1's
work, with its evidence. Decisions still go in `DECISIONS.md`.

## Tool paths: say they are relative to the repository root, and make errors say what to do instead (owner, 2026-10-17)

**What happened.** The Mac's night recording `tack-cassette-run-ken-7b` (2026-10-16) ran Qwen2.5-7B-Instruct on Metal.
Its first two calls were `grep` with `"path":"ken"`, the repository's name from the user message's `Repository: ken`
line. Both errored with the raw OS error, which names a host path:

    error: ken: lstat /Users/…/cmd/tack/testdata/ken/ken: no such file or directory

The model never learned from it. It went on to a wrong answer, that `applyTokenBudget` is not defined in
`mcp/token_budget.go`. Evidence: `~/tack-runs/cassettes/run-ken-7b.attempt1.json` and the night log
`~/goinfer-logs/night/runs/2026-10-16/tack-cassette-run-ken-7b.log`.

**The cause in the code.** As of `5a1e79b`:

- `grep`'s `path` is described only as "directory or file", and `read`'s `path` has no description. Nothing says
  paths are relative to the workspace root (`internal/tools/fs.go`).
- `Workspace.Resolve` returns `filepath.EvalSymlinks`'s error unchanged (`internal/tools/tools.go:Resolve`).

**For P1:**

- **Descriptions.** Every tool's path parameter says paths are relative to the repository root, and that `.` is all
  of it.
- **Errors say what to do instead.** For example: "no such path 'ken'; paths are relative to the repo root, use '.'
  for all of it". Name the path as the model gave it, not the host's absolute path. Small models learn from error text.
- **Budget.** Both changes cost fixed-prompt tokens or tool-result tokens. The descriptions must fit
  `FixedPromptBudget` and are re-counted by `TestFixedPromptBudget`.
- **Test.** A tier-0 test sends a missing relative path to each path-taking tool and asserts the error carries the
  guidance and no absolute host path.
