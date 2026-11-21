# E1 screen of `summarize` and `summary_retrieve` (C12): the first 36 runs, 2026-11-21

**A partial read of a fixed 108-run screen. No verdict, and no early stop.** The registration
(`e1-summarize-screen-registration-2026-11-16.md`) fixes the screen at 108 runs and reads it only at the end, so the
numbers below decide nothing. Tasks 1 to 4 of 12 have run; tasks 5 to 12 (72 runs) are to run the nights of 2026-11-22 and 2026-11-23, in four
blocks of two tasks (`c12-launch.sh 12 5-6`, `7-8`, `9-10`, `11-12`). The launcher gained the last three ranges on
2026-11-21, its original kept as `c12-launch.sh.orig-2026-11-21`; a block is at most 360 minutes, the night queue's cap. Francis decided on 2026-11-21 that C12 continues.

## What ran

- **Runs:** tasks 1 to 4 of `eval/e1/calibration.jsonl`, 9 runs each in the registered order (A B C C B A A B C), 36 in
  all. No void cells. Two runs hit the 30-minute wall cap, both B (task 3 rep 1, task 4 rep 3), and count as fails.
- **When:**
  - Task 1 ran the night of 2026-11-10, its first run at 22:58 PST. The launcher then stopped after task 1's
    settings check. That check matched `read = ["~/models"]` as a regex, whose brackets made it fail on a correct
    config. It was fixed to a fixed-string match (`grep -qxF`), and the original was kept beside it as
    `c12-launch.sh.orig-2026-11-11`. Task 1's nine runs were all counted before the stop.
  - Tasks 2 to 4 ran 2026-11-17 22:08 PST to 2026-11-18 about 03:20 PST, exit 0.
- **Arms as run:** A is the bundle (`fit_window`, `stop_repeats`, `result_notes`, `instructions_head`,
  `refuse_duplicate_edits`, `verify_done`, `edit_tolerances`); B adds `summarize`; C adds `summary_retrieve`. Every row
  of the results carries its arm's settings, and they match the registration. The launcher's first-block check found
  the same in each run's `config.toml`, and found none of the other levers on.
- **Model and box:** Qwen3.6-35B-A3B `q4_k_m` on nobara at `-ctx 16384`, thinking off, `-moe-cache-experts`, sampling
  0.7 / 0.8 / 20, from `~/models`. The runs used tack `36d1027`. The cell check found the server cuda-resident (int4)
  with 0.6 GB of KV for the 16,384-token cap, and the Mac on AC power.
- **One deviation from the registration:** goinfer-serve is v0.24.1, not `00584c35`. The launcher records why: the
  summarizer is goinfer's `-fast` alias, and `00584c35` predates that alias, so on it B and C would have tested the
  fallback summarizer instead of the real one.
- **C's mechanism check (§18.2) passed:** the first C run that summarized (task 2, C-1) carries the retrieved section,
  5,237 and 5,997 bytes in its two summaries.
- **Raw data:** `docs/measurements/e1-summarize-screen-2026-11-10/results-tasks-1-4.jsonl` (36 rows, the pilot's
  `results.jsonl` as written). The Mac holds the rest, under `~/tack-runs/e1/c12-2026-11-10/s12/`: events, logs, the
  cell check and `cmech.txt`.

## Passes so far (reported, not read)

| task | A | B | C |
|---|---|---|---|
| 1 | 1/3 | 3/3 | 0/3 |
| 2 | 0/3 | 1/3 | 2/3 |
| 3 | 1/3 | 2/3 | 2/3 |
| 4 | 1/3 | 0/3 | 3/3 |
| **1–4** | **3/12** | **6/12** | **7/12** |

The registration's rule is read on 36 runs per arm (12 tasks × 3). These are 12 per arm. Its thresholds (+4 adopts,
+2 to +3 parks) are out of 36 and do not scale to a third of the screen. The per-task differences are noisy: B − A
runs +2, +1, +1, −1, and C − B runs −3, +1, 0, +3.

## Mechanism metrics so far (reported, never decide)

- **Stops:**

  | arm | done | repeat loop | window full (stop or the server's refusal) | max_tokens | wall cap |
  |---|---|---|---|---|---|
  | A | 0 | 7 | 5 (2 `window_full`, 3 prompt-too-long refusals) | 0 | 0 |
  | B | 6 | 1 | 0 | 3 | 2 |
  | C | 6 | 3 | 0 | 3 | 0 |

  - A's three prompt-too-long refusals were 16,392, 16,427 and 16,460 tokens against 16,384. These are the refusals
    design review §5.3 addresses (#212), which these runs predate.
  - One of them, task 4 A-3, passed anyway: it had made its edit before the refusal.
- **Summaries:** 11 of 12 B runs and 11 of 12 C runs logged at least one compaction with trigger `summary` (B: 2, 4,
  5, 6, 1, 5, 7, 5, 7, 0, 1, 9; C: 7, 7, 4, 2, 3, 3, 4, 5, 0, 2, 2, 6, per run in task and rep order).
- **Wall time:** A's runs averaged 3.4 minutes, B's 17.2 and C's 14.5; all 36 averaged 11.7 minutes, 7.0 hours in all.
  - The registration planned 108 runs at about 3.1 minutes each, one night. That figure is A's alone. A summarizing
    run keeps going where A stops on the window.
  - At this pace, the 72 runs left take about 14 hours, two nights. Splitting a fixed screen over nights adds no runs
    and changes no rule: the registration's stopping rule counts runs, not nights.
- **Against the registration's predictions** (reported only, from a third of the runs):
  - Summaries fired in nearly every B run.
  - B's window-full stops are 0 against A's 5.
  - The fallback rate is not read here. It needs the summary bodies, which are on the Mac.
