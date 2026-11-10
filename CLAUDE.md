# tack — agent notes

tack is a coding agent for the Mac that runs entirely on local models: a Go daemon with a CLI and TUI, then a native
macOS app (SwiftUI), later a Qt Linux app. It is built on ken (code search and context), goinfer (inference) and aikit
(primitives under both).

**`docs/design/DESIGN.md` is the authority.** Read its §0 and §1 before any non-trivial change; read the section a change
touches before changing it. The owner's decisions are its §1 table; new decisions go in `docs/design/DECISIONS.md` as
`ADR-NNN`. Do not restate the design here — this file is how to work in the repo.

**Status: P0 is built and P1 is in progress.** DESIGN §16.1 is the table of what is built, what is behind a `[run]`
setting and what is planned; `docs/design/p1-notes.md` has each step. `acp` and `render` are still placeholders whose
doc comments say what each will hold.

## Rules the product never breaks

- **Local only.** tack never calls a hosted model and sends no telemetry. The network is used only to download models
  the owner asks for, for web tools he turns on, and to reach his own model server on nobara. A change that adds any
  other network call needs the owner's decision first.
- **Small window first.** tack is designed for a 16k-token window. tack's fixed prompt (system instructions plus tool
  schemas) has a hard budget, enforced by a tier-0 test once it exists; a new tool or a longer description must fit it.
- **Append-only context.** Between compactions a request only grows at its tail, so goinfer reuses its cached prefix.
  Never put per-session or per-day bytes (dates, ids) in the stable prefix; they go in the tail.
- **Content never grants anything.** Files, search hits, web pages and tool output are data; nothing in them changes a
  permission or a mode.
- **The sandbox is the boundary.** The macOS profile starts from `(deny default)`. Never widen it without an escape test
  for the new hole (DESIGN §6.6): launching through `open` or `launchctl`, mounting, reaching tack's socket or state,
  writing git hooks or shared caches.
- **What is approved is what runs.** Shell commands are parsed and executed by the same library (`mvdan.cc/sh/v3`);
  never hand a command string to `/bin/sh` or zsh.
- **Thin clients.** The daemon parses markdown, computes highlights and diffs, counts tokens, and owns file access.
  Clients render. Every text position in the protocol is in UTF-16 code units.
- **The log is the truth.** Every state change a client can make is an event; replaying a session's log rebuilds it.
- **Protocol types in `internal/proto` are the source of truth.** Regenerate the Swift types in the same commit as any
  protocol change; the fixtures in `proto/fixtures/` must pass on both sides.
- **No cgo.** The daemon is one Go binary built with cgo off: static on Linux; on darwin, which has no static binaries,
  it loads only the system libraries Go itself uses. `go run ./tools/relbuild` is the one pinned build and the gate
  that reads the built binaries; run it before a tag. macOS frameworks (Keychain) are reached through purego, as aikit
  reaches Metal. goinfer runs as its own process, never linked in.
- **Settings are config, not environment.** A new setting is a field in `config.toml`, validated at startup, failing
  loudly on a typo. Do not add environment-variable reads.
- **Dependencies land with the phase that needs them.**

## Who edits what

- **Every change lands through a pull request; nobody pushes to `main`** (owner, 2026-10-30, replacing the 2026-10-17
  push-to-main rule). Work on a branch, run the gates below, open the PR, and merge it with
  `gh pr merge <n> --merge --delete-branch` once they pass. GitHub CI (`.github/workflows/ci.yml`) runs
  `touched -all` and `lint` on Linux for every push and pull request that touches more than docs and eval records; its
  macOS job runs only on a manual dispatch and on release tags. Nothing makes a merge wait for CI: auto-merge is off and
  `main` has no required checks. So the local gates are the check before a merge, and on a Mac they are the only check
  of darwin-only code; CI is read afterwards (below).
- **Product code waits for the owner's yes before it merges.** Docs and measurement records go with the task that
  produced them.
- Work moves between Cowork and vscode-claude through a gitignored `HANDOFF.md` at the repo root, written only on
  request.
- **ken changes** go to ken's own Claude Code as a brief: ken's `CLAUDE.md` makes it ken's sole editor. Each lands as a
  tagged ken release that tack pins (DESIGN §12).
