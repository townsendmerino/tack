# E1 calibration C10 §16.1: `lean_results` on the 35B at 16k, read 2026-11-01

<!-- citations-at: b10eb8f -->

This is the read of `e1-calibration-preregistration-2026-10-18.md` §16.1, under its rule and arms line.
- **Model and server:** Qwen3.6-35B-A3B `q4_k_m` on nobara at 16,384, `-moe-cache-experts`, goinfer `00584c35`.
- **Build:** tack `2f2870d`.
- **Arms:** A is the bundle; B is A plus `lean_results`. A B B A A B order, 72 runs.
- **The rule:** sd 2.0. d ≥ +4 is adopted, +2 to +3 parked, +1 or less not shown.
- **The command:** `go run ./tools/calreport -rule sd=2.0,n=36 …/s161/runs/results.jsonl`. The report is
  `~/tack-runs/e1/c10-lean-2026-11-01/calreport-s161.md`.

## The runs, checked before the read

- **When:** 2026-11-01, 08:55 to 13:11 PDT (night.py: ok in 4 h 16 min).
- **Records:** 72, with 36 per arm, 0 void, 0 oracle errors, 0 cap hits. The launcher exited 0.
- **The first block's checks passed:**
  - `lean_results` was in B's configs and not A's;
  - every config had `[sandbox] read = ["~/models"]`;
  - B's tools returned 80-line whole reads and stopped grep at 30 matches, where A's returned 200 and 100.
- **`left_repo` names no path to the mirrors, the task file or another run's records:**
  - paths the model invented (`/home/user/goinfer` and others);
  - `/tmp` scratch paths;
  - `/srv/models`;
  - one `cd ~/Projects/ken-mcp` in task 11 A-3, guessed by the model. The directory does not exist, and the shell
    answered "no such file or directory".

## The read: d = +2, parked

| arm | passes | counted runs |
|---|---|---|
| A, the bundle | 13 | 36 |
| B, A plus `lean_results` | 15 | 36 |

**d = +2. Under §16.1's rule this is parked: reported, with no more runs unless Francis says so.**

The +2 is all on task 8 (A 1/3, B 3/3). Every other task has the same passes in both arms: 10, 11 and 12 at 3/3;
1, 3 and 4 at 1/3; the other five at 0/3.

**Order.** §16.1 registered that lever 2 (§16.2) follows only on "not shown", and that a parked result stops for
Francis, so §16.2 is not started.

## The mechanism metric

| | A | B |
|---|---|---|
| runs that reached the "window nearly full" note | 26 of 36 | 21 of 36 |
| calls before the first note (mean, runs that reached it) | 8.0 | 12.2 |
| reads before it (mean bytes) | 20.5 KB | 24.3 KB |
| grep and glob before it (mean bytes) | 12.8 KB | 9.5 KB |
| `repeat_loop` stops | 12 | 10 |
| of those, the repeated call answered with the note | 12 | 10 |
| window-full stops (`window_full` plus prompt-too-long) | 13 | 13 |
| wall time, all runs | 105 min | 121 min |

- **The cliff came later and less often in B.** Runs that reached it got there after about 4 more calls.
- **They still reached it with about the same 33 KB of results.** The smaller results bought more calls, not more
  room: B read more, in smaller pieces.
- **Every repeat loop in both arms was still the model asking again at the cliff.**

## The predictions (made before any run)

| prediction | result | |
|---|---|---|
| B's calls before the first nearly-full note rise from about 9 to 12 or more | 8.0 to 12.2 | held |
| B's repeat-loop stops fall by a third or more against A's | 12 to 10 | missed |
| d between 0 and +4 | +2 | held |
