# Decisions

Architecture decisions, one entry each: the decision, the alternatives considered, and the consequences. The owner's
decisions from the design review are in `DESIGN.md` §1 (D1–D13); new ones are recorded here as `ADR-NNN` once work
starts.

## ADR-001 — Swift types come from tack's own small generator, not goduct

**Status:** decided 2026-10-16 (P0; DESIGN §9.3 left it open).

**Question.** Can goduct's intermediate representation take a Swift emitter, or does tack write its own generator?

**What goduct is** (read at goduct `4c5c458`, v0.6.1): a Go-handlers-to-TypeScript generator. Its IR
(`internal/ir/ir.go:API`) is built around HTTP routes: `Route` with method, path, path/query/header params, body and
response types; `TypeDef` for structs, string enums and aliases. The analyzer (`internal/analyzer/analyze.go:Analyze`)
loads packages with `golang.org/x/tools/go/packages` and discovers types only by walking out from functions annotated
`goduct:route`.

**Why not goduct.**

- The IR is in an `internal/` package, so tack cannot import it. Using it means changing goduct to export the IR and
  analyzer, in goduct's repo, on its schedule.
- tack's protocol has no HTTP routes. It is JSON-RPC methods plus an event stream. goduct would need a second way to
  seed type discovery (a method table), which it does not have.
- tack's events are a tagged union: `Event.Payload`'s shape is fixed by `Event.Type`. goduct's IR has no discriminated
  union (`KindUnion` is a generic-constraint union), and the Swift side needs one (`enum EventPayload`).
- It would bring `golang.org/x/tools` into tack's module for a job reflection does in about 300 lines.

**Decision.** `internal/proto/swiftgen` reflects over `proto.Methods` and `proto.EventTypes`, which are the same tables
dispatch and the fixture conformance test use. Reflection sees exactly what `encoding/json` sees: tag names, `omitempty`
and pointers. `go run ./tools/protogen` writes `apps/macos/Sources/TackProto/Proto.generated.swift`. A Go test fails
while that file is stale, and on macOS CI a Swift check decodes and re-encodes every fixture event.

**Consequences.** Doc comments do not carry into Swift; `go/doc` can add them when the app wants them. Timestamps are
strings on the Swift side for now. JSON Schema, and a C++ emitter for the Qt app (DESIGN §18), would be further
emitters over the same walk. If goduct later exports its IR and gains unions, revisit.

## ADR-002 — `assistant.message` keeps the model's raw content

**Status:** decided 2026-10-16. DESIGN now records it (§9.3 and Appendix B, from the second design review), with one
refinement: rendered blocks are derived from the raw content and are not stored in the event at all.

DESIGN §9.3 and Appendix B have `assistant.message` carry rendered blocks (paragraph, heading, code with highlight
spans). Rendered blocks cannot rebuild the request history byte for byte. The next request must resend exactly what
the model produced, or goinfer's cached prefix is lost (§7.2) and "the log is the truth" (principle 4) fails for the
model's view.

**Decision.** `assistant.message` carries `content`: the model's blocks (text, thinking with signature, tool_use) exactly
as the server returned them, which is what the loop sends back. Rendered blocks for clients are derived from it when
`internal/render` lands, and are not stored in the log. `tool.finished` likewise carries `result`, the exact text the model
received, with the full output in the blob store.

## ADR-003 — The fixed-prompt budget is a fifth of the served window; per-turn budgets are larger

**Status:** decided 2026-10-16 by the owner; reconsider as goinfer serves larger windows.

**Context.** DESIGN §2.3 proposed a fixed 3,000-token budget for tack's fixed prompt (system instructions plus tool
schemas). The owner judged the defaults too tight for real users. Two different budgets were in play:

- The fixed-prompt budget caps tack's own overhead, which every request carries. Raising it does not give the user
  anything; it shrinks the user's share of the window.
- The per-turn budgets (`[run]` in `config.toml`) are what a user feels: output per reply, requests and tool calls per
  turn, wall time.

**Decision.**

- **The fixed-prompt budget is a fifth of the served window** (`internal/loop/budget.go:FixedPromptBudget`): 3,276
  tokens at the 16k design window, about 6,500 at 32k. The user's share of any window stays at least four fifths, and
  a larger window from better hardware or a better goinfer raises the cap without a design change.
  `TestFixedPromptBudget` holds the prompt to the budget at the 16k design window. It measured 447 tokens when this was
  decided.
- **Per-turn defaults:**

  | setting | was | now |
  |---|---|---|
  | `max_requests` | 12 | 40 |
  | `max_tool_calls` | 24 | 100 |
  | `max_wall` | 10m | 30m |
  | `max_tokens` | 1024 | 2048 |

  `max_tokens` stops at 2048 because output comes out of the same window: 4,096 would leave only 12k of a 16k window
  for the prompt.