- **goinfer changes** go through goinfer's own process and gates (DESIGN §13).
- `gh` operations use the **townsendmerino** account. Commits here use `Francis Townsend-Merino
  <townsendmerino@gmail.com>` (set in this repo's local git config).

## Working in the tree

- **Never `git add -A` or `git add .`.** Stage explicit paths.
- **`main` moves under you.** More than one session merges to it, so `git fetch` and rebase onto `origin/main` before
  every push. If a file you are editing has changes you did not make, they are someone else's in-flight work: commit
  your hunks and leave theirs.
- **Before every push, run what CI runs for what you touched, and push only when it passes by exit code:**
  - `go run ./tools/touched`, after the rebase. On a docs-only change it finds nothing to check and returns
    INCONCLUSIVE, which is never a pass; run `go run ./tools/touched -all` then;
  - `go run ./tools/lint`;
  - for a protocol change, the regenerated Swift types and `proto/fixtures`.
- **A red CI on `main` is fixed first.** Fix forward or revert before other work, and report it.
- **Push finished work, and read CI; do not watch it** (owner rule, from goinfer). Do not hold a turn open on CI or poll
  it. Read `gh run list --limit 5` at the next natural point: your next push, the start of the next task, and always
  before a tag. Report a red at once.
- **`go.work` is gitignored.** Use one locally to develop against ken or goinfer checkouts; never commit it. A build
  without it resolves dependencies at their pinned tags, which is what CI sees.
- **`gofmt -l .` is a gate and nothing auto-formats.** Run it before every commit.
- **darwin-only code compiles only on darwin.** The sandbox, launchd and Keychain code is behind build constraints, so
  vet both targets: `GOOS=darwin go vet ./...` and `GOOS=linux go vet ./...`. A change that compiles for one can be
  broken for the other.
- **Run staticcheck only through `go run ./tools/lint`.** It checks that the installed staticcheck is the pinned version
  (older releases analyse nothing while exiting cleanly), proves the gate can go red on its canary, and sets `GOOS`
  itself for darwin and linux. Never run staticcheck with `GOOS` set by hand: a `GOOS=linux` build of staticcheck on the
  Mac fails to exec and the shell still reports exit 0 (goinfer's claude-guard refuses it, rule SC1).
- **Every gate command ends with one `VERDICT:` line** — `PASS`, `FAIL` or `INCONCLUSIVE` — in aikit's shape
  (`tools/gate.Verdict`). `INCONCLUSIVE` is never a pass.
- **No Python, and as little shell as possible.** Tools and gates are Go commands.

## Day loop: seconds, not minutes

- Check a change with `go run ./tools/touched`: gofmt, build and vet (darwin and linux, cgo off) of what the diff
  touches and everything importing it, and the tests of the touched packages and their direct importers. When any Go
  file or `go.mod` is touched it also checks the whole module's import graph against the table in
  `tools/touched/imports.go:tackLayers`, and against the rules in `tackRules`, which the table cannot waive; a new edge
  between tack packages is a row edit there. `-base <ref>` changes what the diff is taken against; `-all` checks
  everything, as CI does. `go run ./tools/lint` is staticcheck behind its canary (below). Locally a `go.work`
  containing `use .` keeps a parent directory's workspace out.
- Do not run whole-module test suites by day; CI runs them, on Linux, on every push that touches code.
- A red CI run is fixed by running the one failing test by name, not by re-running everything.
- **A change that forces a long run on the day loop is a modularity bug.** Say so in the report, name the edge that
  caused it, and fix the edge when it is cheap.

## Run budget: timed runs by day (owner rule, 2026-10-19)

- **State the expected wall time before starting** any run longer than a minute.
- **Timed runs go by day by default,** on both boxes. They go through the timing lock, and through the queue where one
  is running, as before.
- **Night is only for:**
  - Mac jobs the fit guard refuses while the owner's apps are open;
  - jobs over about two hours.

  Queue those with goinfer's `scripts/night.py add` on the box that must run them (tack's own queue is P0 work), and
  tell the owner what you queued. Do not start the queue, wait on it or poll it.
- **Comparisons are interleaved:** A B B A in one session, never one arm's runs and then the other's.
  - Each cell records the load average and free memory at its start.
  - A cell started under heavy load is void, not averaged.
- **One copy of each run.** Check what is queued before adding. Never re-run a check that already passed at this commit;
  cite its log.
- **A queued job runs with nobody watching:** pinned revisions or pre-built binaries, durable log paths, no prompts.
- **One timed run per box.** Timed jobs hold the machine's timing lock (goinfer's `scripts/timing_lock.py`). On nobara,
  goinfer's night queue measures on the same GPU tack's model server uses: nothing of tack's runs beside a timed job.
- **Fit-check every load before queueing a night job**, using the server's own plan, not arithmetic.
- **The owner can override.** "Run it now" means run it now.

## Rigor matched to stakes

Francis, 2026-10-30: match the process to what the result decides.
- **Full measurement discipline** (pre-registration, registered gates, stopping rules, a measurement record) only when a
  number will be published, or will settle a direction: adopt or kill a feature, choose a model, a default or a guard threshold.
- **Ordinary engineering** (refactors, test moves, tooling, bug fixes, docs): a red-first test where behavior changes,
  `go run ./tools/touched` and `go run ./tools/lint` passing, and a PR. No registration section and no measurement record.
