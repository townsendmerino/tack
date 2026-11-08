# E1 calibration C6: edits and writes that leave a Go file unparseable, by replay, 2026-11-08

<!-- citations-at: 1533b8b -->

The question (Francis, 2026-11-08, from an outside note on edit matching; DESIGN §7.5): would refusing an edit or write
that leaves a `.go` file unparseable, before it reaches disk, change pass rates? Today only edits placed by
`edit_tolerances` are checked for parsing. This is a replay to decide whether a screen is worth it, not a registered
measurement.

## What ran

- **Tool:** `tools/replayparse`. For each run it rebuilds each file from the task's base commit in its mirror plus the
  run's own successful edits and writes, as `tools/replaytolerant` does. It follows every `.go` file outside `testdata/`.
- **A break** is a successful edit or write after which a file that parsed no longer parses. It is restored when a later
  edit or write makes the file parse again.
- **Runs:** every non-void run under `~/tack-runs/e1` with a `results.jsonl`: 1,677 runs, 273 passing, 1,099 successful
  `.go` edits. Every edit could be placed, so no file was dropped from the replay.
- **Records:** `~/tack-runs/e1/c6-parse-breaks-2026-11-08/` on the Mac: `parse-replay.txt` (per job) and
  `parse-breaks.jsonl` (each break).

## Results

45 breaks in 43 runs. 38 runs ended with a file that does not parse: 37 failed, and 1 passed (a broken test file the
oracle did not need).

| | breaks | later restored | restored, in a passing run | never restored, in a failing run |
|---|---|---|---|---|
| `write` | 16 | 0 | 0 | 15 |
| `edit` | 29 | 6 | 4 | 23 |

- **Writes:** 15 of the 16 left a `.go` file with no `package` clause. The model wrote a fragment over a whole file,
  which destroys it. None was ever repaired. Refusing them would have harmed no run here.
- **Edits:** 23 breaks were never repaired, all in failing runs. 6 were a step in a change made over several edits, for
  example an `if` opened in one edit and closed in the next. They were repaired 1 to 11 tool calls later. 4 of those 6
  are in passing runs, all task 10, 3 of them in the newest screen (`c10-cliff-2026-11-02`). A refusal would have
  stopped a step those runs needed.

## What this leaves

- **The ceiling is small.** Even if every unrepaired break had become a pass, that is 37 runs of 1,677 (2.2%), and the
  §12.17 build note shows the model often does not recover once told its code is broken.
- **A guard on writes** is safe on this evidence. It has 15 cases and no harmed run, and it protects the file whatever
  the pass rate does. It can be adopted by replay, as `edit_tolerances` was.
- **A guard on edits** would cost runs as well as save them. On this evidence it is not worth a screen. It becomes
  worth another look if `multi_edit` is built, since a change made in several steps could then be sent as one call.
