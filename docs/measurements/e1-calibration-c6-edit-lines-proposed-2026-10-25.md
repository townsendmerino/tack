# C6 candidate 4.9: edit by line range — proposed tier-1 check and screen, 2026-10-25

**Status: the tier-1 check is registered as §12.19 of the pre-registration** (owner, 2026-10-25: both rules as drafted).
The screen's rule is approved and is registered with its run after a passed check. §12.19 is the authority; this page
stays as it was proposed. The candidate is §4.9 of the pre-registration.

## The change, as built

- **Setting:** `[run] edit_lines` (`config.Run.EditLines`, `tools.Options.LineEdits`). It is off by default, and it
  is an arm setting for `tack eval pilot`.
- **What the model can now send:** `edit` also takes `start_line` and `end_line` with `new_string`, and
  `new_string` replaces those lines. An empty `new_string` deletes them. The description gains one sentence: "Or give
  start_line and end_line, as read numbers them, instead of old_string: new_string replaces those lines."
- **The guard** (`internal/tools/edit_lines.go:editTool.byLines`). The edit is refused unless:
  - every line of the range was shown to the model by `read` or `symbol`;
  - those lines are the same now as when they were shown.
- **Why not `lastSeen`:** §4.9 named the workspace's `lastSeen`, but tack updates that after its own edits. A range
  shifted by the model's earlier edit would pass a check against it. So tack keeps, per file, the content `read` or
  `symbol` showed and the ranges shown of it. `edit` and `write` leave that record alone, since the model has not
  seen the new numbers.
  - The tests show both shifts refused, after the model's own edit and after an outside change, each failing when the
    guard is removed.
- **Also refused:** a range holding a line that `read` cuts (over 300 characters), an edit that changes nothing, and,
  with `refuse_duplicate_edits`, a repeated edit. `old_string` and `symbol` work as before.
- **Fixed prompt:** the largest set is 1,197 tokens, up 38 from 1,159 (`TestFixedPromptBudget`, recorded
  2026-10-25). §4.9 estimated 40–60. Without the setting the schema is byte for byte as before (`TestEditSchemaForms`).

## Why (§4.9)

- In 4.1, after a failed edit, 26 and 27 of the next failed edits in A and B sent the same `old_string` again
  (`e1-calibration-c6-41-2026-10-20.md`).
- In the 4.1 replay, 217 of 370 failed edits had text that is nowhere in the repository
  (`e1-calibration-c6-41-replay-2026-10-20.md`).
- A range asks the model to copy two numbers that `read` printed, not a block of text.

## Draft tier-1 check (§12.12's shape)

- **Run:** Qwen3-8B `Q4_K_M` at 16,384, thinking off, goinfer `b2b5e816`.
  - Tasks 2, 4 and 6, two runs each (6 runs), with the bundle settings plus `edit_lines`.
  - These are the tasks §12.12's check used, where the model reached edits.
- **Cost:** about 20 minutes, holding both machines' locks; §12.12's check took 21 minutes.
- **Where:** on nobara after the build-after-edit screen and goinfer's jobs release it.
- **Mechanism:**
  - how many runs make a line edit at all;
  - of the line edits, how many apply and how many are refused, and why.
- **Draft pass rule:**
  - at least 3 of the 6 runs make at least one line edit;
  - at least half of all line edits apply (not refused);
  - every first message matches the bundle's layout. Only `edit`'s schema and description change.
- **If it passes,** the screen below is registered. If the model never uses the range, the setting stays off.

## Draft screen (after a passed tier-1 check)

- **Arms:** A is the bundle as adopted at the time. B is A plus `edit_lines`. A is the do-nothing arm.
- **Run:** Qwen3-8B, the 12 tasks, A B B A A B, 72 runs, by day, about 1 h 45 m.
- **Mechanism metrics** (§4.9):
  1. edit failures by kind, per arm;
  2. in B, how often the model uses a range after an `old_string` failure;
  3. in B, line edits refused for shifted or unshown lines.
- **Draft decision rule:**
  - **Enters:** B's failed edits whose text is not in the file are at most half of A's, and d ≥ −1.
  - **Parked:** the failure kind moves but d < −1, or d ≥ +2 with no change in edit failures.
  - **Not adopted:** otherwise. At 72 runs, "not adopted" means "not shown".
