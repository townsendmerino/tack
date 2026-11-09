# ken's SearchFiles on goinfer: not exact (P1 step 5, by day)

<!-- citations-at: tack 941b0ed + tools/searchfilescheck, ken v1.12.1, goinfer 9f95557ad -->

P1 step 5's by-day check (`docs/design/p1-notes.md`, "P1's order and decisions", step 5; DESIGN §12, K9 "On
adoption"). It was read-only and untimed, run on mbp2 on 2026-11-08 by `mbp2-tack-p1s5`.

## Result

**`SearchFiles` is not exact on goinfer. Its index is about 80,000 chunks, 16 times ken's 5,000-candidate cap. For most
queries, the instruction-file hits ranked below the cap are lost.**

| file | k | queries | answers that differ | exact hits returned | deepest whole-repo rank an exact answer needed |
|---|---|---|---|---|---|
| `CLAUDE.md` | 5 | 25 | 11 | 87 of 117 | 26,058 |
| `CLAUDE.md` | 20 | 25 | 20 | 101 of 299 | 62,035 |
| `internal/runtime/scheduler/CLAUDE.md` | 5 | 11 | 7 | 2 of 9 | 45,067 |
| `internal/runtime/scheduler/CLAUDE.md` | 20 | 11 | 7 | 2 of 9 | 45,067 |
| `internal/runtime/mem/CLAUDE.md` | 5 | 11 | 9 | 2 of 11 | 41,427 |
| `internal/runtime/mem/CLAUDE.md` | 20 | 11 | 9 | 2 of 11 | 41,427 |

`VERDICT: FAIL — searchfilescheck: 63 of 94 answers differ from the exact ranking`, over 79,919 chunks.

- The misses include headings asked for by their own text: at k=5, "Run budget", "Gate tiers" and "Rigor matched to
  stakes" in `CLAUDE.md` miss sections of their own file. Generic words lift the repository's docs and code above the
  instruction file.
- The nested `CLAUDE.md` files fare worst: SearchFiles found 2 of 9 and 2 of 11 of their exact hits.
- **What it means for tack:** ken's route to sections ("through ken, from the user's message", DESIGN §2.3) can't use
  `SearchFiles` on goinfer as it stands. It would bring in the wrong sections, or none, with no sign anything was cut.
  The deterministic route (tool calls), in PR `window: instruction files by section`, doesn't use ken and is
  unaffected.

## How it was measured

- **Tool:** `go run ./tools/searchfilescheck -root DIR -files A,B -mode bm25`.
  - It opens ken with no snapshot and no watcher, so it writes nothing.
  - The queries are each file's headings plus ten fixed task-like messages.
  - For each query and k, it compares `kensearch.Searcher.SearchFile` (ken's `SearchFiles` in the workspace's mode)
    with the exact answer: the whole ranking (k = the index's size) filtered to the file, compared by span and score,
    in order.
- **Proven able to go red:** `TestCheckFailsPastTheCap` builds a 5,102-chunk index whose instruction-file hit ranks
  below the cap, and the check says FAIL. `TestCheckPassesOnASmallIndex` passes, and with no file the check is
  INCONCLUSIVE.
- **The tree:** goinfer `9f95557ad` (origin/main on 2026-11-08), exported with `git archive` into the job's scratch
  directory, so neither the checkout nor its untracked `.claude/worktrees/` was indexed or touched. Three instruction
  files: the root `CLAUDE.md` (16,420 bytes) and the two nested ones. goinfer has no `AGENTS.md`.
- **Mode:** bm25 only. mbp2 has no Model2Vec model, so hybrid, ken's default mode for tack, wasn't checked. Hybrid
  fuses two arms fetched at depth `armOverfetch(5000)`, and the cap is on the fused list, so the same loss is expected
  there. That is an inference, not a measurement.
- **Logs:** `~/tack-runs/p1s5/searchfilescheck-goinfer-9f95557ad-bm25.log` on mbp2, which also records the load and
  free memory at the start.
  - That is the second run, with the recall column added. The first run, at load 6.0, gave the same verdict: 62 of 94
    differ, over 79,355 chunks.

**A side finding: the chunk count differed between the two runs of the same tree,** 79,355 at load 6.0 and 79,919 at
load 3.0. Most likely the tree-sitter chunker's 1 s parse timeout (`cmd/tack/ken.go`, and this tool's own
registration) cuts some files differently when the box is busy. ken's index over a large repo then isn't reproducible
under load, which matters to any measurement that counts chunks or ranks. It isn't confirmed; ken's brief asks
about it.

## What comes next

- **ken change (brief in `~/goinfer-status/mbp2-tack-p1s5.md`, "Brief for ken"):** make `SearchFiles` exact at any
  index size, by scoring the requested files' chunks directly rather than filtering a capped list. Until it lands,
  tack doesn't use `SearchFiles` for sections. `kensearch.Searcher.SearchFile` returns `exact = false` on an index over
  5,000 chunks, so a caller can't take its answer as the file's best sections without seeing that.
