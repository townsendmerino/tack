# ken v1.12.0 (K16–K18) Delivery & CB1 S53 Retrieval Record (2026-10-28)

**Status:** Complete (`VERDICT: PASS`)
**Source notes:** `~/tack-runs/NOTE-from-ken-2026-10-28-k16-k18.md` and `~/tack-runs/NOTE-from-ken-2026-10-28-cb1-s53.md` (`ken` `v1.12.0` tagged at `574b7d9`, `CB1 S53` recorded at `4deeb42`, ADR-049)
**Parent brief & research:** [`docs/briefs/ken-k16-k18-contextbench-memory.md`](../briefs/ken-k16-k18-contextbench-memory.md), [`docs/research/contextbench-leann-2026-10-28.md`](../research/contextbench-leann-2026-10-28.md) (§4–§6, §8)

---

## 1. Delivered Commits in `ken` (`36575ba..27d58b1`)

| Commit | Item | Summary |
|---|---|---|
| `6ff2098` | Step 0: bump `aikit` to `v1.74.0` | Unlocks BM25 postings arena (`-17.0%` to `-23.5%` BM25 memory) and zero-copy mmap model sharing. |
| `5c55d7e` | `K16`: Index memory composition (`-tags=bench`) | `internal/search/index_memory.go` and `index_memory_bench_test.go`; Tier-0 formulas verified within `< 5%` of single-component heap deltas on a 10k-chunk synthetic fixture; measured `tack`, `ken`, `aikit`, `goinfer`, and all 4 combined in one process (`K7`). Recorded in `ken`'s `docs/PERF-expectations.md`. |
| `75db4ac` | `K17`: Drop retained `f32` vectors from `WatchedIndex` | Replaced `WatchedIndex.vecs [][]float32` with pluggable `denseStore` (`flatStore`, `flatI8Store`, `flatBinaryI8Store`) in `internal/search/dense_store.go`, `watch.go`, `corpus.go`, and `index.go`; all 4 pre-registered red-first gates passed (`internal/search/k17_gates_test.go`, ADR-049). |
| `27d58b1` | `K18`: ContextBench retrieval harness (`bench/contextbench`) | Pure-Go `CB1` harness (`//go:build bench`) with arms `G`, `B`, `K`, `KT`, and external `L1`/`L2`, `cl100k_base` budget packer (`2048`, `8192`, `16384`), generic `traj_data` emitter, external `contextbench.evaluate` runner, and paired bootstrap 95% CIs (`10,000` resamples, seed `42`). |

---

## 2. K16: Index Memory Composition & K7 Multi-Workspace Verification

### 2.1 Synthetic 10k-Chunk Calibration Gate (Formula vs Heap Delta `< 5%`)

| Component | Formula Estimate | Measured Heap Delta | Error | Verdict |
|---|---:|---:|---:|:---:|
| Chunk Text | `3.62 MB` | `3.76 MB` | `3.78%` | **PASS (`< 5%`)** |
| BM25 Index | `3.56 MB` | `3.58 MB` | `0.45%` | **PASS (`< 5%`)** |
| Retained `f32` Vectors | `10.48 MB` | `10.49 MB` | `0.06%` | **PASS (`< 5%`)** |
| `FlatI8` Index | `2.60 MB` | `2.61 MB` | `0.21%` | **PASS (`< 5%`)** |
| `FlatBinaryI8` Index | `2.92 MB` | `2.93 MB` | `0.45%` | **PASS (`< 5%`)** |
| Structural Index | `304.8 KB` | `301.6 KB` | `1.04%` | **PASS (`< 5%`)** |

### 2.2 `aikit` Bump (`v1.70.1` $\to$ `v1.74.0`) BM25 Postings Arena Savings

