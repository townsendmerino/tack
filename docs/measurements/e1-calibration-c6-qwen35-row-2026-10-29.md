# E1 calibration C6: Qwen3.5-9B on the calibration set, with the bundle, 2026-10-29

<!-- citations-at: 3382aa8 -->

This is §12.23 step 2, registered as `e1-calibration-preregistration-2026-10-18.md` §12.24 on 2026-10-29 before the
run: one C5-style arm, Qwen3.5-9B `Q4_K_M` at 32,768 with the bundle as adopted (`fit_window`, `stop_repeats`,
`result_notes`, `instructions_head`, `refuse_duplicate_edits`, `verify_done`, `edit_tolerances`), the 12 tasks, 3 runs
each.

**Verdict, as registered: usable, at the bar.** The row passed 4 of 36 runs, on 2 tasks: task 10 once and task 11
three times. §12.24 reads §2's bar, at least 4 of 36 on at least 2 tasks, and 4 on 2 meets it with nothing to spare.

- **0 cells void,** so none was re-run.
- **It is not a model verdict** (§12.24). The window, the model and the sampling defaults' fit all differ from
  §12.21's B arm, which ran on a different night. Whether C6 moves to Qwen3.5-9B is Francis's decision.

## What ran

- **When:** the runs went from 22:53:35 PDT on 2026-10-29 to 03:03:27 PDT on 2026-10-30, 4 h 10 min. The
  registered estimate was 1.9–3.7 hours.
  - The job was `tack-c6-1224-qwen35-row` on the Mac's `night.py`, started 22:49:41 and ended rc 0 at 03:03:28
    (`~/goinfer-logs/night/runs/2026-10-29/`).
  - Its launcher (`launch.sh`) found nobara's queue or lock busy at 22:01, 22:17 and 22:33, and exited 75 each time
    so that `night.py` retried it.
  - At 22:50 it cleared Go's build cache once (10 → 30 GiB free, the owner's standing permission).
  - From 22:50 to 22:53 it warmed tack's sandbox cache untimed (12 tasks, every build and test compile ok; cache
    9.9 GB).
  - The cell check was clean, and then the runs started.
- **Builds:**
  - tack `3382aa8` on both machines (`~/tack-runs/bin/tack-3382aa8`; every result line carries `3382aa83ac9e…`);
  - goinfer `95bc3c7b` (`goinfer-serve-cuda-95bc3c7b` on nobara), the build §12.23 step 1 measured;
  - Qwen3.5-9B `Q4_K_M` at 32,768, `cuda-resident (int4)`, 2.0 GB of KV, thinking off (`thinking-off`), forced-call
    mode `grammar`;
  - sampling 0.7 / 0.8 / 20 on the first request, as registered.
- **The arm as registered:** all 36 result lines carry exactly the seven settings, and the 10-minute cap.
- **Locks:** `night.py` held the Mac's timing lock for the job, and the pilot inherited it
  (`cmd/tack/pilot_a1.go:withRemoteServer`). pilot-serve takes nobara's (`cmd/tack/pilot.go:startPilotServer`). This
  read did not check nobara's side, because nobara was rebooting this morning.
- **Cells (§12.2):**
  - **0 void, so none re-run.**
  - At every cell's start, nobara's load was 0.86–2.00 of 16 cores (limit 8.0), with 57.8–59.0 GB available with
    the model loaded (limit 2 GB). The Mac was on mains for all 36.
  - The Mac's 1-minute load ran 1.09–5.52, recorded only.
- **The cell check before the start, model loaded and idle:** nobara load 1.30 of 16 cores, 59.0 GB available, the
  Mac on mains, `void: ""`.
- **Stopping:** §12.24 registers no smoke and no stop partway through. The only gate before the runs was the clean
  cell check, so all 36 ran.
- **The read:** `tools/calreport` exactly as §12.24 gives it, at tack `483754b`, with `GOWORK=off` so that a
  workspace in a parent directory stays out. It ended `VERDICT: PASS — calreport: 36 counted runs, 0 void, over 1
  arms and 12 tasks`. Its output is `calreport.out` in the job directory.
