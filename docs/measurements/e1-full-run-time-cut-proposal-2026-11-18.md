# The full E1 run and the P1 rule: a proposal to cut machine time to about two hours, 2026-11-18

<!-- citations-at: ffd8094 -->

**Status: Francis chose Option S on 2026-11-18, about 15:38.** The registration `e1-full-run-p1-rule-registration-2026-11-16.md` carries it as
Amendment 1; this page is the costing behind it. Nothing here was run, and nothing is queued: every number is arithmetic over records already in
`docs/measurements`. The model is a parameter (see "The model line").

Francis, 2026-11-18: "let's try to get machine time down to a couple of hours, whatever that takes." The registered run is 60 tasks ×
2 runs × 2 arms = 240 runs, about 14 hours expected and 40 at the 10-minute cap.

## The short answer

- **Two hours buys about 34 to 40 runs at the 35B's speed.** A run is about 3 to 3.5 minutes. That is 17 to 20 tasks with one run
  per arm.
- **At that size the margin *m* is about 29 points, not 14.** *m* = 2.5 standard errors, and the standard error of *d* at 20 tasks and
  one run per arm is about 11.7 points. No combination of the levers below gets *m* under about 25 while keeping expected time near
  two hours.
- **So the two-hour run is a screen, not §15.3's rule.** A pass says A1 is not more than about 29 points below A0. If A0 passes 37%, that
  allows A1 near 8%. A miss says A1 is broken, as the pilot's 0 of 20 was. It does not say "competent" in the sense §15.3 and the
  registration use, whose margin is about 14.
- **The recommendation (Option S)** is 20 tasks, one run per arm, alternating order, a 6-minute cap (if the check below passes) and the
  existing floor. It is about 2.3 hours expected (2.0 at 3 minutes a run) and at most 4.0 hours.
- **Option T keeps the competence claim.** It is the same first 20 tasks, then a pre-registered continuation to 60 tasks (7 hours in all)
  for *m* = 17. Francis picks between them.

## The records used

All at 16,384 on nobara with the bundle (the shipped A1), Qwen3.6-35B-A3B `q4_k_m`:

| record | what it gives |
|---|---|
| `e1-calibration-c9-screens-2026-11-01.md` | §15.3's A arm, 36 runs: 127 minutes, median 199 s a run (mean 212 s); per-task passes; longest run in the screen 14 minutes |
| `e1-calibration-c10-lean-2026-11-01.md` | the A arm over 72 runs: 105 and 222 minutes for 36 and 72 runs (175 and 185 s a run); no cap hit in 144 runs; longest 14 minutes |
| `e1-calibration-c8-qwen36-2026-10-30.md` | the 16k set of 9 runs: passes at 54, 66, 93, 214, 91, 139 and 231 s, failures at 164 to 167 s; first-token time median 5.8 s, p90 32.9 s (the 32k set, with 500 to 900 s runs, is not what E1 runs) |
| `e0-nobara-qwen36-35b-2026-10-30.md` | decode 51.5 tok/s; cold prefill 110 to 150 tok/s |
| `e1-calibration-c10-cliff-2026-11-02.md` | the longest passing run seen at 16k, a B-arm run: 105 calls, 988 s |

**Mean cost of a run: 3.0 to 3.5 minutes** (175 to 212 s measured, plus the cell check and the oracle). The registration's 3.5 is the
upper end of it. Qwen3-8B's one-night screen (`e1-calibration-c11-onenight-2026-11-07.md`) took 103 s a run, so the model sets this
figure and the proposal keeps it a parameter.

## The variance model, and where it comes from

Time is easy. Precision is the part that decides what the rule can say, so the model is stated.

For each task, *d*ᵢ = (A1's pass rate) − (A0's pass rate) over its *r* runs per arm. Its variance over tasks is

> V(*r*) = 2*b*(1 − ρ) + 2*w* / *r*

with *b* the variance between tasks of a task's true pass probability, *w* the mean within-task variance of one run (Bernoulli noise,
mean of *q*(1 − *q*)), and ρ the correlation between the two arms' task probabilities. SE(*d*) = √(V(*r*) / *N*) for *N* tasks.

