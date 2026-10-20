# E1 calibration C6, change 4.2: the screen stopped at 41 of 72 runs, 2026-10-20

<!-- citations-at: 84eecf1 -->

This is change 4.2's screen, registered in §12.15. It was stopped, and it has no verdict.

**Why it was stopped: the budgeted head (§12.12), as built, makes Qwen3-8B treat goinfer's tasks as a request to read
instructions.**
- 35 of the 36 runs on tasks 1–6 (all goinfer) called `done` at once with no other call, in both arms. The refusal of
  `done` with nothing changed came once, and then the second `done` ended the run.
- The 5 runs on task 7 (ken, whose head is 642 bytes) worked as before: 15–28 requests each.
- 4.2's rule acts only after a change, so it never fired. The screen measured nothing about 4.2.

## What ran

- **When:** 16:39:11–16:50:57 PDT. The pilot was stopped with SIGTERM after run 41 of 72 (task 7 A-2). Its locks went
  with it; no lock file was touched.
- **Before the start:**
  - Go's build cache was cleared (owner's standing permission), 18.4 → 25.3 GiB free;
  - tack's sandbox cache was warmed untimed for all 12 tasks, 16:16–16:19, under the Mac's lock held shared.
- **Builds:**
  - tack `84eecf1`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, thinking off.
- **Cells:** none void. The cell check at 16:39 read nobara at load 2.13 with 55.5 GB available, the model loaded.
- **Other load:** another session's Go tests ran on the Mac 16:45:12–16:49:04 (goinfer-3f's report). The Mac is the
  harness box, so this voids nothing. Nothing else ran on nobara.
- **Records:** `~/tack-runs/e1/c6-42-2026-10-20/` on the Mac (`runs/`, `runs.log`, `launch.log`, `notes.txt`).

## The runs

| task | repo | A: runs, done with no work | B: runs, done with no work | requests per run |
|---|---|---|---|---|
| 1–3, 5, 6 | goinfer | 3 of 3 each | 3 of 3 each | 2 |
| 4 | goinfer | 3 of 3 | 2 of 3 | A 2, B 5 |
| 7 | ken | 3, none | 2, none | A 18, B 22 |

What the model said it was doing, in `done`'s reason:
- task 2: "The task is to understand the repository's context and instructions, which have been reviewed. No code
  changes are needed at this time."
- task 4: "The task is to understand and address the issue with the `pull.Select` function and the model storage
  behavior described in the CLAUDE.md document."
- task 6: "… no specific task was requested to perform."

## What the model saw

1. The task statement.
2. The head:
   - its frame: "Project instructions, from CLAUDE.md: its first section, then the headings of the rest. Read CLAUDE.md
     for any section you need.";
   - goinfer's first section (its model-storage rules, about 2 KB);
   - "Other sections of CLAUDE.md:" and twelve headings.

With the whole file in the same place (4.8's arm A), and with no file (4.8's arm B), the same model worked on these
tasks. The head ends the message with a short imperative frame and a table of contents, and that is what the model
took for its task.

## What it means

- The head's mechanism check (§12.12: first-prompt tokens under the cap) cannot see this. It measures size, not what
  the model does.
- The head must change before 4.2 is screened again. The owner chooses the fix; one tier-1 check on goinfer tasks
  should come before any 72-run screen.
