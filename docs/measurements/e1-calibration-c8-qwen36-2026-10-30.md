# E1 calibration C8: Qwen3.6-35B-A3B on the calibration set, 2026-10-30

<!-- citations-at: 5704cba -->

This is calibration C8's step 2 (`e1-calibration-preregistration-2026-10-18.md` §14.2), registered before its runs:
- Qwen3.6-35B-A3B `q4_k_m` at 16,384 with `-moe-cache-experts`, on goinfer `00584c35` and tack `5704cba`, with the
  bundle and a 30-minute cap;
- tasks 11, 1 and 10, 3 runs each, expanded in a fixed order if any run passes.

Step 1, E0, was eligible (`e0-nobara-qwen36-35b-2026-10-30.md`).

## The first attempt, 12:14–12:16 PDT: void (disk), not a result

- **All 9 runs of the first set were void,** each in 0 s: "disk: 5.1 GB free on …/runs's volume, under the 20 GB a run
  needs". The pilot's disk floor refused each run before it started, so no run reached the model. The record is
  `~/tack-runs/e1/c8-qwen36-2026-10-30/void-disk-1214/`.
- **The launcher counted the 9 voids as non-passes.** It logged "0 passing runs of 9" and stopped under §14.2's
  "no pass, the run stops" rule. That was a defect in the launcher, not a read: a void run is no result (§12.2).
- **This attempt is recorded as void.** It is not a 0 of 9, and §14.2's stopping rule was not triggered by any
  result.
- **The launcher is fixed:**
  - a block with any void run now stops the launcher and reports the void reasons, so nothing is counted;
  - before each block it needs 21 GiB free, since a run needs 20. The guard is not lowered.
- **The disk:**
  - the coordinator freed 4.4 GB (an unused clone cache);
  - its watcher ran `go clean -cache` when no Go process was running (12:24, 8.9 → 23 GiB free;
    `~/goinfer-work/gocache-when-idle.log`).
- **The first set was relaunched at 12:25,** as registered, ending by 21:45.

## The first set at 16,384, 12:28–12:51 PDT: 7 of 9 pass

The first set re-ran from 12:25: warm-up, then the cell check (clean), then the runs. Each failure is classified by its
stop cause, from the run's own records.

| task | run | result | stop | requests | wall | largest prompt sent | edits applied |
|---|---|---|---|---|---|---|---|
| 11 | 1 | **pass** | `done` | 8 | 54 s | 4,101 | yes |
| 11 | 2 | **pass** | `done` | 7 | 66 s | 5,326 | yes |
| 11 | 3 | **pass** | `done` | 9 | 93 s | 7,469 | yes |
| 1 | 1 | **pass** | the server's prompt-too-long refusal | 21 | 214 s | 16,532 refused (16,259 the largest accepted) | 2 |
| 1 | 2 | fail | `repeat_loop`: 8 unchanged `read`s of `loadflags_test.go` at offset 220 | 15 | 167 s | 15,032 | none |
| 1 | 3 | fail | `repeat_loop`: 8 unchanged `grep`s for `ResidentWindowedKV` | 14 | 164 s | 14,689 | none |
| 10 | 1 | **pass** | `done` | 9 | 91 s | 7608 | yes |
| 10 | 2 | **pass** | `done` | 13 | 139 s | 8931 | yes |
| 10 | 3 | **pass** | `done` | 15 | 231 s | 13374 | yes |

**The first set: 7 of 9 passed,** on all 3 tasks: task 11 at 3/3, task 1 at 1/3 and task 10 at 3/3. There were no void runs
and no cap hits. Every passing run ended on `done` except task 1 run 1. Qwen3-8B never passed task 10 (C5, §12.21's A
and B), and Qwen3.5-9B passed it once (§12.24). This set stands as its own read at 16,384 (§14.3).

**How task 1's runs ended:**
- **Neither failure is a full window, a wrong edit or the cap.** Both stopped on the repeat rule (`stop_repeats`)
  before making any edit, at 14.7k and 15.0k tokens, under the window.
- **Run 1's 400 is tack sending past the window.** Its last request was 16,532 tokens to a server served at 16,384:
  "prompt is too long: 16532 tokens > 16384 maximum".
  - It is the creep §12.24's record traced: past `fit_window`'s reserve, each request still adds a call and a
    short result, and nothing in the loop checks a request against the window before sending it.
  - The run had already applied its edit, and the oracle passes it.
