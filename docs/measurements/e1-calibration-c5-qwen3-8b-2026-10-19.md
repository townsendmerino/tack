# E1 calibration C5: Qwen3-8B at 16,384, 2026-10-19

<!-- citations-at: d60e986 -->

This is amendment C5 of `e1-calibration-preregistration-2026-10-18.md` (§11): A1, unchanged, on Qwen3-8B. It is
compared task by task with the baseline (`e1-calibration-baseline-2026-10-18.md`) and with C4
(`e1-calibration-c4-qwen3-4b-2026-10-19.md`), never pooled.

**Result: 4 passes in 36 runs, over 2 tasks: task 2 once and task 11 in all three runs. That meets §2's bar exactly**
(at least 4 passes over at least 2 tasks). Over the 9 tasks without tasks 3, 8 and 9: 4 of 27. Both tasks are among
the 9.

**Both predictions missed, and are recorded as missed:**
- passes, predicted 0–3: **4**;
- window-full stops including prompt-too-long refusals, predicted 12–20: **25**. That is 22 of the 32 failing runs, plus
  3 passing runs that made the fix and then filled the window.

## What ran

- **When:** by day, each step holding nobara's timing lock.
  - Fit load and smoke: 08:50–08:58 PDT. The fit waited about 2 minutes for goinfer's lock.
  - Pass 1: 08:58–09:19.
  - Pass 2: 09:19–09:36.
  - Pass 3: 09:36–09:59.
  - The 36 runs took 57 minutes of run time.
- **Builds:**
  - tack `9f4e0e0`, clean, on both machines;
  - goinfer-serve `f20f0d93`;
  - Qwen3-8B `Q4_K_M` (sha256 `d98cdcbd…`), `cuda-resident (int4)` at 16,384 (2.2 GB of KV beside 4.3 GB of weights,
    7.2 GB of VRAM).
- **Thinking off,** enforced as in C4.
- **Calls:** forced-call mode grammar on every run, and 0 post-check resends.
- **Records:** `~/tack-runs/e1/cal-c4c5-2026-10-18/c5/` on the Mac.

## Task by task

Runs are classed as in C4's record. *budget* is tack's limit of 8 tool errors in a row.

| task | baseline (Qwen2.5-7B, 32,768) | C4 (Qwen3-4B, 30,720) | C5 (Qwen3-8B, 16,384) |
|---|---|---|---|
| 1 | 2 done-wrong, 1 budget | 1 budget, 1 cap, 1 max_tokens | 2 budget, 1 window |
| 2 | 3 done-wrong | 3 done-no-work | **1 pass**, 2 window |
| 3 | 2 done-wrong, 1 window | 3 done-no-work | 3 window |
| 4 | 1 done-wrong, 2 window | 3 done-no-work | 1 done-no-work, 2 window |
| 5 | 3 window | 3 window | 1 budget, 2 window |
| 6 | 3 done-wrong | 3 done-no-work | 1 budget, 2 window |
| 7 | 2 done-wrong, 1 window | 1 done-no-work, 2 window | 3 window |
| 8 | 2 done-wrong, 1 window | 3 done-no-work | 3 done-no-work |
| 9 | 3 done-wrong | 3 done-no-work | 1 budget, 2 window |
| 10 | 3 done-wrong | 2 done-wrong, 1 window | 3 window |
| 11 | 3 done-wrong | 3 window | **3 pass** |
| 12 | 3 done-wrong | 1 done-wrong, 2 window | 1 done-wrong, 2 window |
| **all** | **0 pass** | **0 pass** | **4 pass**; 22 window, 5 budget, 4 done-no-work, 1 done-wrong |

**Against the baseline:** d = 4, on tasks 2 (+1) and 11 (+3). Every other task is 0 in both arms.

## The passes are genuine

Each passing run was checked:
- its oracle (the task commit's named test) passes;
- its diff touches no test file;
- its fix is the task's intended change.

Detail for each:
- **Task 2** (goinfer, the banner's KV line): a one-line fix to a `case` in `internal/serveapp/banner.go`. The model's
  `go mod tidy` also pruned `go.sum`, which the oracle does not depend on. The run then filled the window after the
  fix.
- **Task 11** (ken, `mcp/server.go`): the `resolveRepo` error string lowercased, as ken `1416be9` does. The test checks
  for "no repo specified". The three runs' wordings differ in the rest of the string, which the test does not check. In
  two of them the window filled after the fix; the third called `done`.
- Task 11 is the smallest change in the set: one string. Task 2 is one condition. **The bar is met by the two easiest
  fixes, and only just.**

## What the failures show

- **The 16k window is C5's main failure, as §11 warned.** 22 of 32 failing runs stopped on the window. 11 runs ended
  within 3 requests.
- **Tool output is what fills the window.** 10 of the 22 window stops came straight after a `bash` call:
  - `go test -v ./...` or a CUDA build, whose 300–360 lines of output pushed a 10–12k prompt past 16,384;
  - goinfer then refused the next request as too long.

  No change in §4 addresses this. It is the mechanism a tool-output budget sized to the window would act on.
- **Few loops.** Unlike C4's 120–380-call grep loops, C5's longest runs are 54–113 requests (tasks 1, 2, 5 and 10; tasks 2 and 5 in two runs each). The
  window ends them early.
- **Done-no-work: 4 runs** (task 8 in all three, task 4 once), against C4's 19.

## Harness and conditions

- **No void, no harness error, no sleep.**
  - Every request is paired.
  - The client's total is within 4–198 ms of the server's.
  - Cold first tokens took up to 15.7 s, on first prompts of 0.9k–11.6k tokens.
- **Power: `ac` for all 36 runs.** The stage script now waits for mains before every pass.
- **Load at each cell's start:** nobara 0.2–1.1, under its 8.0 line, with 56 GB available. The Mac drove only, at load
  1.7–2.8.

## What it means

**Under §2, Qwen3-8B at 16,384 is the first arm to reach the bar.** That opens the question §2 left for this case:
whether loop changes are now measured on this model. That is the owner's decision. The numbers it would rest on:

- **§3's rule with C5's per-task rates as the baseline:**
  - task 2 at 1/3, task 11 at 3/3, all others 0;
  - variance under "no effect" is 2·3·(1/3·2/3) = 4/3, so sd ≈ 1.15;
  - **adopted needs d ≥ 2.31, so 3 or more passes over C5's; parked is d = 2; not adopted is d ≤ 1.**
- **A baseline at the bar on its two easiest tasks is thin.** Ten tasks sit at 0, so §3's sd is small, and the rule
  only sees changes that create passes there.
- **The thinking-on arm** (draft, `e1-calibration-thinking-arm-draft-2026-10-19.md` §4) falls under the "C5 at the
  bar" case. C5's passing runs already ended at 15.8–16.3k of prompt, except one at 5.7k. Any thinking kept in the window
  would push them out. By the draft's own rule it is not worth running at 16k.
