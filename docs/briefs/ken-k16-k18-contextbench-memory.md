# Brief for ken: aikit bump, index memory composition, recompute in the watched index, and the ContextBench harness (tack's K16–K18)

<!-- citations-at: ken 5ab3888 (v1.11.0), aikit edd9c5b (v1.74.0), tack f75780d, LEANN c443fc4, ContextBench cd5f62e -->

**From:** tack (`docs/design/DESIGN.md` §12, asks K16–K18; `docs/research/contextbench-leann-2026-10-28.md` §4–§6, §8–§9). Approved by the owner 2026-10-28.

**For:** ken's Claude Code, the sole editor of ken (`CLAUDE.md`). The work lands on a branch and becomes a tagged release only on the owner's yes.

**Order:**
1. **K16** first (the index memory composition harness, `-tags=bench`), run at current `aikit v1.70.1`, then with **`aikit` bumped to `v1.74.0`** (`>= v1.71.0`), so the before-and-after table records what `v1.71.0`'s BM25 postings arena and mappable BM25 index save.
2. **K17** (`WatchedIndex` keeps no retained `f32` vectors, recomputing from chunk text when needed), **only if K16's table shows the retained `f32` vectors matter** at the owner's corpus sizes or kernel scale.
3. **K18** (`bench/contextbench`, `-tags=bench`, for CB1 retrieval-only ContextBench and optional CB2).

## Why

Two questions from `docs/research/contextbench-leann-2026-10-28.md` meet in ken:

