# E1 calibration C11 §17: the one-night screen of the bundle on what ships, read, 2026-11-07

<!-- citations-at: 3bd6354 -->

This is the read of `e1-calibration-preregistration-2026-10-18.md` §17: the bundle (B) against A1 with no settings (A), both
with `snapshots` on (ADR-006), on Qwen3-8B `Q4_K_M` at 16,384, goinfer `b2b5e816` on nobara, tack `4d34f17`, 96 runs. It is
read under §17's rule, unchanged. The runs are in `~/tack-runs/e1/c11-onenight-2026-11-05/night-2026-11-06/`. In the
directory above it are the report, `calreport-night-2026-11-06.md`, and the second-night arithmetic, `second-night-power/`.

## The night

**The night started at 03:35 PST on 2026-11-07, outside §17's window (22:00 to about 01:30), at Francis's word.** The
cell check ran at 03:35 and the runs from 03:36 to 06:21.

| step | when (PST) | what |
|---|---|---|
| first try | 2026-11-06 22:01 | stopped before any run on the launcher's disk check: 38 GiB free, 40 needed. It left only the stop-point marker (`done`, exit 8) |
| retry | 2026-11-07 03:30 | stopped on that marker. The coordinator freed disk and removed the marker, since nothing had run in the directory |
| warm-up | 03:32 to 03:35 | untimed, 40 GiB free at its start |
| cell check | 03:35:43 to 03:35:58 | clean: void `""`, `cuda-resident (int4)`, a fit line for the 16,384 cap |
| runs | 03:36 to 06:21 | 96 runs in 2 h 45 min, under the 3.5 h estimate. Exit 0 |

- 96 counted runs, 48 per arm and 8 slots in each of the 12 tasks, with 0 void, 0 oracle errors and 0 re-runs. One cap hit
  (task 6 B-2).
- Every run was on AC. nobara's load at the cells' starts was 0.06 to 1.73, with 59.3 to 59.7 GB available. The Mac's 1-minute
  load was 1.20 to 4.44 (recorded only).
- The config check was clean: `snapshots = true` in every config, the bundle's seven in every B config and in no A config.
- The lowest free disk during the runs was 45 GiB (`disk.tsv`), and no snapshot shadow was left at the end.
- C11's first night (2026-11-05) is void entirely and is not used (tack #73).

## The rule's read

```
go run ./tools/calreport -rule sd=2.0,n=48 -compare $P/night-1/runs/results.jsonl,$P/night-2/runs/results.jsonl \
    ~/tack-runs/e1/c11-onenight-2026-11-05/night-2026-11-06/runs/results.jsonl
```

with `P=~/tack-runs/e1/c6-powered-2026-10-30`, §12.25's two nights. It ends `VERDICT: PASS` (96 counted runs, 0 void).

| arm | passes | counted runs |
|---|---|---|
| A, A1 with no settings | 3 | 48 |
| B, the bundle | 6 | 48 |

**d = +3, against edges of 2 (1 sd) and 4 (2 sd). Under §17's rule this is parked: no more runs, and whether a second
night is worth it goes to Francis (below).** The bundle stays adopted (§12.25) whatever he decides. Pass credit is the
bundle's, not any one setting's (§12.9).

## Task by task, with §12.25 beside it (not pooled)

§12.25 ran with snapshots off on tack `f5f7ecc`. Its runs are here for context only (the Note before §17).

| task | A | B | A window-full | B window-full | §12.25 A | §12.25 B |
|---|---|---|---|---|---|---|
| 1 | 0/4 | 4/4 | 4 | 0 | 0/14 | 12/14 |
| 2 | 0/4 | 0/4 | 4 | 1 | 0/14 | 0/14 |
| 3 | 0/4 | 0/4 | 4 | 0 | 0/14 | 0/14 |
| 4 | 0/4 | 0/4 | 2 | 0 | 0/14 | 0/14 |
| 5 | 0/4 | 0/4 | 3 | 1 | 0/14 | 0/14 |
| 6 | 0/4 | 0/4 | 4 | 0 | 0/14 | 0/14 |
| 7 | 0/4 | 0/4 | 4 | 0 | 0/14 | 0/14 |
| 8 | 0/4 | 0/4 | 0 | 0 | 0/14 | 0/14 |
| 9 | 0/4 | 0/4 | 4 | 0 | 0/14 | 0/14 |
| 10 | 0/4 | 0/4 | 4 | 0 | 0/14 | 0/14 |
| 11 | 3/4 | 2/4 | 1 | 0 | 9/14 | 12/14 |
| 12 | 0/4 | 0/4 | 2 | 1 | 0/14 | 2/14 |
| **all** | **3/48** | **6/48** | **36** | **3** | **9/168** | **26/168** |

The +3 is task 1 (+4) and task 11 (−1). As in §12.25, the other nine tasks pass in neither arm. Task 12, +2 in §12.25,
passed in neither arm here.

## The predictions (made before any run)

