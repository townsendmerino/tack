# tack — agent notes

tack is a coding agent for the Mac that runs entirely on local models: a Go daemon with a CLI and TUI, then a native
macOS app (SwiftUI), later a Qt Linux app. It is built on ken (code search and context), goinfer (inference) and aikit
(primitives under both).

**`docs/design/DESIGN.md` is the authority.** Read its §0 and §1 before any non-trivial change; read the section a change
touches before changing it. The owner's decisions are its §1 table; new decisions go in `docs/design/DECISIONS.md` as
`ADR-NNN`. Do not restate the design here — this file is how to work in the repo.

**Status: nothing is built. P0 is next** (DESIGN §16). The packages under `internal/` are placeholders whose doc comments
say what each will hold.

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
- **No cgo.** The daemon is one static Go binary. macOS frameworks (Keychain) are reached through purego, as aikit
  reaches Metal. goinfer runs as its own process, never linked in.
- **Settings are config, not environment.** A new setting is a field in `config.toml`, validated at startup, failing
  loudly on a typo. Do not add environment-variable reads.
- **Dependencies land with the phase that needs them.**

## Who edits what

- **Cowork and vscode-claude both edit tack.** Work on a branch per track; push and merge only when the track is done.
  Work moves between them through a gitignored `HANDOFF.md` at the repo root, written only on request.
- **ken changes** go to ken's own Claude Code as a brief: ken's `CLAUDE.md` makes it ken's sole editor. Each lands as a
  tagged ken release that tack pins (DESIGN §12).
- **goinfer changes** go through goinfer's own process and gates (DESIGN §13).
- `gh` operations use the **townsendmerino** account. Commits here use `Francis Townsend-Merino
  <townsendmerino@gmail.com>` (set in this repo's local git config).

## Working in the tree

- **Never `git add -A` or `git add .`.** Stage explicit paths.
- **`main` moves under you** once more than one session works here. `git fetch` and rebase before pushing; if a file you
  are editing has changes you did not make, they are someone else's in-flight work — commit your hunks, leave theirs.
- **Push finished work, and read CI; do not watch it** (owner rule, from goinfer). Commit locally; push when the owner
  asks or a finished track merges. Do not hold a turn open on CI or poll it. Read `gh run list --limit 5` at the next
  natural point (your next push, the start of the next task, always before a merge or a tag) and report a red at once.
- **`go.work` is gitignored.** Use one locally to develop against ken or goinfer checkouts; never commit it. A build
  without it resolves dependencies at their pinned tags, which is what CI sees.
- **`gofmt -l .` is a gate and nothing auto-formats.** Run it before every commit.
- **darwin-only code compiles only on darwin.** The sandbox, launchd and Keychain code is behind build constraints, so
  vet both targets: `GOOS=darwin go vet ./...` and `GOOS=linux go vet ./...`. A change that compiles for one can be
  broken for the other.
- **staticcheck can silently check nothing.** Toolchain export data moved to version 5 with Go 1.27.2, and older
  staticcheck releases analyse nothing while exiting cleanly. Use the version goinfer's CI pins (built from source, as
  goinfer's `CLAUDE.md` describes), and prove the gate can go red: a throwaway package with an unused struct field that
  **imports a standard-library package** must print `field deadField is unused (U1000)`. A canary that imports nothing
  passes on a broken staticcheck.
- **Do not combine `go run …staticcheck` with `GOOS=linux`** on the Mac: it builds a Linux binary, fails to exec it, and
  the shell still reports exit 0. Use an installed native binary and set `GOOS` only for the analysis target.
- **Every gate command ends with one `VERDICT:` line** — `PASS`, `FAIL` or `INCONCLUSIVE` — in aikit's shape
  (`tools/gate.Verdict`). `INCONCLUSIVE` is never a pass.
- **No Python, and as little shell as possible.** Tools and gates are Go commands.

## Day loop: seconds, not minutes

- Check a change with the touched-files checker once it exists (P0): gofmt, build and vet of what the diff touches, and
  the tests of the touched packages. Until then: `gofmt -l`, `go vet` (both GOOS), and `go test -run '<yours>'` on the
  package you changed.
- Do not run whole-module test suites by day; CI runs them on every push.
- A red CI run is fixed by running the one failing test by name, not by re-running everything.
- **A change that forces a long run on the day loop is a modularity bug.** Say so in the report, name the edge that
  caused it, and fix the edge when it is cheap.

## Run budget: quick by day, long by night (owner rule)

- **Estimate first.** Before any run longer than a minute, state its estimated wall time.
- **By day, nothing over about ten minutes**, and no timed or graded measurement on the Mac: the owner is using it.
- **Everything else goes on the night queue.** tack's own queue is P0 work; until it exists, queue jobs with goinfer's
  `scripts/night.py add` on the box that must run them, and tell the owner what you queued. Do not start the queue,
  wait on it or poll it; the owner starts it at bedtime.
- **One copy of each run.** Check what is queued before adding. Never re-run a check that already passed at this commit;
  cite its log.
- **A queued job runs with nobody watching:** pinned revisions or pre-built binaries, durable log paths, no prompts.
- **One timed run per box.** Timed jobs hold the machine's timing lock (goinfer's `scripts/timing_lock.py`). On nobara,
  goinfer's night queue measures on the same GPU tack's model server uses: nothing of tack's runs beside a timed job.
- **Fit-check every load before queueing a night job**, using the server's own plan, not arithmetic.
- **The owner can override.** "Run it now" means run it now.

## Testing

- **The smallest test that answers** (owner rule). Tier 0, seconds: deterministic counters, bit-identity, cassettes and
  fixtures — the default for every change. Tier 1, ten minutes or less by day: one small real model at the smallest
  size that exercises the mechanism. Tier 2, night: only for a number that will be published or a decision that hinges
  on a small effect, with a stated reason why tiers 0 and 1 cannot answer it.
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

- **Pre-register the decision rule**, with a parked band, before the run — even a run that is twelve hours away.
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
