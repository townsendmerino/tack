# E1 calibration C6, change 4.2: done requires verification, a screen, 2026-10-20

<!-- citations-at: 9583ada -->

This is change 4.2 under C6, registered in §12.15 before its runs, as a screen. It was restarted after the budgeted
head's fix passed its tier-1 check (§12.12). The first launch's 41 runs are recorded, not counted
(`e1-calibration-c6-42-stopped-2026-10-20.md`).
- A is the bundle: `fit_window`, `stop_repeats` (amended), `result_notes`, `instructions_head` and
  `refuse_duplicate_edits`.
- B is A plus `verify_done`.

**Verdict, as registered: 4.2 enters the bundle, at the floor.**
- Metric 1 moved as predicted: of the runs ending in `done` after a change, B verified 10 of 10 and A 7 of 9.
- d = −1 (B 5/36, A 6/36), which meets d ≥ −1 exactly. As the owner noted, the 90% share is close to certain by
  construction, so d is the effective test, and it passes at its floor.
- The rule had little to do: it refused `done` in one B run of 36. Qwen3-8B with this bundle mostly ran a test before
  `done` on its own, in both arms.

## What ran

- **When:** 20:12:19–21:51:25 PDT (1 h 39 m), holding nobara's lock and the Mac's lock.
  - The launcher was submitted at 17:26 and waited for goinfer's nobara queue, then for mains: the Mac was on battery
    from about 18:46 to about 20:10.
- **Before the start:**
  - Go's build cache was cleared (owner's standing permission);
  - tack's sandbox cache was warmed untimed for all 12 tasks, 16:16–16:19.
- **Builds:**
  - tack `9b71ee0`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, thinking off.
- **Cells:**
  - 0 void, all 72 runs on mains.
  - The cell check before the start read nobara at load 1.30 with 54.9 GB available, the model loaded.
- **Records:** `~/tack-runs/e1/c6-42-2026-10-20/runs-restart/` on the Mac, with `runs-restart.log`,
  `launch-restart.log`, `replay-restart.txt` and `notes.txt`.

## Task by task

| task | A passes | B passes | A stops | B stops | A requests/run | B requests/run |
|---|---|---|---|---|---|---|
| 1 | **3/3** | **3/3** | done 3 | done 3 | 12 | 13 |
| 2 | 0/3 | 0/3 | max_tokens, repeat_loop, window_full | done, max_tokens, repeat_loop | 25 | 19 |
| 3 | 0/3 | 0/3 | done, max_tokens, repeat_loop | budget, repeat_loop 2 | 23 | 25 |
| 4 | 0/3 | 0/3 | error (prompt too long), repeat_loop 2 | error (prompt too long), repeat_loop 2 | 40 | 34 |
| 5 | 0/3 | 0/3 | done, repeat_loop 2 | budget, done 2 | 14 | 27 |
| 6 | 0/3 | 0/3 | budget, repeat_loop 2 | budget, done, repeat_loop | 20 | 25 |
| 7 | 0/3 | 0/3 | repeat_loop 3 | budget, done, repeat_loop | 37 | 23 |
| 8 | 0/3 | 0/3 | budget, done, repeat_loop | budget, done, repeat_loop | 15 | 23 |
| 9 | 0/3 | 0/3 | done 3 | done 3 | 2 | 2 |
| 10 | 0/3 | 0/3 | repeat_loop 3 | repeat_loop 3 | 10 | 11 |
| 11 | **3/3** | **2/3** | done 3 | done 3 | 8 | 8 |
| 12 (no instructions) | 0/3 | 0/3 | done 2, error | done 3 | 10 | 3 |
| **all** | **6/36** | **5/36** | | | **18.2** | **17.9** |

Over the 9 tasks without tasks 3, 8 and 9: A 6/27, B 5/27.

- **Task 9 calls `done` at once in all 6 runs** (2 requests), as it did in 4.8. Its statement is answered without work
  in every arm so far. That is the task, not the head.
- **Task 1 passed in all 6 runs.** Over every earlier Qwen3-8B job it passed once in 27: C5 0/3, 4.6 0/6, 4.4 1/6,
  4.1 0/6, 4.8 0/6. 4.8's arm with no instructions at all is among them. Both arms here share the bundle, so this cannot be
  credited to 4.2. The bundle comparison (§12.9) is where it gets measured.