| prediction | result | |
|---|---|---|
| A passes 0 to 6 of 48 | 3 | held |
| B passes 3 to 13 of 48 | 6 | held |
| d between +1 and +11 | +3 | held |
| task 1: B at least 3 of 4, A at most 1 | B 4, A 0 | held |
| task 11: A at least 2 of 4 | 3 | held |
| every run with a bash call has a `checkpoint` event, and none is unavailable | 44 of 44 runs, 0 unavailable | held |
| snapshots add under 10% to a run's wall time | not measured | no snapshot-off arm here |

All six predictions that could be checked held.

**A correction to two of §17's figures.** §17 put the rule's chance of "shown" under no effect at about 2%, and of
"parked" at about 14%, taking d as normal. The exact distribution from the same per-task rates is 3.8% shown and 18.7%
parked (`second-night-power/out.txt`). Its 81% power at §12.25's effect is right (81.5% exact). The bands themselves are
unchanged, and the read above does not depend on these figures.

## Snapshots: checkpoints, and what they let through

- **Checkpoints.** There were 257 bash calls. 232 ran, and each of those has a `checkpoint` event with a snapshot, 0
  unavailable. The other 25 never ran, because tack answered them "not run: this is the same call as the one that just
  failed". A checkpoint is recorded only for a call the permission engine lets run (`internal/loop/loop.go:Session.takeSnapshot`
  and its caller), so those 25 have none, by design.
- **ken's workspaces got snapshots.** All 89 checkpoints on the ken tasks (7, 10 and 11) hold a snapshot. ADR-006's
  "Workspaces over `snapshot_max_bytes` (ken is one)" does not hold for E1's ken checkouts, as §17 predicted (4, 47 and
  307 MiB, under the 512 MiB cap).
- **Commands a snapshot let through: none.** A snapshot answers only discarding `git` subcommands and `rm -r` inside the
  workspace (`internal/perms/destroy.go:Engine.afterSnapshot` and its reasons). The 257 calls were 205 `go` commands and
  52 `find` commands, with no `git` and no `rm`. All 446 permission decisions were "allow" by mode, so no question was asked
  in either arm.
- **So in these runs, snapshots changed no command's fate.** Whatever separates this night from §12.25 is the other build
  changes (#59 to #63, §9.3's escaping), or chance.

## Reported, not decided on

- **Stops by reason:**

  | arm | done | repeat_loop | prompt-too-long | window_full | budget | max_tokens | cancelled |
  |---|---|---|---|---|---|---|---|
  | A | 11 | 0 | 30 | 6 | 0 | 1 | 0 |
  | B | 18 | 19 | 2 | 1 | 5 | 2 | 1 |

- **Requests per run:** A had a mean of 22.9 and a median of 8, and B a mean of 19.8 and a median of 18. In §12.25 these
  were A 23.6 and 10, B 19.2 and 18.
- **Wall time per run (median):** A 48 s, B 39 s. In §12.25 these were A 54 s, B 38 s. The builds differ, so this is not a
  measurement of what snapshots cost.
- **`done`:** B ended with `done` 18 times. 9 of those carried tack's note that the last build or test after the last change
  failed, and 1 run had a verification refusal. A ended with `done` 11 times. **The test-change report (#63):** no run in either
  arm changed the tests.
- **Guard firings in B (§13.4):**
  - `result_notes` fired in 39 runs, 518 times;
  - `fit_window` fired in 14 runs, 40 times;
  - `stop_repeats` fired in 19 runs and ended each of them;
  - `refuse_duplicate_edits` fired in 3 runs, 4 times;
  - `verify_done` fired in 1 run;
  - `edit_tolerances` did not fire.
- **§2's bar:** A has 3 passes on 1 task and is below the bar. B has 6 passes on 2 tasks and is at or above it.

## A second night: Francis's call

§17 asks for this when the read is parked: whether a second night is worth it, from the 96 runs' own spread and the
power of 96 more. The exact distributions are in `second-night-power/` (the program and its output).

- **The spread.** This night's per-task rates, the two arms pooled (task 1 at 4/8, task 11 at 5/8), give an sd of 1.97 for
  d under no effect. That is the registered 2.00, so the rule was sized right.
- **What 96 more runs could show.** It would be the same 96 runs, a fresh night, d summed over 96 per arm. The cut
  that keeps the false-positive rate near the one-night rule's 3.8% is **shown at d ≥ +7** over the two nights, with no
  third night. That puts the whole two-stage chance of "shown" under no effect at 4.2%. Given this night's +3, the second
  night must give d ≥ +4, which happens:
  - 81.5% of the time at §12.25's effect;
  - 26% at half of it;
  - 3.8% under no effect.
- **What it would decide: nothing about adoption.** Under §17 the bundle stays adopted under every verdict this screen can
  reach. A second night can only turn "parked" into a "shown on what ships" sentence, or into "not shown".
- **Recommendation: no second night now.** The screen guards against snapshots changing how the arms behave, and here that
  mechanism did not engage: no command of a kind a snapshot answers, and no question asked. Task 1, where §12.25's gain
  is, again went B 4 of 4 and A 0 of 4. A night of the Mac and nobara's card would buy one sentence, at about four chances
  in five if §12.25's effect holds.
- **If Francis wants the sentence,** the rule above (d ≥ +7 over 96 per arm, not shown otherwise, no third night) is
  registered in §17 before the night is queued. Expect about 2 h 50 min, with 40 GiB free on the Mac.
