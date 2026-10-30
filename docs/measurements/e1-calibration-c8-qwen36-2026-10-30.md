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

**No 32k arm in this row.** §14.2 registered the expansion at 16,384, and adding an arm now would change the design
  in the middle of the run and might not end by 21:45. A 32,768 row is for its own registration after this one is
  read. nobara-big01's serve check ran the model at 32,768 with no swap.
