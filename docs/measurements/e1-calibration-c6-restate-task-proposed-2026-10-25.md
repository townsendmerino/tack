# C6 candidate: a no-change `done` refusal that restates the task — proposed tier-1 check and screen, 2026-10-25

**Status: PROPOSED, not registered, not run.** The setting is built and tested at tier 0 (branch
`c6-done-restates-task`). It changes what the model reads, so it needs a tier-1 live check before any screen
(§12.12, owner 2026-10-20). Both rules below are drafts for the owner to set.

The candidate is in `e1-calibration-c6-status-2026-10-21.md` ("A no-change `done` refusal that restates the task")
and at the end of §12.12.

## The change, as built

- **Setting:** `[run] restate_task` (`config.Run.RestateTask`, `loop.Config.RestateTask`). It is off by default, and
  it is an arm setting for `tack eval pilot`.
- **What it changes:** only the text of the refusal of an edit task's first `done` with no file changed. Today that
  refusal is `loop.DoneNoChange`:

  > no files changed; make the change, or call done again with the reason none is needed

  With the setting on, it is `loop.DoneNoChangeRestated(task)`:

  > no files changed; the task asks for a change. The task was:
  >
  > *the turn's message, as the user gave it, without the workspace facts; cut at a line past 2,000 bytes*
  >
  > Make the change it asks for, or call done again with the reason none is needed.

- **What stays the same:** the rule itself. It is still once per turn, and the second `done` still ends the turn
  whatever the files say (R13).
- **Fixed prompt and prefix:** both unchanged; the text is a tool result, in the tail.
- **The pilot's count:** `pilot.TackSummary.DoneRefused` counts both forms (`loop.IsDoneNoChange`). It had compared
  results with `DoneNoChange` exactly, which would have missed every restated refusal.

## Why

- **Qwen3-4B with the bundle** (`e1-calibration-c6-4b-bundle-2026-10-21.md`, tasks 2, 3, 4, 6, 8 and 9, two runs
  each): 7 of the 12 runs searched, called `done` with nothing changed, were refused once, and ended on the second
  `done`. 0 of those 7 made an edit after the refusal. Only 2 of 12 runs made an edit attempt at all, both on task 8.
- **Task 9 is called `done` at once by every model and arm so far.** Its statement reads as answered without work, and
  the refusal is the one point where the run could still turn.

## Draft tier-1 check (§12.12's shape)

- **Run** the 4B bundle check again with `restate_task` added; everything else as on 2026-10-21:
  - Qwen3-4B as C4 loaded it (bf16, int4 at load), at 30,720, thinking off, goinfer `b2b5e816`;
  - `fit_window`, `stop_repeats`, `result_notes`, `instructions_head`, `refuse_duplicate_edits`, `verify_done`,
    `restate_task`;
  - tasks 2, 3, 4, 6, 8 and 9, two runs each, 12 runs (`-only 2,3,4,6,8,9 -reps 2`).
- **Cost:** about 5 minutes; the 2026-10-21 check took 4 m 27 s. It holds both machines' locks.
- **Where:** on nobara, after the build-after-edit screen and goinfer's jobs release it (owner: not beside them).
  The Mac alone would need a Qwen3-4B configuration E0 has not measured there.
- **Mechanism:** of the runs refused with the restated text, how many make an `edit` or `write` after the refusal.
  The baseline is 0 of 7.
- **Draft pass rule:** at least 3 of the runs refused with the restated text make an `edit` or `write` after it.
  - If fewer than 4 runs are refused at all, the check reads as not shown, not failed.
  - Every run's first message must be byte for byte the 2026-10-21 layout. Only the refusal changes.
- **If it passes,** the screen below is registered. If not, the setting stays in tack, off, as 4.1's did.

## Draft screen (after a passed tier-1 check)

- **Arms:** A is the bundle as adopted at the time (today: `fit_window`, `stop_repeats` amended, `result_notes`,
  `instructions_head`, `refuse_duplicate_edits`, `verify_done`, plus `build_after_edit` if §12.17 adopts it). B is A
  plus `restate_task`. A is the do-nothing arm.
- **Model and tasks:** Qwen3-8B `Q4_K_M` at 16,384, thinking off, goinfer `b2b5e816`; the 12 tasks, A B B A A B, 72
  runs, by day, about 1 h 45 m.
- **Mechanism metrics:**
  1. Per arm, the runs refused for no change.
  2. In B, after the refusal: an `edit` or `write`, more searching only, or the second `done` at once.
  3. Per arm, the runs that end with no file changed.
  4. Task 9 on its own: whether any B run makes a change.
- **Draft decision rule:**
  - **Enters:** B's runs ending with no file changed are at most two thirds of A's, and d ≥ −1.
  - **Parked:** the mechanism moves (metric 2 shows an edit or write after at least half of B's refusals) but metric 3
    does not, or d < −1.
  - **Not adopted:** otherwise. At 72 runs, "not adopted" means "not shown".
  - Qwen3-8B reaches the refusal less often than Qwen3-4B (task 9 is the steady case), so this screen can read "not
    shown" even if the check passes. A Qwen3-4B screen is the alternative arm for the owner to choose.
