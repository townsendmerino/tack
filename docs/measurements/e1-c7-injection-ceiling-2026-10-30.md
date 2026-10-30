# E1 C7 gate 0: the turn-1 injection's ceiling, with no model, 2026-10-30

<!-- citations-at: ab6d5ab -->

This is C7's gate 0 for the turn-1 injection (`e1-calibration-preregistration-2026-10-18.md` §13.1). Its selection
and its rule were committed in `a6956e8` before this check ran. It asks: for each E1 task, at the base commit, does
the code the fix changes land inside the injection that ken builds from the task statement?

**Verdict, as registered: parked.** Of the 12 calibration tasks, 4 land at 1,600 tokens with `hybrid`: tasks 4, 6, 8
and 11. Three of them (4, 6 and 8) are among the 8 tasks whose runs reached gold under half the time.
- The rule's pass needs 6 of 12, with at least 3 of those 8.
- §13.1 says a parked result goes to Francis with the no-budget figure. **With no budget, 6 of 12 land, 3 of them
  among the 8.** The two the budget leaves out are tasks 2 and 12, both higher-reach tasks.
- What follows is Francis's decision. Nothing here changes §13.1.

## What ran

- **Code:** `tools/injectceiling` (`identifiers`, `candidates`, `pack`, `score`), with ken v1.11.0 through
  `internal/kensearch` and gold from `internal/eval/pilot/context.go:FixGold`.
- **Command:** at 09:13–09:20 PDT, from the repository root at `a6956e8` plus the tool, in about 7 minutes with no
  model:

  ```
  go run ./tools/injectceiling -mirrors ~/tack-runs/e1/mirrors
  ```

  It ended `VERDICT: PASS — injectceiling: 22 tasks measured`.
- **Output:** `~/tack-runs/e1/c7-ceiling-2026-10-30/report.md` on the Mac, with every task's packed spans.
- **Tasks:** CB3's 22, the 12 calibration tasks and the 10 pilot tasks (pilot task 8 was removed before the pilot).
- **ken's modes:** `hybrid` with ken's Model2Vec model (`~/.ken/model`) is the one read. `bm25` is reported beside
  it.

## Task by task (1,600 tokens)

"Lands" means every raw span of the fix overlaps the injection; the column gives raw spans landed out of all of
them. "cov" is the share of `FixGold` lines injected, and "reach" is CB3's share of runs that reached gold (CB3 §4).

| task | CB3 reach | identifiers (defined) | hybrid lands | cov | bm25 lands | cov | no budget lands | cov | no budget, tokens |
|---|---|---|---|---|---|---|---|---|---|
| 1 | 37.5% | 10 (8) | no (0/2) | 0.00 | no (0/2) | 0.00 | no (1/2) | 0.93 | 13,994 |
| 2 | 61.9% | 10 (7) | no (0/2) | 0.00 | no (0/2) | 0.00 | **yes** | 1.00 | 10,554 |
| 3 | 22.4% | 9 (6) | no (1/3) | 0.47 | no (1/3) | 0.47 | no (2/3) | 0.48 | 10,956 |
| 4 | 5.5% | 13 (5) | **yes** | 1.00 | **yes** | 1.00 | **yes** | 1.00 | 5,338 |
| 5 | 46.4% | 14 (8) | no (1/4) | 0.22 | no (1/4) | 0.13 | no (3/4) | 1.00 | 7,691 |
| 6 | 48.5% | 6 (3) | **yes** | 1.00 | **yes** | 1.00 | **yes** | 1.00 | 4,759 |
| 7 | 2.2% | 6 (0) | no (0/1) | 0.00 | no (0/1) | 0.00 | no (0/1) | 0.00 | 4,318 |
| 8 | 0.0% | 8 (2) | **yes** | 1.00 | **yes** | 1.00 | **yes** | 1.00 | 4,529 |
| 9 | 2.0% | 6 (3) | no (1/2) | 0.98 | no (1/2) | 0.98 | no (1/2) | 0.98 | 10,908 |
| 10 | 64.4% | 5 (0) | no (0/1) | 0.00 | no (0/1) | 0.00 | no (0/1) | 0.08 | 4,417 |
| 11 | 97.8% | 4 (1) | **yes** | 1.00 | **yes** | 1.00 | **yes** | 1.00 | 2,811 |
| 12 | 53.3% | 4 (3) | no (0/1) | 0.00 | no (0/1) | 0.00 | **yes** | 1.00 | 6,914 |

