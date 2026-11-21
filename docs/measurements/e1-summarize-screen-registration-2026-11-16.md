# E1 screen of `summarize` and `summary_retrieve`: registration, 2026-11-16

**Status (2026-11-21):** 36 of the 108 runs are made, read as a partial with no verdict in
`e1-summarize-screen-partial-2026-11-21.md`. Francis decided on 2026-11-21 that C12 continues; tasks 5 to 12 are to run
the nights of 2026-11-22 and 2026-11-23, two tasks a block and two blocks a night. This page first said "Registered before any run. No run has been made",
which was wrong: task 1's nine runs were made the night of 2026-11-10 under §18's rule (drafted 2026-11-09), and tasks
2 to 4 the night of 2026-11-17. The rule, sizes and stopping rule below are unchanged.

This page closes P1 step 6's "E1 screen of summarize not registered" (`docs/design/p1-notes.md`). It registers calibration
pre-registration §18 (C12, drafted 2026-11-09) at the size below; §18 holds the mechanism checks and predictions and
stays the detail authority. Nothing here changes §18's rule or sizes.

- **Question.** Does summarizing compaction at 75% of the window (`[run] summarize`) raise passes on a model that fills
  its 16k window, and does re-retrieval after the summary (`[run] summary_retrieve`, 10% of the window) add to it?
- **Arms.** A is the bundle as adopted (`fit_window`, `stop_repeats`, `result_notes`, `instructions_head`,
  `refuse_duplicate_edits`, `verify_done`, `edit_tolerances`), masking off, snapshots on (ADR-006): the do-nothing arm.
  B is A plus `summarize`. C is B plus `summary_retrieve`. The 12 tasks in `eval/e1/calibration.jsonl`, 3 runs each per arm.
- **Metric.** Passes out of 36 per arm (the task's named tests, none skipped). sd = 2.0 per contrast, from §15.2's
  per-task rates at this model, window and run count. Mechanism metrics (summaries per run, fallback rate, fault rate
  after a summary, window-full stops, retrieved-section hits) are reported and never decide.
- **Rule, for each contrast alone.** B − A decides `summarize`; C − B decides `summary_retrieve`, and only if
  `summarize` was shown. C is never compared with A.

  | d (out of 36 each) | verdict |
  |---|---|
  | +4 or more (≥ 2 sd) | adopted: the setting goes on |
  | +2 to +3 | **parked**: stop and report; Francis decides on a second night |
  | −1 to +1 | not shown: stays off (ties go to the do-nothing arm) |
  | −2 or less | parked low: reported, stays off |

  Two contrasts at 2 sd each: about 4% that either is shown by chance. "Not shown" at 36 runs means not shown, not
  disproved.
- **Stopping.** Fixed at 108 runs (12 tasks × 9, order A B C C B A A B C per task, each after the cell check). No runs
  are added after seeing results. A cell started under heavy load (§15) is void, re-run in its place and listed. A night
  that ends early is listed, not counted, and re-run only with Francis's say. Stop and report if a B or C run's config
  lacks its setting, or the first C run that summarizes carries no retrieved section (§18.2).
- **Before the screen, tier 1 (minutes, by day):** `go run ./tools/summarycheck`: at least 4 of 5 real sessions'
  summaries keep the seven headings is PASS; 1 of 5 or fewer is FAIL and holds the screen; between is INCONCLUSIVE and
  parks it for Francis. Without the headings the screen would test the fallback.
- **Tier 2,** because the decision hinges on a small effect (about 4 passes of 36) that no tier-0 or tier-1 run can
  show; tier 0 already covers the mechanics (`loop.Summarize`, `loop.Rebuild` replay).
- **Model and box.** Qwen3.6-35B-A3B `q4_k_m` on nobara at `-ctx 16384`, goinfer `00584c35` with `-moe-cache-experts`
  and `-fast-alias` (the summarizer is the thinking-off alias), sampling 0.7 / 0.8 / 20, from `~/models`. The 35B is
  chosen because it fills its window (7 of 36 A runs stopped window-full, §15.2); a model that rarely passes 75% would
  test nothing. Holds nobara's timing lock; nothing else of tack's runs beside it.
- **Machine time:** 108 runs at about 3.1 minutes (§14.4's mean) plus summarizer calls and cell checks: 6 to 7 hours,
  one night on nobara. Over two hours, so a night job (run budget); the tier-1 check is minutes, by day.
- **Predictions (before any run):** summaries fire in at least half of B's runs that pass 75%; B's window-full stops are
  half of A's or fewer; fallback rate under 20%; B − A between −2 and +3 and C − B between −2 and +2, since the 35B's
  failures are mostly repeat loops (13 of 22) that a summary does not touch.
- **Order.** The design review (`docs/design/proposal-design-review-2026-11-09.md` §2.3) recommends screening the
  handoff first because it changes this screen's question. This page does not decide that order; Francis does.
