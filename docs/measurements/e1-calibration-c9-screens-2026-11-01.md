# E1 calibration C9: §15.2 (`mask_observations`) and §15.3 (`best_of`) on the 35B at 16k, read 2026-11-01

<!-- citations-at: d800cce -->

These are the reads of `e1-calibration-preregistration-2026-10-18.md` §15.2 and §15.3, under their arms lines of
2026-10-31.
- **Model and server:** Qwen3.6-35B-A3B `q4_k_m` on nobara at 16,384, `-moe-cache-experts`, goinfer `00584c35`.
- **Build:** tack `5212608`, main with the sandbox read fix, #34.
- **Arms:** A is the bundle as in §14.4, in A B B A A B order, 72 runs per screen.
- **The rule:** sd 2.0. d ≥ +4 is adopted, +2 to +3 parked, +1 or less not shown, with A winning ties.
- **The command:** `go run ./tools/calreport -rule sd=2.0,n=36 …/s15N/runs/results.jsonl`. The reports are
  `~/tack-runs/e1/c9-screens-2026-10-31/calreport-s152.md` and `calreport-s153.md`.

## The runs, checked before the read

| screen | when (PDT) | runs | per arm | void | oracle errors | cap hits | launcher exit |
|---|---|---|---|---|---|---|---|
| §15.2 | 2026-10-31 19:22 to 23:50 | 72 | 36 A, 36 B | 0 | 0 | 0 | 0 |
| §15.3 | 2026-11-01 00:00 to 06:05, in two halves (tasks 1–6, 7–12) | 72 | 36 A, 36 B | 0 | 0 | 0 | 0, 0 |

- **Each launcher's first-block checks passed:**
  - the setting was in B's configs and not A's;
  - every config had `[sandbox] read = ["~/models"]`;
  - in §15.3, every B run logged best_of's attempts.
- **`left_repo` names no path to the mirrors, the task file or another run's records.** What it does name:
  - the module cache, which the sandbox allows;
  - paths the model invented, such as `/home/user/goinfer` (19 runs);
  - `/tmp` scratch paths given as environment settings;
  - `/srv/models` once.
- **§15.3's A** was chosen by its launcher from §15.2's records, as registered: §15.2's d is −1, so A is the bundle
  without `mask_observations`.

## §15.2, `mask_observations`: d = −1, not shown

| arm | passes | counted runs |
|---|---|---|
| A, the bundle | 13 | 36 |
| B, A plus `mask_observations` | 12 | 36 |

**d = −1. Under §15.2's rule, masking is not shown, and the do-nothing arm wins.**

**Masking barely fired.**
- **How often:** 11 compactions in 7 of the 36 B runs.
- **How much:** the runs' masked results came to about 14.7 KB in all, and 8.1 KB of that was one run (task 11 B-1,
  18 results, 8.1 KB to 4.1 KB). The other six runs masked one or two results each, 358 bytes to 2.6 KB.
- **Why:** the 35B fills its window with grep output and whole first reads of files. When it reads again, the results
  already come back as `fit_window`'s short "window nearly full" notes. The rule as registered masks only a read that a
  later whole read covers and old command output, so there was almost nothing for it to act on (19:55 note in the
  screen's `notes.txt`).
- **So the screen did not test masking's effect on the window.** It shows that this rule finds little to mask on
  this model.

**The mechanism metric, window-full stops** (`window_full` plus prompt-too-long): A 7, B 12. B had more, not fewer.

**Predictions:**

| prediction | result | |
|---|---|---|
| window-full stops in B half of A's or fewer | A 7, B 12 | missed |
| passes up by 0 to +3 | −1 | missed, low |

Task by task: A 2/3, B 1/3 on task 1; A 1/3, B 0/3 on task 5; A 0/3, B 1/3 on task 4. Tasks 10, 11 and 12 are 3/3 in
both arms, task 8 is 1/3 in both, and the other five tasks are 0/3 in both.

**A wider rule, not started:** mask grep and glob output and whole first reads older than the last 3 requests, over
300 bytes, under the same 75% trigger and regrowth rule. It would need its own registration and Francis's yes.

## §15.3, `best_of`: d = +3, parked

| arm | passes | counted runs |
|---|---|---|
| A, the bundle, one attempt (the per-attempt rate) | 14 | 36 |
| B, A plus `best_of = 3` (any of up to 3, as the verifier keeps) | 17 | 36 |

**d = +3. Under §15.3's rule this is parked: reported, with no more runs unless Francis says so.**

**Task by task:**

| task | A | B | task | A | B | task | A | B |
|---|---|---|---|---|---|---|---|---|
| 1 | 1/3 | 2/3 | 5 | 1/3 | 0/3 | 9 | 0/3 | 0/3 |
| 2 | 0/3 | 0/3 | 6 | 0/3 | 0/3 | 10 | 3/3 | 3/3 |
| 3 | 0/3 | 2/3 | 7 | 0/3 | 0/3 | 11 | 3/3 | 3/3 |
| 4 | 1/3 | 1/3 | 8 | 2/3 | 3/3 | 12 | 3/3 | 3/3 |

**Attempts in B** (from each run's `stderr.log`, `best_of attempt` lines):
- **Per run:** 8 runs kept their first attempt, 4 their second, and 24 made all three. That is 88 attempts in all.
- **Verified:** 13 attempts.
- **The other 75 failed on their exit code, not on the build or test check:**
  - 48 ended in a repeat loop (exit 9);
  - 14 in an error (exit 1), each the server's prompt-too-long refusal;
  - 10 on a full window (exit 7);
  - 3 at max tokens (exit 4).
  - No attempt ended cleanly and then failed its tests.

**The mechanism metric, the verifier against the grader:**
- **False accepts: 0.** All 13 runs whose attempt was verified pass the grader.
- **False rejects: 4.** In 23 runs no attempt verified, and the last attempt that changed a Go file was kept and
  graded. The grader passes 4 of those (task 1, task 3 twice, task 4). Each such attempt had ended without exit 0.
  - The 4 count toward B's 17. So 13 of B's passes were verified, and 4 came from a kept attempt that the verifier
    could not accept.
  - Attempts reset before the run ended were never graded, as registered.

**Wall time:** A 127 minutes in all, a median of 199 s per run. B 281 minutes, a median of 569 s, about 2.2 times A.
No run reached the 90-minute cap; the longest took 14 minutes.

**Predictions:**

| prediction | result | |
|---|---|---|
| d between +2 and +6 | +3 | held |
| tasks at 1/3 (1, 3, 8) rise toward 2/3 | in this A, 1 at 1/3, 3 at 0/3, 8 at 2/3; in B, 2/3, 2/3, 3/3 | held |
| 0/3 tasks stay near 0 | 2, 6, 7, 9 at 0/3 in both | held |
| B's wall time 2–3 times A's | 2.2 times | held |

## Beside §14.4

The two screens' A arms ran the same bundle on the same build and passed 13 and 14 of 36; §14.4's 16k row passed 12
of 36 on `31a89c2`. Their runs are not pooled (§15's leak note: these runs are on the fixed sandbox).