| Corpus | Files | Chunks | BM25 (`v1.70.1`) | BM25 (`v1.74.0`) | BM25 Savings |
|---|---:|---:|---:|---:|---:|
| `tack` | 303 | 2,003 | `2.87 MB` | `2.20 MB` | **`-23.5%`** |
| `ken` | 465 | 4,276 | `6.11 MB` | `4.73 MB` | **`-22.6%`** |
| `aikit` | 1,104 | 8,582 | `11.84 MB` | `9.28 MB` | **`-21.6%`** |
| `goinfer` | 25,468 | 197,926 | `205.91 MB` | `170.96 MB` | **`-17.0%` (`-34.95 MB`)** |
| **All 4 Combined (1 process)** | **27,340** | **212,787** | **`226.73 MB`** | **`187.17 MB`** | **`-17.4%` (`-39.56 MB`)** |

### 2.3 Full Component Breakdown by Corpus (`aikit v1.74.0`, Pre-K17 Reference)

| Corpus | Chunks | Chunk Text | BM25 | Retained `f32` vecs | `FlatI8` | `FlatBinaryI8` | Structural | Model2Vec (`mmap`) | Model2Vec (`heap`) | Component Sum (`Flat`) | Peak RSS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `tack` | 2,003 | `2.27 MB` | `2.20 MB` | `2.00 MB` | `0.50 MB` | `0.56 MB` | `2.15 MB` | `3.90 MB` | `3.87 MB` | `12.51 MB` | `606.92 MB` |
| `ken` | 4,276 | `4.68 MB` | `4.73 MB` | `4.27 MB` | `1.06 MB` | `1.19 MB` | `3.48 MB` | `3.90 MB` | `3.90 MB` | `21.07 MB` | `960.17 MB` |
| `aikit` | 8,582 | `10.41 MB` | `9.28 MB` | `8.58 MB` | `2.13 MB` | `2.39 MB` | `7.01 MB` | `3.90 MB` | `3.90 MB` | `39.18 MB` | `1,263.75 MB` |
| `goinfer` | 197,926 | `300.48 MB` | `170.96 MB` | `197.82 MB` | `49.08 MB` | `55.12 MB` | `107.13 MB` | `3.87 MB` | `3.90 MB` | `780.25 MB` | `4,236.20 MB` |
| **All 4 (1 proc, `K7`)** | **212,787** | **`317.84 MB`** | **`187.17 MB`** | **`212.67 MB`** | **`52.77 MB`** | **`59.26 MB`** | **`119.77 MB`** | **`3.90 MB`** | **`15.57 MB`** | **`841.35 MB`** | **`5,622.61 MB`** |

- **`K7` confirmed:** Opening all 4 workspaces in one process via `engine.Open` with `MmapModel: true` shares a single `3.90 MB` page-cached mapping (`0` duplicate heap copies), compared to `15.57 MB` (`4x`) with `MmapModel: false`.

---

## 3. K17: Dropping Retained `f32` Vectors from `WatchedIndex` (ADR-049)

### 3.1 Four Pre-Registered Gates (`internal/search/k17_gates_test.go`, Proven Red First)

| Gate | Requirement | Measured Result | Verdict |
|---|---|---|---|
| **Gate 1: Recompute Bit-Identity** | Recomputed vector from `Chunk.Text` matches index-time vector bit-for-bit (`math.Float32bits`) | Bit-identical across all 256 dims on 100 synthetic + 4,276 real `ken` chunks | **PASS** |
| **Gate 2: Pipeline Ranking Parity** | Recall@10 and NDCG@10 on gold queries identical to 3 decimal places | Identical to 3 decimal places under `Flat` and `FlatI8` | **PASS** |
| **Gate 3: Watcher Republish Latency** | Single-file incremental edit republish `< 2,000 ms` | **`50.3 ms`** | **PASS** |
| **Gate 4: Dense Bytes/Chunk (`flat-i8`)** | Dense resident bytes/chunk $\le 300\text{ B}$ under `KEN_ANN=flat-i8` | **`260.01 B/chunk`** (down from `1,308.01 B/chunk`) | **PASS** |

### 3.2 Dense-Side Memory per Chunk (Pre-K17 vs Post-K17)

