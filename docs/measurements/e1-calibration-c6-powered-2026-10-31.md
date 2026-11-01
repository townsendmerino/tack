# E1 calibration C6 §12.25: the powered comparison, read, 2026-10-31

<!-- citations-at: 8ff336f -->

This is the read of `e1-calibration-preregistration-2026-10-18.md` §12.25: the bundle (B) against A1 with no
settings (A), on Qwen3-8B `Q4_K_M` at 16,384, goinfer `b2b5e816`, tack `f5f7ecc`, over two nights of 168 runs. It
is read under §12.25's rule and its amendment of 2026-10-31 17:17 PDT, which was committed before either night's
passes were tabulated.

## The nights

| night | when (PDT) | runs | per arm | void | oracle errors | cap hits |
|---|---|---|---|---|---|---|
| 1 | 2026-10-30 23:07 to 2026-10-31 04:02 | 168 | 84 A, 84 B | 0 | 0 | 3 |
| 2 | 2026-10-31 12:44 to 17:11, by day (§12.25's line) | 168 | 84 A, 84 B | 0 | 0 | 0 |

- Every run was on mains, and every counted run carries tack's summary.
- nobara's load at the cells' starts was 0.00–3.14, with 59.0–59.5 GB available.
- **The Mac's 1-minute load at a cell's start is recorded only; it is not a void rule.** It was 6 or more at four
  starts, all failures:
  - night 1 task 12 B-1, B-2 and A-2 (13.83, 9.43 and 6.97, at 03:48 PDT);
  - night 2 task 3 A-2 (7.17, 14:13 PDT). That start falls inside the gates tack's session ran on the Mac at
    nice 19 from 14:10 to 14:16 (recorded in the nights' `notes.txt`).
  - A run started during the session's other gate run (13:30–13:52) had a Mac load under 6.

## The rule's read

The command (§12.25's amendment) gives this, with the report in `~/tack-runs/e1/c6-powered-2026-10-30/calreport-both.md`:

```
go run ./tools/calreport -rule sd=3.18,n=168 -compare … $P/night-1/runs/results.jsonl,$P/night-2/runs/results.jsonl
```

| arm | passes | counted runs |
|---|---|---|
| A, A1 with no settings | 9 | 168 |
| B, the bundle | 26 | 168 |

**d = +17**, against edges of 3.18 (1 sd) and 6.36 (2 sd). **Under §12.25's rule, the bundle is shown to beat A1**
(+7 or more). Pass credit is the bundle's, not any one setting's (§12.9).

## Task by task

| task | A passes | B passes | A window-full | B window-full | B cap hits |
|---|---|---|---|---|---|
| 1 | 0/14 | 12/14 | 12 | 0 | 0 |
| 2 | 0/14 | 0/14 | 14 | 4 | 0 |
| 3 | 0/14 | 0/14 | 13 | 2 | 1 |
| 4 | 0/14 | 0/14 | 10 | 0 | 0 |
| 5 | 0/14 | 0/14 | 13 | 1 | 0 |
| 6 | 0/14 | 0/14 | 7 | 0 | 2 |
| 7 | 0/14 | 0/14 | 12 | 1 | 0 |
| 8 | 0/14 | 0/14 | 0 | 1 | 0 |
| 9 | 0/14 | 0/14 | 12 | 0 | 0 |
| 10 | 0/14 | 0/14 | 14 | 0 | 0 |
| 11 | 9/14 | 12/14 | 4 | 0 | 0 |
| 12 | 0/14 | 2/14 | 9 | 3 | 0 |
| **all** | **9/168** | **26/168** | **120** | **12** | **3** |

The +17 comes from task 1 (+12), task 11 (+3) and task 12 (+2). The other nine tasks pass in neither arm. A had no cap
hits.

## The predictions (made before any run)

| prediction | result | |
|---|---|---|
| A passes 8–20 of 168 | 9 | held |
| B passes 12–32 of 168 | 26 | held |
| d between +3 and +14 | +17 | missed, high |
| task 1: B at least 10 of 14, A at most 3 of 14 | B 12, A 0 | held |
| task 11: A at least 7 of 14, B within 4 of A | A 9, B 12 | held |
| window-full stops: A at least 100 of 168 | 120 | held |
| window-full stops: B at most 6 | 12 | missed |

## Reported, not decided on

- **Stops by reason:**

  | arm | done | repeat_loop | prompt-too-long | window_full | budget | max_tokens | cancelled |
  |---|---|---|---|---|---|---|---|
  | A | 33 | 0 | 100 | 20 | 13 | 2 | 0 |
  | B | 70 | 57 | 5 | 7 | 20 | 6 | 3 |

  B's 12 window-full stops are 5 prompt-too-long refusals and 7 `window_full` stops.
- **Requests per run:** A had a mean of 23.6 and a median of 10; B had a mean of 19.2 and a median of 18.
- **Wall time per run (median):** A 54 s, B 38 s.
- **`done`:** B ended with `done` 70 times. 43 of those carried tack's note that the last build or test after the
  last change failed, and 3 runs had a verification refusal. A ended with `done` 33 times.
- **Guard firings in B (§13.4):**
  - `result_notes` fired in 136 runs, 1,482 times;
  - `fit_window` fired in 59 runs, 160 times;
  - `stop_repeats` fired in 57 runs and ended each of them;
  - `refuse_duplicate_edits` fired in 25 runs, 40 times;
  - `verify_done` fired in 3 runs;
  - `edit_tolerances` fired in 1 run.
- **§2's bar:** A has 9 passes on 1 task and is below the bar. B has 26 passes on 3 tasks and is at or above it.
- **Beside §12.21's bundle job and C5:** see the report. §12.21's arms were A 3/36 and B 4/36; C5's A1 was 4/36.
