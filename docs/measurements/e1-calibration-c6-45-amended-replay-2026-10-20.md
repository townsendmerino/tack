# E1 calibration C6: 4.5's stop rule as amended, and the duplicate-edit guard, replayed over all captured runs, 2026-10-20

<!-- citations-at: 4a06e94 -->

This is the replay that judges §12.13's amendment to 4.5, and counts §12.14's duplicate-edit guard. Both were registered
before the replay ran.

**Verdict, as registered: the amended rule is adopted.** It stops 98 of 406 counted runs and no passing run. The rule as
first registered stops 102, one of them passing (4.8's task 12 B-3). Four stops move, every one from "stops" to "does
not stop".

## What ran

- **When:** 15:23 PDT on the Mac. No model and no lock: the replay reads captured events only.
- **Build:** `tools/replayrepeats` at `4a06e94`, with `-mirrors ~/tack-runs/e1/mirrors` for the amended rule
  and without it for the registered rule, over the same list of jobs.
- **Runs:** every captured A1 job under `~/tack-runs/e1/` with a `results.jsonl`, void runs skipped: 406 runs.
  - Two files are left out of the tool's list, because they hold void runs whose records are not marked void:
    - `cal-c4c5-2026-10-18/c4/pass-1-aborted-0654/` (C4's record), one run;
    - `cal-baseline-2026-10-18/pass-3/`, whose tasks 5–12 were void and replaced by `pass-3-rerun/` (the baseline
      record). Tasks 6–12 have no events.
  - **That file's tasks 1–4 are valid, and they are counted** from a run over every file. They hold one stop, task 4
    after request 18 (grep), the same under both rules: no moved stop, and no duplicate edit.
  - The run over every file reports the 8 runs without events as missing, and returns INCONCLUSIVE. It also counts
    the void task 5, which is not counted here.
- **Outputs,** on the Mac under `~/tack-runs/e1/`:
  - `c6-45-amended-replay-2026-10-20.txt` (with `-mirrors`);
  - `c6-45-registered-replay-2026-10-20.txt` (without);
  - `c6-45-amended-replay-2026-10-20.jobs`, the list of jobs, passed to both runs through `xargs`;
  - `c6-45-amended-replay-2026-10-20-allfiles.txt`, the amended rule over every file.

  The tables below are copied from them.

## The two rules, job by job

| job | runs | passing | registered rule: stops | amended rule: stops | passing runs stopped (registered / amended) |
|---|---|---|---|---|---|
| baseline (Qwen2.5-7B: pass-1, pass-2, pass-3 tasks 1–4, pass-3-rerun) | 36 | 0 | 8 | 8 | 0 / 0 |
| C4 (Qwen3-4B, passes 1–3 and smoke) | 37 | 0 | 13 | 13 | 0 / 0 |
| C5 (Qwen3-8B, passes 1–3 and smoke) | 37 | 4 | 6 | 6 | 0 / 0 |
| 4.6 | 72 | 3 | 18 | 17 | 0 / 0 |
| 4.4 | 72 | 5 | 20 | 20 | 0 / 0 |
| 4.1 (all three launches, incomplete blocks included) | 80 | 5 | 18 | 18 | 0 / 0 |
| 4.8 | 72 | 6 | 19 | 16 | 1 / 0 |
| **all** | **406** | **23** | **102** | **98** | **1 / 0** |

## The stops that move

| run | registered rule | amended rule | passed | what the run did |
|---|---|---|---|---|
| 4.6 task 5 B-2 | stops after request 16 | does not stop (26 requests) | no | re-applied one edit 17 times |
| 4.8 task 10 A-1 | stops after request 26 | does not stop | no | re-applied one edit 10 times |
| 4.8 task 10 A-3 | stops after request 24 | does not stop | no | re-applied one edit 8 times |
| 4.8 task 12 B-3 | stops after request 18 | does not stop | **yes** | re-applied one edit 8 times; §12.11's finding |

- `stop_repeats` was on live in 4.8, so those three runs ended at the registered rule's stop. What the amended rule does
  after that point is not seen.
- 4.6 ran without the rule. Its task 5 B-2 went on 10 more requests and failed.
- No run the amended rule stops has a lost rebuild: every successful edit in it found its text in the rebuilt file. A
  lost rebuild would leave the state unchanged, so it errs toward stopping, never toward a missed stop.

## The duplicate-edit guard

Over the same 406 runs, the guard would have:
- touched 21 runs, 8 of them passing;
- refused 92 calls, 61 of which the run applied again.

| run | identical edits | applied again | first, at request | of | passed |
|---|---|---|---|---|---|
| baseline pass-2 task 1 | 1 | 0 | 13 | 23 | no |
| C5 pass-1 task 11 | 1 | 0 | 5 | 22 | **yes** |
| C5 pass-3 task 11 | 7 | 0 | 5 | 19 | **yes** |
| 4.6 task 5 B-2 | 17 | 17 | 9 | 26 | no |
| 4.6 task 10 B-2 | 7 | 7 | 20 | 34 | no |
| 4.6 task 10 B-3 | 2 | 2 | 15 | 21 | no |
| 4.6 task 11 B-2 | 1 | 0 | 5 | 71 | no |
| 4.6 task 11 A-2 | 1 | 0 | 5 | 8 | **yes** |
| 4.4 task 10 B-2 | 1 | 0 | 13 | 18 | no |
| 4.4 task 11 A-2 | 7 | 0 | 5 | 19 | **yes** |
| 4.4 task 11 A-3 | 1 | 0 | 5 | 8 | **yes** |
| 4.4 task 12 B-2 | 4 | 4 | 14 | 30 | no |
| 4.1 task 5 A-2 | 4 | 4 | 27 | 33 | no |
| 4.1 task 5 B-3 | 1 | 1 | 22 | 30 | no |
| 4.1 task 11 A-3 | 7 | 0 | 5 | 18 | **yes** |
| 4.8 task 2 B-1 | 2 | 0 | 7 | 11 | no |
| 4.8 task 6 B-1 | 1 | 0 | 17 | 26 | no |
| 4.8 task 10 A-1 | 10 | 10 | 17 | 26 | no |
| 4.8 task 10 A-3 | 8 | 8 | 17 | 24 | no |
| 4.8 task 11 B-2 | 1 | 0 | 5 | 8 | **yes** |
| 4.8 task 12 B-3 | 8 | 8 | 11 | 18 | **yes** |

- **Task 11's passing runs follow one pattern** (read in 4.1 A-3 and 4.8 B-2):
  1. The model edits the error string at line 81, and the edit succeeds.
  2. At request 5 it sends the same edit again and is told "this text isn't in … the edit may already have been made".
  3. It reads the line and either sends the same edit again or finishes.

  The guard would have said "already applied at line 81" at step 2. That points the same way, more directly. What the
  model does after a different reply is not seen.
- **Every run that applied an edit again is a run the amended rule no longer stops for that edit** (4.6 task 5 B-2, 4.8
  task 10 A-1 and A-3, task 12 B-3). The guard refuses those calls instead. The registered rule had stopped them.

## What it means

- The amendment removes the one passing run the registered rule stopped, and stops nothing new.
- **It gives up four stops of runs that re-apply an edit.** Without the guard, those loops run until another stop or the
  budget. The guard and the amendment are two halves of one fix:
  - the amendment stops counting a changing file as a repeat;
  - the guard stops the change from repeating.
- The guard changes replies in 8 passing runs, so a replay cannot judge it. Whether it enters the bundle, and by what
  test, is the owner's decision (§12.14).