- **Records:** `~/tack-runs/e1/c6-qwen35-row-2026-10-29/` on the Mac: `launch.log`, `warm.log`, `cellcheck.out`,
  `runs.log`, `runs/results.jsonl`, `runs/task-NN/A1-<rep>/`, `runs/goinfer-serve.log`, `calreport.out`.

## Task by task, beside §12.21 and C5

Window-full stops count `window_full` stops plus prompt-too-long refusals from the server. The §12.21 arms are
Qwen3-8B at 16,384 on goinfer `b2b5e816`: A has no settings, and B is the bundle without `edit_tolerances`. C5 is
Qwen3-8B at 16,384 with no settings, on `f20f0d93`.

| task | Qwen3.5-9B passes | window-full | cap hits | §12.21 A | §12.21 B | C5 |
|---|---|---|---|---|---|---|
| 1 | 0/3 | 0 | 1 | 0/3 | 3/3 | 0/3 |
| 2 | 0/3 | 1 | 0 | 1/3 | 0/3 | 1/3 |
| 3 | 0/3 | 0 | 0 | 0/3 | 0/3 | 0/3 |
| 4 | 0/3 | 0 | 1 | 0/3 | 0/3 | 0/3 |
| 5 | 0/3 | 0 | 2 | 0/3 | 0/3 | 0/3 |
| 6 | 0/3 | 0 | 1 | 0/3 | 0/3 | 0/3 |
| 7 | 0/3 | 1 | 1 | 0/3 | 0/3 | 0/3 |
| 8 | 0/3 | 1 | 1 | 0/3 | 0/3 | 0/3 |
| 9 | 0/3 | 0 | 2 | 0/3 | 0/3 | 0/3 |
| 10 | **1/3** | 1 | 0 | 0/3 | 0/3 | 0/3 |
| 11 | **3/3** | 0 | 0 | 2/3 | 1/3 | 3/3 |
| 12 | 0/3 | 1 | 2 | 0/3 | 0/3 | 0/3 |
| **all** | **4/36** | **5** | **11** | 3/36 | 4/36 | 4/36 |

Window-full stops and cap hits in the other arms: §12.21 A 27 and 0, §12.21 B 0 and 1, C5 25 and 0.

**Stop reasons, per run:**

| task | rep 1 | rep 2 | rep 3 |
|---|---|---|---|
| 1 | cap | repeat_loop | budget |
| 2 | repeat_loop | prompt-too-long | repeat_loop |
| 3 | repeat_loop | repeat_loop | repeat_loop |
| 4 | cap | repeat_loop | repeat_loop |
| 5 | repeat_loop | cap | cap |
| 6 | repeat_loop | repeat_loop | cap |
| 7 | cap | repeat_loop | window_full |
| 8 | budget | cap | prompt-too-long |
| 9 | cap | cap | repeat_loop |
| 10 | budget | prompt-too-long | done (pass) |
| 11 | budget (pass) | done (pass) | done (pass) |
| 12 | prompt-too-long | cap | cap |

Totals: repeat_loop 13, cap 11 (recorded as `cancelled`), budget 4, prompt-too-long 4, done 3, window_full 1. In
§12.21's B the totals were done 16, repeat_loop 16, budget 3, cap 1.

**The window-full stops:**
- **Prompt-too-long, 4.** The server refused prompts of 32,781–32,846 tokens against its 32,768 maximum, 13–78
  tokens over, in task 2 rep 2, task 8 rep 3, task 10 rep 2 and task 12 rep 1.
- **`window_full`, 1:** task 7 rep 3, at 32,767 tokens.

**Requests per run:** 1,335 in all, mean 37.1, median 32, range 15–197. The 197 were in task 12 rep 1, over 490 s.
§12.21's B averaged 16.9, median 14.

**Wall time per run:** median 462 s, max 600 s, 14,732 s in all.

**Server first-token time per request:** median 1.3 s, p90 21.8 s, max 263.7 s, over the 1,265 requests that carry
server timings. The median request prefilled 90 tokens.
- Two capped runs carry none, task 7 rep 1 and task 12 rep 3. Their `timing_err` reads "25 model requests captured,
  26 in goinfer's log: not paired" and "31 … 33 …".
