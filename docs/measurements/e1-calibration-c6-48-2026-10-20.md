# E1 calibration C6, change 4.8: instruction files, whole against none, 2026-10-20

<!-- citations-at: fdef819 -->

This is change 4.8 under C6, registered in §12.11 before its runs, as a screen.
- A is the bundle (`fit_window`, `stop_repeats`, `result_notes`) with the repository's instruction file loaded whole.
- B is A plus `no_instructions`.

**Verdict, as registered: the window cost of whole instructions is shown.**
- Window-full stops fell from 4 in A to 1 in B.
- d = +2 (B 4/36, A 2/36), so d ≥ −1.
- **By §12.11's rule, section loading (4.8b) is built next and screened against whole.** "None" never enters the bundle.

**The evidence is thin, and the record says so.** The window-full counts are 4 against 1. One of A's 4 is on task 12,
whose arms are identical (it has no instruction file). Task 12's two arms also differ by a pass. At 72 runs, a
difference of that size is within what identical arms produce.

## What ran

- **When:** 12:50:45–14:23:49 PDT (1 h 33 m), holding nobara's lock and the Mac's lock exclusively.
  - It waited 12:28–12:50 for another session's kv0h study on nobara.
  - The sandbox cache was warmed untimed for all 12 tasks beforehand: tasks 1–5 at 12:26–12:28, tasks 6–12 at
    11:20–11:22.
- **Builds:**
  - tack `1ea11e7`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Cells:**
  - 0 void, all 72 on mains;
  - the cell check before the start read nobara at load 0.90 with 55.6 GB available, model loaded.
- **Records:** `~/tack-runs/e1/c6-48-2026-10-20/runs/` on the Mac.

## Task by task

| task | A passes | B passes | d | A window-full | B window-full | A requests/run | B requests/run | A first prompt | B first prompt |
|---|---|---|---|---|---|---|---|---|---|
| 1 | 0/3 | 0/3 | 0 | 0 | 0 | 14 | 19 | 10,580 | 878 |
| 2 | 0/3 | 0/3 | 0 | 2 | 0 | 23 | 16 | 11,626 | 915 |
| 3 | 0/3 | 0/3 | 0 | 0 | 0 | 13 | 23 | 10,674 | 972 |
| 4 | 0/3 | 0/3 | 0 | 0 | 0 | 5 | 6 | 6,894 | 926 |
| 5 | 0/3 | 0/3 | 0 | 0 | 0 | 23 | 45 | 10,643 | 941 |
| 6 | 0/3 | 0/3 | 0 | 1 | 0 | 41 | 11 | 8,685 | 928 |
| 7 | 0/3 | 0/3 | 0 | 0 | 1 | 13 | 33 | 5,282 | 913 |
| 8 | 0/3 | 0/3 | 0 | 0 | 0 | 2 | 2 | 6,463 | 921 |
| 9 | 0/3 | 0/3 | 0 | 0 | 0 | 6 | 2 | 9,693 | 999 |
| 10 | 0/3 | 0/3 | 0 | 0 | 0 | 21 | 9 | 6,229 | 893 |
| 11 | 2/3 | **3/3** | +1 | 0 | 0 | 9 | 5 | 4,064 | 875 |
| 12 (control) | 0/3 | **1/3** | +1 | 1 | 0 | 19 | 13 | 906 | 906 |
| **all** | **2/36** | **4/36** | **+2** | **4** | **1** | **16** | **15** | | |

Over the 9 tasks without tasks 3, 8 and 9: A 2/27, B 4/27.

## Against the predictions (§12.11)

| prediction | outcome |
|---|---|
| B's first prompt equals the predicted figure within 20 tokens on every task | every task 9 tokens under it (task 12: equal): **held** |
| B has fewer window-full stops, mostly on tasks 1, 2, 3, 5 and 9 | 1 against 4: **held** in total. A's stops were on tasks 2 (2), 6 and 12, so only 2 of 4 were on the predicted tasks. |
| task 12's two arms end alike | A: 0 passes and 1 window-full stop; B: 1 pass and 0: **missed**. This shows the noise between identical arms at this size. |
| d between −1 and +2 | +2: **held** |

## 4.5's live check, and a finding

- **Agreement:** `stop_repeats` ended 19 runs, and all 19 exited with status 9. `tools/replayrepeats` stops exactly
  those 19, each at its own last request. The live rule and the replay agree.
- **One of the 19 passed:** task 12 B-3. The replay tool's verdict line flags it, but the stop did not cost the pass.
  - The model's first edit added a check at the top of `flatI8Layout` and succeeded. Its new_string contains its
    old_string, so the same edit, sent 8 more times, matched and applied again each time.
  - The file ended with 9 identical copies of the check, and the oracle passes with them.
  - Every repeat's reply was the same ("edited ann/flat_i8_persist.go at line 97"), so the rule counted unchanged
    repeats and ended the run at the 8th. The note `result_notes` added each time said the call "got the same
    result".
- **The finding:** the rule reads "unchanged" from the reply text. An edit whose new_string contains its old_string can
  repeat with the same reply while the file changes. Here the stop limited the damage.
  - The edit tool could say so: for example, refuse or flag an edit whose old_string occurs inside text the session
    itself inserted.
  - That is a candidate, not a change.

## What it means

- Without the instruction file, goinfer's tasks start with about 900 tokens instead of 6,900–11,600. That frees
  5,000–10,700 tokens of the 16k window.
- The runs that gained room did not convert it into passes, apart from task 11, which already passed. Window-full
  stops were already rare with the bundle on (4 in 36).
- Under the registered rule, section loading (4.8b) is next: ken v1.10.0's K9 sections, about a day to build, then its
  own screen against whole.
- Given how thin the window-full evidence is, the owner may instead prefer §12.11's noted fallback, a budgeted head
  (headings plus the first section, capped at 15% of the window), as the cheaper first step.
