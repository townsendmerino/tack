# The P1 screen (the full E1 run and the P1 rule): registration, 2026-11-16, amended 2026-11-18

Registered before any run. No run has been made, and nothing is queued: GPU time is approved separately by Francis. This
is the "full E1 run and the P1 rule" that DESIGN §1's evaluation row and §15.3 named; **since Amendment 1 (2026-11-18) it is called the
P1 screen**, and §15.3 and §16 say so. This page fixes the numbers §15.3 leaves open.

**Amendment 1, 2026-11-18, by Francis's choice.** On 2026-11-18, about 15:28, Francis asked for machine time "down to a couple of hours,
whatever that takes". The proposal `e1-full-run-time-cut-proposal-2026-11-18.md` costed the levers, and at about 15:38 he chose its
**Option S**. The lines below are Option S's. What each replaced, and why, is under "What the amendment changed". The registered
numbers of 2026-11-16 (60 tasks, 2 runs, *m* = 14, about 14 hours) are not in force.

- **Question (the P1 screen).** Is tack's loop (A1) not badly worse than the established local agent (A0, opencode at a pinned
  version, its own clean configuration), on the same model, server and tasks: is the gap, A0 − A1, smaller than the margin *m*? **This is
  a screen for a broken loop. It is not a test of competence at 14 points** (see "What a pass and a miss say").
- **Tasks.** The first **20** of `eval/e1/e1-order.jsonl` after the pilot, at most 30 per repo; each stated as an issue and reviewed for
  leakage by the owner. **N = 20**, fixed before the first run. The first N of a fixed order is not a selection by outcome.
- **Arms.** A0: opencode with the repo's instruction files unmodified. A1: tack as released, with the bundle as it ships at the
  registering commit (adopted settings only), snapshots on (ADR-006). Web tools off in both. A0k, A2, A3 are P2's. A0's possible window
  overflow is recorded as what happened (§15.1).
- **Runs.** **One per task per arm**, 40 in all. The order alternates across tasks: A0 then A1 on the odd tasks, A1 then A0 on the even
  tasks, so a drift in the box falls evenly on both arms. Each run follows the cell check recording load and free memory (heavy-load cells
  void and re-run in place). No runs are added after seeing results. **Cap 6 minutes per run, provisional** (below); a failed run counts
  as the cap in capped time (PAR-1). The same cap holds for both arms.
- **Metric and analysis.** Per task, the run's pass (oracle: named tests all `pass` in `go test -json`; a skip fails), as 1 or 0. *d* =
  A1 − A0 per task, averaged over tasks; one-sided 95% lower bound by bootstrap over tasks (10,000 resamples, fixed seed). Capped-time
  ratio and tokens, malformed calls, instruction-file violations, and **each arm's cap hits** are reported, not decisive. Results are broken
  down per repo.