- **For comparison, task 1 on Qwen3-8B:** the bundle passed 3/3 (§12.21's B), and A1 with no settings passed 0/3
  (C5, §12.21's A). Qwen3.5-9B with the bundle passed 0/3 (§12.24).

**No 32k arm in this row** (superseded at 12:47: Francis chose a 32k arm, §14.3, read below). §14.2 registered the expansion at 16,384, and adding an arm now would change the design
  in the middle of the run and might not end by 21:45. A 32,768 row is for its own registration after this one is
  read. nobara-big01's serve check ran the model at 32,768 with no swap.

## The 32k arm (§14.3), 12:59–19:46 PDT: 13 of 36 pass, usable at the bar

All 12 tasks ran 3 times at `-ctx 32768`, after a cell check at 32,768 came up `cuda-resident (int4)` with 1.3 GB of KV
and a clean cell. There were 0 void runs and 0 cap hits.

**Read under §14.3 (§2's bar): usable.** 13 passes on 8 tasks, against a bar of 4 passes on 2 tasks.

| task | passes | task | passes | task | passes |
|---|---|---|---|---|---|
| 11 | 3/3 | 1 | 1/3 | 2 | 0/3 |
| 12 | 3/3 | 3 | 1/3 | 5 | 0/3 |
| 4 | 2/3 | 8 | 1/3 | 6 | 0/3 |
| 10 | 2/3 | | | 7 | 0/3 |
| | | | | 9 | 0/3 |

For comparison on the same 12 tasks: Qwen3-8B with the bundle at 16,384 passed 4/36 (§12.21's B), and Qwen3.5-9B with
the bundle at 32,768 passed 4/36 (§12.24).

**Every run.** "Tokens at stop" is the largest prompt the server accepted, or, for a prompt-too-long refusal, the size
it refused. "Power" and "build" are each run's own record.

| task | run | result | how it ended | tokens at stop | requests | wall s | power | build |
|---|---|---|---|---|---|---|---|---|
| 1 | 1 | fail | repeat loop, no edit | 31,074 | 31 | 536 | ac | 5704cba |
| 1 | 2 | **pass** | pass (window_full) | 32,760 | 35 | 719 | ac | 5704cba |
| 1 | 3 | fail | window full (prompt too long) | 32,840 | 46 | 593 | ac | 5704cba |
| 2 | 1 | fail | window full (prompt too long) | 32,934 | 31 | 564 | ac | 5704cba |
| 2 | 2 | fail | max_tokens | 27,846 | 21 | 527 | ac | 5704cba |
| 2 | 3 | fail | repeat loop, no edit | 32,063 | 21 | 545 | ac | 5704cba |
| 12 | 1 | **pass** | pass (done) | 12,893 | 11 | 198 | ac | 5704cba |
| 12 | 2 | **pass** | pass (done) | 5,653 | 9 | 85 | ac | 5704cba |
| 12 | 3 | **pass** | pass (done) | 5,677 | 7 | 81 | ac | 5704cba |
| 6 | 1 | fail | window full (prompt too long) | 32,816 | 49 | 634 | ac | 5704cba |
| 6 | 2 | fail | wrong edit (repeat_loop) | 30,958 | 46 | 684 | ac | 5704cba |
| 6 | 3 | fail | window full (prompt too long) | 32,772 | 33 | 555 | ac | 5704cba |
| 5 | 1 | fail | window full (prompt too long) | 32,817 | 44 | 608 | ac | 5704cba |
| 5 | 2 | fail | repeat loop, no edit | 31,010 | 24 | 515 | ac | 5704cba |
| 5 | 3 | fail | window full (prompt too long) | 32,903 | 75 | 676 | ac | 5704cba |
| 3 | 1 | fail | repeat loop, no edit | 24,769 | 47 | 436 | ac | 5704cba |
| 3 | 2 | **pass** | pass (error) | 32,822 | 55 | 901 | ac | 5704cba |
| 3 | 3 | fail | repeat loop, no edit | 31,107 | 32 | 538 | ac | 5704cba |
| 4 | 1 | **pass** | pass (error) | 32,803 | 44 | 619 | ac | 5704cba |
| 4 | 2 | fail | max_tokens | 11,581 | 9 | 206 | ac | 5704cba |
| 4 | 3 | **pass** | pass (done) | 30,520 | 68 | 615 | ac→battery | 5704cba |
| 7 | 1 | fail | repeat loop, no edit | 31,204 | 25 | 513 | ac | 31a89c2 |
| 7 | 2 | fail | window full | 32,751 | 48 | 590 | ac | 31a89c2 |
| 7 | 3 | fail | repeat loop, no edit | 32,524 | 35 | 566 | ac | 31a89c2 |
| 9 | 1 | fail | window full (prompt too long) | 33,020 | 40 | 623 | ac | 31a89c2 |
| 9 | 2 | fail | repeat loop, no edit | 31,395 | 29 | 544 | ac | 31a89c2 |
| 9 | 3 | fail | window full (prompt too long) | 32,936 | 28 | 557 | ac | 31a89c2 |
| 8 | 1 | fail | repeat loop, no edit | 13,077 | 23 | 203 | ac | 31a89c2 |
| 8 | 2 | fail | repeat loop, no edit | 9,790 | 20 | 144 | ac | 31a89c2 |
| 8 | 3 | **pass** | pass (done) | 12,627 | 13 | 179 | ac | 31a89c2 |
| 11 | 1 | **pass** | pass (done) | 5,185 | 9 | 81 | ac | 31a89c2 |
| 11 | 2 | **pass** | pass (done) | 7,196 | 7 | 99 | ac | 31a89c2 |
| 11 | 3 | **pass** | pass (done) | 4,462 | 11 | 73 | ac | 31a89c2 |
| 10 | 1 | **pass** | pass (done) | 10,936 | 12 | 170 | ac | 31a89c2 |
| 10 | 2 | fail | wrong edit (repeat_loop) | 18,059 | 25 | 328 | ac | 31a89c2 |
| 10 | 3 | **pass** | pass (done) | 7,047 | 9 | 112 | ac | 31a89c2 |

**How the 23 failures ended:**
- 10 were repeat loops with no edit (`stop_repeats`);
- 9 were a full window, 8 of them prompt-too-long refusals and 1 `window_full`;
- 2 were replies cut at `max_tokens` (2048);
- 2 were wrong edits: an edit applied, then a repeat loop;
- none hit the 30-minute cap.

**Tokens at stop:**
- 17 of the 23 failures stopped at 30k tokens or more, so the runs read until the 32,768 window was nearly full.
- The 13 passes split in two. The easy tasks (11, 12, 10, 8) passed by about 13k. The harder ones (1, 3, 4) passed
  only near the top, at 30.5–32.8k, and three of those passes ended on the window (one `window_full`, two
  prompt-too-long refusals) after the edit was applied.

**The battery override (§14.3, Francis 18:05):** the last five blocks (tasks 7, 9, 8, 11, 10) ran on tack `31a89c2`
with `-allow-battery`. Every one of those 15 runs recorded `ac`: the Mac was back on mains when they started, so the
override let no run start on battery. Only task 4's run 3, on `5704cba`, went from mains to battery during the run
(`ac→battery`). Mac-side timings from the override blocks are reported only, as §14.3 registered.

**`calreport`** (`calreport-runs32.md` in the job directory):
- stops: `done` 10, `repeat_loop` 12, prompt-too-long 10, `window_full` 2, `max_tokens` 2;
- 1,072 requests, a median of 28 a run;
- median wall time 537 s;
- server first-token time: median 5.8 s, p90 32.9 s.

The guard table shows:
- `fit_window` fired in 20 runs, 170 times, and was followed by progress 8% of the time;
- `result_notes` fired in 22 runs, 199 times, followed 10% of the time;
- `verify_done`, `refuse_duplicate_edits` and `edit_tolerances` never fired.

## 16k against 32k, on the tasks both windows ran (1, 10, 11)

| task | 16k | 32k | how 16k ended | how 32k ended | tokens at stop, 16k | tokens at stop, 32k |
|---|---|---|---|---|---|---|
| 11 | 3/3 | 3/3 | `done` ×3 | `done` ×3 | 4.1–7.5k | 4.5–7.2k |
| 1 | 1/3 | 1/3 | pass then prompt-too-long; 2 repeat loops, no edit | pass then `window_full`; 1 repeat loop, no edit; 1 prompt-too-long | 14.7–16.5k | 31.1–32.8k |
| 10 | 3/3 | 2/3 | `done` ×3 | `done` ×2; 1 wrong edit, then a repeat loop | 7.6–13.4k | 7.0–18.1k |
| **all** | **7/9** | **6/9** | | | | |

- **The larger window did not add passes** on the three tasks both ran. On task 1, the runs at either size read
  until the window was nearly full, and stopped there whether they passed or not.
- **The runs took longer at 32k.** Task 1's median wall time was 167 s at 16k and 593 s at 32k.
- **The comparison is small:** 3 tasks, 9 runs a window. The 16k row did not run the other 9 tasks.

## Recommendations, for Francis to decide

1. **The window for the 35B's next screens: 16,384.**
   - On the shared tasks it did as well (7/9 against 6/9) in about a third of the time.
   - The runs read to the top of either window, so a larger one buys more reading, not more passes.
   - It is tack's design window, and C6's.
   - Against it: 32k is the only window this model has a full 12-task row at. Some tasks may need more than 16k, and
     the hard tasks' 32k passes came near the top.
   - **The cheapest way to settle it:** a 16k row on the other 9 tasks (27 runs, about 1–1.5 hours by day), so both
     windows cover all 12.
2. **The repo-map screen (§13.3) on the 35B, not Qwen3-8B: yes, at 16k, after that 16k row.**
   - The 35B is the model the loop is now aimed at, and it passes enough tasks for a reach effect to show.
   - The screen's arms line, registered to be fixed before its first run, would then name the 35B. Its sd would come
     from the 35B's own 16k per-task reach, not §12.21's Qwen3-8B rates.
   - Against it: the screen loses comparability with C6's Qwen3-8B history, and needs the 16k row first.