- ***w* and *b*** come from the three bundle-arm replicates on the 12 calibration tasks at 16k (C9 §15.2's A, C9 §15.3's A, C10's A), nine
  runs a task, 40 passes in 108 runs (mean 0.370). Per-task passes out of 9: 4, 0, 1, 2, 2, 0, 0, 4, 0, 9, 9, 9. The unbiased
  within-task variance averaged over tasks is **w = 0.088**. The variance of one run is 0.370 × 0.630 = 0.233, so **b = 0.145**.
- **ρ is not measured** (there is no A0 on this model yet). The registration's own figure, SE near 5.6 points at 60 tasks with 2 runs, fixes
  it: V(2) = 60 × 0.056² = 0.188, so 2*b*(1 − ρ) = 0.100 and **ρ ≈ 0.65**. Then **V(2) = 0.188 and V(1) = 0.276**.
- **Sensitivity to ρ** (V(1) = 2 × 0.145 × (1 − ρ) + 0.176): ρ = 0.5 gives 0.321, 0.65 gives 0.276, 0.8 gives 0.234. At 20 tasks that is SE
  12.7, 11.7 and 10.8 points, and *m* 31.7, 29.4 and 27.0.
- **The assumption that matters:** A0 (opencode on this model) is taken to have the same *w* and *b* as A1, and tasks are as spread as the 12 calibration
  tasks (4 of 12 never passed, 3 always did). The 60 E1 tasks may be better or worse spread. The floor (below) and the
  per-repo breakdown are the guards; the pilot-sized variance (D13) is what replaces this model before the run.

**Why one run per arm is right.** For a fixed number of runs *R* = 2*Nr*, V(*r*) × 2*r* / *R* = (0.2*r* + 0.352) / *R*, which rises with *r*.
More tasks beat more repeats at every size: 80 runs as 40 tasks × 1 run give SE 8.3, and as 20 tasks × 2 runs give 9.7.

## Each lever: its effect on time and on power

Time is *R* × 3.5 minutes unless stated. Power is SE(*d*), the margin *m* = 2.5 SE (which passes a truly equal arm about 80% of the time,
and a truly worse-by-*m* arm 5% of the time), and the parked band of 0.7 SE.

| design | tasks × runs × arms | runs | expected time | worst case at the 10-min cap | SE(*d*) | *m* |
|---|---|---|---|---|---|---|
| registered | 60 × 2 × 2 | 240 | 14.0 h | 40 h | 5.6 | 14.0 |
| (a) one run per arm | 60 × 1 × 2 | 120 | 7.0 h | 20 h | 6.8 | 17.0 |
| (a) with (b) | 40 × 1 × 2 | 80 | 4.7 h | 13 h | 8.3 | 20.8 |
| | 30 × 1 × 2 | 60 | 3.5 h | 10 h | 9.6 | 24.0 |
| | 24 × 1 × 2 | 48 | 2.8 h | 8 h | 10.7 | 26.8 |
| **Option S** | **20 × 1 × 2** | **40** | **2.3 h** (2.0 at 3.0 min) | 6.7 h; **4.0 h at a 6-min cap** | **11.7** | **29.4** |
| | 17 × 1 × 2 | 34 | 2.0 h | 5.7 h; 3.4 h at 6 min | 12.7 | 31.9 |
| | 12 × 1 × 2 | 24 | 1.4 h | 4 h; 2.4 h at 6 min | 15.2 | 37.9 |
| (b) alone | 20 × 2 × 2 | 80 | 4.7 h | 13 h | 9.7 | 24.3 |

- **(a) one run per arm, order alternating across tasks.** Halves the time and costs about 21% in SE (6.8 against 5.6 at 60 tasks). The
  registered A0 A1 A1 A0 per task becomes A0 A1 on odd tasks and A1 A0 on even tasks, so a drift in the box over the run falls evenly on the
  arms. It is the best lever per run, for the reason above.
- **(b) fewer tasks, the first *N* of `eval/e1/e1-order.jsonl` after the pilot.** *N* is fixed before the first run, and the first *N* of a
  fixed order is not a selection by outcome. This is the lever that sets *m*: *m* grows as √(1/*N*).
- **(c) a group-sequential look.** Worth little here; the numbers are in their own section below.
- **(d) a shorter cap.** Changes the worst case and almost nothing else. 16k runs of the bundle are not slow: the failures stop on a
  repeat loop or a full window at about 14.7 to 15k tokens (164 to 167 s in C8's 16k set), and the passes in that set took 54 to 231 s. A
  6-minute cap would not have cut any of them. It would have cut the occasional 8 to 16-minute run in the lean and cliff arms (988 s
  for the longest pass), which are not the shipped loop. The worst case falls from 10 to 6 minutes a run, 40% (6.7 h to 4.0 h at Option S).
  **The rule that picks the cap** is the smallest of 4, 5, 6, 8 and 10 minutes that keeps at least 95% of the passing A-arm runs of C9 §15.3,
  C10 and C8's 16k set (`go run ./tools/calreport` over their `results.jsonl`, a read, no run). **It was checked 2026-11-18 with calreport at f2d0e61: 6
  minutes keeps 64 of 67 passing bundle-arm runs (95.5%); 5 minutes keeps 63 (94.0%); 8 keeps all 67.** The margin is one run: the A arms alone, without
  C8's 12, would pick 8. The check is to be re-run on the chosen model's own records once the model is fixed: Qwen3-Coder decodes at 30 tok/s against the
  35B's 51.5, so its runs are longer. (The record is `~/tack-runs/e1/c13-qwen3coder-2026-11-18/cap-check-f2d0e61.md` on the Mac.) Two cautions: a cap is the same for A0, and opencode may be slower, so a short cap may fail A0 runs that would
  have passed, which would flatter A1. The tier-1 A0 smoke's time to pass is the check: the cap should be at least 1.5 times it. The
  per-arm cap-hit counts are reported either way. The pass rate then means "passes within the cap".
- **(e) warm prefix and smaller prompts.** Small. Decode is the cost: about 190 s over 20 requests is about 9.5 s a request, and at 51.5
  tok/s a 300 to 450-token reply is 6 to 9 s, with the new tail's prefill the rest. The fixed prompt (about 1,300 tokens) plus the first
  injected context is cold once per run, at 110 to 150 tok/s: about 10 to 25 s of a 190 s run, 5 to 12%. Running the A1 runs of
  neighbouring tasks together would warm the fixed prefix, but the instruction files differ by repo and the alternating order would
  break. **Not counted: 0 to 5%.**
- **(f) anything else, from the records.**
  - *Two decode slots* (A0 and A1 of one task together) would help a memory-bound MoE decode, and pass rate is the decisive metric, so
    timing contention would not change what is measured. But nothing in the records measures goinfer with two slots on this card, so
    **it is not in the numbers.**
  - *The cell check and warm-up* are minutes in a night (C11: one 15 s cell check and a 3-minute warm-up before 96 runs), and the oracle
    is seconds.
  - *Picking tasks both arms can pass* would cut variance and is a selection effect: it changes which tasks are measured. **Not proposed.**

## (c) the group-sequential look

A look cannot shorten the worst case, and with *m* fixed at its final value it stops early only when the answer is far from the margin.
With a maximum of 24 tasks (*m* = 26.8, SE 10.7) and one look at 12 tasks (SE 15.2), two equally spaced looks and boundaries of the
O'Brien-Fleming kind, one-sided 5% (about 2.37 at the look and 1.68 at the end; exact values from a standard Lan-DeMets routine before it is
registered):

- **Pass at the look** if the lower bound at 12 tasks is at least −26.8, with z = 2.37: that needs *d* ≥ +9.0 points at 12 tasks.
  Probability of stopping there is about 28% if the arms are truly equal, 53% if A1 is 10 points better, 11% if A1 is 10 points worse,
  under 1% at the margin.
- **Miss at the look** needs *d* below about −70: it cannot happen at any plausible gap, so there is no early miss.
- **Expected runs** are 41 if the arms are equal (2.4 h) and 35 if A1 is 10 points better (2.1 h), against 48 (2.8 h) without the look.
  **A saving of 14 to 26%, bought with a stricter early boundary and a more complicated rule.** I do not recommend it in Option S. It makes
  sense only if Francis wants the extra four tasks.

## Option S: the recommended two-hour screen

Replaces the registration's Tasks, Runs, Rule, Floor and Machine time lines, in this form:

- **Tasks.** The first **20** of `eval/e1/e1-order.jsonl` after the pilot, at most 30 per repo, each reviewed for leakage as before. *N* = 20
  is fixed before the first run.
- **Runs.** **One per task per arm**, A0 A1 on odd tasks and A1 A0 on even tasks, each after the cell check (heavy-load cells void and re-run
  in place). Cap **6 minutes**, if the check above keeps at least 95% of passing A-arm runs; else the smallest listed cap that does, and the
  worst case is then 40 runs × that cap. A failed run counts as the cap in capped time. No runs are added after seeing results.
- **Metric and analysis.** Unchanged: per-task *d* = A1 − A0 (each a 0 or 1 pass), averaged over tasks; one-sided 95% lower bound by bootstrap over
  tasks (10,000 resamples, fixed seed). Results per repo.
- **Margin and rule.** ***m* = 29 points** (2.5 SE at SE 11.7 and ρ = 0.65; re-derived from the pilot's variance before the run, as D13 says, and
  never changed after). The parked band is 8 points.

  | read | verdict |
  |---|---|
  | lower bound ≥ −29 | **P1 screen passes** |
  | −37 ≤ lower bound < −29 | **parked**: Francis decides: count as a miss, or continue to Option T's 60 tasks |
  | lower bound < −37 | **miss**: fix the loop, run the screen once more; a second miss stops P2 until the owner decides |

- **Floor.** Unchanged: if A0's pass rate over the 20 tasks is under 15% (fewer than 3 passes), the run reads INCONCLUSIVE, never pass.
- **Machine time.** 40 runs: about 2.3 hours (2.0 at 3.0 minutes a run), at most 4.0 hours at a 6-minute cap. A night job, run budget rule
  (the run-budget rule sends jobs over about two hours to the night; this one is at that line, so by day only if Francis says so): one night on nobara.
- **What a pass and a miss can say.**
  - *A pass* excludes, at 95% confidence, a gap larger than 29 points. With A0 near 37% (the bundle's level on the calibration tasks) A1 could still
    be near 8%. It says the loop is not broken. **It does not say "competent" in §15.3's sense**, whose margin is about 14 points (about 10
    with D13's 120 tasks); §15.3 itself puts *m* at 2.5 SE and offers only those two sizes.
  - *A miss* (lower bound below −37) says A1 is worse than A0 by about 37 points or more: with A0 near 37%, A1 near zero. That is the
    pilot's failure (0 of 20), found in two hours and not fourteen.
  - *Parked* is the band between, where the screen cannot tell.
  - Power is symmetric: a truly equal A1 passes about 80% of the time; one truly worse by 14 points passes about 37%; one worse by 29 passes 5%.

  The two-hour run therefore **needs a new name: "the P1 screen"**, and §15.3's P1 exit ("is the loop competent?") would either be re-worded to
  mean this, or stay with Option T. That is Francis's call, and I have not made it.

## Option T: the screen first, then the competence run

If the competence claim stays at *m* of about 14 to 17:

- **Stage 1** is Option S: tasks 1 to 20, 40 runs, about 2.3 hours. Verdicts as above.
- **Stage 2** is tasks 21 to 60 at one run per arm, 80 more runs, about 4.7 hours, if Francis continues. All 60 tasks then give SE 6.8 and
  ***m* = 17**, for 7.0 hours in all (20 hours at the 10-minute cap, 12 at 6).
- **The analysis of record** is the 60-task test with its own rule (the registered table at *m* = 17, parked band 5 points), not a re-reading
  of stage 1. Stopping or continuing after a stage-1 look has to be part of the registration, or the 60-task bound is a little too
  optimistic; the proper way is a two-look group-sequential design with a stricter bound at 20 tasks (a z of about 2.8 to 3, set by the 20-of-60 spacing),
  under which stage 1 can almost never end the run early by passing (it would need A1 about 17 points ahead). **This option therefore means
  "plan for 7 hours and be glad if it ends at 2",** and I would register it as a two-look design with the exact boundaries before the run.

## The model line

The registration names Qwen3.6-35B-A3B. If today's C9 calibration on Qwen3-Coder-30B-A3B (the Mac, this evening) beats it, E1 may move to
that model, and every number above moves with the model through three inputs. **Leave the model a parameter**, and re-derive from the Coder's
C9 records before registering:

1. ***t̄*, the mean minutes a run** (A-arm wall time over its runs, plus about 15 seconds). It sets the runs two hours buy: *R* = 120 / *t̄*, so
   *N* = *R* / 2 at one run per arm: *t̄* = 2 min gives 60 runs and 30 tasks (SE 9.6, *m* 24); 3 gives 40 and 20 (11.7, 29); 3.5 gives 34 and 17
   (12.7, 32); 5 gives 24 and 12 (15.2, 38).
2. ***w* and *b***, from the model's per-task pass counts on the 12 tasks (replicates pooled as above). A model that passes more tasks at
   mid-range rates has a larger *w* and a larger SE at the same *N*; one that passes all or none has smaller.
3. **The cap**, by the 95%-of-passing-runs rule on the model's own records.

`go run ./tools/calreport` already reads the wall times and the per-task counts; nothing needs a new run.

## What I did not do

- No run, no queue entry, and no change to the registration page.
- I did not compute exact group-sequential boundaries; the values above are the standard O'Brien-Fleming ones to two decimals and are to be
  recomputed before they are registered.
- ρ, and A0's *w* and *b*, are assumptions until A0 has run on this model. The rule's margin should be re-derived from the pilot's variance
  before the run.
- The 6-minute cap was checked on the 35B's bundle-arm records, with a margin of one run (see the cap paragraph above); it must be checked again on the chosen model's own.
