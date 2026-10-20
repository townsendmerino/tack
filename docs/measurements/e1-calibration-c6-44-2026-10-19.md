# E1 calibration C6, change 4.4: repeat and empty-result messages, a screen, 2026-10-19/20

<!-- citations-at: 986c178 -->

This is change 4.4 under C6, registered in §12.8 before its runs, as a screen.
- A is A1 with `fit_window` (§12.6, background) and `stop_repeats` (4.5, adopted).
- B is A plus `result_notes`.

**Verdict, as registered: 4.4 enters the bundle (§12.9).** Both conditions hold:
- **The glob mechanism moved as predicted.** After an empty glob, B's next call was a glob 8 times out of 35 (23%),
  against A's 16 out of 22 (73%): half the count, and under a third of the rate. B turned to grep 20 times (57%),
  against A's 2 (9%).
- **d = +1, so d ≥ −1:** B 3/36, A 2/36.

At 72 runs passes can show only gains of about 25 points (§12.9), so the pass difference is not evidence either way.

## 4.5's live check, in the same runs

Registered in §12.7 and §12.8.
- `stop_repeats` ended 20 runs with `repeat_loop`: 14 in A, 6 in B.
- **All 20 exited with status 9.**
- `tools/replayrepeats` over all 72 runs stops exactly those 20, each at the run's own last request (0 requests saved).
  So every live stop came at a real 8th unchanged repeat.
- No other run reaches 8 unchanged repeats, and no passing run was stopped.
- **No disagreement.**

## What ran

- **When:** 22:21:12 PDT 2026-10-19 to 00:15:32 PDT 2026-10-20, 1 h 54 m, holding both machines' locks.
  - A first launch at 22:18 voided all 72 cells on the Mac's 20 GB disk floor (19.2 GB free) before any model
    request. That attempt is kept as `runs-void-disk-2218`.
  - With the owner's yes, 20 leftover repository copies (records kept) and Go's build cache were removed, bringing free
    space to 31 GB. The relaunch ended with 24 GB free.
- **Builds:**
  - tack `987eb2f`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Cells:** 0 void, all 72 on mains. The cell check before the start read nobara at load 0.40 of 16 cores with 55.9 GB
  available, model loaded.
- **Records:** `~/tack-runs/e1/c6-44-2026-10-19/runs/` on the Mac.

## Task by task

| task | A passes | B passes | d | A window-full | B window-full | A `repeat_loop` | B `repeat_loop` |
|---|---|---|---|---|---|---|---|
| 1 | 0/3 | **1/3** | +1 | 0 | 0 | 0 | 1 |
| 2 | 0/3 | 0/3 | 0 | 0 | 2 | 3 | 0 |
| 3 | 0/3 | 0/3 | 0 | 0 | 1 | 2 | 1 |
| 4 | 0/3 | 0/3 | 0 | 0 | 0 | 0 | 0 |
| 5 | 0/3 | 0/3 | 0 | 0 | 1 | 3 | 1 |
| 6 | 0/3 | 0/3 | 0 | 0 | 0 | 1 | 0 |
| 7 | 0/3 | 0/3 | 0 | 0 | 0 | 1 | 2 |
| 8 | 0/3 | 0/3 | 0 | 0 | 0 | 0 | 0 |
| 9 | 0/3 | 0/3 | 0 | 0 | 0 | 0 | 0 |
| 10 | 0/3 | 0/3 | 0 | 0 | 0 | 3 | 0 |
| 11 | 2/3 | 2/3 | 0 | 0 | 0 | 1 | 0 |
| 12 | 0/3 | 0/3 | 0 | 0 | 1 | 0 | 1 |
| **all** | **2/36** | **3/36** | **+1** | **0** | **5** | **14** | **6** |

Over the 9 tasks without tasks 3, 8 and 9: A 2/27, B 3/27.

**Stop reasons:**

| | done | `repeat_loop` | budget | window-full | max_tokens |
|---|---|---|---|---|---|
| A | 16 | 14 | 6 | 0 | 0 |
| B | 18 | 6 | 5 | 5 | 2 |

The window-full column counts `window_full` and prompt-too-long refusals together.

## Against the predictions (§12.8)

| prediction | outcome |
|---|---|
| after an empty glob, B's next call is glob at most half as often as A's | 8 against 16 (23% against 73%): **held** |
| B has fewer `repeat_loop` stops than A | 6 against 14: **held** |
| d between −1 and +1 | +1: **held** |

## What else the runs show

- **Empty-result repeats** (an empty glob or grep repeated unchanged) fell from 158 in A to 44 in B.
- **Task 1 passed for the first time in the calibration.** B-1 flips `windowed-kv`'s default to true in
  `internal/loadflags`, the task's own fix, after 3 greps, a read and two edits. Its other change is a `go.sum` pruned
  by the model's `go mod tidy`. It is one run, so it is not read as a gain.
- **No passing diff touches a test file:** task 11's four passes and task 1's.
- **A had no window-full stop, and B had 5.** With `fit_window` and `stop_repeats` in both arms, A's loops end on the
  repeat rule first. B's runs go on, because the notes break loops, so they meet the window instead.
- **Both arms beat the pooled baseline's 5/72, at 2/36 and 3/36.** At this size that is not a difference.
