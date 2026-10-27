# E1 calibration C6: the current_symbol tier-1 check, 2026-10-27

<!-- citations-at: 8c29530 -->

The tier-1 check registered in §12.20 before its code. **It reads "not shown": the runs produced 1 note, and the rule
needed at least 4.** The setting stays in tack, off. After the one note, the model sent the identical failed edit 7 more
times.

## What ran

- **When:** 15:32:13–15:52:19 PDT (19 m 54 s), holding nobara's lock and the Mac's lock. Both queues were idle.
- **Builds:**
  - tack `8c29530` (branch `c6-current-symbol`), clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, thinking off, decode path `cuda-resident (int4)`.
- **Before the start:** the Mac had 20 GiB free, under the 21 GiB floor. Go's build cache was cleared (20 → 39 GiB; the
  owner's standing permission, Go's build cache only).
- **Cells:** the cell check was clean: nobara at load 0.50 with 57.0 GB available, the model loaded; the Mac at load
  2.9, on mains. 0 void.
- **Settings:** the bundle (`fit_window`, `stop_repeats` amended, `result_notes`, `instructions_head`,
  `refuse_duplicate_edits`, `verify_done`) plus `current_symbol`. Tasks 1, 5 and 6, three runs each.
- **First requests:** the setting adds no tool and no schema field. That is checked at tier 0
  (`TestCurrentSymbolLeavesThePrompt`, `TestCurrentSymbolThroughTheCore`), and no request in these runs offers
  `search` or `symbol`.
- **Records:** `~/tack-runs/e1/c6-cursym-2026-10-27/` on the Mac. `notes.txt` lists what each file is.

## Runs

| task | run 1 | run 2 | run 3 |
|---|---|---|---|
| 1 | **passed**; 4 edits, 1 repeated not-found, no note (file scope) | **passed**; 6 edits, 2 repeated not-found, no note | **passed**; 6 edits, 2 repeated not-found, no note |
| 5 | 9 edits, all failed; **1 note**; `budget` (consecutive errors) | no edit; `repeat_loop` on a grep | 4 edits, 3 applied; no repeated not-found; `done` |
| 6 | no edit; `repeat_loop` on a read | 1 edit, applied; `repeat_loop` | no edit; `repeat_loop` on a read |

- **3 of 9 passed, all on task 1.** Task 1 passed 11 of 12 times in recent bundle runs, so the note did not cost it.
- **ken's index** opened at the first repeated not-found failure in 4 runs: 8.2–10.6 s to open, then 5.5–6.3 s for
  the structural index. It opened in all three task 1 runs and produced no note there.

## The pass rule (§12.20)

- **Notes: 1. The rule needs at least 4, so the check reads "not shown", not failed.**
- **After the one note** (task 5 run 1): the note showed `modelBannerFrom`, lines 120–159 of 114–258, 2,359 bytes. The
  model's next 7 calls were the same edit with the same `old_string`. The loop refused each without running it, and
  the run ended on consecutive errors. Its next edit never applied, and its `old_string` was not taken from the note.

## Why there was one note: the trigger, replayed

The 33 base failures were replayed through the edit tool, with ken's bm25 index at each task's base commit. The
replay is in `base-replay.txt`.
- **Task 1's 15 can carry no note.** Every one is `old_string` `default: false`. The line most like it (4.1's line score)
  is a file-scope `const` help string in `internal/loadflags/loadflags.go`, which no symbol holds. Today's 5 task 1
  failures were the same text.
- **The other 18 would carry one** (tasks 2, 5 and 6, all in `internal/serveapp/banner.go`).
  - 6 of task 6's 13 aim at the wrong symbol. The failed text is `if f.resident && f.residentReuseOff {`. The file has
    the condition as `case f.resident && f.residentReuseOff:` inside `modelBannerFrom`, but the 12-line window with
    the most matching words is the `bannerFacts` struct, whose fields have those names.
- **Tasks 5 and 6 rarely reach the trigger.** One of their six runs today had a not-found failure; in the base runs,
  6 of 24 did. Four of today's six made no failed edit at all, and task 6's three ended in `repeat_loop` on reads.
- **So the registration's choice of tasks overstated the chance of a note.** "Tasks 1, 5 and 6 hold 32 of the 33 base
  failures" counted task 1's 15, which a replay before the run would have shown can carry none. Replay the note over
  the base failures before choosing a check's tasks.

## What this leaves

- `current_symbol` stays in tack, off, on branch `c6-current-symbol`. It is not on main.
- The one observation matches the four text nudges before it (§12.10, §12.17, §12.18, §12.19): Qwen3-8B resent the
  failing text unchanged.
- If it is checked again, the tasks that can carry notes are 2, 5 and 6, and the aim needs a fix for a struct whose
  field names match. Both are for Francis to decide.
