# E1 calibration C6: the bundle on Qwen3-4B, an exploratory point, 2026-10-21

<!-- citations-at: 1dfb475 -->

This is the exploratory point §12.15 registered, to follow 4.2's screen: the bundle on Qwen3-4B, one arm. It has no A
arm, and its only comparison, C4, ran an older setup (goinfer `f20f0d93`, no `fit_window` or `stop_repeats`, the whole
instruction file). No rule follows from it.

**Outcome: 2 of 12 runs made an edit attempt before `done`,** both on task 8. C4 had 0 of 18 on these six tasks.

## What ran

- **When:** 07:33:03–07:37:30 PDT, holding nobara's lock and the Mac's lock. The launcher waited from 07:30 for mains.
- **Settings:** `fit_window`, `stop_repeats`, `result_notes`, `instructions_head`, `refuse_duplicate_edits`,
  `verify_done`. Tasks 2, 3, 4, 6, 8 and 9, two runs each (`-only 2,3,4,6,8,9 -reps 2`).
- **Builds:**
  - tack `9b71ee0`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-4B as C4 loaded it (bf16, int4 at load), at 30,720, thinking off.
- **Before the start:** Go's build cache was cleared (owner's standing permission).
- **Cells:** clean, all on mains.
- **Other load:** none on nobara. On the Mac:
  - goinfer-3f's builds at 07:28–07:32, before the start;
  - tack's own sandbox tests at 07:37:21–38, in the last seconds.

  The Mac is the harness box, so this voids nothing.
- **Records:** `~/tack-runs/e1/c6-4b-bundle-2026-10-21/` on the Mac.

## The runs

| task | run 1 | run 2 |
|---|---|---|
| 2 | searched (grep, read), then `done` with no change | searched, then `done` with no change |
| 3 | searched, then `done` with no change | searched, then `done` with no change |
| 4 | 63 greps, `repeat_loop` | searched, then `done` with no change |
| 6 | 10 greps, `repeat_loop` | 10 greps, `repeat_loop` |
| 8 | **edit**, write, build, edit, build, `done` | **edit** (×2), grep, `done` |
| 9 | `done` at once | `done` at once |

- **Unlike C4, no run began with `done`, except task 9's two.** Qwen3-8B also calls `done` at once on task 9. The other
  runs searched first. The 7 runs that then called `done` with nothing changed were refused once as "no files
  changed", and their next `done` ended them.
- **4.2 at work (task 8, run 1).** The model wrote a stub `serve.go` and called `done`, and was refused. It ran
  `go build ./...`, which failed on its own file. It fixed the file, was refused again since nothing had built since,
  built cleanly, and finished. The refusal did what it is for: a build after a change caught the model's own error. The
  change itself was not the fix: the run did not pass.
- No run passed.

## What it means

- Qwen3-4B's quitting at once is mostly gone with the new layout, but its runs still end without an attempt. It now
  searches, then declares the work done.
- The unregistered candidate, a no-change `done` refusal that restates the task (§12.12), is aimed at exactly those
  7 runs.