- Requests that prefilled 4,096 tokens or more were 83 of the 1,268 timed ones, and took 6,323 s of the 12,657 s of
  server time.

**`done` refusals:** none. No run met the no-change refusal or `verify_done`'s refusal. In §12.21's B, 6 runs met the
first and 1 the second.

**The passes:**
- task 10 rep 3: a 1,509-byte diff to `cmd/ken/build_index.go`, ended `done` at 509 s;
- task 11 rep 1: 615 bytes to `mcp/server.go`, ended on the failure budget at 86 s;
- task 11 rep 2: 1,095 bytes to `mcp/server.go` and `mcp/server_test.go`, ended `done` at 102 s;
- task 11 rep 3: 1,166 bytes to `mcp/server.go` and `mcp/server_test.go`, ended `done` at 104 s.

`mcp/server_test.go` is task 11's named test file. The oracle writes the fix commit's copy of that file over the
model's edit before it grades (`internal/eval/pilot/run.go:Oracle`), so those edits play no part in the grade. No
other passing diff touches a test file.

## Against the predictions (§12.24)

| prediction | outcome |
|---|---|
| passes: 2–8 of 36 | **4:** held |
| task 1 at least 2 of 3 | **0/3:** missed |
| task 11 at least 1 of 3 | **3/3:** held |
| window-full stops: 0–2 | **5** (4 prompt-too-long, 1 `window_full`): missed |
| 10-minute cap hits: 2–10 | **11:** missed |
| median run time: 2.5–6 minutes | **7.7 minutes** (462 s): missed |
| total time: 1.9–3.7 hours | **4 h 10 min:** over |

## What the registered rule allows to be said

- Under §2's bar, as §12.24 reads it, the row is **usable**: 4 of 36 passes, on 2 tasks.
- It is not a model verdict, and it is not compared with §12.21 under §12.3's rule. The table beside §12.21 and C5
  is reported, not decided on.
- Everything after the verdict is reported, not decided on: each task's passes, the stops, the window-full counts,
  the cap hits, the requests, the first-token times, the refusals and the predictions.
- Whether C6 moves to Qwen3.5-9B is Francis's decision. The options are in the session's report.

## Francis's decision (2026-10-30, 08:01 PDT)

Option (b), relayed by the coordinator:
- **C6 stays on Qwen3-8B at 16,384,** and the powered comparison runs there.
- **Qwen3.5-9B stays a candidate** for when goinfer's long-prefill time drops.
- This does not change the verdict: the row is usable, at the bar.

## The prompt-too-long refusals: `fit_window`'s margin held (2026-10-30, Francis's side item)

Francis asked whether the four refusals (13–78 tokens over 32,768 with `fit_window` on) show a margin bug. The run
records say they do not. This changes nothing above.
- **The reserve line held.** `loop.Session.fitRoom` keeps the next reply's `max_tokens` (2,048) plus 512 tokens free,
  so the reserve line at 32,768 is 30,208.
  - 18 of the 36 runs crossed it.
  - Each crossing that followed a cut result landed 11–225 tokens past the line, well inside the 2,560-token reserve.
  - No fitted result took a prompt over the window.
- **What filled the window was the zone after it.** From there on, every long result became the "window nearly full"
  note (116–267 bytes), and the model kept calling. Each request added a call and a note, 80–125 tokens.
  - The four refused runs spent 21–28 requests past the line before the server refused (task 2 rep 2, task 8 rep 3,
    task 10 rep 2, task 12 rep 1).
  - Task 7 rep 3 crept the same way to 32,767 and ended `window_full`.
  - In task 12 rep 1, the creep was one 128-byte `read` result repeated, under `fitMinBytes`, so it passed whole.
- **No test was written.** The arithmetic the test would cover did not fail, so there was nothing to see go red.
- **A stop at the reserve line would cost passes on C6's arm.** In §12.21's B (Qwen3-8B at 16,384, line at 13,824),
  task 1 B-2 and B-3 were past the line when they passed, and B-1 passed after a nearly-full note.
  B had 0 window-full stops.
  - So any stop in that zone would be a loop change under §12, registered and measured like the others, not a margin
    fix.