1. **What ken's index is made of (K16), and dropping the watched index's duplicate `f32` vectors (K17).**
   - Today only two memory records exist: a whole-process peak at Linux-kernel scale (`7.00 GB` hybrid against `6.14 GB` BM25 in `docs/PERF-expectations.md`) and an analytic table in goinfer's memory campaign for a 768-d model ken does not use. tack's ask K8 ("latency and memory records for the in-process engine at the owner's repo sizes", DESIGN §12) has its structural half from v1.11.0's note, while its search/memory composition half is still open.
   - `WatchedIndex` (`internal/search/watch.go:WatchedIndex`) retains `vecs [][]float32` (1,024 bytes per 256-d chunk) so that it can append and compact. Because of that, enabling `KEN_ANN=flat-i8` on the watched path — the path both `ken-mcp` and `engine.Open` (tack) use — *adds* ~260–290 bytes per vector on top of the 1,024 instead of replacing them (ADR-043's guardrail).
   - With Model2Vec's deterministic embedding (`docs/DESIGN.md` §4: `f64` accumulation), re-embedding a chunk from `Chunk.Text` reproduces its index-time `f32` vector byte for byte. So `WatchedIndex` never needs to hold `vecs [][]float32` beside the dense retriever's own store.

2. **An outside instrument for retrieval on human-annotated gold (K18, CB1).**
   - LEANN (MLSys 2026) ships a code-search MCP server (`leann_mcp`) and claims in its README that Claude Code with LEANN found 2.1× the initial relevant code of BM25 on 30 ContextBench tasks under an 8,192-token budget.
   - Reading LEANN's committed code and results (`benchmarks/contextbench/`, commit `c443fc4`) shows its runner had no BM25 arm, no token budget, `--no-recompute`, and an embedding model (`jina-embeddings-v2-base-code`) that LEANN found six days later (`0463b76`) could silently load as a constant-vector encoder; on its 53 committed tasks, LEANN's final line coverage was `0.262` against plain Claude Code's `0.329` (better on 15 tasks, worse on 30).
   - ContextBench (1,136 tasks across 66 repositories and 8 languages, human-annotated minimal gold spans) lets ken measure **single-call coverage under a token budget** (CB1) across grep (`G`), ken BM25 (`B`), ken hybrid regex (`K`), ken hybrid tree-sitter (`KT`), and LEANN (`L1`, `L2`) without a model in the loop, and optionally with Claude Code in the loop (CB2).

---

## The asks

### 0. Bump `aikit` to `v1.74.0` (`>= v1.71.0`)

- **Today:** ken `v1.11.0` pins `aikit v1.70.1`, which predates `v1.71.0`'s BM25 postings arena (BM25 index ~20% smaller, scores bit-identical) and saved, mappable BM25 index.
- **The ask:** bump `github.com/townsendmerino/aikit` to `v1.74.0` (current head `edd9c5b`), following ken's ADR process if mappable BM25 is wired into snapshot loading. Measure K16's table before and after the bump so the saving is recorded on ken's own corpora.

---

### K16. Index memory composition table (`-tags=bench`)

**The ask.** A `-tags=bench` benchmark/measurement harness beside `retriever_scale_bench_test.go`, reusing its analytic-plus-measured method.

**Corpora:**
1. `tack`, `ken`, `aikit` and `goinfer` at their current heads (the owner's working set);
2. all four open in one process via `engine.Open` with `MmapModel: true` (tack's multi-workspace case, K7: verify N workspaces pay for one mapped Model2Vec model);
3. the 584,019-chunk sparse Linux kernel checkout from ADR-043's spot-check.

**Components**, each reported both as an **exact byte formula** from the live data structures (Tier 0) and as a **measured heap delta** (`runtime.MemStats.HeapAlloc` after ` runtime.GC()`) from building that component alone (Tier 1):

| component | where |
|---|---|
| chunk text | `aikit/chunk.Chunk.Text`, held by ken's index |
| BM25 | `aikit/bm25.Index`: postings, vocabulary, document lengths |
| retained `f32` vectors | `internal/search/watch.go:WatchedIndex` (`vecs [][]float32`) |
| dense retriever store | `Flat`, `FlatI8`, `FlatBinaryI8`, each in turn |
| structural index | `internal/structural.Index` |
| Model2Vec | mapped (`MmapModel: true`, ADR-045) and heap (`MmapModel: false`) |
| neural reranker | when loaded |
| on disk | `.ken/snapshot.bin`, `.ken/embed.db` |

Plus the whole-process footprint (macOS `footprint` / `/usr/bin/time -l`, Linux `VmHWM`) beside the sum of components, so unaccounted bytes are visible. (This is a measurement record, not a guard; no guard keys on RSS.)

**Tiers and wall time:**
- Tier 0–1 for the formulas and the four owner repositories: under 2 minutes by day.
- Tier 1 timed run for the 584k-chunk kernel row: expected 6–10 minutes under goinfer's timing lock (`timing_lock.py`).

**Output:**
- A component table under `docs/PERF-expectations.md` § "Memory" (before and after the `aikit` bump, and before and after K17).
- Summary numbers in the completion note (`~/tack-runs/NOTE-from-ken-2026-10-28-k16-k18.md`), closing the memory half of tack DESIGN §12 K8 and K7.

---

### K17. `WatchedIndex` keeps no retained `f32` vectors (gated on K16)

**When to build:** after K16's table is in hand, if the retained `f32` vectors are a meaningful share of the index on the watched path (expected: ~1,024 B/chunk, or ~600 MB at 584k chunks, and the only reason `FlatI8` costs extra memory on `WatchedIndex`).

**Today.**
- `WatchedIndex` (`internal/search/watch.go:WatchedIndex`) stores `vecs [][]float32` for every live chunk so that incremental flushes (`ReconcileFiles` / watcher batches) and compactions can rebuild or append to the dense retriever without re-embedding unchanged chunks.
- When `KEN_ANN=flat-i8` or `flat-binary-i8`, the quantized store sits *beside* `vecs [][]float32`, increasing memory by ~260–290 B/chunk instead of shrinking the 1,024 B/chunk `f32` store.

**The ask.**
- Drop `vecs [][]float32` from `WatchedIndex`. Keep only the active dense retriever's own store (`Flat`, `FlatI8`, or `FlatBinaryI8`).
- On incremental append (`ReconcileFiles` / watcher flush): embed only the new/changed chunks (as today) and append/quantize them directly into the dense retriever; on delete/compaction, compact the dense retriever's rows directly (using `FlatI8`'s / `Flat`'s row compaction or rebuilding from its kept rows).
- Any rare path that needs an existing chunk's `f32` vector (e.g., switching dense retriever modes, or `FindRelated` when seeding a query vector from a stored chunk) recomputes the `f32` vector on demand from `Chunk.Text` (with its enrichment label, as indexed) through the same Model2Vec encoder.
- Snapshot persistence (`.ken/snapshot.bin`, ADR-039): verify how snapshot load/save interacts when `FlatI8` is selected; if the snapshot format stores `f32` vectors on disk, keep the disk format unchanged unless an ADR bumps the snapshot key.

**Pre-registered gates (each proven red first by perturbing the invariant):**
1. **Recompute bit-identity (Tier 0):** over ken's golden test corpus and one owner repository, every chunk's recomputed `f32` vector equals its index-time vector byte for byte (`math.Float32bits`). Perturb one weight or token row first to prove the test fails.
2. **Pipeline recall (Tier 1):** `recall@10` and `NDCG@10` on semble's 1,251-query benchmark under `Flat` and `FlatI8` match pre-K17 to three decimal places (ADR-043's gate).
3. **Watcher republish latency (Tier 1):** single-file edit republish stays at ~2 s (debounce included) on the medium corpus.
4. **Memory (Tier 1, via K16's harness):** with `KEN_ANN=flat-i8` on `WatchedIndex`, dense-side resident bytes per chunk are at or under `FlatI8`'s ~260 B plus a stated small slack (down from ~1,284 B/chunk today).

**What K17 does not decide:** whether `FlatI8` becomes ken's default (ADR-043 kept `Flat` as default; flipping it is ken's own ADR if wanted).

---

### K18. `bench/contextbench`: ContextBench harness (`-tags=bench`)

**License constraint (hard rule):** ContextBench has no license ("All Rights Reserved" on its site). **Vendor nothing from ContextBench or LEANN into ken's tree** — no code, no parquet files, no gold spans. Everything of theirs lives in external directories passed by flag/path, and ContextBench's evaluator and LEANN are invoked as external executables.

**The ask.** A pure-Go benchmark harness under `bench/contextbench/` (gated behind `//go:build bench`, no Python in ken's tree) that implements **CB1** (retrieval-only, no LLM) and, behind a subcommand/flag, **CB2** (agent-in-the-loop reproduction, run only if CB1's gate passes and the owner says go).

#### CB1 — Retrieval-only (`bench/contextbench`)

For each task in a ContextBench parquet/JSONL task list (`instance_id`, `repo`, `base_commit`, `problem_statement`, `language`, `source`):

1. **Checkout isolation:** materialize `base_commit` from the shared repo cache into a fresh temp directory via `git archive` (so no later commit, `gold_context`, `patch`, or `test_patch` exists on disk).
2. **Arms (paired on every task):**
   - **`G` (tokenized grep baseline):** extract distinct identifier tokens from `problem_statement`, match whole files containing any identifier, order files by distinct identifiers matched (descending) then slash path (ascending). This is ken `docs/BENCH.md`'s grep-plus-Read baseline with a deterministic order so a token budget can be filled.
   - **`B`:** ken in `bm25` mode (`engine.Open` with `Mode: "bm25"`, default regex chunker).
   - **`K`:** ken default (`Mode: "hybrid"`, regex chunker, `Flat`).
   - **`KT`:** ken hybrid with the tree-sitter chunker (`Mode: "hybrid"`, `Chunker: "treesitter"`, `Flat`).
   - **`L1` (external command adapter):** LEANN as committed in `benchmarks/contextbench/` (`jina-embeddings-v2-base-code` with `LEANN_TRUST_REMOTE_CODE=1` at a pinned model revision, AST chunking 600/96, HNSW, `--no-recompute`).
   - **`L2` (external command adapter):** LEANN CLI defaults (`BAAI/bge-base-en-v1.5` on CUDA, HNSW, recompute on, no AST chunking).
   - *(Optional adapter `LH`)*: LEANN Python-API hybrid at a registered `vector_weight`, if needed later.
3. **Index build measurement (outside the query clock):** record index build wall time and peak footprint per task and arm (`ken` in-process / subprocess; `LEANN` across its process tree including its ZMQ embedding server).
4. **Single-call query & token budgeting:**
   - Query the arm once with `problem_statement` verbatim (no query rewriting; raise `k` so the candidate list is deep enough to fill the largest budget).
   - Count chunk tokens with `cl100k_base` through `tiktoken-go` (already used behind `-tags=bench` in ken's token benchmark; never in release binaries) — **the exact same counter for every arm**.
   - Fill budgets **`2,048`, `8,192` (primary), and `16,384` tokens** top-down: a chunk that would cross the budget is dropped (not truncated), except the #1 ranked chunk is always kept even if it exceeds the budget.
5. **Output & scoring:**
   - Emit one generic ContextBench `traj_data` JSON record per `(task, arm, budget)` (`instance_id`, `model_patch: ""`, `traj_data: {pred_steps: [...], pred_files: [...], pred_spans: {path: [{start, end}]}}`).
   - Invoke ContextBench's external `evaluate.py` (in its Python 3.11 venv) or read its scored output, and write one provenance-stamped JSONL line per `(task, arm, budget)` with:
     - ContextBench's **line coverage** (primary at `8,192`), **symbol (block) coverage**, **file coverage**, **line precision**, and **file precision**;
     - **tokens to first gold line** (computed directly in the Go harness from the ranked hits and the task's gold spans, read only by the grader after the arm finishes);
     - index build time, query latency, peak footprint, and provenance (`ken` commit, chunker, mode, `LEANN` commit/version, embedding model revision).
   - Delete the temporary tree and per-task index before the next task.
6. **Slices supported by flag:**
   - `S53`: the 53 SWE-bench Pro instance IDs from LEANN's `benchmarks/contextbench/` scripts;
   - `Lite`: the 500 tasks in `contextbench_verified.parquet`;
   - `L100`: a seeded, language-stratified 100-task subsample of `Lite` (seed fixed by flag/registration).
7. **Summary & bootstrap reporter:**
   - Compute paired per-task differences between any two arms (e.g., `K − L1`, `K − L2`, `K − B`, `KT − K`, `K − G`), macro-averaged overall and broken down by `language`, `source`, and `repo`, with paired bootstrap 95% confidence intervals (10,000 resamples, deterministic seed) over tasks.

**Checks for K18 (Tier 0, proven red first):**
- **Budget packer unit test:** verifies top-chunk-always-kept, whole-chunk drop on budget overflow, and identical `cl100k_base` counts across synthetic hits; perturb the boundary by 1 token to show the test fails.
- **Tokens-to-first-gold-line & span merge unit test:** synthetic ranked chunks against known gold spans; perturb a line boundary to show the test fails.
- **Determinism check:** running `G`, `B`, `K`, and `KT` twice on a small fixture repo produces byte-identical `traj_data` and JSONL records (timestamps excluded).

---

## Constraints on every item

- **No ranking changes.** Nothing in K16, K17 or K18 alters BM25, Model2Vec weights, RRF fusion or reranking.
- **No Python and no ContextBench/LEANN files in ken's repository.**
- **Public API:** K16, K17 and K18 require **zero breaking changes** to `ken/engine` or `ken/mcp`.
- **Timing lock:** hold goinfer's timing lock (`python3 ~/tmcode/goinfer/scripts/timing_lock.py run ...`) for `make check` (shared) and exclusively for K16's timed kernel run and CB1's timed runs. State expected wall time before any run over a minute.

## Process for ken's session

1. Read this brief and `~/tmcode/tack/docs/research/contextbench-leann-2026-10-28.md` (§4–§6, §8). Do not edit anything in `tack`'s repository.
2. Work on a branch from `main` (`5ab3888` / `v1.11.0`). Commit per item (`build(deps): bump aikit to v1.74.0`, `bench: index memory composition harness (K16)`, `search: drop retained f32 vectors from WatchedIndex (K17)`, `bench: contextbench retrieval harness (K18)`).
3. Run `GOWORK=off make check` before each commit.
4. Write `~/tack-runs/NOTE-from-ken-2026-10-28-k16-k18.md` with:
   - what landed, by commit;
   - K16's full component memory tables (before/after `aikit` bump, and before/after K17);
   - K17's four gate verdicts;
   - K18's CLI flags and smoke test output;
   - proposed version tag (`v1.12.0`).
5. Stop and wait for the owner's yes before pushing, merging, or tagging.

