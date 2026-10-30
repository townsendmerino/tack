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
