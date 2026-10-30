# E1 calibration C6: where it stands, 2026-10-21

<!-- citations-at: ac4cded -->

A resume note for C6, the loop changes measured on Qwen3-8B at 16,384 (pre-registration §12). First written
2026-10-21; brought up to date 2026-10-30.

## Where it stands, 2026-10-30

- **The bundle against A1 at screen size (§12.21): not shown, d = +1.** B passed 4/36 and A 3/36
  (`-c6-bundle-2026-10-28`).
  - Francis kept the bundle, with `edit_tolerances` added, as C6's loop.
  - The powered comparison (§12.9's 336 runs) waits for the model question.
- **Qwen3.5-9B (§12.23):** step 1 passed. E0 found it eligible, and the append probe's gate held at 0.15 and 0.07
  of the cold time (`e0-nobara-qwen35-2026-10-29.md`, goinfer `95bc3c7b`).
- **Step 2 is §12.24:** Qwen3.5-9B at 32,768 with the bundle, 36 runs.
  - It ran on the Mac's `night.py` from 22:53 on 2026-10-29 to 03:03 on 2026-10-30, at tack `3382aa8`.
  - **Usable, at the bar:** 4 of 36 passed, on 2 tasks (task 10 1/3, task 11 3/3; task 1 0/3), with 0 void
    (`-c6-qwen35-row-2026-10-29`).
  - It had 5 window-full stops and 11 cap hits, and a median run of 7.7 minutes.
  - **The model question is now open.** Francis chooses whether C6 moves to Qwen3.5-9B.
- **CB3's scorer** no longer counts a whole-file write as seen (Francis, 2026-10-29). The record's counts are
  unchanged (`e1-cb3-context-metrics-2026-10-28.md` §7).

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

## Joined the bundle after §12.21's run (2026-10-29)

- **Edit tolerances** (`edit_tolerances`; §12.22, adopted by replay 2026-10-27, `-c6-tolerances-2026-10-27`): all whitespace
  ignored, and a trailing `;` ignored, in `.go` files, only at one place and only if the file still parses. `,` `)` `]`
  `}` `.` `:` were replayed and left out.
  - It is on in §12.24's row, the first run with it.
  - §12.21's arms did not include it.

## Not in the bundle

- **Build after every edit:** not shown (§12.17, 2026-10-25): runs ending with code not compiling, A 6 and B 6; d = −1;
  the next change built after none of B's 7 notes (`-c6-build-2026-10-25`). The setting stays in tack, off.
- **The no-change refusal that restates the task:** its tier-1 check fails (§12.18, 2026-10-26): 0 of 8 refused runs
  acted after it (`-c6-t1-restate-edit-lines-2026-10-26`). The setting stays in tack, off; no screen.
- **4.9, edit by line range:** its tier-1 check fails (§12.19, 2026-10-26): 0 of 6 runs used it. The setting stays in
  tack, off; no screen.
- **The current text of a symbol after a repeated failed edit:** not adopted (§12.20, 2026-10-27).
  - The first check read "not shown" (1 note in 9 runs).
  - After the aim fix, the second check failed: 5 notes, and the next edit applied after none.
  - The setting stays in tack, off, on main (`bc5d428`, `3e80dd5`), for good in C6.
  - Records: `-c6-t1-current-symbol-2026-10-27`, `-c6-t1-current-symbol-2-2026-10-27`.
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
  - Built 2026-10-25 as `[run] restate_task`, off by default. Tier-1 check registered as §12.18 (owner,
    2026-10-25); the proposal:
    `e1-calibration-c6-restate-task-proposed-2026-10-25.md`.
- **Edit by line range** (§4.9): refused unless the lines match what was last read.
  - Built 2026-10-25 as `[run] edit_lines`, off by default. Tier-1 check registered as §12.19 (owner, 2026-10-25):
    `e1-calibration-c6-edit-lines-proposed-2026-10-25.md`.
- **ken's symbol tools:** show a symbol, edit by symbol, and the tests covering a symbol. Each comes to tack through a
  ken release. Delivered in ken v1.11.0 (DESIGN §12), which tack now pins. Calibration arms still run without ken.

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
  - **Chosen next (owner, 2026-10-27), tier-1 check registered as §12.20 before its code,** as `[run] current_symbol`:
    the note follows a repeated edit to one file whose text is not in it. With the bundle, such failures are 33 in 144
    runs (the 217 of 370 were mostly loops the bundle now ends), and the next edit to that file applied after 5 of them.
- **A cheap first stage of compaction** (for the P1 compaction design, not a C6 screen). At 60% of the window, replace
  old tool results with short placeholders, with no model call; summarize only at 80%.
  - The cost is one break in prompt-prefix reuse. It aims at window-full stops.
- **Not taken:** telling the model it is graded by tests. That tunes the agent to the benchmark, not to real work.

Sources: <https://www.langchain.com/blog/improving-deep-agents-with-harness-engineering>,
<https://github.com/decodingai-magazine/building-a-coding-agent-from-scratch-course>.

Added 2026-10-29, from RudderStack's DevBox (DESIGN §2.5). DevBox reports pull-request outcomes on frontier models, not
changes measured one by one, so these too are ideas to screen.
- **Self-review of the diff before `done`.** On a write task's first `done`, tack hands the model its own `git diff`
  beside the task statement; the model fixes what it finds or says what it left undone.
  - The nearest evidence is against nudges on Qwen3-8B: the restating refusal moved 0 of 8 runs (§12.18), and no
    build-after-edit note was followed by a change that built (§12.17). `verify_done` already makes `done` wait for a
    build or test.
  - As a prompt change, it needs a tier-1 live check before any screen (§12.12). The mechanism metric: reviews followed
    by an edit, and whether that edit's run passes.
- **Plan, then edit.** A fixed order for an edit task: a plan, a review of the plan, then the edits, with mutating
  tools refused until the plan exists (as plan mode refuses them, DESIGN §6.3).
  - It pairs with "thinking on only for planning and checking" above: one arm could carry both.
  - Costs: each stage boundary must keep the cached prefix (DESIGN §7.2), and the review spends window.
  - A different model for the plan, as DevBox sets per stage, is a nobara option only: two models do not fit the Mac
    together (DESIGN §2.3).
- **Learnings written back to the repo** belong to P1's instruction-file step, not a screen (`docs/design/p1-notes.md`,
  step 5).