**Totals:**

| | lands | of them in tasks 1, 3–9 | reaches gold |
|---|---|---|---|
| calibration, hybrid | 4 of 12 | 3 | 7 of 12 |
| calibration, bm25 | 4 of 12 | 3 | 7 of 12 |
| calibration, hybrid, no budget | 6 of 12 | 3 | 11 of 12 |
| pilot, hybrid | 2 of 10 | — | 6 of 10 |
| pilot, bm25 | 3 of 10 | — | 7 of 10 |
| pilot, hybrid, no budget | 2 of 10 | — | 6 of 10 |

- **Every injection used 1,255–1,599 of its 1,600 tokens.**
- With no budget, the calibration injections ran 2,811–13,994 tokens.

## Reported, not decided on

These are what the packed spans in `report.md` show. They are observations for Francis's decision, not changes to the
rule.
- **One search hit recurs.** In the goinfer tasks, `pull/checkpoint_test.go:34-78` (about 45 lines) is packed in
  tasks 1, 2, 5 and 9. In task 1 it is the first item. Search hits from test files and docs (`docs/…`, `CHANGELOG.md`,
  `CLAUDE.md`, one Python script) take budget in 14 of the 22 injections.
- **Generic identifiers.** The statements' backquoted words include flags and prose: `serve`, `chat`, `kv`, `ctx`,
  `f32`, `index.bin`.
  - Their definitions, when ken finds any, are the first 3 of many.
  - In task 1, the gold function (`loadflags.go:55-82`) is not packed, but its neighbors 30–52 and 84–108 are.
- **ken-repository tasks find few definitions.** Tasks 7 and 10, and pilot task 7, named identifiers ken could not
  resolve (0 defined), so their injections are search hits alone, and none lands.
- **`bm25` and `hybrid` agree** on the calibration tasks (4 of 12 each). They differ on pilot tasks 7 and 11.

## Guard firings, the first reading (§13.4)

`tools/calreport -mirrors` (`tools/calreport/guards.go:guards`) over two jobs' bundle arms. A firing is "followed" if
within 3 requests an edit is applied, a `go build`, `go test` or `go vet` passes, or a line of `FixGold` is seen for
the first time. This is reported only: what to remove is Francis's decision.

| job, arm | setting | runs it fired in | firings | followed by an edit | a passing build | new gold lines | any |
|---|---|---|---|---|---|---|---|
| §12.21 B (Qwen3-8B, 16k) | `fit_window` | 11 of 36 | 21 | 2 | 0 | 0 | 2 (10%) |
| | `refuse_duplicate_edits` | 3 | 4 | 0 | 0 | 1 | 1 (25%) |
| | `result_notes` | 29 | 301 | 3 | 0 | 24 | 27 (9%) |
| | `stop_repeats` | 16 | 16 | it ends the turn | | | |
| | `verify_done` | 1 | 1 | 0 | 1 | 0 | 1 (100%) |
| §12.24 (Qwen3.5-9B, 32k) | `edit_tolerances` | 1 of 36 | 1 | 0 | 1 | 0 | 1 (100%) |
| | `fit_window` | 16 | 211 | 0 | 0 | 0 | 0 (0%) |
| | `refuse_duplicate_edits` | 0 | 0 | | | | |
| | `result_notes` | 34 | 261 | 0 | 2 | 11 | 13 (5%) |
| | `stop_repeats` | 13 | 13 | it ends the turn | | | |
| | `verify_done` | 0 | 0 | | | | |

`instructions_head` changes only the prompt, so it has no firing to count.

**What records carry, and what tack must start logging:**
- **Today**, each firing is in the run's `events.jsonl` only as the text the setting writes into a tool result, and
  `stop_repeats` as the turn's `repeat_loop` end. `tools/calreport/guards.go:guardMarks` matches that text, so
  rewording a message would silently zero its count.
