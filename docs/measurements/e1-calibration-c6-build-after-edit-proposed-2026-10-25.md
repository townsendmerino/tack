# C6 candidate: a build after every edit — a proposed screen, 2026-10-25

**Status: PROPOSED, not registered, not run.** The setting is built and tested at tier 0 (branch
`c6-build-after-edit`). Nothing here is queued. The decision rule below is a draft for the owner to set before any
run, as the pre-registration's §12 requires.

The candidate comes from `e1-calibration-c6-status-2026-10-21.md`, "Candidates, not registered": the 2026-10-24
addition, "A build after every edit".

## The change, as built

- **Setting:** `[run] build_after_edit` (`config.Run.BuildAfterEdit`). It is off by default, and it is an arm setting
  `tack eval pilot -setting` accepts (`pilot.RunSettings`).
- **Code:** `internal/loop/build.go` (`Session.buildNote`, `buildErrors`), called from the turn after each tool call.
- **What it does:** after a successful `edit` or `write` of a `.go` file, tack builds that file's package in the
  session's own shell, inside the sandbox.
  - The command is `go -C <module root> build -o /dev/null ./<package>`, or `go test -c -o /dev/null ./<package>` for
    a `_test.go` file, so test files are compiled too. Nothing is run.
  - `-C` leaves the session's working directory as the model's `cd` left it (`TestBuildAfterEditInTheSandbox`).
- **When the package does not build,** the tool's result gains a note, after a blank line:

  ```
  [tack: this change does not build (go build ./p):
  p/x.go:3:37: undefined: c]
  ```

  - go's `# package` headers are dropped and the module's path is cut.
  - At most 10 lines are shown, each cut at 200 characters, then "… N more lines".
- **It adds nothing:**
  - when the package builds;
  - when the file is in no module inside the workspace;
  - when the build fails to finish within 60 s or is stopped;
  - when the permission engine would not run the same `bash` call unasked. The command is tack's, so it runs where
    the model's own call would run without a question: in auto mode, inside the sandbox. Every A1 run is there.
- **Not a verification:** `verify_done` counts only the model's own builds and tests, so the two settings stay
  separate (`TestBuildAfterEditIsNotAVerification`).
- **The fixed prompt is unchanged.** No tool's schema or description and no system prompt changes, and the note is
  tail content (DESIGN §7.2).
- **The repeat rule compares results without the note:** `loop.StripNotes` removes it, as it removes the one-line
  notes.

## Why

From the C6 status note, over the recorded C6 runs:

- 62 runs changed code and still failed. 23 of those left the model's own code not compiling, across 8 of the 12
  tasks.
- Examples: two `default` cases in one switch, a field that doesn't exist, a missing closing brace.
- Those runs had 30 passes in all.
- A package build costs about a second, against a median model request of 1.1 s. The first build of a run pays for the
  sandbox's Go cache, which is warmed untimed first, as in §12.15.

## Draft screen (for the owner to set)

- **Arms:**
  - A is the bundle as the bundle night will run it (status note, "In the bundle"): `fit_window`, `stop_repeats` (as
    amended), `refuse_duplicate_edits`, `result_notes`, `verify_done` and `instructions_head`, under `--instructions
    whole`.
  - B is A plus `build_after_edit`.
  - A is this change's do-nothing arm.
- **Build:** tack at the commit that carries the setting, clean; goinfer at the bundle night's pin; Qwen3-8B `Q4_K_M`
  at 16,384, `-serve-option thinking-off`.
- **Run:**
  - the 12 tasks, A B B A A B, 72 runs, each after the cell check, interleaved per task;
  - by day, about as §12.15's screen of 4.2, which used the same shape (about 1 h 45 m), plus about a second per edit
    in B.
  - It waits for nobara to be free of goinfer's work.
- **Mechanism metrics:**
  1. In B, the share of `.go` edits and writes whose result carries the note: how often the model's change breaks the
     build.
  2. In B, after each note, whether the run's next `.go` edit or write builds (no note), breaks it again, or never
     comes.
  3. Per arm, the runs whose last change leaves the code not compiling. A's count is the baseline the 23 above
     describe.
  4. The cost per arm: wall time per run, requests per run, window-full stops, and the build time per edit in B.
- **Draft predictions:**
  - metric 3: B at most half A's count;
  - metric 2: the next `.go` change builds after at least half of B's notes;
  - d between −1 and +3.
- **Draft decision rule,** in the shape of the other screens:
  - **Enters:** metric 3 moves as predicted (B at most half A's count) and d ≥ −1.
  - **Parked** (examined, not adopted): metric 3 moves but d < −1, or d ≥ +2 with metric 3 unmoved, since a gain
    with no mechanism needs one before it moves a bar.
  - **Not adopted:** neither. At 72 runs, "not adopted" means "not shown".
  - The margin is the other screens'. Its size against the variance is for the owner to confirm when this is
    registered.
- **The harness:** `tack eval pilot -a1 … -setting fit_window -setting stop_repeats -setting result_notes -setting
  instructions_head -setting refuse_duplicate_edits -setting verify_done -compare-setting build_after_edit`.

## Open before registering

- Whether A carries `verify_done`. With it, every B run that ends in `done` has built at the model's own hand as well;
  without it, the note is the only build many runs see.
- The 60 s timeout on goinfer's packages in the sandbox: one untimed build per task base before the screen, to see that
  it fits.