## The bundle night (§12.9)

**Deferred (Francis, 2026-10-29)** until the model question that follows §12.24 is settled. It would then run on
whichever model C6 keeps. The 2026-10-21 plan follows, unchanged.

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

- **Done:** goinfer B35 landed. Qwen3.5-9B's E0 ran again with the append probe on `95bc3c7b` (§12.23 step 1, passed).
- **CI minutes:** CI runs again on every push.
- **Done 2026-10-29:** the first manual dispatch ran, on Francis's yes at 14:58 PDT. It was run `40962188012`, on
  main `4f31811`, and finished green at 15:02.
  - The macOS job passed: `touched -all`, `lint`, the sandbox escape tests and the Swift fixture check.
  - The escape tests reported 47 `--- PASS` in `internal/sandbox` and `internal/shell`, with 0 skipped and 0 failed.
  - The Linux job passed as well.
- **Mellum2.1:** its E0 runs again only on a change that could alter its tool choice.

## Open decisions for Francis

1. 4.3's path: a VM, `go test` outside Seatbelt, a port-scoped firewall rule, or keep loopback denied.
2. Settled 2026-10-27: the bundle comparison is registered as §12.21. It is 72 runs by day after the 2026-10-28
   power outage, A1 with no settings against the bundle, under §12.3's rule.
   - **Ran 2026-10-28: not shown, d = +1** (B 4/36, A 3/36; `-c6-bundle-2026-10-28`).
   - Francis, 2026-10-29: the bundle with `edit_tolerances` stays C6's loop and runs §12.23's row; the powered
     comparison waits for the model question.
3. **Next, now that §12.24's row is read (usable, at the bar):** whether C6 moves to Qwen3.5-9B at 32,768.
   Then which model the powered comparison runs on.
4. Settled 2026-10-29: CI's macOS job was dispatched once, and it is green (Triggers, above).

Settled since 2026-10-21:
- the restate_task refusal was registered and failed its check (§12.18);
- ken v1.11.0's symbol tools are in tack;
- tack is on Go 1.27.3;
- (2026-10-29, Francis's yes, `2f901b9`) tack's gates no longer go red when the Mac is under memory pressure. The
  `internal/eval` tests' injected pressure reading now reaches the model supervisor's load guard, through
  `models.ChildConfig.Pressure`.
