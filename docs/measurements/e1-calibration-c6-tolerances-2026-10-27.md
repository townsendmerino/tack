# E1 calibration C6: the safe edit tolerances, judged by replay, 2026-10-27

<!-- citations-at: 0876e65 -->

§12.22's replay, registered before the code. **Kept: all whitespace ignored, and a trailing `;` ignored. Left out: `,`
`)` `]` `}` `.` `:`.** The setting `[run] edit_tolerances` is in tack, off by default. It joins the bundle after
§12.21's run, whose arms stay as registered.

## What ran

- **Code:** `internal/tools/tolerance.go:MatchTolerances`, the function the edit tool calls.
  - The edit applies only in a `.go` file, only when exactly one place matches, and only when the file still parses
    afterwards.
- **Replay:** `tools/replaytolerant`, which rebuilds each file as it was at the failed edit, as `tools/replayedits` did
  for 4.1. Each kind is tried alone.
  - Set A: C4, C5, 4.6 and 4.4, 216 runs, 285 failed `.go` edits whose text was not in the file.
  - Set B: the bundle-era Qwen3-8B screens and checks (4.1, 4.8, 4.2, the build screen, the edit_lines and two
    current_symbol checks), 335 runs, 94 such edits.
- **Records:** `~/tack-runs/e1/c6-tolerances-2026-10-27/` on the Mac: `setA.txt`, `setB.txt`, and each case in
  `set*-cases.jsonl`.

## Per tolerance

| tolerance | set A: calls (distinct) | set B | parses after | on the fix's lines | every edit the one the model meant? | verdict |
|---|---|---|---|---|---|---|
| all whitespace ignored | 61 (2) | 0 | 61 of 61 | 61 of 61 | yes | **kept** |
| a trailing `;` ignored | 11 (1) | 8 (1) | 19 of 19 | 0 | yes | **kept** |
| a trailing `)` ignored | 32 (1) | 0 | 0 of 32 | 32 | no | left out |
| a trailing `:` ignored | 0 | 12 (3) | 0 of 12 | 9 | no | left out |
| a trailing `,` `]` `}` `.` ignored | 0 | 0 | — | — | — | left out: nothing to apply |

**The cases, read one by one:**
- **All whitespace ignored:** task 11, A-1 in 4.6 and in 4.4. The model wrote `return "" , fmt.Errorf(…)`, with a space
  before the comma. The applied edit lowercases the error string, which is the task's fix.
- **A trailing `;`:** task 6, 4.6's A-2 and 4.2's A-3. The model's `old_string` ends a line with `;`, and its first
  line's indentation drifts too. The applied edit adds the condition it wrote (`lm.gpuBackend &&`, `&& f.resident`).
  - The result parses; the `;` and the extra tab are legal, and gofmt would drop them.
  - It is not on the fix's lines: the edit is what the model meant, not the fix.
- **A trailing `)`, left out:** task 11, 4.6's B-2. Dropping the model's final `)` placed its text inside the `return`
  line, and its `new_string` has no closing `)` either, so the edited file does not parse. The guard would have
  refused it, but the kind applies nothing right, so it stays out.
- **A trailing `:`, left out:** task 2 and task 5, three edits. Each would leave `::` or a `case` with no colon. None
  parses. Dropping `:` meets the model's own mistyped `":` and makes it worse.

## What this leaves

- Across 551 runs, the kept tolerances rescue 4 runs' failed edits: 3 in set A, 1 in set B. That is about one rescued
  run per 140, too few for a screen to show in passes. That is why they are adopted by replay, as §12.14's guard was.
- The parse guard refused every wrong edit the replay found. It stays a requirement for any tolerance added later.
- Tier-0 tests, each broken once: `internal/tools/tolerance_test.go`, and `TestEditTolerancesThroughTheCore` for the
  wiring.
