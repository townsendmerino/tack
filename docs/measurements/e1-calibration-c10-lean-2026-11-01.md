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

## The continuation and the combined read (read 2026-11-02)

Francis chose both at 13:47 on 2026-11-01: continue §16.1 with 72 more runs, and build lever 2 meanwhile. The
continuation's rule was registered before any of its runs (§16.1, "The continuation"): one read over all 144 runs,
sd 2.83, d ≥ +6 adopted, +3 to +5 parked, +2 or less not shown, with A winning ties.

**The runs, checked before the read.** 144 counted runs, 72 per arm: exactly six per task in each half, 0 oracle errors,
0 cap hits. The 43 void records are all cell checks refusing to start on battery; they are listed and count in neither
arm. `left_repo` names no path to the mirrors, the task file or another run's records.

**The read:**

| arm | passes | counted runs |
|---|---|---|
| A, the bundle | 25 | 72 |
| B, A plus `lean_results` | 29 | 72 |

**d = +4, against edges of 2.83 and 5.66. Under the combined rule this is parked: `lean_results` is not adopted, and
§16.2 runs against the bundle alone.**

| task | A | B | task | A | B | task | A | B |
|---|---|---|---|---|---|---|---|---|
| 1 | 4/6 | 2/6 | 5 | 0/6 | 0/6 | 9 | 0/6 | 0/6 |
| 2 | 0/6 | 0/6 | 6 | 0/6 | 0/6 | 10 | 4/6 | 6/6 |
| 3 | 2/6 | 1/6 | 7 | 0/6 | 0/6 | 11 | 6/6 | 6/6 |
| 4 | 1/6 | 2/6 | 8 | 2/6 | 6/6 | 12 | 6/6 | 6/6 |

The gain is on tasks 8 (+4), 10 (+2) and 4 (+1), less tasks 1 (−2) and 3 (−1). Task 8 is one of the four tasks whose
statement states part of its fix (§15, "The answer-leak audit"), a limit on absolute rates that both arms share.

**The mechanism metric, over all 144:**

| | A | B |
|---|---|---|
| runs that reached the "window nearly full" note | 53 of 72 | 44 of 72 |
| calls before the first note (mean, runs that reached it) | 8.4 | 13.3 |
| reads before it (mean) | 22.3 KB | 23.8 KB |
| grep and glob before it (mean) | 11.7 KB | 10.0 KB |
| `repeat_loop` stops | 25 | 22 |
| of those, the repeated call answered with the note | 24 | 22 |
| window-full stops (`window_full` plus prompt-too-long) | 26 | 25 |
| wall time, all runs | 222 min | 246 min |

As in the first half, the cliff came later and less often in B, with about the same 34 KB of results by then. Nearly every
repeat loop in both arms is still the model asking again at the cliff.

**The predictions (registered for the first 72 runs, set beside all 144):**

| prediction | result | |
|---|---|---|
| B's calls before the first note rise to 12 or more | 8.4 to 13.3 | held |
| B's repeat-loop stops fall by a third or more | 25 to 22 | missed |
| d between 0 and +4 | +4 | held, at the edge |

**What happened during the continuation; nothing was voided beyond the cell checks, and no result is affected:**
- **The Mac on battery, twice.**
  - From 18:24 to 18:53, task 9's cell checks voided 30 attempts, and the launcher stopped.
  - From 20:28 to 20:42 the cable was out: 13 more void attempts, which the pilot re-ran in place.
  - Task 9's block was re-run whole from 20:23, and no counted run came from the stopped attempt.
  - Task 9 B-1 started on AC at 20:27 and ended on battery. It counts, since §12.2 voids a run only on a battery start
    cell. It failed, as all task 9 runs did.
- **Launchers.** The first launcher stopped on any void record, so it stopped after task 9 at 20:56 with all six runs
  counted. Tasks 10–12 ran on `launch-v2.sh`, which checks each task's six slots instead.
- **Load on the Mac.** tack's session ran builds and gates at nice 19 from 14:22 to 16:08, which slowed task 1 (a mean of
  354 s a run against 246 s in the first half). No run reached the 30-minute cap; the longest took 14 minutes.
- **Load on nobara.** nobara-sb01 ran three short unlocked CPU checks, at 14:20–14:21 and 15:05:05–15:05:15 (both during a
  server load) and 17:05:40–17:06:09 (during task 7's second run). None used the card.
- **Command timeouts: none.** The text "timed out" appears in 3 of task 10's runs only as a repository document in grep
  output.
- All of this is in `~/tack-runs/e1/c10-lean-2026-11-01/notes.txt`. The report is `calreport-s161-combined.md` there.
