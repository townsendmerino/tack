# E1 calibration C6: the restate_task and edit_lines tier-1 checks, 2026-10-26

<!-- citations-at: 38fe8dd -->

The two tier-1 checks registered in §12.18 and §12.19 before their runs. **Both fail their pass rules; neither screen
runs, and both settings stay in tack, off.**

## What ran

- **When:**
  - `restate_task`: 2026-10-26 03:36:41–03:40:04 PDT.
  - `edit_lines`: 03:40:20–03:56:24.
  - Both held nobara's lock and the Mac's.
  - The launcher waited behind §12.17's screen, goinfer's day jobs and its 00:30 night job (`ks-o4-gfid`).
- **Builds:** tack `a2679df`, clean, on both machines; goinfer `b2b5e816`; thinking off; decode path
  `cuda-resident (int4)` for both models.
- **Before the start:** Go's build cache was cleared at 03:36 (19 → 23 GiB; owner's standing permission).
- **Cells:**
  - Both cell checks were clean: nobara at load 1.42 and 1.76, with 57.9 and 57.8 GB available; the Mac on mains.
  - 0 void.
- **Settings:** the bundle (`fit_window`, `stop_repeats` amended, `result_notes`, `instructions_head`,
  `refuse_duplicate_edits`, `verify_done`) plus the setting under check; `build_after_edit` off.
- **First messages:** the first user message of every run's first request is byte for byte the one in the earlier
  check's run on the same task: the 2026-10-21 Qwen3-4B bundle check for `restate_task`, and the 2026-10-20 Qwen3-8B
  head check for `edit_lines`.
- **Records:** `~/tack-runs/e1/c6-t1-2026-10-25/` on the Mac: `restate-qwen3-4b/`, `edit-lines-qwen3-8b/`,
  `launch.log` and `notes.txt`.

## §12.18, restate_task (Qwen3-4B at 30,720; tasks 2, 3, 4, 6, 8, 9, two runs each)

**Fails: 0 of the 8 runs refused with the restated text made an `edit` or `write` after it. The rule needed 3.**
- 8 runs were refused, so the check reads as a result, not "not shown" (that needed fewer than 4).
- Every one of the 8 called `done` again at once, and that call ended the run.
- The restated refusal was the one the model received: "no files changed; the task asks for a change. The task
  was: …".

| task | run 1 | run 2 |
|---|---|---|
| 2 | `repeat_loop`, no refusal | refused after 5 calls, then `done` |
| 3 | refused after 4 calls, then `done` | refused after 4 calls, then `done` |
| 4 | refused after 3 calls, then `done` | refused after 3 calls, then `done` |
| 6 | `repeat_loop`, no refusal | `repeat_loop`, no refusal |
| 8 | `done` after a change, no refusal | refused after 1 call, then `done` |
| 9 | refused at once (0 calls), then `done` | refused at once, then `done` |

0 of 12 passed (2026-10-21: 0 of 12). On 2026-10-21, 7 runs were refused and 0 of the 7 acted after it. Quoting the
task back changed nothing in what Qwen3-4B did next.

## §12.19, edit_lines (Qwen3-8B at 16,384; tasks 2, 4, 6, two runs each)

**Fails: 0 of the 6 runs made a line edit. The rule needed 3.**
- The schema and description the model received carried the line form: `"start_line":{"type":"integer"}` and "Or
  give start_line and end_line, as read numbers them, instead of old_string: new_string replaces those lines."
- The model made 21 `edit` calls, all with `old_string`. 17 of them failed.

| task | run 1 | run 2 |
|---|---|---|
| 2 | 8 edits, 8 failed; `window_full` | 1 edit; `window_full`; **passed** |
| 4 | 1 edit; cancelled (the cap) | 8 edits, 8 failed; `budget` |
| 6 | 1 edit; `max_tokens` | 2 edits, 1 failed; `repeat_loop` |

## What this leaves

- Neither screen runs, and neither setting enters the bundle. `restate_task` and `edit_lines` stay in tack, off.
- `restate_task`:
  - Qwen3-4B does not act on the task's text when it is quoted back.
  - Its runs that quit do so on the second `done`, whatever the first refusal said.
  - A change there would have to alter the rule itself (a second refusal, or none ending the run), not its text. That
    is a different candidate, with R13's loop risk (DESIGN §17).
- `edit_lines`:
  - Qwen3-8B does not pick up an optional form of a tool it already uses, even after `old_string` fails.
  - Taking it further would mean steering it after a failure: an `old_string` failure's reply pointing to the range
    form. 4.1 showed that reply text alone did not change what the model resent (§12.10).
