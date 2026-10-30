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

## The runs

To follow.
