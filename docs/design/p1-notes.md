# P1 notes

<!-- citations-at: 5a1e79b -->

Findings for P1 (DESIGN §16), recorded as they arrive from P0's runs and measurements. Each one is an input to P1's
work, with its evidence. Decisions still go in `DECISIONS.md`.

## P1's order and decisions (owner, 2026-10-17)

The order the owner approved, each step with its exit check:

1. **File tools:** `edit`, `write`, `read_output`, `todo`, `ask_user`, with the path and error rules below applied to
   every path-taking tool (`read`, `glob` and `grep` too).
   - **Exit:** a tier-0 test per tool; a missing relative path to each path-taking tool gets the guidance and no
     absolute host path; `edit` refuses a region that changed since the model last saw it (§7.5);
     `TestFixedPromptBudget` re-recorded under budget.
2. **Permissions, sandbox, `tackd shell`, `bash`:** the modes, the rules, the run-time recheck, Seatbelt from
   `(deny default)`, secrets, halt and budgets.
   - **Exit:** a build-tagged escape test per §6.6 hole, each seen red first. **The network stays fully denied, with
     an escape test proving it.**
3. **Checkpoints and rewind:** pre-images, a shadow-repo snapshot before `bash`, rewind as restore plus fork.
4. **Daemon with auto-spawn:** lock, socket and token, subscribe, crash recovery, drain, versions; the goinfer child
   under the daemon.
   - **nobara on demand** (owner, 2026-10-17; DESIGN §8.2). A systemd user unit with socket activation on nobara is
     preferred; otherwise the daemon runs the start script over ssh.
   - It unloads after an idle timeout (15 minutes by default, a `config.toml` setting).
   - The start refuses while goinfer's timing lock is held, and tack reports nobara as busy and offers the Mac's
     model.
   - The TUI's status shows the first request's load ("loading model on nobara").
   - Until this step, nothing runs on nobara by day.
5. **Instruction files by section on ken v1.10.0**, pinned at the start of this step.
   - By day: an exactness check of `SearchFiles` on goinfer.
   - At night: `SearchFiles`'s latency on goinfer.
6. **Compaction, subagents, the usage table.**
7. **The OpenAI-compatible adapter**, with cassettes against goinfer's `/v1/chat/completions`; then `find_related` and
   `recently_changed`.
8. **TUI**, then the full E1 run. P1's exit is the owner's week of real use and the P1 rule.

**Decisions:**

- **`recently_changed` is implemented in tack, not ken.** It runs git directly (exec, never a shell) and returns recent
  commits (`git log`) and uncommitted changes (`git status`). ken's change events (K3) can feed it in P2.
- **`web_fetch` moves to P5** with the per-domain network proxy (DESIGN §16, §6.5, §6.6). P1's sandbox denies the
  network outright.
- **G2 and G6 briefs went to goinfer** in this order: its tool-call parsing fix first (the pilot and E1 wait on it),
  then G2, then G6 (`docs/briefs/goinfer-g2-fit-json.md`, `docs/briefs/goinfer-g6-night-queue-stops-tack-server.md`).
  Until each lands, tack keeps a workaround:
  - **G2:** P0's admission — a pressure check before the load, goinfer's own refusal, and the decode-path check after
    it.
  - **G6** is now a safety net (owner, 2026-10-17). nobara's tack-facing server is on demand and never starts while
    the timing lock is held, so the night queue only stops one that is still running when it starts. No bedtime
    routine: until step 4, nothing runs on nobara by day.

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

## Step 2's shell slice: what it does, and what is open (2026-10-17)

- **What it does.** `bash` runs each command line in `tack shell`, a child process in its own process group, so a
  timeout or cancel kills everything it started (`internal/shell`). The child checks every program the interpreter
  executes, and every file a redirection opens, at the moment it happens:
  - a program approval did not see stops the whole command, unless a rule covers it **and** commands run inside the
    sandbox;
  - so does a run-program flag (`go test -exec`) approval did not see literally;
  - so does a redirection that writes outside the repository, or reads the secrets deny-list.
  The redirection check covers the shell's own redirections only; where programs write is the sandbox's to enforce
  (step 2c).
- **Open: "stops and asks."** DESIGN §6.6 says a command whose expansion differs from what was approved stops and
  asks. This slice stops, and the result says what appeared and why. Asking in the middle of a command needs the
  child to call back to the daemon and wait for the answer, and is left for later in step 2. Until then the model or
  the user re-issues the command with the program written out, which approval then sees.
- **Not wired in yet.** `tack run` keeps the read-only tool set. `edit`, `write` and `bash` reach a session with the
  daemon (step 4), where the permission engine and an approver exist.

## Step 2's sandbox slice (2026-10-17)

- **What it does.** `internal/sandbox` (macOS) builds a Seatbelt profile from `(deny default)` in named sections. It
  allows reading the file system and running programs. It allows writing only the workspace, the session's temp
  directory and tack's own Go cache. It denies:
  - `open`, `launchctl`, `osascript` and LaunchServices;
  - mounting;
  - all network;
  - tack's own files;
  - workspace files that would run later outside the sandbox (git hooks and config, `.claude/`, `.tack/`, `.vscode/`,
    `.mcp.json`) and shell startup files;
  - the secrets deny-list.

  `shell.Session.Wrap` runs `tack shell` inside it, and a session refuses to run if the engine's `Sandboxed` and the
  shell's sandbox disagree.
- **Escape tests** (build tag `sandboxtest`; CI's macOS job, and `touched` when `sandbox-exec` can start). Each escape
  gets out with no sandbox, is stopped by the profile, and gets out again with only its own section removed, so each
  section is shown to be what stops its escape. Network denial is among them. A shell test runs the real child under
  the real profile: a program writing outside the workspace is refused by the kernel. Skipping the wrap let it write.
- **Open: detaching.** DESIGN §6.6 says commands cannot detach, so halt reaches everything they started. The profile
  stops `open`, `launchctl` and LaunchServices, but a program can still call `setsid()` and leave the process group.
  It stays inside the sandbox, but a process-group kill misses it. Halt (step 2d) has to find such descendants, and an
  escape test has to show it.
- **Not wired in yet.** The daemon (step 4) sets `Engine.Sandboxed` and `Session.Wrap` together on macOS. On a
  platform without a sandbox, the engine fails closed.