**Why the window stays small.** The only agent setup measured working is Qwen2.5-7B q4_k_m at 16k on nobara's 8 GB
card, which refuses the model at 24k. On the Mac, E0's fit check on 2026-10-16 admitted the same model at 16k with
0.2 GB of goinfer's plan left for prefill. Turns are prefill-heavy, and the fixed prompt is re-prefilled on every cache
break. Small models also follow instructions less well as the context grows. The window is the hardware's limit, not
a product choice.

## ADR-004 — The session log survives power loss: F_FULLFSYNC everywhere, directory entries included

**Status:** decided 2026-10-16 by the owner.

**Question.** How durable does the session log need to be? Data written but not synced already survives a daemon
crash; only a kernel panic or power loss loses it. On darwin, Go's `File.Sync` is `F_FULLFSYNC`, which flushes the
drive's cache (go1.27.2 `src/internal/poll/fd_fsync_darwin.go`). The store pays it at every turn boundary
(`user.message`, `turn.end`) and before every tool runs (`tool.started`).

**Measured** on the M1 Pro, 2026-10-16 12:56 PDT, memory pressure normal, an idle disk: 300 syncs per method, each
after a 1 KiB append to a file in `~/tack-runs` on the volume that holds tack's state directory.

| method | p50 | p99 | max |
|---|---|---|---|
| `F_FULLFSYNC` (`File.Sync`) | 4.0 ms | 9.5 ms | 25 ms |
| `fsync(2)` | 0.08 ms | 0.9 ms | 5.4 ms |
| `F_BARRIERFSYNC` | 0.6 ms | 1.5 ms | 4.6 ms |

On a GitHub macOS runner (run 38958679509), one turn's syncs took 36 ms (`user.message`), 4.6 ms (`tool.started`) and
17.5 ms (`turn.end`).

**Options.** (a) `F_FULLFSYNC` everywhere; (b) plain `fsync(2)`, which leaves data in the drive's volatile cache; (c)
`F_FULLFSYNC` at turn end and plain `fsync` before tools.

**Decision: (a).** A 10-tool turn makes 12 syncs, about 50 ms at p50 and about 115 ms at p99. Model turns take seconds
of prefill, so that is 1–2% at most.

- **The sync before a tool protects against side effects repeating.** If `tool.started` is lost to power loss, the log
  can lose the call and the model's response with it. On resume the model is asked again and may re-run a command
  that already ran, which DESIGN §6.1 forbids.
- **(b) can reorder.** A later block can survive power loss while an earlier one is lost. That leaves a malformed line
  mid-file, which the store refuses to open rather than guess at.
- **(c) weakens the one sync that matters most.**
- If sync time ever shows in a turn's profile, the fallback is `F_BARRIERFSYNC`, not plain `fsync`. It keeps writes in
  order, so the torn-tail repair still holds.

**Directory entries are part of the promise.** A synced file is unreachable after power loss if its directory entry
was lost. The store therefore syncs the containing directory after:

- every directory it creates (`internal/store/dirsync.go:mkdirAllSynced`, the parent of each new directory, top
  down);
- creating a session's `events.jsonl` (`Store.Create`);
- renaming a blob into place (`Blobs.Put`).

The rule is that **a blob, directory entry included, is durable before any event that refers to it is synced.** `Put`
returns only after both syncs, and callers put a blob before appending the event that names it.

**Tests that hold it:**

- `TestAppendFsyncsExactlyWhereRequired` fails if the sync on a turn boundary or on `tool.started` is removed.
- `TestDirectoryEntriesAreSynced` and `TestBlobDirSyncFollowsTheRename` fail if any of the three directory syncs is
  removed.

**Closed 2026-10-16: syncs during a goinfer cache write.** The open item was to measure `F_FULLFSYNC` once while
goinfer wrote a large cache file. The 9.5 ms p99 above is from an idle disk, and goinfer's first load of a model
writes gigabytes beside it.

`tools/synctime` timed 300 syncs per method while goinfer-serve (Metal, `0e598d3f`) did its one-time transcode of
gemma-2-2b `Q8_0` into a new Metal cache. All 900 samples fell inside the write. They span its first 1.8 s.
Records: night job `tack-adr004-synctime`, `~/tack-runs/synctime/synctime-2026-10-16T20-45.json`.

| method | p50 | p99 | max |
|---|---:|---:|---:|
| `F_FULLFSYNC` (`File.Sync`) | 4.5 ms | 12.6 ms | 36.8 ms |
| `fsync(2)` | 0.04 ms | 0.38 ms | 0.76 ms |
| `F_BARRIERFSYNC` | 0.74 ms | 2.6 ms | 3.5 ms |

