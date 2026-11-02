# E1 calibration C10 §16.2: `compact_at_cliff` on the 35B at 16k (2026-11-02)

The registration is §16.2 of `e1-calibration-preregistration-2026-10-18.md`, with its amendments and arms lines. Runs and
reports are in `~/tack-runs/e1/c10-cliff-2026-11-02/`: `s162/runs/`, `calreport-s162.md`, `mechanism-s162.tsv` (one row
per run) and `notes.txt`.

**Arms.** Qwen3.6-35B-A3B `q4_k_m` on nobara at 16,384, tack `38541a9`, A B B A A B per task, a 30-minute cap.
- A is the bundle plus `lean_results`, which is in the base by Francis's override (§16.1); its own read stays parked.
- B is A plus `compact_at_cliff`.

## The read

**The runs, checked before the read.** 72 counted runs, 36 per arm and six per task, all on AC, with 0 void, 0 cap hits
and no oracle errors. The first-block check passed at 08:36: `compact_at_cliff` in B's configs only, `lean_results` in
all, no other lever on. `left_repo` names no path to the mirrors, the task file or another run's records, and no tool
call in any run does: it lists guessed paths such as `/home/user/repo`, `/tmp/repo`, and a nonexistent `~/Documents/ken`.

| arm | passes | counted runs |
|---|---|---|
| A, the bundle plus `lean_results` | 16 | 36 |
| B, A plus `compact_at_cliff` | 17 | 36 |

**d = +1, against edges of 2 and 4. Under the registered rule this is not shown, and the do-nothing arm A wins:
`compact_at_cliff` is not adopted.**

| task | A | B | task | A | B | task | A | B |
|---|---|---|---|---|---|---|---|---|
| 1 | 2/3 | 2/3 | 5 | 1/3 | 0/3 | 9 | 0/3 | 0/3 |
| 2 | 0/3 | 0/3 | 6 | 0/3 | 0/3 | 10 | 3/3 | 3/3 |
| 3 | 0/3 | 2/3 | 7 | 0/3 | 0/3 | 11 | 3/3 | 3/3 |
| 4 | 2/3 | 3/3 | 8 | 2/3 | 1/3 | 12 | 3/3 | 3/3 |

## The mechanism metric (reported, never used for adoption)

| | A | B |
|---|---|---|
| `repeat_loop` stops | 13 | 5 |
| of those, the repeated call answered with the nearly-full note | 12 | 2 |
| window-full stops (`window_full` plus prompt-too-long) | 11 (5 + 6) | 14 (4 + 10) |
| `done` stops | 11 | 13 |
| runs that reached the nearly-full note | 25 | 21 |
| passes among runs that reached it | 6 of 25 | 5 of 21 |
| passes among runs that did not | 10 of 11 | 12 of 15 |
| calls before the first note (mean, runs that reached it) | 15.0 | 15.1 |
| calls after the first note (mean, runs that reached it) | 12.6 | 21.6 |
| runs with at least one edit call | 19 | 24 |
| wall time, all runs | 115 min | 185 min |

**Compactions, B only:**
- **Cliff:** 21, one in each of the 21 B runs that reached the note.
- **Stuck:** 3, one each in three of those runs.
- **Stubbed:** 124 results holding 345 KB, about 14 KB per compaction, as sized in their stubs. That is the size of what was
  stubbed, before counting the stubs themselves.
- **A:** no compaction in any A run.

**The three turns that continued past a would-be `repeat_loop`:**

| run | after the stuck compaction | how it ended |
|---|---|---|
| task 2, B-3 | 4 reads, 2 bash, 1 edit, `done` | `done`, failed the oracle |
| task 7, B-3 | 11 reads | `repeat_loop` |
| task 9, B-3 | 13 reads | `repeat_loop` |

**Where B's stops moved.** Repeat loops fell by 8 and `window_full` by 1. Prompt-too-long rose by 4, `done`
and `max_tokens` by 2 each, and budget by 1. Of B's 10 prompt-too-long stops, 9 failed; of A's 6, 4 did. These are counts at this size, set beside each other; the rule decides
nothing from them.

## The predictions

| prediction (the ninth amendment's) | result | |
|---|---|---|
| B's `repeat_loop` stops fall by two-thirds or more | 13 to 5, a fall of 62% | missed |
| d between 0 and +5 | +1 | held |
| B's wall time per run rises | 115 to 185 min over 36 runs | held |

The first registration also predicted compactions in most B runs that reach the note: all 21 did.

## What happened during the screen

Nothing here changes a count.
- **Before the first run.**
  - Two attempts stopped at nobara's lock (06:31, 06:47), held by nobara's own session.
  - Francis's override moved the arms at 07:00, and the launcher was edited at 07:31 on his direct word.
  - A job from 07:33 stopped at 07:37:45 before task 1, with 20 GiB free against the launcher's floor of 21. The coordinator
    cleared Go's build cache, under Francis's standing permission, and the empty directory is kept as `s162-stopped-0737`.
  - The re-queued job refused once on battery (07:48).
- **The runs.** The job started at 08:03, task 1 at 08:08, and tasks 1–12 were done at 13:50. The launcher waited on nobara's
  queue or lock between blocks at least four times (09:08, 09:45, 10:19, 12:07); no run was in progress during a wait.
- **Load on the Mac.** Four cells started with a 1-minute Mac load of 13 or more: task 3 B-2 (13.5), task 6 A-1 (22.4), task 6
  A-2 (17.1) and task 9 A-3 (18.1). The source was not identified. Mac load is recorded only (§12.2): the model runs on
  nobara, whose load stayed at or under 4.03. No run reached the cap.
- **The longest run.** Task 8 B-2 made 105 calls in 988 s, ended on `window_full`, and passed.
- **"Timed out"** appears only in task 8 B-2, as a test's own `panic: test timed out after 30s`. No command timed out.
- **calreport's guard table** lists `compact_at_cliff` as "prompt only", because it does not count this setting's
  firings. The compaction counts above come from the runs' `compaction` events.
