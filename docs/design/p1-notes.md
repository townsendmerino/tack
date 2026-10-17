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
- **Detaching: closed in step 2d** (below). A program can call `setsid()` and leave the process group; it stays
  inside the sandbox, and halt finds it there.
- **Not wired in yet.** The daemon (step 4) sets `Engine.Sandboxed` and `Session.Wrap` together on macOS. On a
  platform without a sandbox, the engine fails closed.

## Step 2d: redaction, budgets and halt (2026-10-17)

- **Redaction.** `perms.Redact` replaces well-known credential formats in every tool result before the model, the log
  or the blob store sees it; `tool.finished` names the kinds it replaced.
- **Budgets** (`loop.Budget`, `config.toml` `[run]`). Per turn: requests, wall time, tool calls, tokens, failed calls in
  a row. Per session: tokens and tool calls. Tokens are what the server computed, prefilled plus output. A limit of zero
  is refused, not read as "no limit". The defaults are proposals (DESIGN §6.3).
  - **Open:** the session counts start at zero when the loop is made. The daemon (step 4) rebuilds them from the log's
    `usage` and `tool.finished` events when it reopens a session.
  - **Open:** "asks the user" is only the stop for now; raising a budget mid-session comes with the TUI (step 8).
- **Halt.** `tack halt` writes the HALT file, which a running `tack run` (and later the daemon) watches every 250 ms.
  Every running turn is cancelled, records `daemon.halted` and ends with stop reason `halted`; new prompts get
  `CodeHalted` until `tack resume`. `tack run` exits 5 when halted.
- **Finding what a command detached.** Every sandbox profile allows `mach-lookup` of two names under `dev.tack.mark.`
  and denies the rest of that prefix: the session's mark and the command's, each random (`sandbox.NewMark`); nothing
  is shared between sessions. `sandbox_check` (libsystem, reached through purego) asks the kernel what a process's
  profile decides for a name. A process carries a mark when its profile allows the mark and denies a control name
  under the same prefix; the control check keeps a sandbox that allows every lookup from matching.
- **A sweep cannot kill outside its session, even with a bug in the mark check** (owner, 2026-10-17). `sandbox.Plan`
  selects a process only if it carries the mark **and** started at or after the target's start: the session's start
  for a halt, the command's for a cancel. It refuses, killing nothing, when a process carrying the mark started
  earlier, or when more than `sandbox.MaxSweep` (32) are selected. A refusal is recorded as `daemon.halt_refused` with
  the list (`core.Halt`) and printed by `tack halt`. `tack halt -dry-run` lists each session's selection and changes
  nothing.
  - Sessions register their targets under the state directory's `sweep/` (`halt.Register`), so that `tack halt` can
    sweep a session whose host has gone. Sandboxed commands cannot write there.
  - **Tests assert on lists and never signal** (the break-it-first runs too):
    - `TestStartTimeAloneKeepsPreexistingProcessesOut` runs `Plan` over the real process table with the mark check
      replaced by "everything carries it". It selects none of the running processes, and the plan is refused.
    - `TestPlanSanityLimit` and `TestPlanRefusesAMarkedOlderProcess` cover the two refusals.
    - Each went red when its condition was removed from `Plan`.
  - `sandbox_check` is variadic, and on darwin/arm64 a variadic argument goes on the stack: the call pads five
    registers so purego places the name in the first stack slot. Other architectures are refused.
  - **Measured on macOS 26 (M1 Pro), 2026-10-17:**
    - `(deny syscall-unix (syscall-number SYS_setsid))` does make `setsid()` fail with EPERM.
    - `posix_spawn` with `POSIX_SPAWN_SETSID` still starts a child in a new session. So denying the syscall is not a
      boundary, and the profile does not deny it.
  - **Escape tests** (`sandboxtest`):
    - `TestDetachedProcessesAreSwept`: a process that calls `setsid()` or `setpgid()`, clears its environment and
      closes its files survives a process-group kill with no sandbox and inside the profile alike. The sweep kills it;
      with section `mark` removed, the sweep cannot find it.
    - `TestSweepRefusesAProcessOlderThanItsTarget`: a real marked process older than the target is listed and left
      running.
    - `TestSweepTouchesNothingElse`: a sweep spares unsandboxed processes and other sessions.
    - `TestCancelReachesDetachedProcesses`: the same escape through the real shell child.
- **Open:** `daemon.resumed` waits for the daemon; until then no session is running to record it in.
