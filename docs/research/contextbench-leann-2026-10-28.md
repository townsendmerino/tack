# LEANN and ContextBench: what they mean for tack, ken and aikit (2026-10-28)

> Corrected 2026-11-10: this page named the dense store as `KEN_ANN=flat-i8`. `KEN_ANN` is read by ken's own binaries.
> `engine.Open`, tack's path, never read it: the store is chosen by `engine.Options.DenseRetriever` (`"flat-i8"`,
> `"flat-binary-i8"`). The figures are unchanged.

<!-- citations-at: tack ccd130f, ken 5ab3888 (v1.11.0), aikit edd9c5b (v1.74.0), goinfer 093db74b, LEANN c443fc4, ContextBench cd5f62e -->

**Status: approved 2026-10-28 (decisions 1–9 as recommended, §9).** Written in the Claude app on Francis's word ("yes to the
write-up, make it very detailed"), after he asked whether the LEANN paper (MLSys 2026) intersects ken, aikit and
goinfer, and how it could influence progress. One consolidated doc, by his standing preference: a new instrument for
tack (§4), a brief for ken (§5, §6), aikit's part (§7) and goinfer's (none, §3.4). Decisions for Francis are collected
in §9, each with a recommendation first, and the execution order is in §9.1. Every item names its test tier and, where it is timed, its expected wall time.

**How the facts were gathered.**

- ken, aikit, goinfer, tack and skiff: read from the trees on 2026-10-28 at the commits above. ken's checkout on the Mac
  was 14 commits behind `origin/main`; the ken files cited here are unchanged between it and v1.11.0 except
  `docs/internal/DECISIONS.md` (ADR-048 added), so they are cited at v1.11.0.
- LEANN and ContextBench: from their papers, and from shallow clones of both repositories read in the Claude
  workspace (LEANN `c443fc4`, 2026-10-27; ContextBench `cd5f62e`, 2026-01-29). The task counts in §1.3 were recounted
  from ContextBench's own parquet files, and §1.4's numbers were recomputed from LEANN's committed result files.
- The MarkTechPost article that started this (2026-10-27) blocks automated reads; the paper and repositories were read
  instead.

---

## 0. Summary

**LEANN** (Wang et al., UC Berkeley; best research paper, MLSys 2026) is a vector index that keeps a pruned proximity
graph and tiny compressed codes instead of embeddings, and recomputes the embeddings a query visits. It reports
indexes up to 50× smaller at a few seconds of search, justified by RAG answers that take 17–70 s to generate.

**ContextBench** (Li et al., February 2026) measures how coding agents retrieve context: 1,136 issue-resolution tasks
from 66 repositories in eight languages, each with human-annotated gold context, scored along the agent's trajectory.

**LEANN's project claims, in its README,** that Claude Code with LEANN found 2.1× the initial relevant code of Claude
Code with BM25 on 30 ContextBench tasks under an 8,192-token retrieval budget. **What LEANN committed does not show
that** (§1.4). Its runner has no BM25 arm and no token budget: the baseline is Claude Code with its own tools. Its
committed results, on 53 tasks, show LEANN reaching gold code sooner but ending with less of it: final line coverage
0.262 against the baseline's 0.329, LEANN better on 15 tasks and worse on 30. It used one run per task, did not use the
paper's recompute technique (`--no-recompute`), and built its indexes with an embedding model that LEANN found six days
after committing the results could load silently as a constant-vector encoder.

**What it means here, heaviest first:**

1. **An outside instrument for the context bet (tack, ken).** LEANN's project now ships what ken ships, a code-search
   MCP server for Claude Code, and has a headline number in ken's lane, while ken has no agent-in-the-loop measurement
   at all (ken `docs/BENCH.md` names it an open tier-2 follow-up). ContextBench, used properly, gives both projects
   what tack's pass rates cannot: a direct measure of whether the right code reached the model. §4 proposes it in steps.
   The first needs no model (CB1); one needs no new runs (CB3); the agent-in-the-loop step (CB2) reproduces LEANN's
   protocol with its gaps closed, and is optional.
2. **Recompute instead of store (ken).** LEANN's central idea fits ken better than LEANN's own design does. ken embeds
   with Model2Vec, which recomputes a chunk's vector exactly from its text with table lookups. ken's dense arm is flat
   by decision (ADR-043 declined HNSW), and every ken workspace, tack's included, keeps an f32 copy of every vector only
   so it can append and compact. §5 measures what ken's index is made of; §6 orders the memory work that could justify,
   starting with dropping that copy, and parks a LEANN-style mode at the end.
3. **aikit: nothing now.** One small primitive is parked with that mode (§7). No product quantization: aikit's
   dead-ends already rule it out, and its existing binary codes do the same job here.
4. **goinfer: nothing** (§3.4).
5. **skiff: a note** (§3.5). Its Phase 0 result already points away from shipping vectors at all.

**Corrections to the conversation that led here:**

- The README's comparison is against BM25 in its text only; the committed experiment is against plain Claude Code, and
  its committed numbers differ (§1.4). The earlier answer repeated the README's figures as LEANN's result.
- MEM17 (chunk text by reference) is not on hold. Francis decided D4 on 2026-10-21; the work is open (goinfer
  `docs/tasks/task-memory-campaign-2026-10.md`, MEM17).
- Hub-preserving pruning has no consumer: nothing outside aikit builds or loads its HNSW (checked across ken, goinfer,
  tack and skiff), and ken declined HNSW in ADR-043.
- "skiff is where the trade bites hardest" overstated it; skiff's best arm ships no vectors (§3.5).
- The claim that vectors are a minority of ken's index holds: at Linux-kernel scale ken's hybrid index peaks at 7.00 GB
  against 6.14 GB for BM25 alone (ken `docs/PERF-expectations.md`, "Measured Linux-kernel numbers").

---

## 1. The sources

### 1.1 LEANN, the paper

*LEANN: A Low-Storage Overhead Vector Index*, Yichuan Wang, Zhifei Li, Shu Liu, Yongji Wu, Ziming Mao, Yilong Zhao,
Xiao Yan, Zhiying Xu, Yang Zhou, Ion Stoica, Sewon Min, Matei Zaharia, Joseph E. Gonzalez. MLSys 2026 (best research
paper); arXiv 2506.08276 (v1 June 2025, v2 November 2025). Built on FAISS's HNSW.

**What it stores.** For N chunks: a pruned graph in CSR form (4-byte neighbor ids) and a product-quantization table about
100× smaller than the f32 embeddings. No embeddings, except an optional cache of frequently visited ones when disk
allows.

**How it searches.**

- **Recompute.** Best-first search on the graph, as HNSW does, but each visited node's embedding is recomputed by the
  encoder that built the index. A graph search visits O(log N) nodes, which is what makes recomputing affordable. An IVF
  index visits O(√N), which is why its recompute baseline (EdgeRAG-style) was up to 200× slower.
- **Two-level search.** Every neighbor gets a cheap PQ distance; only the top α% of the approximate queue is recomputed
  exactly, and exact distances choose where the walk goes next. This is interleaved, not DiskANN's walk-then-rerank: at
  100× compression the PQ distances alone send the walk on detours and miss true neighbors, and reranking more of them
  does not recover recall.
- **Dynamic batching.** It relaxes best-first order slightly so recomputes accumulate across hops until a target batch
  (e.g. 64, set by offline profiling) is reached, for GPU utilization.
- Two-level search alone gave 1.4× on average (up to 1.6×); with batching, 1.8× (up to 2.0×).

**How it shrinks the graph (high-degree-preserving pruning).** Hub nodes are visited far more often than others. The top
β% of nodes by degree (2% worked) keep up to M edges; the rest are capped at m = M/5; every node may still accept
back-links up to M, so it can reach a hub. Halving average degree (18 → 9) this way needed about as many recomputes as
the unpruned graph for the same recall; random pruning needed up to 1.8× more, and a smaller M up to 5.8× more (and
never reached 94% or 96% recall).

**Build and update.** A sharded build bounds peak storage: k-means on a sample, each chunk assigned to its two nearest
centroids, a graph per shard with embeddings discarded after each, then a merge (15 shards gave about 5× lower peak).
Adds use a distance cache and simplified neighbor selection, O(M · efC) instead of cubic in M (up to 63.3× faster);
batched adds are buffered, searched alongside the graph and inserted asynchronously; deletes are soft.

**Numbers.**