- **Margin and rule.** ***m* = 29 points**, which is 2.5 standard errors (about 80% power for a truly equal arm) at the standard error
  of *d* the proposal derived for 20 tasks with one run per arm, about 11.7 points. It comes from V(1) = 2*b*(1 − ρ) + 2*w* and SE =
  √(V(1) / 20), where *w* and *b* are re-measured from the 10-task A1 pilot (the proposal's 0.088 and 0.145 are from the 12 calibration tasks)
  and ρ = 0.65 is the registration's own figure, kept. ***m* is the rounded 2.5 SE, committed with the pilot's *w* and *b* before the
  first run, and never changed after.** If the pilot moves it by less than 2 points it stays 29.

  | read | verdict |
  |---|---|
  | lower bound ≥ −*m* (−29) | **P1 screen passes** |
  | −*m* − 8 ≤ lower bound < −*m* (−37 to −29) | **parked**: Francis decides: count as a miss, or buy the 60-task continuation (below) |
  | lower bound < −*m* − 8 (below −37) | **miss**: fix the loop, run the screen once more on the next 20 tasks; a second miss stops P2 until the owner decides (§15.3) |

  The 8-point parked band is 0.7 standard error, as before (4 points at 60 tasks with 2 runs).
- **What a pass and a miss say.**
  - **A pass** says that, with 95% confidence, A1 is not worse than A0 by more than about 29 points. With A0 near 37% (the bundle's level on
    the calibration tasks), A1 could still be near 8%. **The screen rules out a gap over about 29 points. It does not establish competence
    at 14 points**, §15.3's original margin for the P1 exit.
  - **A miss** says A1 trails A0 by about 37 points or more: with A0 near 37%, A1 near zero. That is the failure of the pilot's 0 of 20,
    found in about two hours.
  - **Parked** is the band between, where 20 tasks cannot tell.
  - Power: a truly equal A1 passes about 80% of the time; one truly worse by 14 points about 37%; one worse by 29, 5%.
  - **P1's exit** is therefore this screen **plus the owner's week of real use** (DESIGN §15.3, §16). The week is what speaks to
    competence at the margin the screen cannot reach.
- **Floor.** If A0's pass rate over the 20 tasks is under 15% (fewer than 3 passes), there is no power to see A1 trail it and the run reads
  INCONCLUSIVE, never pass. The pilot's 20–80% band was the design; this floor guards the case where it was missed.
- **Cap: 6 minutes, provisional.** It is final only when two checks are committed before the first run:
  1. **At least 95% of the passing A-arm runs of C8's 16k set, C9 §15.3 and C10 finished inside it.** The 16k bundle runs on the page records
     do not contradict it (C8's passes took 54 to 231 s, the A arms' mean run 175 to 212 s) but those give totals and medians, not each
     passing run's time, so this is **not yet checked**. The result files are `~/tack-runs/e1/c8-qwen36-2026-10-30/`, `…/c9-screens-2026-10-31/` and
     `…/c10-lean-2026-11-01/` on the Mac or nobara; they were not reachable from mbp2 on 2026-11-18. `go run ./tools/calreport` over them prints
     wall time per arm as a total, a median and a maximum, **not the passing runs' percentiles**, so the check needs one added line
     (the passing runs' wall: median, p90, p95, max) or an equivalent read, with a tier-0 test. If 6 minutes keeps fewer than 95%, the cap is the
     smallest of 8 and 10 minutes that does.
  2. **The cap is at least 1.5 times the tier-1 A0 smoke's time to pass**, since opencode may be slower and a short cap would fail runs A0
     would have passed, which flatters A1.
  The worst case is then 40 runs × the cap: 4.0 hours at 6 minutes, 6.7 at 10.
- **Model and box.** **The model is a parameter, fixed before the run.** The registered model is Qwen3.6-35B-A3B `q4_k_m` on nobara at 16,384
  (the model §14 moved C6 to: Qwen3-8B passed 9 of 168 at A, below any useful power), goinfer `00584c35` with `-moe-cache-experts`, sampling
  0.7 / 0.8 / 20, from `~/models`. **E1 may move to Qwen3-Coder-30B-A3B** if its C9 calibration (on the Mac, the evening of 2026-11-18) beats the 35B's.
  Before the run, this page is amended and re-committed with the model that runs, and with the three things that move with the model: the
  mean minutes a run (which sets what two hours buys: 120 divided by it, halved, is the tasks), *w* and *b* (and so *m*), and the cap
  (by the 95% rule above, on that model's records). If the model changes, *N* may change with it; the rule's form does not. Same server for
  both arms; the Mac drives the harness. Holds nobara's timing lock when the model runs there; nothing else of tack's runs beside it.
- **Tier.** Tier 2: the screen hinges on a gap of tens of points, which no tier-0 or tier-1 run resolves. Tier 1 (one A0 and one A1 smoke
  task, minutes, by day) runs first to prove both harnesses reach the oracle.
- **Machine time (estimates, no run made).** 40 runs at 3.0 to 3.5 minutes each with cell checks: **about 2.3 hours** (2.0 at 3.0
  minutes), at most **4.0 hours** at the 6-minute cap. The run-budget rule sends jobs over about two hours to the night, and this is at that
  line: **one night job on nobara** (the box that must run it), unless Francis says to run it by day. A night that ends early is listed, not
  counted, and re-run whole.
- **The 60-task continuation (the parked band's way out).** If the screen parks, or Francis wants the competence margin, tasks 21 to 60
  run at one run per arm, 80 more runs, about 4.7 hours. All 60 tasks then give a standard error near 6.8 and a margin near 17. It is a
  separate registration, written before it runs, with the screen's 20 runs inside it and a two-look group-sequential boundary stricter at
  20 tasks than at 60; it is not authorised by this page.
- **Before it runs.** The 10-task A1 pilot has sized *w* and *b* (D13 closed for P1); the tier-1 A0 smoke passed; the cap checks above are
  committed; the model is fixed; and Francis has given his word on the night.

## What the amendment changed

| line | registered 2026-11-16 | Amendment 1, 2026-11-18 | mechanism |
|---|---|---|---|
| tasks *N* | 60 (120 if D13) | 20 | machine time to about two hours: 40 runs |
| runs per task per arm | 2, order A0 A1 A1 A0 | 1, order alternating across tasks | more tasks beat more repeats at every size (proposal, "Why one run per arm is right") |
| margin *m* | 14 (10 at 120) | 29 | 2.5 SE at 20 tasks, 1 run per arm: SE 11.7 |
| parked band | 4 points | 8 points | 0.7 SE |
| floor | A0 under 15% | A0 under 15%: fewer than 3 of 20 passes | unchanged in rate |
| cap | 10 minutes | 6 minutes, provisional | worst case 4.0 hours; the 95% check decides |
| machine time | about 14 h, worst 40 h | about 2.3 h, worst 4.0 h | the above |
| name | the P1 rule | the P1 screen | the screen does not establish competence at 14 points |
| model | Qwen3.6-35B-A3B | a parameter, fixed before the run | Qwen3-Coder-30B-A3B's C9 |
| P1's exit | the P1 rule | the P1 screen and the owner's week of real use | DESIGN §15.3, §16 |

The arms, the metric, the analysis, the floor's rate, the tier, the box and the one-sided 95% bootstrap are unchanged. Records cited are in the
proposal.