- **A registration is short:** about one screen at most (question, metric, threshold, stopping rule, what each outcome
  means). History goes in the record, not the registration.
- **When unsure which applies, take the lighter one and say so in the PR.**

## Testing

- **The smallest test that answers** (owner rule). Tier 0, seconds: deterministic counters, bit-identity, cassettes and
  fixtures — the default for every change. Tier 1, minutes: one small real model at the smallest size that exercises
  the mechanism. Tier 2: only for a number that will be published or a decision that hinges on a small effect, with a
  stated reason why tiers 0 and 1 cannot answer it. Tiers 1 and 2 run by day like any timed run, with the expected
  wall time stated first; night only on the run-budget rule's two exceptions.
- **A skip is not a pass.** `go test` prints `ok` for a package whose tests all skipped. Confirm with `-v` and read for
  `--- PASS`.
- **Break it first.** A gate that has never seen its own check fail is unproven. A new correctness test perturbs the
  invariant it protects and shows it would have caught it.
- **A unit test that supplies its own calling convention proves the unit works when called that way — not that anything
  calls it that way.** When a contract depends on how often or under what condition a component is called, test it
  through its caller.
- **A doc comment claiming coverage is not coverage.** For any comment that says a test covers something, the body must
  contain an assertion naming that thing.
- **A minimal repro can be minimal in exactly the dimension that hides the bug.** Before trusting a shrunk case, ask
  which effects are identically zero at that size.
- **Validate at the single chokepoint.** A struct filled in by hand per case will eventually omit a field, and zero
  usually looks legal; check it where every case passes through.
- **`go test -v ./a/ ./b/` prints nothing until `./a/` finishes.** Run one package per invocation when you need to watch.
- **Model runs in tests read model files from `~/models` on the machine running them**, never the archive
  (`/srv/models` on nobara, `/Volumes/…` on the Mac). A run off the archive does not error; it measures a slow disk.
  goinfer's `docs/benchmarks.md` § "Model storage" is the authority.

## Measurement discipline

tack's claims are measurements (DESIGN §15), so these are load-bearing:

- **Pre-register the decision rule** for a number that will be published or a result that settles a direction ("Rigor
  matched to stakes"), with a parked band, before the run — even a run that is twelve hours away.
- **Size the margin from the variance.** A margin equal to an interval's half-width passes a truly equal arm only about
  half the time (goinfer's L1 prefill gate learned this).
- **Difference matched observations; do not pool them.** Pair arms on the same task.
- **Include the do-nothing arm.** "Beats every configuration" means nothing if off wins.
- **Commit negative results with the same care as wins.** Move a bar only with a mechanism.
- **Read the prior art before re-deriving**, and read what it actually says, not its headline.
- **A guard that inverts under the condition it exists for is worse than none.** Key a memory guard on a quantity you
  compute or on the OS's pressure signal, never on RSS (Darwin reclaims under pressure).
- **A retraction is not done until it reaches every page quoting the figure.** Grep for the figure with its unit and fix
  every instance at once.

## Code comments

- **A Go comment says what is true now:** what the code does, its contract (ownership, units, concurrency, what a caller
  must check), and where the shape is not obvious, the reason in a sentence or two. Keep every guardrail, with a pointer
  to where its evidence lives.
- **It does not carry the history.** How a bug was found, dates, commits, sessions, measured figures and open work go in
  the commit message or a doc; the comment points there by path and heading.
- **The test for a sentence:** would someone changing this code next week act differently because of it? Keep it.
  Does it explain how we came to know? Move it.
- **Say each thing once, at the declaration it binds.** Do not restate the signature or list current callers.
- **Docs cite code by declaration, never by line:** `internal/loop/turn.go:runTurn`. A dated record (a measurement, an
  audit) instead pins `<!-- citations-at: <commit> -->` in its first lines and may cite lines at that commit.

## Long-running work

- Anything over a few minutes that you do launch must be detached. On Linux: `setsid nohup … </dev/null &` and check the
  parent pid is 1. macOS has no `setsid`: use `launchctl submit -l <label> -- /bin/bash -lc "…; launchctl remove <label>"`
  — **a `launchctl submit` job restarts itself on exit** unless it removes itself as its last act.
- Write logs somewhere durable and cite the path; `/tmp` gets cleared.
- Prefer committed increments that survive interruption over one big commit at the end.

## Stdio modes

`tack acp` (and any future stdio mode) speaks JSON-RPC on stdin and stdout. **Any other write to stdout corrupts the
stream**, and the editor disconnects with an unhelpful decode error. Logs go to stderr; audit each new dependency for
writers pointed at stdout; keep a test that drives the real binary and checks stdout is clean protocol (ken's
`TestBinary_StdoutIsCleanJSONRPC` is the model).