| measure | value |
|---|---|
| corpus | RPJ-Wiki, 76 GB raw, 60M chunks of 256 tokens, Contriever (110M parameters, 768-d) |
| storage | LEANN 4 GB against HNSW 188 GB, PQ 20 GB, BM25 59 GB (Table 1, NQ, Qwen3-4B on an RTX 4090) |
| retrieval at 90% recall@3, RTX 4090 | 2.48 s (NQ), 2.96 s (TriviaQA), 1.12 s (GPQA), 7.12 s (HotpotQA) |
| generation it hides behind, RTX 4090 | 17.17–69.60 s per answer |
| retrieval at 90% recall@3, M1 Ultra, 128 GB (AWS) | 13.84 s (NQ), 17.15 s (TriviaQA), 5.22 s (GPQA), 44.80 s (HotpotQA); HNSW and IVF ran out of memory |
| where query time goes | recompute about 76%; the rest PQ lookup and fetching and tokenizing text |
| smaller encoder | GTE-small (34M) instead of Contriever: 2.3× faster, downstream accuracy within 2% (2M chunks, ef = 50) |
| caching hot embeddings | 10% stored → 1.5× faster, cache hit rate up to 41.9% |
| downstream accuracy | matches HNSW at 90% recall; exact match up to 11.8% above BM25 (the paper's wording) |

**What it assumes.** Storage is the scarce resource; an encoder is available at query time; reading a visited chunk's
text is cheap; and seconds of retrieval hide behind a long generation. §3 checks each against our projects.

### 1.2 LEANN, the project

`github.com/yichuan-w/LEANN` (moved to `StarTrail-org/LEANN`), MIT, package version 0.3.9, Python ≥ 3.10 with C++
backends: FAISS-based HNSW by default, DiskANN and IVF optional, ZeroMQ between the searcher and an embedding server.
Roughly 12,000 GitHub stars by September 2026. What it ships that overlaps ken, as read in its code at `c443fc4`:

- **An MCP server for Claude Code** (`leann_mcp`) with four tools: `leann_search` (`index_name`, `query`, `top_k`
  1–20, default 5; `complexity` 16–128, default 32), `leann_list`, `leann_build` (incremental; its backend defaults to
  IVF) and `leann_status`. Search is vector-only through the MCP server.
- **AST-aware chunking**, opt-in (`--use-ast-chunking`), for Python, Java, C# and TypeScript; `.js` and `.jsx` go
  through the TypeScript parser; Go, Rust, C and C++ fall back to plain text chunks
  (`packages/leann-core/src/leann/chunking_utils.py`, `CODE_EXTENSIONS`).
- **Hybrid BM25 plus vectors** (pull request #211): SQLite FTS5 BM25, fused by an unnormalized weighted sum,
  `vector_weight × vector score + (1 − vector_weight) × BM25 score`, default `vector_weight` 1.0 (pure vectors), no RRF.
  Available in the Python API only, not the CLI or the MCP server. A separate grep mode (#87) is not fused.
- **Change detection**: `leann watch` polls Merkle-tree snapshots (every 30 s by default) and rebuilds incrementally.
- **Defaults**: `leann build` embeds with `sentence-transformers/all-MiniLM-L6-v2` on macOS or CPU and
  `BAAI/bge-base-en-v1.5` with CUDA (`cli.py:_default_embedding_model`; the README's CLI reference still says
  `facebook/contriever`, which is now the default only for its app subcommands); HNSW backend; recompute on.

### 1.3 ContextBench

*ContextBench: A Benchmark for Context Retrieval in Coding Agents*, Han Li, Letian Zhu, Bohan Zhang, Rili Feng, Jiaming
Wang, Yue Pan, Earl T. Barr, Federica Sarro, Zhaoyang Chu, He Ye. arXiv 2602.05892 (v1 5 February 2026, v3 11
February). Code at `github.com/anonymousUser2026/ContextBench` (five commits, all 2026-01-29); data at
`huggingface.co/datasets/Contextbench/ContextBench`, also committed as parquet files in the repository; project site
`cioutn.github.io/context-bench`.

**License: none.** No license file in the repository outside its vendored agent frameworks; the dataset card names none;
the project site's footer says "All Rights Reserved". So nothing of ContextBench's (code or data) goes into ken's or
tack's trees: it is fetched and run outside them, as LEANN is.

**Tasks** (recounted from `Context-Bench/data/full.parquet`):

| | count |
|---|---|
| tasks | 1,136, from 66 repositories |
| by language | Python 512, JavaScript 153, TypeScript 119, Go 104, C 68, Rust 63, C++ 60, Java 57 |
| by source | Multi-SWE-bench 461, SWE-bench Verified 313, SWE-PolyBench 219, SWE-bench Pro 143 |
| SWE-bench Pro | Python 96, Go 27, JavaScript 20 (ansible, openlibrary, qutebrowser, flipt, NodeBB, element-web, navidrome, vuls, teleport) |
| Lite subset (`contextbench_verified.parquet`, 500) | Verified 174, Multi 156, Poly 116, Pro 54; every agent and model result in the paper is on these 500 |

Each row carries `instance_id`, `repo`, `repo_url`, `base_commit`, `problem_statement`, `patch`, `test_patch`,
`f2p`/`p2p` test lists, `language`, `source` and `gold_context`.

**Gold context.** A JSON list of `{file, start_line, end_line, content}` spans. Experts traced from the gold patch along
calls, inheritance and data flow under a minimal-context rule, about 40 minutes per issue; a context counted as
sufficient if GPT-5, given only that context, produced a passing patch in at least one of five tries; a second annotator
pruned it. Note that `content` is the gold code itself: it must never reach any arm.

**Metrics** (`contextbench/metrics/compute.py`, `evaluate.py`):

- **Coverage** (its word for recall) and **precision**, at four granularities: file (paths), symbol (tree-sitter
  definitions overlapping the spans; the paper calls it "block"), span (byte intervals) and line (merged line ranges).
  An empty gold scores coverage 1.0; an empty prediction scores precision 1.0.
- **Final** metrics score the agent's declared final context; **trajectory** metrics score the cumulative union after
  each step.
- **Efficiency (AUC-Cov):** the mean of cumulative coverage over the run's steps.
- **Redundancy:** one minus the union's size over the sum of per-step sizes.
- **Usage drop** (explored versus used): of the gold the agent saw, the share missing from its final context.
- **EditLoc:** the share of the model patch's deleted lines that fall inside gold.
- There is no "initial recall" metric; that term is LEANN's.

**Scoring an outside agent.** Besides its built-in trajectory parsers (mini-SWE-agent, SWE-agent, Agentless,
OpenHands, Prometheus), the evaluator reads a generic JSON record per task: `instance_id`, `model_patch`, and
`traj_data` with `pred_steps` (each a list of files and `{path: [{start, end}]}` spans), `pred_files` and `pred_spans`.
Paths are matched by suffix against a checkout at `base_commit`, which the evaluator clones itself. **Scoring needs no
Docker**, but it needs `tree-sitter` 0.20.4 and `tree-sitter-languages` 1.10.2, whose wheels stop at Python 3.11.

**What the paper found** (Lite subset):

- Agents with GPT-5: pass@1 0.452–0.512; line coverage 0.461–0.606 and line precision 0.195–0.318. The most elaborate
  scaffold (Prometheus) had the best pass@1 and among the worst precision; plain mini-SWE-agent had the best file
  precision ("the bitter lesson").
- Models on mini-SWE-agent: Claude Sonnet 4.5 line coverage 0.588, precision 0.374, pass@1 0.530; GPT-5 0.606, 0.301,
  0.472; Gemini 2.5 Pro and Devstral 2 lower on coverage.
- Recall over precision holds at block and line level, not at file level.
- Usage drop from 0.179 (GPT-5) to 0.435 (Devstral 2): agents saw gold code they did not keep.
- Claude Code was not evaluated.

### 1.4 LEANN's ContextBench experiment, as committed

**The claim** (LEANN README, "Making Your Coding Agent Smarter", added 2026-09-05 by pull request #408): Claude Code with
LEANN against BM25, on 30 SWE-Bench Pro tasks from ContextBench, with model, agent, tools and an 8,192-token retrieval
budget held fixed. Initial relevant-code recall 24.2% against 11.4% (2.1×); coverage after exploration 38.4% against
25.8%; tokens 3.22M against 3.51M. "Macro-averaged annotated gold-line metrics."

**The code** (`benchmarks/contextbench/`, pull request #318, merged 2026-10-21):

- **Two arms, neither BM25.** `LEANN_MODE=mcp` gives Claude Code the LEANN MCP server plus a prompt hint (call
  `leann_search` once before broad exploration, `top_k` 5, at most 10; open the top one to three files; then targeted
  grep; at most two searches). `LEANN_MODE=none` is Claude Code with its built-in tools and the LEANN server removed
  (`--strict-mcp-config`). No BM25 implementation exists under `benchmarks/contextbench`.
- **No token budget.** Neither 8192 nor 8,192 appears in the runner; the only limits are the hint's instructions.
- **Invocation:** `claude -p <prompt> --dangerously-skip-permissions` on host checkouts, 1,200 s timeout, the model the
  CLI defaults to unless `MODEL` is set (`scripts/auto_run.py`). Which Claude model ran is not recorded.
- **Index:** `jinaai/jina-embeddings-v2-base-code` through sentence-transformers, HNSW, **`--no-recompute`**, AST
  chunking at 600 characters with 96 overlap, built on MPS by default (`scripts/prepare_repos_with_leann.py`).
- **Capture:** mitmproxy on the Messages API traffic. Each response with tool calls is a step. A `Read` gives a span
  only when it sets `offset`; `Grep`, `Glob` and `Bash` reads give files only. The "final" context is the union of all
  steps, so final coverage equals explored coverage, and whole-file reads are undercounted at line level.
- **Tasks and runs:** 53 SWE-bench Pro instance ids are listed in the scripts (43 Python, 6 Go, 4 JavaScript; 20 of them
  in the Lite subset). One run per task, no seed. Which 30 the README used is not recorded.

**The committed results** (`contextbench_official_eval_claude_{baseline,leann}.jsonl`, 53 tasks each, the same ids;
macro averages recomputed for this doc):

| metric | Claude Code alone | Claude Code + LEANN |
|---|---|---|
| final line coverage | 0.329 | 0.262 |
| final line precision | 0.348 | 0.427 |
| line AUC-Cov (efficiency) | 0.167 | 0.147 |
| final file coverage | 0.647 | 0.555 |
| final file precision | 0.534 | 0.661 |
| steps per task | 21.6 | 14.4 |
| line coverage after step 3 / 5 / 10 | 0.021 / 0.072 / 0.159 | 0.054 / 0.126 / 0.195 |
| file coverage after step 1 / 3 | 0.025 / 0.227 | 0.083 / 0.318 |

Paired on final line coverage, LEANN is better on 15 tasks, worse on 30 and tied on 8.

**Read fairly:** with LEANN the agent reached gold code sooner, explored less, and kept a more precise but smaller
context; without it, the agent explored longer and ended with more. That matches the README's direction for early
recall and contradicts it for final coverage. The README's exact figures, its BM25 arm and its budget do not appear in
the committed code or results; they may come from runs not committed.

**The embedding model.** LEANN's own fix of 2026-10-26 (`0463b76`, pull request #445) records that this model, loaded
without `trust_remote_code`, silently becomes a plain BERT with ~70 randomly initialized tensors and returns one
constant vector for every input (an 8-chunk probe: recall@1 0.000 before, 1.000 after). The results above were committed
on 2026-10-20. Whether their runs were affected depends on library versions they did not record.

**What this means for us:** LEANN's code-search claim is unsettled, not established. A careful reproduction with ken
in it (CB2) would be the first clean agent-in-the-loop comparison in this lane, and the retrieval-only step (CB1)
settles most of the question without any agent.

---

## 2. Where each project stands (from the trees)

### 2.1 tack

- **The context bet is the product's case** (DESIGN §7: "The product's case rests on this section"), decided by the P2
  exit rule: A3 (tack with ken and auto-context) against A1 (tack with `grep` and `read` only) on capped time, with pass
  rate as the guardrail (DESIGN §15.3).
- **DESIGN §15.1 already wants a context metric** for the context arms: "whether the files the oracle's diff touches
  entered the window and when (retrieval recall at the edit site), and the share of window tokens never referred to
  again". It is not built.
- **Pass rates are near the floor.** Qwen3-8B's calibration row passed 4 of 36 runs, all on two tasks
  (`docs/measurements/e1-calibration-c5-qwen3-8b-2026-10-19.md`); the tier-1 check of 2026-10-27 passed 0 of 24
  (`docs/measurements/e1-calibration-c6-t1-current-symbol-2-2026-10-27.md`). Five text nudges in a row changed nothing
  (`docs/measurements/e1-calibration-c6-next-direction-proposed-2026-10-27.md`). A binary outcome at these rates has
  almost no power to show a retrieval change.
- **ken in tack's runs.** The K10–K15 brief recorded that no calibration run had used ken
  (`docs/briefs/ken-k10-k15-structural.md`, "Why"). ken v1.11.0 is now pinned and the `symbol` tool uses it
  (`internal/tools/symbol.go`); ken's engine turns on only with a `[ken]` section in tack's config
  (`internal/config/config.go:Ken`). The A0k arm (opencode with ken-mcp) warms ken outside the clock
  (`internal/eval/pilot/ken.go:WarmKen`, with `internal/eval/pilot/pilot.go:KenEnv`).
- **tack opens ken through `engine.Open`** (`internal/kensearch/kensearch.go`), with `Watch` on in the daemon's shape.
  Every `engine.Open` workspace holds a `search.WatchedIndex`, with or without the background watcher (ken
  `engine/engine.go:Workspace`), so tack carries the index that keeps f32 vectors (§2.2).
- Rules that shape this doc: local models only in the product; no Python in tools and gates; timed runs by day under the
  timing lock, with the expected wall time stated first; pre-registered rules with a do-nothing arm and paired
  differences (`CLAUDE.md`).

### 2.2 ken

- **Pipeline** (README, ARCHITECTURE): BM25 (Lucene variant, k1 = 1.5, b = 0.75) plus Model2Vec semantic
  (`potion-code-16M`, 61,826 × 256, about 60 MB) fused by α-weighted RRF (α = 0.3 for symbol queries, 0.5 for natural
  language), then file-coherence and query-type boosts; a verbatim port of MinishLab's semble. Candidates per arm:
  `topK × 5`.
- **Measured quality**: recall@10 0.967 NL / 0.995 symbol, NDCG@10 0.842 on semble's 1,251-query benchmark; about 46×
  fewer tokens than `grep` plus `Read` at K = 10 (4,120 against 189,773 median tokens on NL queries). The semantic arm
  adds +0.13 recall@10 over BM25 alone (0.841 → 0.971). These measure ranking of a known target and the cost of reading
  it, not an agent's trajectory (`docs/BENCH.md`, "Caveats": the agent-loop measurement "is a separate tier-2
  follow-up").
- **The dense arm is flat.** `aikit/ann.Flat`, exhaustive f32 cosine, O(N). ADR-043 (2026-09-06) added opt-in `FlatI8`
  and `FlatBinaryI8` (`engine.Options.DenseRetriever`) and **declined HNSW**: its build took 271.8 s at 200k vectors, incompatible with the
  watcher's 2-second republish, and it holds the f32 vectors plus a graph, so it costs more memory than `Flat`. At real
  kernel scale (584,019 chunks) `FlatI8` agreed with exact `Flat` at 1.00 on agree@10, `FlatBinaryI8` at 0.96
  (`docs/internal/dense-retriever-adoption-2026-09.md`).
- **The watched path keeps f32 vectors.** ADR-043's guardrail: `WatchedIndex` retains the f32 vectors for append and
  compaction (`internal/search/watch.go:WatchedIndex`, its `vecs [][]float32`), so enabling `FlatI8` there adds about
  260–290 bytes per vector on top of the 1,024 instead of replacing them. The memory win on that path "needs the
  (unscheduled) int8-throughout incremental refactor". Both ken-mcp and `engine.Open` (tack) use this type.
- **Memory at scale** (`docs/PERF-expectations.md`): whole Linux v6.10 tree, 84,892 files, 826k chunks, M1 Pro: bm25
  index 471.9 s, 6.14 GB peak RSS, search p50 5.84 ms; hybrid 501.9 s, 7.00 GB, p50 65.7 ms. Vectors are about 0.86 GB
  of the 7.00. Hybrid search p50 grows about 0.08 ms per 1,000 chunks: the flat scan.
- **Persistence**: snapshot at `<repo>/.ken/snapshot.{bin,manifest}` (ADR-039); optional persistent
  `sha256(chunk) → vector` cache at `<repo>/.ken/embed.db` (`KEN_MCP_EMBED_CACHE`); model mmapped in ken-mcp
  (ADR-045).
- **Embedding**: deterministic, with f64 accumulation (ken `docs/DESIGN.md` §4's precision contract), so re-embedding
  a chunk reproduces its stored vector.
- **Retrieval is treated as closed for 1.0** (README; `docs/internal/road-to-1.0.md`). ken v1.11.0 pins aikit v1.70.1,
  which predates v1.71.0's BM25 postings arena (index 20% smaller, scores bit-identical) and saved, mappable BM25
  index.

### 2.3 aikit

- `ann` holds `Flat`, `FlatI8` (with mmap and persistence), `FlatBinary` (1-bit centered sign codes, Hamming prefilter,
  exact f32 rerank), `FlatBinaryI8` (the same prefilter with an int8 rerank) and `HNSW` (M = 16, layer 0 at 2M,
  efConstruction 200, diversity-heuristic neighbor selection, optional int8 vectors, a CSR graph since v1.71.0 that
  aliases the mapping under `LoadHNSWMmap`). The prefilter (`binaryPrefilter`, `DefaultOverquery` = 16) is unexported,
  and both binary types keep an f32 or int8 copy of every vector beside it.
- No product quantization or IVF, by recorded decision: "PQ's win is memory, and int8 already took the 4×"
  (`docs/internal/perf-dead-ends.md`, "Reasoned out, not built"); the goinfer memory campaign repeats it under "do not
  re-propose".
- The memory campaign's analytic table for a code-search index at 1M chunks × 768-d: BM25 about 1.2 GB (33%), chunk text
  about 1.39 GB (39%), int8 vectors 772 MB (21%), model 209 MB (6%) (goinfer
  `docs/tasks/task-memory-campaign-2026-10.md` §1.2). ken's 256-d Model2Vec vectors are a third of that size per chunk.
- aikit is in wind-down and modularization (goinfer `docs/tasks/task-subsystems-2026-10.md`); its GPU ANN and text
  encoder backends are frozen.
- An embedding micro-benchmark exists: `embed/embed_bench_test.go:BenchmarkInferOne`.

### 2.4 goinfer and skiff

- goinfer is winding down feature work for modularization. Its `/v1/embeddings` path was CPU-only with no peer
  comparison as of goinfer's September GPU-paths survey (G1–G11). ken does not use goinfer for embeddings: Model2Vec
  runs in-process.
- skiff (`~/tmcode/skiff`, no git repository; last files early September) ran Phase 0: arm A1, which ships no vectors,
  no token table and no query-time model at +7.9% index size over plain BM25, beat Pagefind's best configuration by 50%
  relative NDCG; the Model2Vec hybrid (B2) reached +4.4 NDCG points against a +5 exit bar; no exit or kill decision yet,
  pending the conceptual query slice and pooled judgments (`docs/phase-0-results.md`).

---

## 3. Intersections, project by project

### 3.1 tack: an outside instrument for the context bet

- tack's bet is that ken puts the right few thousand tokens in a 16k window. The planned measures are E1's pass rate and
  capped time, and §15.1's unbuilt "retrieval recall at the edit site". With pass rates at 0–11%, the bet can sit
  undecided for a long time while the window's contents are never measured directly.
- ContextBench measures exactly what the bet is about, with gold written by people, and its metrics are defined over a
  trajectory, so they can score tack's own runs (CB3), not only ContextBench's tasks.
- Its findings support DESIGN's bet: scaffolding buys little retrieval, which favors what reaches a small window over
  agent breadth (§0); and explored-but-unused context is common, which E1 already plans to report as "the share of
  window tokens never referred to again" (§15.1).
- What not to take from it: ContextBench's tasks are large, multi-language repositories. Qwen3-8B in a 16k window passed
  about one run in nine on tack's own smaller Go calibration tasks; running ContextBench's tasks through tack's loop
  would mostly measure window exhaustion. CB3 brings the metrics to tack's tasks instead.

### 3.2 ken: the competitor, and recompute instead of store

**The competitor.** LEANN's project is in ken's lane with more visibility (a best paper, roughly 12,000 stars) and a
published agent-in-the-loop claim ken has no answer to. That claim is weaker than it reads (§1.4), and three facts make
a fair comparison with ken feasible and interesting:

- **Their MCP search is vector-only.** ken's is hybrid, and on semble's benchmark ken's semantic arm adds +0.13
  recall@10 over BM25. Whether LEANN's vectors beat ken's hybrid on human gold is open, and CB1 can answer it without an
  agent.
- **Their best code chunking covers four languages.** Their own SWE-bench Pro slice includes Go and JavaScript tasks;
  Go fell back to plain text chunks.
- **Their measurement left gaps a reproduction can close:** a real BM25 arm, a do-nothing arm, two runs per task, a
  recorded model, whole-file reads counted, and an embedding model loaded as intended.

ken's verifiable differences, for any write-up that follows: one static Go binary with in-process embeddings, against
Python plus C++ backends and an embedding server; a regex chunker for every language plus an opt-in tree-sitter
chunker over many grammars, against AST chunking for four; published recall with reproduction commands. Claims about
LEANN's retrieval quality on code wait for CB1.

**Recompute instead of store.** LEANN's idea needs three things; ken has two of them more cheaply than LEANN does.

| LEANN needs | LEANN pays | ken |
|---|---|---|
| an encoder at query time | a 110M-parameter transformer; recompute is 76% of query time | Model2Vec: tokenize, gather 256-d rows, weighted mean; deterministic, so a recomputed vector equals the stored one |
| a cheap approximate distance to choose what to recompute | a PQ table (~100× smaller than f32) | aikit's 1-bit codes: 32 bytes per 256-d vector, already gated on real Model2Vec embeddings |
| a structure that keeps recomputes to O(log N) | an HNSW-style graph and its build | **none**: ken's dense arm is flat; HNSW was declined for build time (271.8 s at 200k against a 2-second republish) |

So LEANN's graph form does not fit ken. A **flat** form does: score every chunk's 1-bit code, shortlist, recompute the
shortlist exactly, rank. That is aikit's `FlatBinary` with the f32 copy replaced by recomputation. It keeps the
prefilter's measured recall (zero pipeline loss at repo scale; agree@10 0.96 at kernel scale for the same prefilter in
`FlatBinaryI8`) and moves cost from memory to query time. §6 works out when that trade pays; at repo scale it does
not.

**What the idea does for ken today, without a new mode.** The watched index keeps f32 vectors only so that it can append
and compact. With deterministic recompute it never needs them: it can keep int8 codes and recompute f32 on the rare
occasion something needs it. That is ADR-043's "unscheduled int8-throughout incremental refactor", and it is the first
LEANN-inspired item worth doing (§6, K17), if §5's measurement shows the vectors matter at the owner's corpus sizes.

Notably, LEANN's own coding-agent experiment did not use recompute either: its indexes were built with
`--no-recompute`.

### 3.3 aikit

- Hub-preserving pruning applies to `ann.HNSW`, which has no consumer outside aikit. Not proposed.
- Two-level search with PQ: PQ is ruled out (perf-dead-ends), and the binary prefilter plays its part (§3.2).
- Dynamic batching exists to keep a GPU busy with transformer recomputes; Model2Vec recompute is a CPU gather. Not
  applicable.
- One parked primitive: export the binary prefilter as a shortlist over codes alone (§7, AK1). It waits for §6's parked
  mode.

### 3.4 goinfer

- Nothing to do. ken's embeddings never touch goinfer. LEANN's premise (generation dominates latency) holds even more on
  the owner's hardware, but ken's search is already sub-millisecond at repo scale, so there is no retrieval latency to
  hide.
- LEANN accepts any OpenAI-compatible embedding endpoint, so goinfer's `/v1/embeddings` could serve as its local
  encoder. That would be a promotion angle, not a measurement; the path had no peer row, and goinfer's feature work is
  winding down.

### 3.5 skiff

- skiff's planned semantic store (`docs/phase-0-results.md`, the A-versus-B cost tie-break) is a binary sketch tier
  (about 2 MB), an int8 refinement tier (about 8 MB), a tokenizer (about 1 MB) and a Model2Vec token table (8–16 MB, or
  sharded to about 1 MB plus tens of KB per query).
- The token table must ship anyway, to embed the query in the browser. With it, the int8 refinement tier is recomputable
  from chunk text for each shortlisted chunk, as in §3.2: the LEANN idea deletes about 8 MB.
- It applies only if family B returns after the conceptual slice, and only if the shortlisted chunks' text is already on
  the client (each recompute must not become a fetch). Phase 0's direction (A1 ships no vectors) makes it a footnote.

---

## 4. CB: ContextBench as an instrument

Four steps. CB1 and CB3 carry the value; CB2 is optional and costs Max-plan usage.

| step | what | tier | model | where the code lives |
|---|---|---|---|---|
| CB0 | set up the evaluator outside every repository; break-it-first smoke; size disk; settle CB2's sandbox | — | none | notes in this doc |
| CB1 | retrieval only: each arm answers the issue text under a token budget; ContextBench scores it | 1 | none | ken, `bench/contextbench` (brief K18, §5.3) |
| CB2 | agent in the loop: LEANN's protocol with its gaps closed, ken added | 2 | Claude Code on the Max plan | ken, behind the same build tag |
| CB3 | ContextBench's trajectory metrics on tack's own captured runs | 0 | none | tack, `internal/eval` |

### 4.1 CB0 — set up and smoke the instrument

No timed runs. Most of what CB0 was to read is now in §1.3 and §1.4. What remains:

- **The evaluator's environment.** A Python 3.11 virtual environment for ContextBench's evaluator, outside every
  repository (ken and tack carry no Python), with its pinned `tree-sitter` 0.20.4 and `tree-sitter-languages` 1.10.2. On
  nobara by default. The evaluator clones each task's repository at `base_commit` into its own cache; CB1's harness
  shares that cache rather than cloning twice.
- **Break it first.** Through the official evaluator, in the generic `traj_data` schema: the gold spans themselves (line
  coverage 1.0), an empty prediction (coverage 0.0), and one hand-built partial case whose coverage and precision are
  computed by hand first. Then one real task end to end.
- **Disk.** Clone sizes for the 66 repositories (Lite's subset first), so the cache's size is known before CB1.
- **LEANN's environment.** LEANN 0.3.9 or later (after the encoder fix) under `uv`, outside every repository. Its
  committed model needs `LEANN_TRUST_REMOTE_CODE=1`, which runs Python from the model's Hugging Face repository: pin the
  model revision by hash and run it as an unprivileged user with no access to the owner's repositories or keys.
- **CB2's sandbox.** LEANN's runner used `--dangerously-skip-permissions` on host checkouts. CB2 does not. CB0 picks the
  containment (a disposable container per task, or a dedicated user and a checkout outside the owner's trees with
  Claude Code's permission allowlist) and records it.
- **Gate to CB1:** the three smoke cases score as computed by hand, and one real task scores without error.

### 4.2 CB1 — retrieval only, no model

**What it measures.** For each task: give each arm the task's `problem_statement` verbatim as the query; take its
ranked results and fill a token budget top-down; hand the spans that fit to ContextBench's evaluator as a one-step
trajectory. The headline is **single-call coverage at a budget**: what one retrieval call puts in front of a model
before any exploration. This is what LEANN's "initial recall" is about, with an exact definition, and it is ken's
`docs/BENCH.md` token-budget method moved onto human-annotated gold.

**Arms** (paired on every task):

| arm | what | why |
|---|---|---|
| G | tokenized grep: files containing any identifier from the query, whole files, ordered by the number of distinct identifiers matched, then path | the do-nothing-tool arm: what an agent with only `grep` and `read` gets (ken BENCH's grep baseline, with an order so a budget can be filled) |
| B | ken `--mode bm25` | the lexical floor, and the baseline LEANN's README names |
| K | ken default: hybrid, regex chunker, `Flat` | ken as shipped |
| KT | ken hybrid with the tree-sitter chunker | ken BENCH found tree-sitter cuts the definition-split rate 3.2× with ranking unchanged; line-level gold may finally see that |
| L1 | LEANN as in its committed experiment: jina-embeddings-v2-base-code with remote code enabled, AST chunking at 600/96, HNSW | the competitor as it measured itself |
| L2 | LEANN's CLI defaults: bge-base-en-v1.5 on CUDA, HNSW, recompute on, no AST chunking | the competitor as a new user installs it |

Optional: **LH**, LEANN's Python-API hybrid at a `vector_weight` stated in the registration, if L1 or L2 lands near K and
the question becomes whether ken's fusion is the difference.

**Budgets.** 8,192 tokens (the README's), with 2,048 and 16,384 as a curve. Tokens counted with cl100k_base through
`tiktoken-go` behind the `bench` build tag, as ken's token bench does, never in released binaries; the same counter for
every arm. A chunk that would cross the budget is dropped, not truncated; the top chunk is always kept.

**Metrics** (ContextBench's own, from its evaluator): primary, **line coverage at 8,192 tokens**, paired per task.
Secondary: symbol and file coverage; line and file precision; tokens to the first gold line (computed in the harness);
EditLoc does not apply (no patch). Every metric broken down by language, source and repository, so no single repository
carries a result.

**Scope.**

- **S53**: the 53 SWE-bench Pro ids in LEANN's scripts, all arms. The comparable slice. Reported, not decided on (§8.1).
- **Lite**: the 500-task subset the paper's agent results use, arms G, B, K and KT. The decision slice for ken against
  BM25 and grep.
- **L100**: a seeded, language-stratified 100 of Lite for L1 and L2, if S53 shows LEANN's build cost per task allows
  more; the decision slice for K against L. The seed is in the registration.
- The full 1,136 for ken arms only, later, if Lite raises a question Lite cannot answer.

**How a task runs.**

1. The repository from the shared cache at `base_commit`, via `git archive` into a fresh temporary directory: only the
   base tree exists, so nothing later is indexed. The `gold_context`, `patch` and `test_patch` fields never reach an arm.
2. Each arm builds its index over that tree, outside the clock. Record build time and peak footprint per arm (ken from
   its own process; LEANN from its process tree, including the embedding server).
3. Each arm is queried once with the issue text, verbatim. No query rewriting in any arm.
4. Fill the budget, emit one `traj_data` record per task and arm, score with the official evaluator, and write one JSON
   line per task and arm with the provenance block ken's benches already emit (commit, chunker, config, LEANN version,
   model revision).
5. Delete the tree and the indexes.

**Where and how long.** On nobara by default: LEANN's encoders on CUDA, the timing lock held, ken on CPU in the same
session. **Pilot first: 5 tasks from S53, all arms, by day, expected under 30 minutes** (unmeasured: SWE-bench Pro
repositories are mid-size; ken builds those in seconds, and LEANN's cost is a transformer pass over every chunk). The
pilot's per-task times set S53's and Lite's stated wall times before either starts; a run over about two hours goes to
the night queue under the run-budget rule.

**Determinism.** G, B, K and KT are bit-reproducible at a pinned ken commit; a re-run must match byte for byte (a tier-0
check on one task). L1 and L2 are reproducible at pinned LEANN, FAISS, sentence-transformers and model revisions, which
CB1 records.

### 4.3 CB2 — agent in the loop, LEANN's protocol with its gaps closed (optional)

**What it adds.** CB1 measures one retrieval call. LEANN's claim is about an agent that calls the tool and explores. CB2
reruns their committed protocol with the gaps in §1.4 closed and ken added, so ken's first agent-in-the-loop number
sits beside LEANN's on the same tasks, measured the same way.

**Arms:**

- **C0**: Claude Code with its own tools, no retrieval server: LEANN's committed baseline and the do-nothing arm.
- **CB**: ken-mcp with `KEN_MCP_MODE=bm25`: the BM25 arm LEANN's README describes and its code lacks.
- **CL**: LEANN's MCP server over an L1 index.
- **CK**: ken-mcp in its default hybrid mode.
- The three tool arms get the same prompt hint, LEANN's committed text with only the tool name changed, so the hint is
  not a confound; C0 gets none.

**Protocol:**

- headless Claude Code (`claude -p`) on the owner's Max login, **never an API key**, and **never
  `--dangerously-skip-permissions` on the host**: the containment CB0 chose;
- the model pinned with `--model` and recorded;
- the retrieval budget enforced, not only described: each retrieval tool's results capped at 8,192 cl100k tokens per
  call and two calls per task, by the server (`max_tokens` for ken; a wrapper for LEANN's tool if its server cannot cap);
- trajectories from Claude Code's own session transcript rather than a proxy, with every `Read` counted as the lines it
  returned (a whole-file read is the whole file), and `Grep` and `Bash` reads counted as the lines they printed;
- ContextBench's evaluator on each trajectory: trajectory coverage at each step, AUC-Cov, final coverage and precision
  (the union of steps, since Claude Code declares no final context), usage drop not reported for the same reason;
- **S53**, or a seeded 30 of it if Francis prefers the smaller bill; 2 runs per task per arm, in A B B A order within a
  task; pass@1 not run (it needs the task images; CB0 sizes them if wanted later).

**Cost.** 53 × 4 × 2 = 424 Claude Code sessions (240 on 30 tasks). LEANN's committed runs averaged 14–22 steps a task;
session length is otherwise unmeasured, and a 2-task pilot sets it. Spread over days to stay inside the plan's limits.

**Gate:** only after CB1, and only if K is not clearly behind L on CB1 (§8.1). If ken is behind on a single retrieval,
an agent run will not say anything CB1 did not.

**Note on tack's rules:** tack's product never calls a hosted model, and nothing here changes that. CB2 measures ken with
a hosted agent, the way ken's users run it; its runner belongs to ken, not tack.

### 4.4 CB3 — trajectory context metrics on tack's runs

**What it measures.** For every tack and opencode run already captured (calibration rows C4–C6, the pilots): which code
the model saw, when, and how much of what it saw it needed. This is DESIGN §15.1's "retrieval recall at the edit site"
plus ContextBench's precision, efficiency and explored-versus-used, computed from records that already exist. **No new
runs.**

**Gold for E1 tasks.** E1's tasks have no annotated gold, but each has an oracle commit.

- **Fix-site gold (automatic):** the lines the oracle commit changes, widened to their enclosing symbol's span at the
  base commit (ken's `Workspace.SymbolAt`, K12). Narrower than ContextBench's gold (where the fix goes, not everything
  needed to understand it), so its numbers are not comparable to ContextBench's and stay internal.
- **Annotated gold (optional):** Francis annotates the 12 calibration tasks in ContextBench's `{file, start_line,
  end_line}` form. ContextBench's experts took about 40 minutes an issue; tack's tasks are smaller, so perhaps half
  that. Unmeasured.

**Seen spans, from captures.** Each tool result that shows file content becomes spans: `read` (its range), `search` hits
(file and lines), `symbol` (the definition's span; caller and test sites as single lines), `grep` (each matching line),
`edit` and `write` (the region changed, as "used"), and `bash` output only where it is a file's text (`cat`, `sed -n`);
other `bash` output is not code. Each span carries the request index and the cumulative prompt tokens at that point.

**Metrics per run:** line coverage of the gold at the end; coverage after the first request and after the first 4,096
tokens of tool output; precision (gold lines seen over code lines seen); the request index of the first gold line;
AUC-Cov over requests; and utilization (gold lines edited over gold lines seen, ContextBench's explored versus used).

**Code:** a pure function in `internal/eval` from (capture, gold) to metrics, so the same code scores new runs. It
implements ContextBench's definitions (§1.3) at line granularity from their published description; none of
ContextBench's code is copied. Tier 0: hand-built captures with known answers, red first (a capture whose only read
misses the gold scores 0; one that reads exactly the gold scores 1). A parity check against the official evaluator on a
few captures converted to its generic schema keeps the two in step.

**Uses:**

- Every existing calibration arm gets a continuous number beside its pass count. For example: did the C6 bundle change
  how early runs reached the fix site, even where it changed no pass?
- The P2 rule (DESIGN §15.3) gains a reported diagnostic for A3 against A1. Not a decision metric until a registration
  says so: explored is not used, as ContextBench found.

**Not proposed:** ContextBench's tasks through tack on a local model (§3.1). Revisit when a local model passes E1's tasks
often enough to matter.

### 4.5 Where results go

- CB1 and CB2: a new "ContextBench" section in ken's `docs/BENCH.md`, with reproduction commands and the provenance
  block; a row in ken's README comparison table only with measured numbers, in the README's existing convention (cells
  it has not measured say so).
- CB3: a dated record in tack's `docs/measurements/`, and the metric added to E1's reported metrics.
- Tone for anything public: a reproduction on a stated protocol, LEANN and ContextBench credited, measured claims only.
  If CB1 or CB2 bears on LEANN's README, a short, friendly issue on LEANN's repository with the numbers and how to
  reproduce them is the courteous route (§9, item 9).

---

## 5. KM: what ken's index is made of (brief for ken, K16)

### 5.1 Why first

Every memory idea in §6 saves one component of ken's index. Their order depends on how big each component is at the
corpus sizes the owner uses, and today only two kinds of numbers exist: a measured whole-process peak at kernel scale
(7.00 GB hybrid, 6.14 GB bm25) and an analytic table at 1M chunks for a 768-d model ken does not use (memory campaign
§1.2). tack's own ask K8 ("latency and memory records for the in-process engine at the owner's repo sizes", DESIGN §12)
is still open. K16 is K8's memory half, by component.

### 5.2 The measurement

**Corpora:** tack, ken, aikit and goinfer at their current heads (the owner's working set); all four in one process
(tack's multi-workspace case, K7); the 584,019-chunk sparse kernel checkout from ADR-043's spot-check.

**Components**, each as an exact byte formula from the data structures (tier 0) and as a measured heap delta from
building that component alone (tier 1):

| component | where |
|---|---|
| chunk text | aikit `chunk.Chunk.Text`, held by ken's index |
| BM25 | aikit `bm25.Index`: postings, vocabulary, document lengths |
| retained f32 vectors | ken's watched index |
| the dense retriever's own store | `Flat`, `FlatI8`, `FlatBinaryI8`, each in turn |
| structural index | ken `internal/structural` |
| Model2Vec | mapped (ken-mcp, ADR-045) and heap (CLI) |
| neural reranker | when loaded |
| on disk | `.ken/snapshot.bin`, `.ken/embed.db` |

Plus the process footprint (macOS `footprint`, Linux `VmHWM`) against the sum, so unaccounted bytes show up. This
records; it does not guard, and no guard keys on RSS.

**Tiers and time:** formulas and the four repositories are tier 0–1, by day, minutes. The kernel row is a timed run:
ken's PERF record puts a full hybrid build of 826k chunks at 8.4 minutes, so expect about 6–10 minutes for 584k plus
the component builds, by day, under the timing lock.

**Output:** a component table in ken's `docs/PERF-expectations.md`, "Memory"; tack's DESIGN §12 K8 marked half done.

### 5.3 Brief items for ken

ken's Claude Code is its sole editor (ken `CLAUDE.md`); these go to it as a brief, and each lands as a tagged release on
the owner's yes.

- **K16 — the component table above.** A `-tags=bench` harness beside `retriever_scale_bench_test.go`, reusing its
  analytic-plus-measured method.
- **K17 — the watched index keeps no f32 vectors** (§6.2). After K16, and only if its table says so.
- **K18 — `bench/contextbench`** for CB1, and CB2's runner if Francis says yes to CB2. Go, `-tags=bench`, no Python in
  ken's tree; LEANN and ContextBench's evaluator are called as external programs from their own environments; nothing
  of ContextBench's is vendored (no license).
- **Also:** bump aikit to v1.71.0 or later for the BM25 postings arena and the saved, mappable BM25 index, if ken's ADR
  process agrees. Measure it in K16's table, before and after.

Not asked: ranking changes. If CB1 shows a gap, ranking stays ken's own measured work (tack DESIGN §12).

---

## 6. ken's memory ladder, and a LEANN-style mode parked at the end

### 6.1 The ladder

Ordered by expected saving per unit of risk. Every number is derived from the records cited, not measured on this
layout; K16 replaces them.

| rung | what | saves (826k-chunk kernel, derived) | query cost | status |
|---|---|---|---|---|
| 1 | aikit ≥ v1.71.0 in ken: BM25 postings arena, mappable BM25 | BM25's index 20% smaller | none (bit-identical scores) | available |
| 2 | MEM17: chunk text by reference, through a mapping | the chunk text leaves the heap (≈39% of the index side at 1M chunks in the campaign's table) | a page-cache read where text is shown | decided (D4), open |
| 3 | K17: the watched index keeps int8 only, recomputing f32 when needed | 1,024 → about 260 bytes a chunk: about 0.6 GB | none on the query path | proposed (§6.2) |
| 4 | LM: 1-bit codes only, recompute the shortlist | about 260 → 32 bytes a chunk: about 0.19 GB more | tens of ms a query (§6.3) | parked (§6.3) |

Against a 7.00 GB peak, rung 3 is about 9% and rung 4 about 3%. At the owner's four repositories the absolute numbers
are far smaller; K16 says how small.

### 6.2 K17: recompute replaces the retained f32 vectors

**Problem** (ADR-043's guardrail). `WatchedIndex` keeps every chunk's f32 vector so it can append new chunks and compact
deleted ones. With `DenseRetriever: "flat-i8"` the int8 store is added on top, so the opt-in costs memory on the one path tack
uses.

**Change.** The watched index keeps only the dense retriever's own codes. Appending embeds the new chunks (as today) and
quantizes them; compaction drops rows. Anything that needs an existing chunk's f32 vector (re-quantization after a
retriever switch; `find_related`, if it takes its query vector from a stored chunk) recomputes it from the chunk text
with the same Model2Vec model. ken's precision contract makes the recomputed vector byte-identical to the one built at
index time.

**Gates** (registered before code, each red first):

- recompute identity: every chunk's recomputed vector equals its index-time vector byte for byte, over ken's golden
  corpus and one owner repository; perturb one row and the gate fails;
- pipeline recall@10 on semble's 1,251 queries identical to three decimals against today's `Flat` (ADR-043's gate);
- the watcher's republish still about 2 seconds on the medium corpus;
- dense-side resident bytes a chunk at or under `FlatI8`'s 260 plus a stated slack, measured by K16's harness.

**What it does not decide:** whether `FlatI8` becomes ken's default. ADR-043 kept `Flat` as default and named kernel-scale
recall as the trigger; the kernel spot-check then favored `FlatI8`. With K17, flipping the default stops costing the
watched path memory. That is ken's own ADR to write, with a scale threshold if `FlatI8`'s small-N latency (192 µs
against 137 µs at 13k, synthetic) matters.

### 6.3 LM: a recompute-only dense arm (parked)

**Design.**

- **Store:** 1-bit centered sign codes, 32 bytes per 256-d chunk (aikit's prefilter), plus the centering mean. No int8,
  no f32.
- **Query:** Hamming scan of every code; shortlist `candidateCount × overquery` chunks (ken's semantic arm takes
  `topK × 5` candidates: 25 at ken-mcp's default `top_k` of 5, 50 at 10; aikit's `DefaultOverquery` is 16, so 400–800
  chunks); recompute their exact vectors from chunk text; exact cosine; the top `candidateCount` go into RRF as today.
- **Recall:** identical to `FlatBinary` at the same overquery, because the rerank is exact over the same shortlist. The
  prefilter's loss is already measured (zero pipeline loss at repo scale; 0.96 agree@10 at kernel scale in
  `FlatBinaryI8`). A larger overquery buys recall back at proportional recompute cost.
- **Optional cache:** LEANN's hot-node cache maps onto ken's existing `sha256(chunk) → vector` store
  (`KEN_MCP_EMBED_CACHE`), at the price of storing some vectors again.

**Cost, derived and unmeasured.** The only figure on record is indirect: hybrid indexing of 826k chunks took 30.0 s
longer than bm25 (501.9 against 471.9 s), which puts embedding at about 36 µs a chunk of wall time across the build's
workers, or up to about 0.3 ms a chunk on one core if all eight were busy. 400–800 recomputes is then roughly 15–240 ms
on one core per query, a few times less across cores. For comparison: `Flat` takes 0.75 ms at 50k chunks, and hybrid
search is 65.7 ms p50 at 826k. At repo scale LM would make the semantic arm 10–100× slower and break ken's own rule that
warm search above 5 ms p50 on a medium corpus is a regression. It can only ever be an opt-in for very large corpora.

**LM0, the first step if unparked:** `embed/embed_bench_test.go:BenchmarkInferOne` on the kernel corpus's real
chunk-length distribution, one core and all cores. Tier 0, seconds. With that number the cost table above becomes
arithmetic, and the decision with it.

**Why not LEANN's graph form.** It needs a proximity graph so that recomputes stay O(log N). aikit's HNSW takes 271.8 s
to build at 200k vectors and needs every embedding at build time, as LEANN's builder does; ken republishes in 2 s.
LEANN's buffered adds and soft deletes would carry edits between rebuilds but not the cold build; and its graph alone,
at an average degree of 9–18, is 36–72 bytes a node, more than the 32-byte codes.

**Why not product quantization.** aikit's dead-end stands: PQ's win is memory, and int8 took the 4×. Here the 1-bit codes
are the approximate tier, already built and gated.

**Triggers to unpark** (any one):

1. K16 shows the dense side at or above 15% of ken's footprint, after K17, on a corpus the owner uses;
2. tack's memory guard (DESIGN §17 R5) records ken's vectors as a named contributor to a pressure event;
3. an "index everything" workspace Francis wants (repositories, docs, transcripts: DESIGN §7.6's transcripts corpus)
   passes 1M chunks;
4. skiff's family B returns and needs its payload cut (§3.5).

---

## 7. aikit

- **Now:** nothing. aikit is in wind-down and modularization, and nothing here needs it until §6.3 is unparked.
- **AK1 (parked with LM): export the binary prefilter as a shortlist over codes alone.** A type holding only the packed
  centered sign bits and the mean, with `Shortlist(q []float32, n int, keep func(id int) bool) []int32`, `Append`,
  `Compact`, and mmap persistence in the style of `FlatI8`'s. No new math: it is `binaryPrefilter`, exported, without
  the f32 or int8 copy beside it. Gates: its shortlist equals `FlatBinary`'s internal candidate set (same ids, same
  order) on aikit's fixtures; bytes per vector equal dim/8 plus a constant. ADR-043 rejected composing the prefilter in
  ken to avoid a second copy of `binaryPrefilter`; exporting it from aikit is the route that rejection leaves open.
- **Not proposed:** PQ or IVF (perf-dead-ends), hub-preserving pruning of `HNSW` (no consumer), dynamic batching (no GPU
  recompute).

---

## 8. Registrations (proposed; each is committed before its run)

### 8.1 CB1

- **Primary comparison:** K against the better of L1 and L2 (chosen on S53, before L100 runs), line coverage at 8,192
  tokens, paired per task, on L100. Bootstrap interval over tasks for the mean paired difference.
- **Also decided:** K against B on Lite (does ken's semantic arm add coverage on human gold, as it adds recall on
  semble's benchmark?).
- **Reported:** KT against K (the chunker); every arm against G; all arms on S53.
- **Margin:** set from the 5-task pilot's paired standard deviation so each rule has 80% power, not from the interval's
  half-width (tack `CLAUDE.md`: a margin equal to the half-width passes an equal arm about half the time).
- **Outcomes for K against L:**
  - interval's upper bound below −m (L clearly ahead): recorded; a ken retrieval investigation becomes an owner
    decision, since ken treats retrieval as closed for 1.0; CB2 does not run;
  - interval inside ±m: "no measured difference at this n"; CB2 may run; no public claim beyond the numbers;
  - lower bound above +m (K clearly ahead): CB2 may run; ken may publish with its reproduction.
- **S53 alone decides nothing.** It exists for comparability with LEANN's committed runs.

### 8.2 CB2

- Primary: CK against CL on trajectory line coverage after 5 steps, paired per task over runs. Reported: final line
  coverage and precision, AUC-Cov, steps, tokens; CK against C0 and CB.
- Runs fixed at 2 per task per arm before the run; none added after seeing results.
- The rule mirrors §8.1, with a margin from a 2-task pilot.

### 8.3 CB3

- No decision rides on it at first. The CB3 specification (`docs/measurements/e1-cb3-context-metrics-preregistration-2026-10-28.md`, a specification rather than a pre-registration because it was committed with the scorer and initial results in `57c9548` and revised post-results in `8076b0c` on 2026-10-29 to align §3.1 failed-edit notes/token rules, §3.2 `opencode` removal, §4 aggregate `Precision`, and §4.1 `-scan` exclusions) defines the gold derivation, the span extraction rules (§4.4) and the
  metrics, and reports per arm and task for every existing calibration row.

### 8.4 K16, K17, LM0

- K16 is a record; its rows are fixed in advance (§5.2).
- K17's gates are in §6.2.
- LM0 is a micro-benchmark; its decision rule is §6.3's unpark triggers.

---

## 9. Decisions for Francis

Marked as approved on 2026-10-28 in commit `57c9548` (note: Francis's approval of decisions 1–9 is not recorded in a separate commit or note in the repository):

1. **CB1, retrieval-only ContextBench with ken, LEANN, BM25 and grep.** Recommended: yes. ken's harness (K18), a 5-task
   pilot by day on nobara, then S53 and Lite. **Decided: yes (2026-10-28).**
2. **CB2, Claude Code runs on the Max plan, 424 sessions (240 on a seeded 30).** Recommended: only after CB1, and only
   if ken is not clearly behind (§8.1); the 30-task version first. **Decided: gated on CB1, 30-task version first (2026-10-28).**
3. **CB3, trajectory context metrics in tack's eval, rescoring existing captures.** Recommended: yes, with fix-site gold.
   Annotating the 12 calibration tasks yourself is optional. **Decided: yes, with fix-site gold (2026-10-28).**
4. **K16, ken's index composition.** Recommended: yes; it is K8's memory half. **Decided: yes (2026-10-28).**
5. **K17, recompute replaces the watched index's f32 vectors.** Recommended: decide after K16's table. **Decided: gated on K16's table (2026-10-28).**
6. **LM and AK1.** Recommended: park, with §6.3's triggers. **Decided: parked (2026-10-28).**
7. **aikit bump in ken (≥ v1.71.0).** Recommended: yes, through ken's own release process, measured in K16. **Decided: yes (2026-10-28).**
8. **Where the ken items go.** Recommended: copy §5.3 and §6.2 into `docs/briefs/ken-k16-k18-contextbench-memory.md`
   when you say go, as tack did for K10–K15. **Decided: yes (2026-10-28).**
9. **Telling LEANN's maintainers about the README and code mismatch (§1.4).** Recommended: not before our own numbers
   exist; then, if they bear on it, one short and friendly issue with the reproduction. It is their project and their
   call; our part is a careful measurement. **Decided: wait for our numbers (2026-10-28).**

### 9.1 Execution plan and status

| track | item | where | artifact / code | status |
|---|---|---|---|---|
| 1 | CB3 specification (§4.4, §8.3; specification, not pre-registration: committed in `57c9548` alongside scorer and record, and revised 2026-10-29 in `8076b0c` after results/review for §3.1 and §4.1) | tack | `docs/measurements/e1-cb3-context-metrics-preregistration-2026-10-28.md` | **done (2026-10-28, revised 2026-10-29)** |
| 1 | CB3 scorer, tier-0 tests (red first), and replay CLI | tack | `internal/eval/pilot/context.go`, `internal/eval/pilot/context_test.go`, `tools/replaycontext/main.go`, `tools/replaycontext/main_test.go` | **done (2026-10-28)** |
| 1 | CB3 measurement record over captured E1 runs | tack | `docs/measurements/e1-cb3-context-metrics-2026-10-28.md` | **done (2026-10-28): 625 counted runs scored (687 with `-include-superseded`)** |
| 2 | Brief for ken: aikit bump, K16, K17, K18 | tack → ken | `docs/briefs/ken-k16-k18-contextbench-memory.md`, `docs/design/DESIGN.md` §12 | **done (2026-10-28)** |
| 3 | CB0 external envs (ContextBench Python 3.10 venv, LEANN checkout), evaluator-side smoke, disk sizing | outside repos (`~/tack-runs/cb0/`) | `docs/measurements/cb0-setup-and-smoke-2026-10-28.md` | **done (2026-10-28)** |
| 4 | K16, K17, K18 executed in ken (`v1.12.0` at `574b7d9`, ADR-049) | ken + tack (`go.mod` pinned to `v1.12.0`) | `~/tack-runs/NOTE-from-ken-2026-10-28-k16-k18.md`, `docs/measurements/ken-k16-k18-and-cb1-pilot5-2026-10-28.md` | **done (2026-10-28)** |
| 4 | CB1 5-task Go pilot (`flipt-io/flipt`) and full `S53` (`53` tasks) sweep | ken (`4deeb42`) + `~/tack-runs/cb0/` | `~/tack-runs/NOTE-from-ken-2026-10-28-cb1-s53.md`, `docs/measurements/ken-k16-k18-and-cb1-pilot5-2026-10-28.md`, `docs/BENCH.md` in ken | **pilot5 & S53 done (2026-10-28)**; Lite / L100 optional follow-on |
| 4 | CB2 (optional, gated on CB1) | ken + Claude Code Max | `docs/BENCH.md` in ken | gated on CB1 |

---

## 10. Risks

| risk | effect | mitigation |
|---|---|---|
| ContextBench's line-level gold rewards chunk boundaries as much as ranking | ken's regex chunks and LEANN's AST chunks score differently for reasons unrelated to retrieval | KT arm; symbol and file coverage reported beside line coverage |
| LEANN's published numbers rest on an unrecorded run (§1.4) | a comparison against its README would rest on nothing checkable | compare only inside our own reproduction (CB1, CB2); cite the committed results, not the README |
| LEANN's committed model executes remote code | arbitrary Python from a model repository on our machine | pinned revision, unprivileged user, no access to the owner's repositories or keys (CB0) |
| LEANN's install: Python, FAISS, ZeroMQ, optional DiskANN and MKL | build friction on nobara or the Mac | HNSW backend only; its own `uv` environment outside every repository |
| ContextBench has no license | its code or data in our trees would be redistribution | fetched and run outside ken and tack; nothing vendored; CB3 implements the definitions, not the code |
| the evaluator needs Python ≤ 3.11 and old tree-sitter wheels | setup friction | one pinned virtual environment on nobara (CB0) |
| cl100k token counts are a proxy | budgets are approximate | the same counter for every arm; ratios, not absolutes |
| eight languages against ken's chunkers | a language ken chunks poorly drags the mean | per-language breakdown; CB0 lists, per task language, which ken chunkers support it (C# is parked from ken's structural enrichment: ken `docs/DESIGN.md`, the C# grammar note) |
| repository clones for 66 projects | disk on nobara | CB0 sizes it first; Lite before the full set; trees and indexes deleted after each task |
| Max-plan limits for CB2 | runs stretch over days, or stop | CB2 is optional and gated; the 30-task version first; never an API key |
| an agent run with permissions skipped | a model acting on a host checkout unchecked | CB2's containment (CB0); never `--dangerously-skip-permissions` on the host |
| fix-site gold in CB3 is narrower than ContextBench's | its coverage is not comparable to published numbers | internal use only; optional annotated gold |
| a public comparison against a best-paper project | tone and accuracy matter more than usual | a stated protocol, LEANN and ContextBench credited, measured claims only |

---

## 11. What this doc does not do

- No tack product code, and nothing changes tack's local-only rule.
- No ken ranking changes (DESIGN §12: not asked).
- No goinfer work; no aikit work until §6.3 is unparked.
- No PQ, no IVF, no HNSW in ken.
- No run of any kind. Every number marked derived or unmeasured stays that way until its step runs.

---

## Sources

- LEANN paper, arXiv 2506.08276v2: https://arxiv.org/html/2506.08276v2 (v1: https://arxiv.org/abs/2506.08276v1)
- MLSys 2026 proceedings entry:
  https://proceedings.mlsys.org/paper_files/paper/2026/hash/e27ea0cd50b798ff8942caf9203f0992-Abstract-Conference.html
- MLSys 2026 best research paper: https://mlsys.org/media/mlsys-2026/best-papers/Best-Research-Paper-LEANN.pdf
- LEANN repository at `c443fc4`: https://github.com/StarTrail-org/LEANN (formerly `yichuan-w/LEANN`): `README.md`
  ("Making Your Coding Agent Smarter"), `benchmarks/contextbench/` (README, `scripts/auto_run.py`,
  `scripts/prepare_repos_with_leann.py`, `scripts/batch_run_selected.py`, the two committed result files),
  `packages/leann-core/src/leann/{cli.py,mcp.py,api.py,chunking_utils.py}`; pull requests #87, #211, #318, #408, #445;
  commit `0463b76`
- ContextBench paper, arXiv 2602.05892: https://arxiv.org/html/2602.05892v3
- ContextBench repository at `cd5f62e`: https://github.com/anonymousUser2026/ContextBench (`Context-Bench/`:
  `contextbench/evaluate.py`, `contextbench/metrics/compute.py`, `contextbench/parsers/`, `docs/agents.md`,
  `requirements.txt`, `data/*.parquet`)
- ContextBench data: https://huggingface.co/datasets/Contextbench/ContextBench ; site: https://cioutn.github.io/context-bench/
- MarkTechPost, 2026-10-27: https://www.marktechpost.com/2026/10/27/leann-vector-index-mlsys-2026/ (not readable by
  automated fetch)
- ken at v1.11.0: `README.md`, `ARCHITECTURE.md`, `docs/BENCH.md`, `docs/DESIGN.md`, `docs/PERF-expectations.md`,
  `docs/internal/dense-retriever-adoption-2026-09.md`, `docs/internal/DECISIONS.md` (ADR-039, ADR-043, ADR-045,
  ADR-046), `engine/engine.go`, `internal/search/watch.go`
- aikit: `ann/hnsw.go`, `ann/flat_binary.go`, `ann/flat_binary_i8.go`, `ann/flat_binary_prefilter.go`,
  `docs/internal/perf-dead-ends.md`, `embed/embed_bench_test.go`, `CHANGELOG.md` (1.71.0)
- goinfer: `docs/tasks/task-memory-campaign-2026-10.md` (§1.2, MEM14, MEM17, §6), `docs/tasks/task-subsystems-2026-10.md`
- tack: `docs/design/DESIGN.md` (§0, §7, §12, §15, §17), `CLAUDE.md`, `docs/briefs/ken-k10-k15-structural.md`, the
  calibration records cited in §2.1, `internal/eval/pilot/ken.go`, `internal/eval/pilot/pilot.go`,
  `internal/config/config.go`, `internal/kensearch/kensearch.go`
- skiff: `docs/phase-0-results.md`