| Config | Dense Representation | Pre-K17 Bytes/Chunk | Post-K17 Bytes/Chunk | Delta | `goinfer` Savings (`198k` chunks) |
|---|---|---:|---:|---:|---:|
| Default (`Flat`) | `ann.Flat` (`float32`) | `1,048 B` | `1,024 B` | `-24 B` (`-2.3%`) | `-4.7 MB` |
| `KEN_ANN=flat-i8` | `ann.FlatI8` (`int8` + scales) | `1,308 B` | **`260 B`** | **`-1,048 B` (`-80.1%`)** | **`-207.4 MB`** |
| `KEN_ANN=flat-binary-i8` | `ann.FlatBinaryI8` (`1-bit` + `int8`) | `1,340 B` | **`292 B`** | **`-1,048 B` (`-78.2%`)** | **`-207.4 MB`** |

---

## 4. K18 & CB1 5-Task Go Pilot (`flipt-io/flipt`)

Run via `bench/contextbench/cmd/cb` against `~/tack-runs/cb0/` under the shared timing lock across the 5 `flipt-io/flipt` Go tasks in `S53`:

| Arm | Budget (tokens) | `LineCov` | `SymCov` | `FileCov` | `LinePrec` | `TokensToGold` | `Build (ms)` |
|---|---:|---:|---:|---:|---:|---:|---:|
| `G` (Grep) | 2,048 | `0.0029` | `0.0000` | `0.0154` | `0.0016` | `0.0` | `0.0` |
| `G` (Grep) | 8,192 | `0.0072` | `0.0000` | `0.0237` | `0.0044` | `4043.5` | `0.0` |
| `G` (Grep) | 16,384 | `0.0864` | `0.0528` | `0.1328` | `0.0290` | `9245.2` | `0.0` |
| `B` (BM25) | 2,048 | `0.0139` | `0.0235` | `0.0628` | `0.0323` | `529.0` | `253.8` |
| `B` (BM25) | 8,192 | `0.0458` | `0.0464` | `0.0785` | `0.0237` | `2493.7` | `253.8` |
| `B` (BM25) | 16,384 | `0.1137` | `0.2383` | `0.1488` | `0.0324` | `5498.8` | `253.8` |
| `K` (ken hybrid, regex) | 2,048 | `0.1266` | `0.2378` | `0.2851` | `0.2250` | `531.4` | `366.8` |
| `K` (ken hybrid, regex) | **8,192** | **`0.2947`** | **`0.4794`** | **`0.5967`** | **`0.1269`** | **`531.4`** | **`366.8`** |
| `K` (ken hybrid, regex) | 16,384 | `0.4054` | `0.6070` | `0.7128` | `0.0878` | `531.4` | `366.8` |
| `KT` (ken hybrid, tree-sitter) | 2,048 | `0.1438` | `0.2502` | `0.3046` | `0.2500` | `471.4` | `1243.2` |
| `KT` (ken hybrid, tree-sitter) | **8,192** | **`0.3050`** | **`0.5259`** | **`0.5772`** | **`0.1267`** | **`471.4`** | **`1243.2`** |
| `KT` (ken hybrid, tree-sitter) | 16,384 | `0.4259` | `0.6063` | `0.7350` | `0.0904` | `471.4` | `1243.2` |

### Paired Bootstrap Differences (`B = 8,192` tokens, `10,000` resamples, seed `42`)

| Comparison | Metric | Budget | Mean Delta (`A - B`) | 95% Bootstrap CI | `N` Tasks |
|---|---|---:|---:|---|---:|
| `K - B` | `line_coverage` | `8,192` | **`+0.2488`** | **`[+0.1019, +0.3958]`** | 5 |
| `KT - K` | `line_coverage` | `8,192` | `+0.0103` | `[-0.0361, +0.0661]` | 5 |
| `K - G` | `line_coverage` | `8,192` | **`+0.2875`** | **`[+0.1255, +0.4363]`** | 5 |

---

## 5. CB1 Full `S53` Retrieval Sweep (`N = 53` tasks, `ken v1.12.0` at `574b7d9`)

Evaluated across all 53 tasks in `S53` (`~/tack-runs/cb1_s53_results.jsonl`, `636` scored records; documented in `ken` `docs/BENCH.md` at `4deeb42`) under the shared timing lock:

### 5.1 Overall Results (`N = 53` tasks)

| Arm | Budget (tokens) | `LineCov` | `SymCov` | `FileCov` | `LinePrec` | `TokensToGold` | `Build (ms)` |
|---|---:|---:|---:|---:|---:|---:|---:|
| `G` (Grep) | 2,048 | `0.0067` | `0.0000` | `0.0244` | `0.0007` | `0.0` | `0.0` |
| `G` (Grep) | 8,192 | `0.0071` | `0.0000` | `0.0251` | `0.0010` | `898.6` | `0.0` |
| `G` (Grep) | 16,384 | `0.0200` | `0.0087` | `0.0400` | `0.0053` | `5140.5` | `0.0` |
| `B` (BM25) | 2,048 | `0.0780` | `0.0967` | `0.2152` | `0.1244` | `537.6` | `1231.3` |
| `B` (BM25) | 8,192 | `0.1406` | `0.1565` | `0.2853` | `0.0649` | `1603.2` | `1231.3` |
| `B` (BM25) | 16,384 | `0.1863` | `0.1996` | `0.3400` | `0.0443` | `2541.8` | `1231.3` |
| `K` (ken hybrid, regex) | 2,048 | `0.0892` | `0.1262` | `0.2890` | `0.1337` | `487.6` | `1497.8` |
| `K` (ken hybrid, regex) | **8,192** | **`0.1686`** | **`0.2124`** | **`0.5209`** | **`0.0700`** | **`960.8`** | **`1497.8`** |
| `K` (ken hybrid, regex) | 16,384 | **`0.2305`** | `0.2833` | **`0.6186`** | `0.0467` | `1797.5` | `1497.8` |
| `KT` (ken hybrid, tree-sitter) | 2,048 | `0.0881` | `0.1545` | `0.2823` | `0.1319` | `408.0` | `2977.9` |
| `KT` (ken hybrid, tree-sitter) | **8,192** | **`0.1653`** | **`0.2539`** | **`0.5153`** | **`0.0691`** | **`924.6`** | **`2977.9`** |
| `KT` (ken hybrid, tree-sitter) | 16,384 | `0.2054` | **`0.2958`** | `0.5925` | `0.0437` | **`1144.3`** | `2977.9` |

### 5.2 Paired Bootstrap Differences on `S53` (`B = 8,192` tokens, `10,000` resamples, seed `42`)

| Comparison | Metric | Budget | Mean Delta (`A - B`) | 95% Bootstrap CI | `N` Tasks |
|---|---|---:|---:|---|---:|
| `K - B` | `line_coverage` | `8,192` | **`+0.0280`** | `[-0.0225, +0.0782]` | 53 |
| `KT - K` | `line_coverage` | `8,192` | `-0.0033` | `[-0.0198, +0.0113]` | 53 |
| `K - G` | `line_coverage` | `8,192` | **`+0.1615`** | **`[+0.1182, +0.2074]`** | 53 |

### 5.3 Per-Language Breakdown at `8,192` Tokens (`LineCov / FileCov / SymCov`)

| Language | Tasks | `G` (Grep) | `B` (BM25) | `K` (ken hybrid, regex) | `KT` (ken hybrid, tree-sitter) |
|---|---:|---|---|---|---|
| **Go** | 6 | `0.0060 / 0.0198 / 0.0000` | `0.0879 / 0.1109 / 0.0784` | **`0.3036` / `0.5578` / `0.4472`** | **`0.3125` / `0.5568` / `0.5096`** |
| **JavaScript / TS** | 4 | `0.0012 / 0.0139 / 0.0000` | `0.0030 / 0.0723 / 0.0000` | **`0.0990` / `0.3088` / `0.0639`** | **`0.1061` / `0.2861` / `0.1164`** |
| **Python** | 43 | `0.0078 / 0.0269 / 0.0000` | `0.1607 / 0.3295 / 0.1819` | `0.1562 /` **`0.5354`** `/ 0.1935` | `0.1503 / 0.5309 /` **`0.2310`** |