- **To start logging** (product and protocol changes, each for Francis's yes):
  1. each `tool.finished` names the settings that changed its result;
  2. the run's first event records what `instructions_head` kept and cut.

  The first is a protocol change, made with its Swift types and fixtures (CLAUDE.md, "Protocol types").

## Gate 0b: the post-hoc selection, 2026-10-30 09:40–09:46 PDT

Registered in §13.1 (`41e797f`) after gate 0's report and before this run, on Francis's option (a) at 09:38. Search
hits from `_test.go` and non-Go files are dropped, and so are single-word, all-lower-case identifiers. It is marked
post hoc. Gate 0's result above stands.

**Verdict, as registered: does not pass.** At 1,600 tokens with `hybrid`, 4 of the 12 calibration tasks land (6
needed) and 2 of the 10 pilot tasks (5 needed).
- The command was `go run ./tools/injectceiling -mirrors ~/tack-runs/e1/mirrors -selection posthoc`, at `baf6ef6`.
  It ended `VERDICT: PASS — injectceiling: 22 tasks measured`.
- Output: `~/tack-runs/e1/c7-ceiling-2026-10-30/report-0b.md`.

| | lands | of them in tasks 1, 3–9 | reaches gold |
|---|---|---|---|
| calibration, hybrid | 4 of 12 (same 4: tasks 4, 6, 8, 11) | 3 | **9 of 12** (gate 0: 7) |
| calibration, bm25 | 5 of 12 (adds task 2) | 3 | 9 of 12 |
| calibration, hybrid, no budget | 5 of 12 | 3 | 11 of 12 |
| pilot, hybrid | 2 of 10 (same 2: tasks 1, 10) | — | 6 of 10 |
| pilot, bm25 | 3 of 10 | — | 7 of 10 |

**Reported, not decided on:**
- **Gold coverage moved.** It went 0.00 → 0.93 on task 1 and 0.22 → 0.35 on task 5, and task 10 reached gold for the
  first time (0.08). The tasks that land did not change.
- **Most misses are fixes with more than one raw span,** where one span lands and another does not:
  - task 1 (1 of 2), task 3 (1 of 3), task 5 (2 of 4), task 9 (1 of 2);
  - pilot tasks 2 (2 of 4), 5 (3 of 4) and 9 (3 of 6).
- **Task 7's injection shrank to 500 tokens,** with no identifier resolved and only one Go search hit left. The ken
  tasks (7 and 10, and pilot tasks 2, 6 and 7) still resolve few definitions.
- **The no-budget injections shrank** to 500–7,537 tokens, from 2,811–13,994.

## §13.2 gate 0: the read outline, by replay, 2026-10-30 09:48–09:50 PDT

Registered in §13.2 and approved by Francis at 09:38. It is the next arm in C7's order, run while the injection's next
step waits on Francis. There is no model.

**Verdict, as registered: not screened.** Over CB3's 625 counted runs, 11 runs, all on calibration task 1, made a
whole-file read that `read` cut at 200 lines, of a gold file whose gold span starts past line 200. §13.2's rule asks
for at least 15 runs over at least 3 tasks. So the outline is built only as the product feature DESIGN §7.3 specifies,
unmeasured.
- **Command:** `go run ./tools/replaycontext -mirrors ~/tack-runs/e1/mirrors -scan ~/tack-runs/e1 -outline-gate`,
  with `tools/replaycontext/outline.go:outlineHit`. Its test was broken first.
  - It ended `VERDICT: PASS — replaycontext: scored 625 of 625 runs across 22 tasks (47 pass, 578 fail; fail reached
    gold 186/578, edited gold 64/578)`, which is CB3's set exactly.
  - Output: `~/tack-runs/e1/c7-ceiling-2026-10-30/outline-gate.md`.
- **`-scan` had grown past CB3's set.** Its first run scored 661 runs, because §12.24's 36 post-CB3 runs were not
  excluded. `isExcludedResultFile` now leaves out `c6-qwen35-row-2026-10-29` and `c6-powered-2026-10-30` beside
  `c6-bundle-2026-10-28`, so `-scan` reproduces CB3's 625.
  - Reported only: those 36 runs added 5 such reads, on tasks 3 and 4 (2 each) and task 1 (1).
- **Reported, not decided on:** task 4's gold (`pull/pull.go:369-410`) is past the cut, but none of CB3's runs read
  `pull.go` whole. Its runs opened it with an explicit range, which `read` does not cut, so an outline at the cut would
  not have reached them.
  - Checked: those runs read `pull.go` 70 times, every time with an offset and a limit (20–30 lines from line 360 on).
- **Outline size:** §13.2 registered it to be reported for each gold file. It was not measured, because the arm does
  not go to a screen. It is measured with the feature when that is built.

## §13.3 gate 0: the generated repository map, 2026-10-30 09:53–10:00 PDT

Registered in §13.3 and approved at 09:38. The map's construction and the counts were fixed in `41faaa6` before the
run (`tools/repomapgate`). There is no model, and the maps are built on E1's base trees, so nothing written later is
in them.

**Verdict, as registered: goes to a screen.** The map names a gold file on **7 of the 22 tasks**, and the instruction
head on **0**. §13.3's rule asks for at least 6 more than the head.
- **Command:** `go run ./tools/repomapgate -mirrors ~/tack-runs/e1/mirrors -dump …`. Its tests were broken first.
  - It ended `VERDICT: PASS — repomapgate: 22 tasks measured`, in about 3 minutes.
  - Output: `~/tack-runs/e1/c7-ceiling-2026-10-30/repomap-gate.md`, with each task's map and head in `repomap/`.
- **The map's size:** 2,162–2,990 bytes (at most 1,000 tokens at bytes / 3), with 19–36 files. The heads were
  0–2,085 bytes; aikit's base trees, and goinfer's at pilot tasks 5, 10 and 11, have no instruction file.

| | names a gold file | tasks |
|---|---|---|
| map | 7 of 22 | calibration 7, 11, 12; pilot 1, 2, 6, 10 |
| head | 0 of 22 | |

**Reported, not decided on:**
- **The map names gold files mostly in the smaller repositories:**
  - ken: calibration 7 and 11, pilot 2 and 6;
  - aikit: calibration 12, pilot 1;
  - goinfer: 1 of its 12 tasks (pilot 10, `decoder/serialize.go`).

  goinfer's map holds about 20 of its files.
- **The gold symbol is on the gold file's row in 0 tasks.** A row lists a file's 8 most-referenced exported symbols,
  and gold symbols such as `resolveRepo` or `modelBannerFrom` are unexported.
- **A defect in a reported-only column was fixed after the first run.** That run counted a gold symbol named anywhere
  in the map (2 tasks). The column now checks the gold file's own row (`rowLists`, with its test broken first), and
  the re-run gives 0. Both runs give the same file and head counts and the same verdict. The first run's output is
  `repomap-gate-first.md`.
- **The screen is registered here before its runs,** with its rule from CB3's per-task reach rates (§13.3, gate 1).
  It needs the map built into tack behind a `[run]` setting, which is product code for Francis's yes.

## Gate 0b re-run through the product's code, 2026-10-30 10:10–10:17 PDT

The injection now lives in `internal/window/inject.go`, and `tools/injectceiling -selection posthoc` calls it. That
makes the screen run exactly what gate 0b measured.
- **The re-run** (`report-0b-window.md`) matches `report-0b.md` in every budgeted column, so the verdict is the same.
- **One cell differs:** calibration task 2's no-budget column, and its count of identifiers ken defined (3 against 2).
  Its identifiers and packed spans are the same.
- **So ken's definitions can vary between runs over the same tree.** The likely cause is the tree-sitter chunker's
  1-second parse timeout (`treesitter.WithParseTimeoutMicros(1_000_000)`, as `cmd/tack` registers it). Under load, a
  large file's parse can time out and leave its definitions out of the index. This is reported, not chased here; it
  bears on the screen as much as on the gate.
