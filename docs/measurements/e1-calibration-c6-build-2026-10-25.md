# E1 calibration C6: build after every edit, a screen, 2026-10-25

<!-- citations-at: a2679df -->

The screen registered in §12.17 before its runs. A is the adopted bundle (`fit_window`, `stop_repeats` as amended,
`result_notes`, `instructions_head`, `refuse_duplicate_edits`, `verify_done`). B is A plus `build_after_edit`.

**Verdict, as registered: `build_after_edit` is not adopted ("not shown").**
- Metric 3, the runs that end with code not compiling: A 6, B 6. The rule needed B at most half of A's, 3 or fewer.
- d = −1 (B 4/36, A 5/36), which meets d ≥ −1 at its floor. Neither parked condition holds.
- The setting stays in tack, off.

## What ran

- **When:** 14:43:10–16:11:50 PDT (1 h 28 m), holding nobara's lock and the Mac's lock.
  - Registered for 22:30 or later; nobara's lock freed in the afternoon, and the launcher started at the first moment
    both machines were free.
- **Before the start:** tack's sandbox cache was warmed untimed for all 12 tasks, 09:31–09:34 (8.4 GB). Go's build
  cache was cleared at 09:31 and again at about 15:40 (owner's standing permission). Old pinned tack binaries were
  deleted at 15:41 (owner).
- **Builds:**
  - tack `e3fdf99`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, thinking off, decode path `cuda-resident (int4)`.
- **Cells:**
  - 0 void: no run started under the harness's 20 GiB disk floor. The Mac read 24 GiB at the start and 21.2 at 15:35,
    then 23–25 after the owner and the session freed space.
  - The cell check read nobara at load 2.39 with 57.8 GB available, the model loaded; the Mac at load 1.4, on mains.
- **Records:** `~/tack-runs/e1/c6-build-2026-10-25/` on the Mac: `runs/` (`results.jsonl`, each run's
  `events.jsonl`, `oracle.log` and `diff.patch`), `runs.log`, `launch.log`, `notes.txt`.

## Task by task

| task | A passes | B passes | A stops | B stops |
|---|---|---|---|---|
| 1 | **3/3** | **2/3** | done 3 | done 2, cancelled (the 600 s cap) |
| 2 | 0/3 | 0/3 | budget, done, repeat_loop | done, repeat_loop 2 |
| 3 | 0/3 | 0/3 | budget, done, repeat_loop | done 2, repeat_loop |
| 4 | 0/3 | 0/3 | repeat_loop 3 | done, repeat_loop 2 |
| 5 | 0/3 | 0/3 | max_tokens 3 | budget, error, repeat_loop |
| 6 | 0/3 | 0/3 | done, repeat_loop 2 | done, repeat_loop 2 |
| 7 | 0/3 | 0/3 | budget, done, repeat_loop | budget, repeat_loop 2 |
| 8 | 0/3 | 0/3 | repeat_loop 3 | budget, repeat_loop 2 |
| 9 | 0/3 | 0/3 | done 3 | done 3 |
| 10 | 0/3 | 0/3 | repeat_loop 3 | repeat_loop 3 |
| 11 | **2/3** | **2/3** | done 3 | done 3 |
| 12 | 0/3 | 0/3 | budget 2, done | budget, done, repeat_loop |
| **all** | **5/36** | **4/36** | | |

## The metrics (§12.17)

1. **How often a `.go` change breaks the build, in B:** 7 of the 17 successful `.go` edits and writes carried the note.
2. **After each note:** the next `.go` change built after 0 of the 7. It broke the build again after 4. In 3 the
   run made no further `.go` change. The prediction was a build after at least half; the mechanism did not appear.
3. **Runs that end with code not compiling** (the oracle's `go test` reports `[build failed]` or `[setup failed]`):
   - A 6: task 3 runs 1–3, task 5 runs 1–3.
   - B 6: task 3 runs 1–2, task 5 runs 1–3, task 12 run 1.
4. **Cost:**
   - Mean capped time: A 531 s, B 556 s. Mean requests: A 20.1, B 21.2. Window-full stops: 0 and 0. Cap hits: 0 and 1.
   - Median `.go` edit or write: A 0.01 s, B 0.33 s, the build included.

## The check that rode on the runs

- `tools/replayrepeats -mirrors` over the 72 runs stops 30, each at the run's last request, and they are exactly the
  30 that ended live with `repeat_loop`. It stops no passing run (`replay.txt` in the records). The note leaves the
  repeat rule as it was.
- The duplicate-edit guard would have refused 11 calls in 7 runs (4 of them passing); the runs applied none of them
  again.

## What metric 3 counted

The oracle reports `[build failed]` both when the model's own code does not compile and when the task's test file
cannot compile against code the model left incomplete. Read from each `oracle.log`'s build errors (not a registered
metric; the verdict above stands on metric 3 as registered):

| kind | A | B |
|---|---|---|
| the test file names an API the model never added, and the model's code compiles | 2 (task 3 runs 1–2) | 4 (task 3 runs 1–2, task 5 runs 2–3) |
| the model's own code does not compile | 4 (task 3 run 3: nested `go.mod` files with a placeholder module path; task 5 runs 1–3: `banner.go` uses a field it never added) | 2 (task 5 run 1: text before `package` in `banner.go`; task 12 run 1: `(n * dim) && dim != 0` in `ann/flat_i8_persist.go`) |

- On the second row, B's count is half A's: the shape the rule asked for. It is one row of a split chosen after the
  runs, at 4 against 2, so it is a reason to look again, not a result.
- Metric 2 points the other way: in no case did the note's next change fix the build. In task 5 run 1 the note
  reported the broken `banner.go`, and the run ended on an error with the file still broken.

## What this leaves

- `build_after_edit` stays in tack, off by default. It is not in the bundle.
- If it is screened again, register the second row's measure first: the model's own files, built at the run's end,
  separate from the oracle's test files.
- Qwen3-8B mostly does not act on the note. A model that does would show it in metric 2 first, so a cheap tier-1
  check on metric 2 alone would come before any second screen.
