# E1 calibration: the baseline, A1 as is, 2026-10-18

<!-- citations-at: dd39263 -->

This is the baseline that `e1-calibration-preregistration-2026-10-18.md` §2 registers: A1 as it is, on the 12
calibration tasks, three runs each.

**Result: 0 passes in 36 runs, and 0 in 27 over the 9 tasks without tasks 3, 8 and 9. That is below the bar** (at
least 4 passes, over at least 2 tasks).

Under §2's rule, as amended by the owner, no loop change is measured on this baseline. **The next decision is the
model or the hardware, per R13** (DESIGN §17).

## What ran

- **When:** by day, on the owner's override.
  - Pass 1: 08:11:58–08:33:56 PDT.
  - Pass 2: 08:33:57–08:53:19.
  - Pass 3: 08:53:19–09:07:30.
  - The rerun of pass 3's tasks 5–12: 09:16–09:34.
  - Every pass held nobara's timing lock.
- **Builds:**
  - tack `1819139`, clean, on both machines;
  - goinfer-serve `31418112`;
  - Qwen2.5-7B-Instruct `q4_k_m` at 32768, on `cuda-resident (int4)`.
  - The sampling, 0.7 / 0.8 / 20, was checked on each pass's first request.
- **Tasks:** `eval/e1/calibration.jsonl`, all 12 with approved statements. The grader check, 12 of 12, passed on the
  Mac at 08:10 (`~/tack-runs/e1/oracle-check-mac-2026-10-18-calibration`).
- **Records:** `~/tack-runs/e1/cal-baseline-2026-10-18/{pass-1,pass-2,pass-3,pass-3-rerun}/results.jsonl` on the Mac.
- **Pass 3's tasks 5–12 were void the first time, and were rerun.**
  - The Mac's disk filled during pass 3, because each run kept a full copy of its repository.
  - Task 5 was cut off mid-run, and tasks 6–12 could not prepare their repositories ("no space left on device").
  - They are kept as void in `pass-3/` and replaced by `pass-3-rerun/`, on the same build and settings.
  - The harness now refuses a run with under 20 GB free, as void, and deletes each repository copy once the run is
    graded (`dd39263`).

## The figures

| | all 12 tasks | the 9 (without 3, 8, 9) |
|---|---|---|
| runs (valid) | 36 | 27 |
| passes | **0** | **0** |
| mean capped time | 10 min | 10 min |
| mean / median wall time | 1.8 / 0.4 min | 1.9 / 0.3 min |
| ended with done | 27 (18 of them after one refusal for no change) | 20 |
| ended with the window full | 8 (6 recorded as `error`, prompt too long; 2 as `window_full`) | 6 |
| ended by the failure budget | 1 | 1 |
| left a diff | 12 | 9 |
| oracle: tests ran and failed | 27 | 21 |
| oracle: build failed | 9 | 6 |

**Per task**, as stop reason, requests and diff bytes for each pass:

| task | pass 1 | pass 2 | pass 3 |
|---|---|---|---|
| 1 | done, 17, 611 | budget, 23, 0 | done, 3, 507 |
| 2 | done, 3, 0 | done, 10, 0 | done, 11, 0 |
| 3 | done, 11, 0 | done, 13, 0 | window, 32, 0 |
| 4 | window, 353, 0 | done, 8, 0 | window, 303, 0 |
| 5 | window, 382, 0 | window, 322, 0 | window, 379, 0 |
| 6 | done, 5, 0 | done, 5, 0 | done, 5, 0 |
| 7 | done, 19, 0 | done, 12, 0 | window, 363, 0 |
| 8 | done, 9, 0 | window, 454, 0 | done, 6, 1421 |
| 9 | done, 14, 0 | done, 9, 250 | done, 8, 674 |
| 10 | done, 13, 0 | done, 7, 516 | done, 8, 590 |
| 11 | done, 8, 828 | done, 6, 828 | done, 3, 426 |
| 12 | done, 12, 641 | done, 11, 0 | done, 16, 629 |

## What the runs show

- **Loops of identical calls.** 7 of the 36 runs repeated one call, almost always the same failing grep, 300–450
  times until the window filled. That is 19% of runs, against none of A1's ten pilot runs.
  - Tasks 4 and 5 looped in at least two of their three runs.
  - The stop rule filed in §4.5 would have ended each of them after 8 unchanged repeats.
- **Giving up.** 27 runs ended with done; in 18 of them done was first refused once because nothing had changed. Most
  are short: the median run took 0.4 minutes.
- **Small tasks, wrong changes.** Tasks 7 (add doc comments to one const block) and 11 (lowercase one error string)
  need a few lines. Task 11 left a diff in all three runs and failed every time, by building or by testing. Task 7 left
  none.
- **Nothing passed,** so there is no paired comparison and no variance to size §3's decision rule from.

## What it decides

**Under §2 as the owner amended it** (fewer than 4 passes, or passes on fewer than 2 tasks):

- **No loop change is measured on this baseline.** That covers the edit tool, done requiring verification,
  loopback, the repeat messages and the stop rule. They stay registered or filed, and are not built for this
  baseline.
- **The next decision is the model or the hardware (R13).** The first candidate the owner named is gemma-4 E4B, in
  the pre-registration's §7.
