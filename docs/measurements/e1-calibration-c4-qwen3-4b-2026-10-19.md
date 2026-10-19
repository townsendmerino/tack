# E1 calibration C4: Qwen3-4B at 30,720, 2026-10-19

<!-- citations-at: fb94106 -->

This is amendment C4 of `e1-calibration-preregistration-2026-10-18.md` (§10): A1, unchanged, on Qwen3-4B. It is
compared task by task with the baseline (`e1-calibration-baseline-2026-10-18.md`), not pooled.

**Result: 0 passes in 36 runs, and 0 in 27 over the 9 tasks without tasks 3, 8 and 9. That is below the bar** (at
least 4 passes over at least 2 tasks). The baseline also scored 0 on every task, so d = 0.

Both predictions held:
- passes, predicted 0–3: **0**;
- window-full stops including prompt-too-long refusals, predicted 6–12: **11**.

## What ran

- **When:** by day, under the owner's 2026-10-19 rule, each step holding nobara's timing lock.
  - The fit load and the smoke: 06:42–06:53 PDT.
  - Pass 1: 07:18–07:48.
  - Pass 2: 07:49–08:10.
  - Pass 3: 08:10–08:49.
  - The 36 runs took 85 minutes of run time.
- **Builds:**
  - tack `9f4e0e0`, clean, on both machines;
  - goinfer-serve `f20f0d93`;
  - Qwen3-4B bf16, quantized to int4 at load, `cuda-resident (int4)` at 30,720 (4.2 GB of KV beside 2.1 GB of
    weights, 7.3 GB of VRAM).
- **Thinking off,** enforced twice: tack sends thinking disabled on every request, and goinfer-serve runs with
  `-thinking off`.
- **Calls:** forced-call mode grammar on every run, and 0 post-check resends, as predicted.
- **Records:** `~/tack-runs/e1/cal-c4c5-2026-10-18/c4/` on the Mac. Raw request and response bodies stay there.

## Task by task

Each run is classed by how it ended:
- *done-no-work*: the model called `done` with no other tool call;
- *done-wrong*: `done` after some work, with the oracle failing;
- *window*: `window_full`, or the server's prompt-too-long refusal.

| task | baseline (Qwen2.5-7B, 32,768) | C4 (Qwen3-4B, 30,720) |
|---|---|---|
| 1 | 2 done-wrong, 1 budget | 1 budget, 1 cap, 1 max_tokens |
| 2 | 3 done-wrong | 3 done-no-work |
| 3 | 2 done-wrong, 1 window | 3 done-no-work |
| 4 | 1 done-wrong, 2 window | 3 done-no-work |
| 5 | 3 window | 3 window |
| 6 | 3 done-wrong | 3 done-no-work |
| 7 | 2 done-wrong, 1 window | 1 done-no-work, 2 window |
| 8 | 2 done-wrong, 1 window | 3 done-no-work |
| 9 | 3 done-wrong | 3 done-no-work |
| 10 | 3 done-wrong | 2 done-wrong, 1 window |
| 11 | 3 done-wrong | 3 window |
| 12 | 3 done-wrong | 1 done-wrong, 2 window |
| **all** | **0 pass**; 27 done-wrong, 8 window, 1 budget | **0 pass**; 19 done-no-work, 3 done-wrong, 11 window, 3 other |

## What the runs show

- **Qwen3-4B gives up without looking.** In 19 of 36 runs, and in all three runs of tasks 2, 3, 4, 6, 8 and 9, its first
  call was `done`, with a reason saying the change was "already in place".
  - tack refused that first `done`, as built, and the model called `done` again.
  - The baseline model never did this: every run of Qwen2.5-7B did some work.
  - This is the failure §4.2's loop change targets: `done` requires verification.
- **When it works, it loops.**
  - 10 of the 11 window-full stops are loops: one identical `grep` (tasks 5, 7 and 12) or an alternating `read`/`edit`
    pair (task 11, 54 rounds in each run).
  - The 11th, task 10 in pass 3, read and edited one large file until the window filled.
  - Task 1's grep loops (349 and 383 calls) ended on the error budget and the cap instead.
  - The grep loops run 120–380 calls. The filed stop rule
  (§4.5, at the 8th unchanged repeat) would have ended each of them early, but not turned any into a pass.
- **Task 1's other endings:**
  - `budget`: 8 tool errors in a row;
  - `cap`: the 10-minute cap during a grep loop;
  - `max_tokens`: one reply hit 2,048 tokens with a grep glob that repeated `/*` hundreds of times.
- **The 3 runs that did work and stopped** (tasks 10 and 12) made short edits that failed the oracle.

## Harness and conditions

- **No void, no harness error, no sleep.**
  - Every request is paired.
  - The client's total is within 8–179 ms of the server's.
  - Cold first tokens took up to 12.3 s on 10.6k-token first prompts.
- **Load at each cell's start** (the 2026-10-19 rule):
  - nobara, the timed box, ran a 1-minute load of 1.2–2.3, under its 8.0 line, with 56 GB available;
  - the Mac drove only, at load 1.7–3.1.
- **Power: pass 1's tasks 1–7 ran with the Mac on battery.**
  - It was on mains at both launches (06:53 and 06:55 PDT). The power log has it on battery from 07:01:49 to 07:37:44.
  - So tasks 1–6 record `battery` and task 7 records `battery→ac`. The other 29 runs record `ac`.
  - No sleep occurred, and the Mac only drives the harness, so the timings on nobara are not affected.
  - These 7 runs did not meet §10's "on mains" condition. They are kept and marked rather than voided: the result is 0
    either way, and their outcomes match the same tasks' runs in passes 2 and 3.
  - The stage script then checked power only at the smoke. It now waits for mains before every pass.
- **Two interruptions, no runs lost:**
  - At 06:54 a pass started in goinfer's queue runner's settle gap, with the lock free, and was stopped 13 s later
    before its first request.
  - At 07:48 passes 2 and 3 lost the lock to goinfer's nobara session in the instant after their check; pilot-serve
    refused, so no runs were made.
  - Both are kept under `c4/pass-*-aborted-*` and `c4/pass-*-lock-refused-*`.
  - The stage script now waits for the queue runner as well as the lock, and retries a pass that pilot-serve refused.

## What it means

- **Under §2 the next decision is still the model.** C4 adds that a smaller Qwen3 with a near-32k window does no better
  than the baseline. It fails differently: it quits at once where Qwen2.5-7B tried and was wrong.
- **C5 (Qwen3-8B at 16,384) runs next,** and is read beside C4 task by task, with its window confound beside it.