- **The night item, `SearchFiles`'s latency on goinfer:** its run spec is below. It isn't run here, and it isn't worth
  running on today's `SearchFiles`, whose answers are wrong at goinfer's size. It measures the exact version once ken
  ships it.

## Run spec: SearchFiles latency on goinfer (night; not run)

*Rule approved by Francis on 2026-11-08 (15:01, through the coordinator). He set the bars at twice the proposed 50 and 200 ms. It is registered before the run; nothing is queued.*

- **Question:** what ken's route adds to a turn on the owner's largest repository. It runs once per user message,
  before the first request, so its latency is on the critical path.
- **Box:** the Mac (M1 Pro, 16 GB), the product's box. It's a night item because the fit guard refuses while the
  owner's apps are open (tack `CLAUDE.md`, "Run budget"). Queue it with goinfer's `scripts/night.py add` on the Mac.
  Pin a pre-built `searchfileslat` binary and a goinfer tree exported at a fixed commit, and write the log to
  `~/tack-runs/p1s5/`.
- **Build:** a small `tools/searchfileslat` beside `searchfilescheck`, built at a pinned tack commit on the ken release
  that makes `SearchFiles` exact.
  - It opens the index once, from a ken snapshot built in an untimed step before the timed one.
  - **The snapshot step is gated like a cell.** It starts only at a load average of 2 or less, and builds with the
    tree-sitter parse timeout off, as ken's CLI does, so the index doesn't depend on how busy the box is (the chunk
    counts above differed under load). It is built twice, and the job stops unless both builds give the same chunk
    count.
  - Then, under the timing lock, for each mode in {bm25, hybrid (model from `~/models`, never the archive)}, it times
    what ken's route runs per user message: `kensearch.Searcher.SectionPaths` at k=5 (20 hits from ken, cut to 5
    distinct sections), the call core makes, its `Sections` check on the file included. It times it over
    `searchfilescheck`'s ten task-like messages (not its heading queries, which no user sends) for each of the three
    instruction files: 30 calls. Each runs 20 rounds, interleaved A B B A across modes, and p95 is taken over them.
    (Amended 2026-11-09, before any run: the spec had named all 94 of the exactness check's calls, headings included,
    and the bare `SearchFiles`.)
- **Records:** per cell, the load average and free memory at its start, p50, p95 and max per call, and the index's
  chunk count. A cell started at a load average above 2 is void.
- **Decision rule (pre-registered; approved, as above):**
  - **p95 ≤ 100 ms** in hybrid: ken's route runs inline on every user message.
  - **100–400 ms: parked.** It runs inline only for the first message of a session, and the owner decides.
  - **> 400 ms:** it runs off the critical path or not at all. The deterministic route stays.
  - The do-nothing arm is the deterministic route alone, which costs no search.
- **Expected wall time:** about 10 minutes: two snapshot builds about 2 minutes, then 2 modes × 30 calls × 20 rounds,
  with hybrid well under 0.5 s a call.

## Rerun on ken v1.13.0 (2026-11-09): exact

ken v1.13.0 makes `SearchFiles` exact at any index size: the requested files' chunks are ordered with the whole
ranking's own scores and ranks, with no candidate cap. The same check on the same tree:

| file | k | answers that differ | exact hits returned |
|---|---|---|---|
| `CLAUDE.md` | 5 | 0 of 25 | 117 of 117 |
| `CLAUDE.md` | 20 | 0 of 25 | 299 of 299 |
| `internal/runtime/scheduler/CLAUDE.md` | 5, 20 | 0 of 11 | 9 of 9 |
| `internal/runtime/mem/CLAUDE.md` | 5, 20 | 0 of 11 | 11 of 11 |

`VERDICT: PASS — searchfilescheck: 94 of 94 answers exact over 79919 chunks`.

- **Setup:** bm25, read-only, untimed, on mbp2 at load 0.79. The log is
  `~/tack-runs/p1s5/searchfilescheck-goinfer-9f95557ad-bm25-ken1.13.0.log`.
- **The tool now parses with no tree-sitter timeout** (`parseTimeoutMicros = 0`; the coordinator's decision, 2026-11-09:
  no ken change). Measurement indexes then don't depend on the box's load. cmd/tack's live path keeps its 1 s guard,
  and `TestMeasurementChunkerHasNoTimeout` holds both.
  - With no timeout the index is 79,919 chunks, the count of the earlier run at load 3.0, so the 79,355 at load 6.0 was
    files the 1 s timeout cut under load.
- **The gate can still go red** (`TestCheckFailsWhenHitsAreMissing`, a searcher that drops hits). ken's own cap no
  longer provides a failing case.
- **ken's route to sections is now wired** (`window.Instructions.ForHits`, through `kensearch.Searcher.SectionPaths`).
  The night latency run (the spec above) measures v1.13.0's `SearchFiles`.
