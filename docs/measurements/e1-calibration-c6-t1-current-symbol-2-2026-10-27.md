# E1 calibration C6: the current_symbol second tier-1 check, after the aim fix, 2026-10-27

<!-- citations-at: 3e80dd5 -->

The targeted second check registered in §12.20's amendment before its run. **It fails its pass rule: 5 notes, and
after none of them did the next edit to the file apply.** Every one was followed by the same failed edit. As the
amendment registered, `current_symbol` stays off for good in C6, and no screen runs.

## What ran

- **When:** 16:49:02–17:42:47 PDT (53 m 34 s), holding nobara's lock and the Mac's lock.
  - The launcher was submitted at 16:22 and failed on a non-executable script, so nothing ran.
  - Resubmitted at 16:32, it waited 16 minutes for the Mac to come off battery.
- **Builds:**
  - tack `3e80dd5` (on main, setting off), clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, thinking off, decode path `cuda-resident (int4)`.
- **Cells:** the cell check was clean: nobara at load 0.32 with 57.9 GB available, the model loaded; the Mac at load
  4.6, on mains. 0 void.
- **Settings:** the bundle plus `current_symbol`. Tasks 2, 5 and 6, eight runs each.
- **Records:** `~/tack-runs/e1/c6-cursym2-2026-10-27/` on the Mac. `notes.txt` lists what each file is.

## Runs

- **0 of 24 passed.**
- **Stops:**
  - task 2: `done` 3, `repeat_loop` 3, `window_full` 1, an error 1;
  - task 5: `done` 3, `max_tokens` 2, `repeat_loop` 1, `window_full` 1, consecutive errors 1;
  - task 6: `repeat_loop` 3, consecutive errors 3, `done` 2.
- **Not-found failures came in 4 runs:** task 2 run 3, and task 5 runs 3, 4 and 7.
- **ken's index** opened in 3 of them, taking 10.8–13.2 s.

## The pass rule (§12.20, amended)

**Notes: 5, all in task 2 run 3. After 0 of the 5 did the next edit to the file apply. The rule needed half.**
- **The note was aimed right.**
  - Its 40 lines of `modelBannerFrom` held `case f.kvPrec != "":`, the line the model was after.
  - The model's `old_string` was `if f.kvPrec != ""`, which the file does not have.
- **What the model did:** it sent that same `if f.kvPrec != ""` edit 12 times. Each one that ran got the note, and the
  loop refused each repeat in between without running it. The model never took an `old_string` from the note.

## Why the other three runs carried no note

- **Task 5 run 4:** its 8 failures were a comment line in `decoder/kv16_cuda.go`. A comment above a function sits outside
  its span, so the line is at file scope, and the note is not given there, as registered.
- **Task 5 run 7:** its 7 failures were in `banner.go`, and the window was nearly full.
  - `fit_window` had already replaced 7 results with its "window nearly full" note.
  - The note does not fit the room that leaves, so it was dropped (§12.20's "Built" line).
  - Replayed at the task's base, those edits would have carried notes on `modelBannerFrom` and `factsOf`
    (`t5-nf.jsonl`).
- **Task 5 run 3:** its one not-found failure was the run's first edit call to that file, so no note was due.

## What this leaves

- **`current_symbol` stays in tack, off by default, on main** (`bc5d428`, aim `3e80dd5`). It does not enter the bundle,
  and no screen runs.
- **Across the two checks: 6 notes.** After every one, the model sent the failed edit again unchanged, including when the
  note held the exact line it was after.
- **This is the fifth text nudge** that Qwen3-8B or Qwen3-4B ignored (§12.10, §12.17, §12.18, §12.19, §12.20).
- **The registered next step for C6** is the bundle comparison (the C6 status note's open decision 2).
