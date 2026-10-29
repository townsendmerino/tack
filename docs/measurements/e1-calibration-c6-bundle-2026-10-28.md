# E1 calibration C6: the bundle against A1, at screen size, 2026-10-28

<!-- citations-at: 5d54f3c -->

This is the bundle comparison under amendment C6 (`e1-calibration-preregistration-2026-10-18.md` §12.21, registered
2026-10-27 before the run). A is A1 as C5 ran it, with no `[run]` settings. B is A plus `fit_window`, `stop_repeats`,
`result_notes`, `instructions_head`, `refuse_duplicate_edits` and `verify_done`. The two arms ran interleaved, A B B A A
B per task.

**Verdict, as registered: not shown, d = +1.** B passed 4 of 36 and A passed 3 of 36. §12.21's rule (§12.3's) reads the
bundle as beating A1 at d ≥ +3 and parks it at d = +2. At +1 or less the do-nothing arm wins ties. The parked row's
3 more runs per task are not triggered, so the comparison stops here.

- **Pass credit is the bundle's** (§12.9), and no change's share is read from it.
- **0 cells void.**
- **The riding check holds:** replaying the stop rule over B's runs stops exactly the 16 B runs that ended live with
  `repeat_loop`.

## What ran

- **When:** 19:01:04–21:19:03 PDT on 2026-10-28, by day, 2 h 18 min, against the registered "about 1.5–2 hours".
  - The launcher (`~/tack-runs/e1/c6-bundle-2026-10-28/launch.sh`) waited for mains until about 18:20, then for
    nobara's queue or lock until 18:57.
  - It cleared Go's build cache once (19 → 32 GiB free, the owner's standing permission).
  - It warmed tack's sandbox cache untimed from 18:57 to 19:00 (12 tasks, every build and test compile ok; cache
    6.3 GB).
  - It then ran the cell check, which was clean, and started the runs.
- **Builds:**
  - tack `5d54f3c` on both machines (`~/tack-runs/bin/tack-5d54f3c`; every result line carries
    `5d54f3ce4861…`);
  - goinfer `b2b5e816` (`goinfer-serve-cuda-b2b5e816` on nobara);
  - Qwen3-8B `Q4_K_M` at 16,384, `cuda-resident (int4)`, thinking off, forced-call mode `grammar`;
  - sampling 0.7 / 0.8 / 20 on the first request, as registered.
- **Locks:** the pilot held the Mac's timing lock (`cmd/tack/pilot_a1.go:withRemoteServer`) and nobara's
  (`cmd/tack/pilot.go:startPilotServer`, through pilot-serve) for the whole run.
- **Arms as registered:** all 36 A results carry no settings, and all 36 B results carry exactly the six. A's prompt
  carries the whole `CLAUDE.md` (9,693 tokens on task 1); B's carries its budgeted head.
- **Cells (§12.2):**
  - **0 void, so none re-run.**
  - At every cell's start, nobara was at load 0.02–1.91 of 16 cores (limit 8.0), with 59.2–59.6 GB available with
    the model loaded (limit 2 GB), and the Mac was on mains.
  - The Mac's 1-minute load ran 1.15–21.37, recorded only.
- **The cell check before the start, model loaded and idle:** nobara at load 0.28 of 16 cores, 59.5 GB available,
  the Mac on mains, `void: ""`.
- **Records:** `~/tack-runs/e1/c6-bundle-2026-10-28/` on the Mac: `runs.log`, `runs/results.jsonl`,
  `runs/task-NN/<arm>-<rep>/`, `launch.log`, `warm.log`, `cellcheck.out`.

## Task by task

Window-full stops count `window_full` stops plus prompt-too-long refusals from the server.

