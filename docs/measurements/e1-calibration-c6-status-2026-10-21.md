# E1 calibration C6: where it stands, 2026-10-21

<!-- citations-at: ac4cded -->

A resume note for C6, the loop changes measured on Qwen3-8B at 16,384 (pre-registration §12). tack is paused here
while the owner works on goinfer. Nothing is running or queued, and both timing locks are free.

## In the bundle

| change | setting | section | record |
|---|---|---|---|
| 4.5, stop rule for repeated calls, with its amendment (file state after edit and write) | `stop_repeats` | §12.7, §12.13 | `-c6-45-replay-2026-10-19`, `-c6-45-amended-replay-2026-10-20` |
| duplicate-edit guard, adopted with 4.5's amendment | `refuse_duplicate_edits` | §12.14 | the same |
| 4.4, notes after an empty search or an unchanged repeat | `result_notes` | §12.8 | `-c6-44-2026-10-19` |
| 4.2, `done` requires a build or test after the last change; it entered at d's floor | `verify_done` | §12.15 | `-c6-42-2026-10-20` |
| budgeted head of the instruction file, notes first and the task last | `instructions_head` | §12.12 | `-c6-42-stopped-2026-10-20`, `-c6-48-2026-10-20` |

- **Background condition, in every arm:** `fit_window` (4.6; §12.5, §12.6).
- Records are `docs/measurements/e1-calibration-<name>.md`.

## Not in the bundle

- **4.1, edit messages:** not shown; the model resends the same text whatever the message says (§12.10). The setting
  stays in tack, off.
- **4.7, glob and grep descriptions:** proposed, never registered.
- **4.8's "none" (no instruction file):** a measuring arm only; it never enters.
- **4.3, loopback in the sandbox: held** (§12.16; `-c6-43-held-2026-10-21`; branch `c6-43-loopback`).
  - Why held: Seatbelt cannot restrict listening to 127.0.0.1, and a sandboxed test server was reachable from nobara
    over Tailscale.
  - Open design question: run `go test` outside Seatbelt, in a VM, or under a port-scoped firewall rule.

## Candidates, not registered

- **A no-change `done` refusal that restates the task.**
  - Today the first `done` with nothing changed gets "no files changed; make the change, or call done again with the
    reason none is needed", and the second ends the run.
  - The draft: that refusal quotes the task statement back ("The task was: …") and asks for the change.
  - It aims at Qwen3-4B's 7 of 12 runs that searched and then quit (`-c6-4b-bundle-2026-10-21`), and at task 9, which
    every model ends at once.
  - As a prompt change, it needs a tier-1 live check before any screen (§12.12).
- **Edit by line range** (§4.9): refused unless the lines match what was last read.
- **ken's symbol tools:** show a symbol, edit by symbol, and the tests covering a symbol. Each comes to tack through a
  ken release. Delivered in ken v1.11.0 (DESIGN §12); tack still pins v1.9.0.

Added 2026-10-24, from LangChain's Terminal-Bench harness post and the "coding agent from scratch" course (sources below).
Neither measured its changes one by one, except LangChain's reasoning split, so these are ideas to screen, not results.
- **A build after every edit.** After a successful edit or write to a `.go` file, tack builds that package and appends
  up to 10 error lines to the tool's result. It stays silent when the package builds.
  - The evidence is in tack's own runs. Across the recorded C6 runs, 62 changed code and still failed, and 23 of those
    left the model's own code not compiling, over 8 of the 12 tasks. Examples: two `default` cases in one switch, a
    field that doesn't exist, a missing closing brace.
  - Those runs had 30 passes in all. The cost is about a second per edit, against a median model request of 1.1 s.
  - The mechanism metric: edits followed by a broken build, and whether the next edit fixes it.
  - Built 2026-10-25 as `[run] build_after_edit`, off by default. Registered as §12.17 (owner, 2026-10-25); the draft:
    `e1-calibration-c6-build-after-edit-proposed-2026-10-25.md`.
- **Thinking on only for planning and checking.** tack runs Qwen3 with thinking off. The variant: thinking on for the
  first request and for the final check, off in between.
  - LangChain's one measured change: 66.5% with this split, against 63.6% at one level throughout. The highest level
    throughout scored 53.9%, mostly from timeouts.
  - It must measure decode time and window use, since thinking costs both on nobara.
- **The current code after repeated edits to one file.** tack's repeat guard catches the same call repeated. LangChain
  adds a note after several edits to one file, even when they differ.
  - tack's version shows the exact current lines of the symbol being edited, from ken v1.11.0's spans.
  - It aims at the 4.1 replay's largest failure kind: edit text that isn't in the file (217 of 370).
- **A cheap first stage of compaction** (for the P1 compaction design, not a C6 screen). At 60% of the window, replace
  old tool results with short placeholders, with no model call; summarize only at 80%.
  - The cost is one break in prompt-prefix reuse. It aims at window-full stops.
- **Not taken:** telling the model it is graded by tests. That tunes the agent to the benchmark, not to real work.

Sources: <https://www.langchain.com/blog/improving-deep-agents-with-harness-engineering>,
<https://github.com/decodingai-magazine/building-a-coding-agent-from-scratch-course>.

## The bundle night (§12.9)

- **The plan:** one bundle arm, with every setting above, against the baseline arm on Qwen3-8B. 14 runs per task per
  arm, 336 runs, interleaved per task, each after the cell check. A night job on nobara through goinfer's
  `night.py`, about 8–9 hours. Power is about 80% to see 7% → 17%.
- **It waits on:**
  - the owner's go, and the decision rule, registered with the run before it;
  - the baseline arm's exact settings, in particular whether `fit_window` stays on there as §12.6's background
    condition;
  - a pinned tack build;
  - a night slot on nobara, with the load fit-checked first.

## Triggers to watch

- **goinfer B35 lands** (Qwen3.5's DeltaNet prefix reuse). Then run Qwen3.5-9B's C3 row again with E0 amendment 11's
  append probe, on a goinfer build at or after `1ea85681`.
- **The CI minutes reset.** Then one real CI run of current main. A manual dispatch also runs the macOS job and its
  sandbox escape tests.
- **Mellum2.1:** its E0 runs again only on a change that could alter its tool choice.

## Open decisions for Francis

1. 4.3's path: a VM, `go test` outside Seatbelt, a port-scoped firewall rule, or keep loopback denied.
2. The bundle night: its date, its decision rule and the baseline arm's settings.
3. Whether to register the no-change refusal that restates the task.
4. Whether to start on ken's symbol tools, which need ken releases.
5. Whether tack moves to Go 1.27.3, as goinfer did for a security fix (tack's `go.mod` says 1.27.2).
