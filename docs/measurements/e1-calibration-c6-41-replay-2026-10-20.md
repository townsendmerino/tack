# E1 calibration C6, change 4.1: the tolerances replayed against every captured failed edit, 2026-10-20

<!-- citations-at: 2b62e38 -->

The owner asked for this replay before 4.1's screen is built.
- It takes every failed `edit` in the captured runs, rebuilds the file as it was at that request, and applies 4.1's
  matching (§4.1).
- It reports what each tolerance would have rescued, what it would have refused as ambiguous, and the failures it cannot
  help, by kind.

**Result: the tolerances rescue nothing.**
- Across 370 failed edit calls, 46 of them distinct, "regex escapes undone" rescues 0 and "whitespace ignored" rescues
  0. Neither would have refused a single call as ambiguous.
- So 4.1 is proposed as **messages only** (below).

## What ran

- **The matching:** `tools.MatchTolerant` on the local branch `c6-41-replay` (`2b62e38`), unpushed.
  - First "regex escapes undone": a backslash before punctuation is removed, then exact match.
  - Then "whitespace ignored": old_string's words joined by any run of whitespace.
  - Each must find exactly one place; more than one is refused.
- **The replay:** `tools/replayedits` (same commit).
  - It rebuilds each file from the task's base commit in its mirror, plus the run's own successful edits and writes,
    in order.
  - It classes each failure by the tool's reply.
  - A "this text isn't in" failure is put through the tolerances. When none matches, it is classed by where the text
    is: another file, or this file before the run's own edits.
- **The runs:** C4 (36), C5 (36), 4.6 (72) and 4.4 (72), 216 in all, with 444 edit calls.
- **Rebuild fidelity:** every one of the runs' successful edits found its text in the rebuilt file, 0 misses. 12 of the
  370 failures came after a `bash` call in the run, which the rebuild cannot replay. None was rescued either way.
- **Output:** `~/tack-runs/e1/c6-41-replayedits-2026-10-20.txt` on the Mac.

## Failures by kind

Counted twice: every failed call, and once per run, path and text. Loops repeat a failure many times; task 11's
`return "No repo specified…` alone is 162 calls.

| kind | calls | distinct |
|---|---|---|
| text not in the file, nowhere in the repository | 217 | 6 |
| text not in the file, but unique with **all** whitespace removed (diagnostic, below) | 61 | 2 |
| old_string and new_string identical ("malformed") | 30 | 11 |
| text the run's own earlier edit had changed | 19 | 7 |
| a repeat of the call that just failed (not run) | 18 | — |
| path does not exist | 17 | 12 |
| file not read first | 6 | 6 |
| exact text occurs more than once | 1 | 1 |
| text in another file | 1 | 1 |
| **rescued by "regex escapes undone"** | **0** | **0** |
| **rescued by "whitespace ignored"** | **0** | **0** |
| **refused as ambiguous** | **0** | **0** |
| **all** | **370** | **46** |

## What the failures are

- **Wrong text, not mis-formatted text.** The most common old_strings leave out code the file has. For example, the model
  writes `return "No repo specified…` where the file has `return "", fmt.Errorf("No repo specified…`. No tolerance can
  rescue those.
- **The diagnostic row.** Two distinct texts insert whitespace where the file has none (`return "" , fmt.Errorf(`).
  §4.1's whitespace tolerance treats a run of whitespace as flexible, but cannot invent one where the file has none. A
  looser rule, ignoring all whitespace, would match those 2 uniquely. They are 61 calls in loops, two texts in all. That
  rule could also join tokens the file keeps apart, so it is not proposed.
- **What messages would address,** distinct failures:
  - **path does not exist, 12:** name the nearest existing path;
  - **changed by the run's own earlier edit, 7:** say so, and name the call;
  - **nowhere, 6 (plus the 2 above):** show the nearest region of the file, with line numbers;
  - **old equals new, 11:** say the edit changes nothing, and that `done` ends the task if it is finished;
  - **in another file, 1:** name the file.

## Proposal: 4.1 as messages only

The owner's rule: if the retries rescue almost nothing, say so, and propose messages only.
- **Drop the tolerances.** Exact matching stays as it is.
- **Keep §4.1's messages:**
  - a missing path names the nearest existing path;
  - text changed by the run's own edit names that call;
  - text found nowhere shows the nearest region of the file (up to 12 lines, with line numbers);
  - text in another file names it.
- **Add one for old equal to new,** the second-largest distinct kind.
- **Its mechanism metric:** edit failures by kind, and, after each message, whether the next edit to that path
  succeeds.