| task | A passes | B passes | d | A window-full | B window-full |
|---|---|---|---|---|---|
| 1 | 0/3 | 3/3 | **+3** | 3 | 0 |
| 2 | 1/3 | 0/3 | −1 | 3 | 0 |
| 3 | 0/3 | 0/3 | 0 | 3 | 0 |
| 4 | 0/3 | 0/3 | 0 | 3 | 0 |
| 5 | 0/3 | 0/3 | 0 | 3 | 0 |
| 6 | 0/3 | 0/3 | 0 | 2 | 0 |
| 7 | 0/3 | 0/3 | 0 | 3 | 0 |
| 8 | 0/3 | 0/3 | 0 | 0 | 0 |
| 9 | 0/3 | 0/3 | 0 | 1 | 0 |
| 10 | 0/3 | 0/3 | 0 | 3 | 0 |
| 11 | 2/3 | 1/3 | −1 | 1 | 0 |
| 12 | 0/3 | 0/3 | 0 | 2 | 0 |
| **all** | **3/36** | **4/36** | **+1** | **27** | **0** |

**Stop reasons:**

| | done | prompt-too-long | window_full | repeat_loop | budget | 10-minute cap |
|---|---|---|---|---|---|---|
| A | 6 | 24 | 3 | 0 | 3 | 0 |
| B | 16 | 0 | 0 | 16 | 3 | 1 |

**Requests per run:** A 808 in all, mean 22.4, median 10, range 2–110; B 607 in all, mean 16.9, median 14, range
2–46.

**`done` refusals:**

- **The no-change refusal** (both arms): A in 4 runs (task 8, all three; task 12 A-3); B in 6 runs (task 7 B-1 and
  B-3; task 9, all three; task 12 B-1).
- **`verify_done`'s refusal** (B only): 1 run, task 8 B-2.
- **How B's 16 `done` ends were recorded:** 10 were "the last build or test after the last change failed", including
  all 4 of B's passes, and 6 were plain `done` with nothing changed.

**The passes:**

- task 1: B-1, B-2 and B-3, each a 642-byte diff to `internal/loadflags/loadflags.go`;
- task 2: A-2, `internal/serveapp/banner.go`;
- task 11: A-1, A-2 and B-3, `mcp/server.go`.

No passing diff touches a test file.

**A beside C5 (§12.1):** C5 passed 4 of 36 (task 2 at 1/3, task 11 at 3/3), and A passed 3 (task 2 at 1/3, task 11
at 2/3). The difference is 1, under the 3 that §12.1 registers as a build effect, so nothing goes to goinfer.

## Against the predictions (§12.21)

| prediction | outcome |
|---|---|
| task 1: B at least 2 of 3, A at most 1 of 3 | **B 3/3, A 0/3:** held |
| task 11 alike in both arms, about 2 of 3 | **A 2/3, B 1/3:** held for A, missed for B |
| d between +2 and +5 | **+1:** missed |
| window-full stops: A at least twice B's | **27 against 0:** held |

## The riding check

`go run ./tools/replayrepeats -mirrors ~/tack-runs/e1/mirrors ~/tack-runs/e1/c6-bundle-2026-10-28/runs/results.jsonl`,
run 2026-10-29 at tack `3f60cec`, over all 72 runs:

- **In B, it stops 16 runs, and they are exactly the 16 that ended live with `repeat_loop`:**
  - task 2 B-1 and B-2;
  - task 3 B-2 and B-3;
  - task 4 B-1 and B-2;
  - task 5 B-2 and B-3;
  - task 6 B-3;
  - task 8 B-1 and B-3;
  - task 10, all three;
  - task 12 B-2 and B-3.

  Each stop falls at the run's last request, saving 0 requests. **The check holds.**
- **In A** (reported, not part of the check), it would have stopped 7 runs, none passing: task 2 A-1 and A-3, task 4
  A-3, task 5 A-1 and A-3, task 10 A-2, task 12 A-1.
- **Its own verdict line:** `VERDICT: PASS`, with 23 stopped, 0 passing, and 0 stops moved between the plain and
  amended rules.
- **The duplicate-edit guard** would have touched 5 runs, refusing 12 calls, 0 of which the run applied again: task
  11 A-1, A-2, B-1 and B-3, and task 5 B-1. In B the guard was on, so its refusals there happened live.

## What the registered rule allows to be said

- The bundle is **not shown** to beat A1 at screen size. Under §12.21's rule, the do-nothing arm stands.
- The rest of this record is reported, not decided on: each task's passes, the stops, the window-full counts, the
  requests, the refusals and the predictions.
- What follows from this verdict for C6 is Francis's decision. The options are put to him in the session's report.