- **The one difference in passes is task 11 B-2.** Its first edit, at request 4, replaced the error string with
  different wording ("no default index…", dropping "No repo specified"). The oracle checks that text. This came
  before any build, test or 4.2 message: the run was never refused.

## The mechanism metrics (§12.15)

| metric | A | B | prediction | outcome |
|---|---|---|---|---|
| 1. runs ending in `done` after a change that verified after it | 7 of 9 (78%) | 10 of 10 (100%) | B ≥ 90%, A 55–90% | **held** |
| 2. the next call after a refusal | — | a verification (1 of 1) | a verification after at least half | held |
| 3. B runs refused | — | 1 (1 refusal) | 3–12 | **missed: fewer** |
| 4. B runs ending unverified (4th `done`) | — | 0 | at most 2 | held |
| 6. requests per run; window-full stops | 18.2; 3 | 17.9; 1 | — | reported |
| d | | −1 | between −2 and +1 | held |

**5. The verification commands B used.** 17 B runs ran one. Most were whole-repository tests:
- `go test ./...` (with `-v` or `-race`): 9 runs, on tasks 1 (3), 2, 5, 11 and 12 (3);
- `go mod tidy && go test ./...`: 4 runs, on tasks 5, 8 and 11 (2). In the sandbox, `go mod tidy` cannot download;
- one package or file: 4 runs, on tasks 2, 6 and 7 (2). Task 6 B-1 ran `go test -v internal/serveapp/banner.go` four
  times.

Of the 14 B runs that ended in `done` after a verification, 10 are recorded "the last build or test after the last
change failed", including the passing runs on tasks 1 and 11. The other 4 (task 7 B-1, and task 12's three) ended
with a verification that passed.

The refusal's advice ("go build ./..., or go test on the package you changed") reached the model once. Task 11's runs
ran ken's whole-repository test on their own, the failure the owner asked the advice to steer away from.

## The checks that ride on these runs

**The head (§12.12), in arm A:**
- Each task's first prompt, less 4.8's arm B's (no instructions), is under the 2,457-token cap on every task:
  - goinfer tasks: 522–555 tokens;
  - ken tasks: 135–143;
  - task 12: 0.
- Against §12.12's predictions, the shares are 24–31 tokens under on every task with a file. The predictions were made
  for the first frame. The fixed frame is about 50 bytes shorter, plus 2 tokens for "Task: ". On the ken tasks this puts
  the share below the predicted band.

| task | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| A first prompt | 1,424 | 1,470 | 1,518 | 1,448 | 1,487 | 1,462 | 1,056 | 1,443 | 1,541 | 1,036 | 1,010 | 906 |
| predicted | 1,448 | 1,494 | 1,542 | 1,474 | 1,511 | 1,487 | 1,087 | 1,469 | 1,564 | 1,067 | 1,041 | 906 |

The records' `instruction_tokens` field still counts the whole file (9,693 on task 1), not what was loaded. It is
the harness's estimate, not the prompt.

**The duplicate-edit guard (§12.14):**
- It refused 10 calls in 8 runs: A 6 in 4, B 4 in 4.
- `tools/replayrepeats -mirrors` counts the same 10 in the same 8 runs.
- **Task 11:** every one of its 6 runs resent its applied edit once and was told "already applied at line 81". The
  next call was `read` in all 6. 5 of the 6 passed; the sixth is B-2 above, wrong from its first edit.
- Task 12 A-2 followed its refusal with an edit, a read and a write. Task 5 B-2 followed its with a read.

**The amended stop rule (§12.13):** 26 runs ended live with `repeat_loop`, all with exit status 9, none passing.
`tools/replayrepeats -mirrors` stops exactly those 26, each at its own last request. No disagreement.

## What it means

- 4.2 enters the bundle under the registered rule, at d's floor. Its own effect here is small: one refusal in 36 runs.
  Qwen3-8B mostly runs a test before `done` anyway.
- What it adds is a guarantee for the runs that would not, and a record of how each run ended.
- The verifications the model chooses are mostly whole-repository tests that fail for reasons of their own: missing
  modules in the sandbox, loopback refusals. The refusal's narrower advice reaches the model only when it is refused,
  which was once.
- The bundle as it now stands produced 6/36 and 5/36, against 4.8's 2/36 and 4/36 and the pooled baseline's 5/72.
  These are not comparable arms. The bundle comparison measures it.
