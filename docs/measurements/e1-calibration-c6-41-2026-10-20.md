# E1 calibration C6, change 4.1: edit messages, a screen, 2026-10-20

<!-- citations-at: e8823a6 -->

This is change 4.1 under C6, registered in §12.10 before its runs, as a screen.
- A is A1 with `fit_window`, `stop_repeats` and `result_notes`.
- B is A plus `edit_messages`.

**Verdict, as registered: 4.1 does not enter the bundle. "Not shown."**
- **Neither edit mechanism moved,** although d = +1 (B 3/36, A 2/36) meets the floor of −1.
- After a failed edit, the next edit to that file succeeded 3 times in B and 3 times in A.
- Every failed next edit, 27 in B and 26 in A, sent the identical old_string again.
- The model resends the same text whatever the message says.

## What ran: three launches, 72 counted runs

| launch | when (PDT) | counted | not counted |
|---|---|---|---|
| first | 08:21:47–09:58:42 | tasks 1–3 (18 runs) | task 4: 5 runs, an incomplete block. Stopped when the Mac went onto battery about 09:26: task 4 B-3 was void 30 times in a row. |
| resumed | 10:32:06–11:16:59 | tasks 4–5 (12) | task 6: 3 runs, an incomplete block. Stopped at the 20 GB disk floor: 39 runs void before starting. |
| resumed again | 11:41:01–12:23:23 | tasks 6–12 (42) | — |

- **Completeness rule (§12.10, registered before the first resumption):** a task whose block of six did not complete is
  re-run whole. Its earlier runs are recorded and not counted.
- **Before the last launch, with the owner's yes:**
  - Go's and tack's sandbox build caches were cleared (19 → 31 GB free);
  - tack's sandbox cache was then warmed untimed at tasks 6–12's base commits (11:19:54–11:22:00, holding the Mac's
    lock shared): the standard library and every dependency, since each run still compiles its own repository's
    packages.
- **Builds:**
  - tack `6743122`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Cells:**
  - no counted cell is void;
  - every counted run was on mains;
  - nobara's load at each counted cell's start was 0.0–1.4 of 16 cores, with about 55.5 GB available.
- **Records:** `~/tack-runs/e1/c6-41-2026-10-20/` on the Mac: `runs/`, `runs-resume/` and `runs-resume2/`.

## Lock and box events (owner, 2026-10-20)

- **The Mac's timing lock:**
  - The first launch held it exclusive from 08:21:47.
  - At about 08:27 I deleted the lock file to release it. That was wrong: the holder kept its lock on the deleted
    file, and a new run could have locked a new file beside it.
  - No timed run took the Mac's lock before that launch ended at 09:58:42. The file was never recreated, and goinfer's
    Mac queue was idle.
  - The owner kept the screen running, with no relaunch.
  - The later launches held the Mac's lock exclusive and normally.
- **goinfer on nobara during the first launch** (its own records):
  - 08:35:00–08:35:20: status checks, an scp, and a `night.py add`;
  - 08:36:17–08:37:12: `night.py update-runner`, a git fetch and checkout, and a serve restart;
  - about 08:37:50–08:38:30: status checks;
  - 08:47:41: one ssh read.

  These overlapped task 1's B-2, A-2 and B-3. **The owner kept all three.** They were not timed runs, every one of those
  cells started light, and none hit the cap.
- **goinfer's Mac work** (08:28–08:46: builds and tests, no timed runs) overlapped task 1–2 cells, whose Mac load read
  4–32. The Mac is the harness box, so its load is recorded and voids nothing.
- **The one cap hit** among counted runs is task 4 B-1, at 10:32 PDT, with the Mac at load 3.0. It did not coincide
  with goinfer's Mac work.
- **kv0h:** another session's runs held nobara's lock 10:10–10:32 and 11:17–11:41. tack's launcher waited for them, as
  designed.

## Task by task

| task | A passes | B passes | d | A window-full | B window-full | A `repeat_loop` | B `repeat_loop` |
|---|---|---|---|---|---|---|---|
| 1 | 0/3 | 0/3 | 0 | 0 | 0 | 0 | 0 |
| 2 | 0/3 | **1/3** | +1 | 0 | 0 | 3 | 2 |
| 3 | 0/3 | 0/3 | 0 | 0 | 0 | 1 | 2 |
| 4 | 0/3 | 0/3 | 0 | 0 | 0 | 1 | 0 |
| 5 | 0/3 | 0/3 | 0 | 1 | 0 | 0 | 1 |
| 6 | 0/3 | 0/3 | 0 | 0 | 0 | 0 | 1 |
| 7 | 0/3 | 0/3 | 0 | 0 | 0 | 1 | 1 |
| 8 | 0/3 | 0/3 | 0 | 0 | 0 | 0 | 0 |
| 9 | 0/3 | 0/3 | 0 | 1 | 0 | 0 | 0 |
| 10 | 0/3 | 0/3 | 0 | 0 | 0 | 2 | 1 |
| 11 | 2/3 | 2/3 | 0 | 0 | 0 | 0 | 0 |
| 12 | 0/3 | 0/3 | 0 | 0 | 0 | 0 | 2 |
| **all** | **2/36** | **3/36** | **+1** | **2** | **0** | **8** | **10** |

Over the 9 tasks without tasks 3, 8 and 9: A 2/27, B 3/27.

## The edit mechanisms

| arm | failed edits | next edit to that file succeeded | next edit failed (identical old_string) | no further edit |
|---|---|---|---|---|
| A | 37 | 3 | 26 (26) | 8 |
| B | 36 | 3 | 27 (27) | 6 |

**Failure kinds, from the tool's reply:**

| arm | text not found | path missing | old equals new | a repeat of the call that just failed (not run) | not read first |
|---|---|---|---|---|---|
| A | 23 | 7 | 3 | 2 | 2 |
| B | 1 | 13 | 7 | 14 | 1 |

B's "not found" failures moved into the other kinds. After a "not found" message, the model's next attempt was mostly
the identical call, which the loop answers "not run: same call".

**Against the predictions (§12.10):**

| prediction | outcome |
|---|---|
| after a failed edit, B's next edit to that file succeeds at least twice as often as A's | 3 against 3: **missed** |
| B resends the identical old_string at most half as often as A | 27 against 26: **missed** |
| the replay's largest kinds (missing paths, old equal to new) appear | they do, in both arms: held |
| d between −1 and +1 | +1: held |

## 4.5's live check, in the same runs

- Over all 80 runs executed, the 72 counted and the two incomplete blocks, `stop_repeats` ended 18 runs with
  `repeat_loop`.
- All 18 exited with status 9.
- `tools/replayrepeats` stops exactly those 18, each at its own last request.
- No disagreement, and no passing run stopped.

## What it means

- **The messages reach the model and do not change what it does next.** Its next move after a failed edit is to resend
  the same text.
- The replay already showed that 4.1's failures are wrong text, not slips a message could correct. This screen shows
  the model does not act on the correction either.
- 4.1 stays out of the bundle. `edit_messages` stays in tack, off by default.
- **The leftover empty `tack-session-*` directories,** from the first launch, are still to be traced (owner).
