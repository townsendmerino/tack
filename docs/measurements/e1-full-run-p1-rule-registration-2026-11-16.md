# The full E1 run and the P1 rule: registration, 2026-11-16

Registered before any run. No run has been made, and nothing is queued: GPU time is approved separately by Francis. This
is the "full E1 run and the P1 rule" that DESIGN §1's evaluation row and §15.3 name. §15.3's wording is the authority;
this page fixes the numbers it leaves open, except D13, which is the owner's (below).

- **Question (P1 exit).** Is tack's loop (A1) competent: not worse than the established local agent (A0, opencode at a
  pinned version, its own clean configuration) by more than the margin *m*, on the same model, server and tasks?
- **Tasks.** The first N of `eval/e1/e1-order.jsonl` after the pilot, at most 30 per repo (selection pre-registration,
  amendment 4); each stated as an issue and reviewed for leakage by the owner. **N = 60 and *m* = 14 points** unless
  Francis picks D13's 120 tasks and *m* = 10 before the run. N is fixed before the first run.
- **Arms.** A0: opencode with the repo's instruction files unmodified. A1: tack as released, with the bundle as it ships
  at the registering commit (adopted settings only), snapshots on (ADR-006). Web tools off in both. A0k, A2, A3 are P2's.
  A0's possible window overflow is recorded as what happened (§15.1).
- **Runs.** 2 per task per arm, order A0 A1 A1 A0 per task, each after the cell check recording load and free memory
  (heavy-load cells void and re-run in place). No runs are added after seeing results. Cap 10 minutes per run; a failed
  run counts as the cap in capped time (PAR-1).
- **Metric and analysis.** Per task, the pass rate over its 2 runs (oracle: named tests all `pass` in `go test -json`;
  a skip fails). *d* = A1 − A0 per task, averaged over tasks; one-sided 95% lower bound by bootstrap over tasks (10,000
  resamples, fixed seed). Capped-time ratio and tokens, malformed calls, instruction-file violations are reported, not
  decisive. Results are broken down per repo.
- **Rule (§15.3's, with a parked band).**

  | read | verdict |
  |---|---|
  | lower bound ≥ −*m* | **P1 rule passes** |
  | −*m* − 4 ≤ lower bound < −*m* | **parked**: Francis decides: count as a miss, or buy D13's larger set |
  | lower bound < −*m* − 4 | **miss**: fix the loop, run the rule once more on the next full run; a second miss stops P2 until the owner decides (§15.3) |

  The 4-point parked band is about 0.7 standard error at 60 tasks (SE near 5.6 points, since *m* = 2.5 SE). A margin
  of one half-width would pass a truly equal arm about half the time, hence 2.5 SE.
- **Floor.** If A0's pass rate over all tasks is under 15%, there is no power to see A1 trail it and the run reads
  INCONCLUSIVE, never pass. The pilot's 20–80% band was the design; this floor guards the case where it was missed.
- **Model and box.** Qwen3.6-35B-A3B `q4_k_m` on nobara at 16,384 (the model §14 moved C6 to: Qwen3-8B passed 9 of 168
  at A, below any useful power), goinfer `00584c35` with `-moe-cache-experts`, sampling 0.7 / 0.8 / 20, from `~/models`;
  same server for both arms; the Mac drives the harness. If Francis picks a different model before the run, this page is
  amended and re-committed first. Holds nobara's timing lock; nothing else of tack's runs beside it.
- **Tier.** Tier 2: the P1 exit hinges on a difference of a few points, which no tier-0 or tier-1 run resolves. Tier 1
  (one A0 and one A1 smoke task, minutes, by day) runs first to prove both harnesses reach the oracle.
- **Machine time (estimates, no run made).** 60 tasks × 2 runs × 2 arms = 240 runs. At about 3.5 minutes each with cell
  checks, about 14 hours; worst case at the 10-minute cap, 40 hours. Over two hours, so night jobs (run budget): about 3
  nights of 5–6 hours on nobara, set by D10, a night job per whole night, each night the same recipe over disjoint tasks.
  A night that ends early is listed, not counted, and re-run whole. With N = 120 it is double.
- **Before it runs.** The 10-task A1 pilot has sized the variance (D13 closed); the run waits on that, on the owner's week
  of real use being scheduled, and on the full A0 harness passing its tier-1 smoke.