Under the owner's rule, tens of milliseconds closes the item, and decision (a) stands. Hundreds of milliseconds would
have gone to the owner with the barrier figures.

## ADR-005 — The sandbox allows loopback, except ports already listening when a command starts

**Status:** decided 2026-10-18 by the owner. It is built after the calibration baseline
(`docs/measurements/e1-calibration-preregistration-2026-10-18.md` §4.3), and measured there as its own amendment.

**Question.** DESIGN §6.6 denied the network outright through P4, loopback included. Should a sandboxed command be able
to use loopback?

**What showed it.** In the A1 run of 2026-10-18 (`docs/measurements/e1-pilot-a1-2026-10-18.md`), `go test ./...` on
ken failed `internal/modelfetch`: its `httptest` server could not bind `[::1]:0`. Any repository whose tests start a
local server fails the same way under tack, and did not under opencode, which runs with no sandbox.

**The risk.** Loopback reaches every local listener, not just the command's own. On the owner's Mac on 2026-10-18
that meant:

- VS Code Remote-SSH's SOCKS proxy to nobara (`ssh -D 50814`), a route to the whole outside network;
- ssh port forwards;
- VS Code and Claude Code helper servers;
- AirPlay.

**Options.**

- (a) Keep loopback denied.
- (b) Allow all of loopback.
- (c) Allow loopback, but deny every port already listening when the command starts.

**Decision: (c).**

- Each command's profile is built when the command starts (`sandbox.Wrap`). The deny-list is read then, from the
  kernel's socket table.
- A test's own server binds port 0 after that, so it is allowed.
- Off loopback, everything stays denied: outside addresses, DNS and UDP.

**Escape tests, each red first:**

- an outside address;
- a listener started before the command;
- an ssh `-D` SOCKS proxy;
- DNS and UDP to the outside.

**Known gap, accepted by the owner.** A listener started outside the sandbox while a command runs is reachable from
that command. `TestLoopbackGap_ListenerStartedDuringCommand` demonstrates it, and DESIGN §6.6 records it.

**Implementation blocked, 2026-10-21 (owner).** Seatbelt cannot restrict listening to 127.0.0.1: a host in a network
rule can only be `*` or `localhost`, and its `localhost` covers every address of the machine, the Tailscale address
included. Inbound is checked only at `listen`, by local address.
- **What was shown.** The built form (each ephemeral port allowed by name, for `go test` commands only) let a test
  server inside the sandbox listen on all addresses, and nobara connected to it over Tailscale.
- **Where it is kept.** The code, its escape tests and the failing cross-machine test are on branch `c6-43-loopback`.
  The record is `docs/measurements/e1-calibration-c6-43-held-2026-10-21.md`.
- **The known cost of leaving loopback denied:** tests that start a local server fail in the sandbox. ken's
  `internal/modelfetch` is the case seen: `httptest: failed to listen on a port: listen tcp6 [::1]:0: bind: operation
  not permitted`.
- **Open design question:** run `go test` outside Seatbelt, in a VM, or under a port-scoped firewall rule.

## ADR-006 — E1 runs with snapshots on

**Decided by the owner (Francis, 2026-11-04):** the E1 calibration and its arms run with `snapshots` on, as the released tack
does. The eval should measure what ships, and users will run with snapshots on.

- **What it changes.** Commands a snapshot can undo (`git reset --hard`, discarding `git checkout` and `git restore`, `git clean -f`,
  `rm -r` inside the workspace) stop asking, and in E1 an ask is a refusal. So runs from here on see fewer permission questions than
  earlier runs, which were refused. Both arms of a comparison get the same change.
- **Not directly comparable.** Runs from here on are not directly comparable with earlier snapshot-off runs. A comparison across the
  two periods must say so. A comparison inside one period is unaffected.
- **What it adds.** A `checkpoint` event per bash call in each log, and a shadow repository per workspace under the state directory.
  The cost per command is in DESIGN §6.8. Workspaces over `snapshot_max_bytes` (ken is one) get no snapshot, and the guard keeps
  asking there, so the setting does not engage on every task.
- **What was recommended against.** The recommendation was to keep it off, because the recorded ask rate (1 of 1,207 bash calls)
  says it should not move a pass count, and a setting that engages on some tasks only is a poor thing to hold fixed. The owner chose
  to match what ships.
- **Still to do.** The A1 pilot's config (`internal/eval/pilot/tack.go`, `snapshots = false`) and its test
  `TestE1RunsWithoutSnapshots` still say off. Changing them is a code change and goes through its own PR.
