# E1 CB3: trajectory context metrics replayed over all captured runs, 2026-10-28

<!-- citations-at: f75780d -->

CB3's replay (`e1-cb3-context-metrics-preregistration-2026-10-28.md`), registered before running the scorer over `~/tack-runs/e1/`. **Zero new model calls:** every figure below comes from replaying the 687 non-void captured E1 runs on disk against `BaseTree` and `FixGold` (`internal/eval/pilot/context.go:FixGold`, `internal/eval/pilot/context.go:ScoreContext`, `tools/replaycontext`).

**Headline decomposition of the 640 failing runs:**
- **452 of 640 failing runs (70.6%) never saw a single line of the gold symbol** (`FirstGoldReq == 0`, mean `FinalCov = 0.170`, `FileCov = 0.316`).
- **123 of 640 failing runs (19.2%) reached the gold symbol but never applied a successful edit inside it** (`FirstGoldReq > 0` and `EditLoc == 0`).
- **65 of 640 failing runs (10.2%) reached and edited the gold symbol** (`EditLoc > 0`) and still failed the oracle tests.
- **All 47 passing runs (100%) reached the gold file and symbol** (`FileCov = 1.000`, `RawCov = 1.000`, `FinalCov = 0.964`, `EditLoc = 0.979`, mean `FirstGoldReq = 2.34`, `AUCCov = 0.794`).

---

## 1. What ran

- **Code:** `internal/eval/pilot/context.go` (`FixGold`, `ScoreContext`, `MergeSpans`) and `tools/replaycontext/main.go`.
- **Command:**
  ```
  go run ./tools/replaycontext -mirrors ~/tack-runs/e1/mirrors -scan ~/tack-runs/e1 -dump /tmp/e1-cb3-context-metrics.jsonl
  ```
- **Runs scored:** 687 non-void runs across 22 distinct `(repo, commit)` tasks (the 10 pilot tasks and the 12 calibration tasks; tasks 3 and 7 of the pilot are tasks 11 and 12 of calibration): 47 passing, 640 failing, 0 missing (`VERDICT: PASS`).

---

## 2. `FixGold` vs `RawGold` across E1 tasks

For each task, `RawGold` is the non-test `.go` diff hunks at `base` (`internal/eval/mine/static.go:isNonTestGo`), and `FixGold` widens each hunk's boundary lines to their innermost enclosing function, method, or type in `ken` v1.11.0's tree-sitter index at `base` (`internal/kensearch/symbols.go:SymbolAt`).

### 2.1 Calibration tasks (12 tasks)

| task | repo | commit | raw lines | `FixGold` lines | `FixGold` spans (`base` coordinates) |
|---:|---|---|---:|---:|---|
| 1 | `goinfer` | `490f760b` | 3 | 30 | `internal/loadflags/loadflags.go:55-82`, `internal/loadflags/loadflags.go:316-317` |
| 2 | `goinfer` | `c7ac10e9` | 2 | 155 | `internal/serveapp/banner.go:128-282` |
| 3 | `goinfer` | `4dd9fd82` | 4 | 161 | `internal/prequant/prequant.go:68-142`, `internal/prequant/prequant.go:150-233`, `internal/prequant/prequant.go:453-454` |
| 4 | `goinfer` | `bbb19952` | 2 | 42 | `pull/pull.go:369-410` |
| 5 | `goinfer` | `09ff5ec4` | 7 | 73 | `cmd/serve/openai.go:7-8`, `cmd/serve/openai.go:474-542`, `cmd/serve/openai.go:555-556` |
| 6 | `goinfer` | `37e72a71` | 1 | 25 | `internal/serveapp/banner.go:73-97` |
| 7 | `ken` | `3961c347` | 10 | 144 | `internal/sql/parser.go:46-189` |
| 8 | `goinfer` | `866a7ff0` | 3 | 36 | `chat/templates.go:116-151` |
| 9 | `goinfer` | `1bfe3117` | 2 | 44 | `internal/serveapp/tower_reserve.go:53-95`, `internal/serveapp/tower_reserve.go:152-152` |
| 10 | `ken` | `4aed5e90` | 2 | 134 | `cmd/ken/build_index.go:31-164` |
| 11 | `ken` | `1416be9f` | 1 | 9 | `mcp/server.go:74-82` |
| 12 | `aikit` | `4c062ede` | 2 | 26 | `ann/flat_i8_persist.go:97-122` |

### 2.2 Pilot-only tasks (10 tasks)

| task | repo | commit | raw lines | `FixGold` lines | `FixGold` spans (`base` coordinates) |
|---:|---|---|---:|---:|---|
| 1 | `aikit` | `8419cf34` | 2 | 4 | `linalg/weightmat.go:50-53` |
| 2 | `ken` | `3aa62a29` | 7 | 160 | `internal/structural/extract_file.go:4-5`, `internal/structural/extract_file.go:32-39`, `internal/structural/index.go:468-617` |
| 3 | `aikit` | `b2a3cb91` | 5 | 5 | `ann/flat_i8_persist.go:4-6`, `ann/flat_i8_persist.go:68-69` |
| 4 | `goinfer` | `6b4ef501` | 7 | 16 | `internal/serveapp/tower_reserve.go:163-178` |
| 5 | `goinfer` | `8d5cd708` | 7 | 225 | `internal/loadflags/loadflags.go:297-297`, `internal/serveapp/banner.go:24-72`, `internal/serveapp/banner.go:74-103`, `internal/serveapp/banner.go:114-258` |
| 6 | `ken` | `5c185feb` | 7 | 41 | `internal/structural/extract_file.go:74-104`, `internal/structural/index.go:410-412`, `internal/structural/index.go:414-420` |
| 7 | `ken` | `376e6096` | 2 | 2 | `mcp/log.go:20-21` |
| 9 | `aikit` | `38b97c97` | 11 | 83 | `vision/gemma4_encoder.go:367-399`, `vision/preprocess.go:29-34`, `vision/preprocess.go:38-45`, `vision/preprocess.go:58-93` |
| 10 | `goinfer` | `a34ffaa5` | 19 | 50 | `decoder/serialize.go:302-351` |
| 11 | `goinfer` | `e9b76046` | 2 | 2 | `constrain/constrain.go:105-106` |

---

## 3. Pass vs fail across all 687 runs

| group | runs | reached gold | edited gold | `FinalCov` | `RawCov` | `FileCov` | `CovReq1` | `Cov4k` | `Precision` | `FirstReq` | `AUCCov` | `Util` | `EditLoc` |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| **passing runs** | 47 | 47/47 (100%) | 46/47 (97.9%) | 0.964 | 1.000 | 1.000 | 0.388 | 0.612 | 0.164 | 2.34 | 0.794 | 0.076 | 0.979 |
| **failing runs** | 640 | 188/640 (29.4%) | 65/640 (10.2%) | 0.170 | 0.223 | 0.316 | 0.007 | 0.096 | 0.561 | 3.75 | 0.124 | 0.016 | 0.092 |

*(Note: `Precision` is defined as `1.0` when `|S_final| == 0`, so runs that quit immediately without reading any file lines inflate mean `Precision` on failing runs.)*

---

## 4. Per-task bottleneck diagnosis across the 12 calibration tasks (667 calibration runs)

Grouping all 667 calibration runs by task separates the 12 tasks into four distinct regimes:

| task | repo | gold symbol / file | runs | pass | reached gold | edited gold | `FinalCov` | `RawCov` | `FileCov` | `FirstReq` | bottleneck regime |
|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 1 | `goinfer` | `loadflags.go:55-82, 316-317` | 57 | 15 | 19/57 (33.3%) | 17/57 (29.8%) | 0.333 | 0.333 | 0.351 | 4.16 | **Retrieval-gated, solved when found:** 15 of the 19 runs that found the gold symbol passed (78.9%), vs 0/38 when not found. |
| 2 | `goinfer` | `banner.go:128-282` | 69 | 3 | 39/69 (56.5%) | 17/69 (24.6%) | 0.366 | 0.471 | 0.580 | 2.77 | **Mixed read-to-edit & edit logic:** 56.5% reach `banner.go`, fewer than half apply an edit, 3 pass. |
| 3 | `goinfer` | `prequant.go:68-233, 453-454` | 55 | 0 | 11/55 (20.0%) | 2/55 (3.6%) | 0.104 | 0.114 | 0.236 | 6.00 | **Retrieval + read-to-edit drop:** 80% never reach `prequant.go`'s target functions; 9 of 11 that do never edit them. |
| 4 | `goinfer` | `pull/pull.go:369-410` | 66 | 0 | 3/66 (4.5%) | 1/66 (1.5%) | 0.029 | 0.045 | 0.212 | 15.67 | **Retrieval-bound:** 21% open `pull.go` (usually the top 200 lines), only 4.5% ever reach lines 369–410. |
| 5 | `goinfer` | `cmd/serve/openai.go:474-542` | 62 | 0 | 26/62 (41.9%) | 21/62 (33.9%) | 0.254 | 0.281 | 0.210 | 6.19 | **Edit-logic-bound:** 21 of 26 runs that reach the symbol edit it, and all 21 fail the oracle test. |
| 6 | `goinfer` | `banner.go:73-97` | 75 | 0 | 32/75 (42.7%) | 1/75 (1.3%) | 0.105 | 0.427 | 0.427 | 3.22 | **Read-to-edit drop:** 32 of 75 runs read the exact hunk line (`RawCov = 0.427`), yet 31 of 32 fail to apply a valid edit to it. |
| 7 | `ken` | `internal/sql/parser.go:46-189` | 50 | 0 | 1/50 (2.0%) | 0/50 (0.0%) | 0.010 | 0.010 | 0.140 | 22.00 | **Retrieval-bound:** 49 of 50 runs never reach `parser.go:46-189`. |
| 8 | `goinfer` | `chat/templates.go:116-151` | 49 | 0 | 0/49 (0.0%) | 0/49 (0.0%) | 0.000 | 0.000 | 0.020 | — | **Retrieval-bound:** 0 of 49 runs ever reach `chat/templates.go:116-151`. |
| 9 | `goinfer` | `tower_reserve.go:53-95, 152` | 49 | 0 | 1/49 (2.0%) | 0/49 (0.0%) | 0.020 | 0.020 | 0.020 | 9.00 | **Retrieval-bound (early quit):** 48 of 49 runs quit before reaching `tower_reserve.go`. |
| 10 | `ken` | `cmd/ken/build_index.go:31-164` | 45 | 0 | 29/45 (64.4%) | 7/45 (15.6%) | 0.146 | 0.178 | 0.644 | 1.59 | **Read-to-edit drop:** 64.4% reach `build_index.go` in ~1.6 requests, but most read only a small slice (`FinalCov = 0.146`) and only 7/45 edit it. |
| 11 | `ken` | `mcp/server.go:74-82` | 45 | 27 | 44/45 (97.8%) | 37/45 (82.2%) | 0.951 | 0.933 | 1.000 | 1.27 | **Solved:** 97.8% reach the 9-line symbol at request 1.27; 27 of 37 edits pass. |
| 12 | `aikit` | `ann/flat_i8_persist.go:97-122` | 45 | 2 | 24/45 (53.3%) | 6/45 (13.3%) | 0.533 | 0.533 | 0.578 | 4.17 | **Read-to-edit + edit-logic drop:** 53.3% read the full symbol (`FinalCov = 0.533`), 6/45 edit it, 2 pass. |

---

## 5. Job-by-job context metrics across E1 calibration and C6 screens

| job | arm | runs | pass | reached | edited gold | `FinalCov` | `RawCov` | `FileCov` | `CovReq1` | `Cov4k` | `FirstReq` | `AUCCov` | `Util` | `EditLoc` |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `a1-2026-10-18-day` (pilot 1–6) | `A1` | 6 | 0 | 1/6 | 1/6 | 0.167 | 0.167 | 0.333 | 0.000 | 0.167 | 3.00 | 0.130 | 0.083 | 0.056 |
| `a1-2026-10-18-day2` (pilot 1–11) | `A1` | 10 | 0 | 5/10 | 1/10 | 0.276 | 0.303 | 0.600 | 0.000 | 0.276 | 3.00 | 0.194 | 0.010 | 0.100 |
| `cal-baseline` (Qwen2.5-7B, passes 1–3) | `A1` | 36 | 0 | 8/36 | 2/36 | 0.107 | 0.083 | 0.250 | 0.009 | 0.051 | 5.00 | 0.054 | 0.008 | 0.044 |
| `c4` (Qwen3-4B, passes 1–3) | `A1` | 36 | 0 | 6/36 | 0/36 | 0.084 | 0.083 | 0.222 | 0.009 | 0.084 | 1.50 | 0.083 | 0.000 | 0.000 |
| `c5` (Qwen3-8B, passes 1–3) | `A1` | 36 | 4 | 11/36 | 6/36 | 0.205 | 0.246 | 0.278 | 0.059 | 0.129 | 2.73 | 0.158 | 0.038 | 0.156 |
| `c6-46` (`fit_window`: A=off, B=on) | `A` | 36 | 1 | 11/36 | 2/36 | 0.167 | 0.194 | 0.306 | 0.033 | 0.098 | 2.09 | 0.143 | 0.006 | 0.056 |
| `c6-46` (`fit_window`: A=off, B=on) | `B` | 36 | 2 | 11/36 | 8/36 | 0.263 | 0.298 | 0.278 | 0.028 | 0.214 | 4.09 | 0.200 | 0.038 | 0.203 |
| `c6-44` (`result_notes`: A=off, B=on) | `A` | 36 | 2 | 8/36 | 3/36 | 0.140 | 0.139 | 0.250 | 0.031 | 0.088 | 2.25 | 0.119 | 0.007 | 0.083 |
| `c6-44` (`result_notes`: A=off, B=on) | `B` | 36 | 3 | 15/36 | 8/36 | 0.315 | 0.315 | 0.458 | 0.001 | 0.178 | 5.53 | 0.203 | 0.039 | 0.212 |
| `c6-41` (`edit_messages`: `runs-resume2`) | `A` | 21 | 2 | 7/21 | 4/21 | 0.233 | 0.238 | 0.333 | 0.054 | 0.191 | 3.29 | 0.183 | 0.018 | 0.190 |
| `c6-41` (`edit_messages`: `runs-resume2`) | `B` | 21 | 2 | 9/21 | 3/21 | 0.287 | 0.286 | 0.429 | 0.001 | 0.239 | 2.89 | 0.222 | 0.016 | 0.143 |
| `c6-48` (`instructions`: A=none, B=full) | `A` | 36 | 2 | 9/36 | 6/36 | 0.217 | 0.222 | 0.250 | 0.007 | 0.162 | 1.89 | 0.178 | 0.011 | 0.167 |
| `c6-48` (`instructions`: A=none, B=full) | `B` | 36 | 4 | 19/36 | 9/36 | 0.400 | 0.409 | 0.569 | 0.087 | 0.160 | 3.74 | 0.298 | 0.040 | 0.231 |
| `c6-42` (`verify_done`: `runs-restart`) | `A` | 36 | 6 | 14/36 | 8/36 | 0.293 | 0.389 | 0.417 | 0.084 | 0.150 | 2.79 | 0.226 | 0.040 | 0.199 |
| `c6-42` (`verify_done`: `runs-restart`) | `B` | 36 | 5 | 16/36 | 11/36 | 0.310 | 0.425 | 0.431 | 0.084 | 0.121 | 3.25 | 0.239 | 0.016 | 0.303 |
| `c6-4b-bundle` (Qwen3-4B + bundle) | `A1` | 12 | 0 | 2/12 | 0/12 | 0.001 | 0.000 | 0.417 | 0.000 | 0.001 | 5.00 | 0.001 | 0.000 | 0.000 |
| `c6-build` (`build_after_edit`: A=off, B=on) | `A` | 36 | 5 | 18/36 | 10/36 | 0.336 | 0.460 | 0.486 | 0.084 | 0.212 | 3.28 | 0.257 | 0.013 | 0.278 |
| `c6-build` (`build_after_edit`: A=off, B=on) | `B` | 36 | 4 | 18/36 | 10/36 | 0.429 | 0.485 | 0.500 | 0.084 | 0.181 | 4.39 | 0.334 | 0.042 | 0.249 |
| `c6-t1/edit-lines-qwen3-8b` | `A1` | 6 | 1 | 5/6 | 2/6 | 0.453 | 0.833 | 1.000 | 0.002 | 0.055 | 4.60 | 0.286 | 0.007 | 0.333 |
| `c6-t1/restate-qwen3-4b` | `A1` | 12 | 0 | 1/12 | 0/12 | 0.001 | 0.000 | 0.250 | 0.000 | 0.001 | 2.00 | 0.000 | 0.000 | 0.000 |
| `c6-cursym` (tasks 2, 5, 12) | `A1` | 9 | 3 | 9/9 | 4/9 | 0.555 | 0.889 | 0.833 | 0.000 | 0.111 | 4.89 | 0.368 | 0.014 | 0.444 |
| `c6-cursym2` (tasks 2, 5, 6, 12) | `A1` | 24 | 0 | 23/24 | 12/24 | 0.403 | 0.821 | 0.812 | 0.004 | 0.306 | 3.04 | 0.307 | 0.060 | 0.464 |

---

## 6. What CB3 shows

1. **The C6 bundle's mechanism is visible in trajectory coverage even when binary pass differences are 1–2 runs:**
   - **4.4 (`result_notes`):** `reached` nearly doubles from `8/36` to `15/36`, `FinalCov` more than doubles from `0.140` to `0.315`, and `edited_gold` rises from `3/36` to `8/36`. When an initial search or grep returns nothing, the note steers the model into a wider search that reaches the gold symbol.
   - **4.8 (`instructions`):** `reached` more than doubles from `9/36` (no instruction file) to `19/36` (with the repository's instruction file), `FileCov` rises from `0.250` to `0.569`, and `CovReq1` rises from `0.007` to `0.087`. The repo's architecture map in `CLAUDE.md`/`AGENTS.md` directs the first tool calls to the right file.
   - **4.6 (`fit_window`):** `FinalCov` rises from `0.167` to `0.263`, `Cov4k` doubles from `0.098` to `0.214`, and `edited_gold` rises from `2/36` to `8/36`.
   - **4.2 (`verify_done`):** `edited_gold` rises from `8/36` (`EditLoc = 0.199`) to `11/36` (`EditLoc = 0.303`).
   - Together, moving from unbundled Qwen3-8B (`C5`: `reached = 11/36`, `edited_gold = 6/36`, `FinalCov = 0.205`, `RawCov = 0.246`) to bundled Qwen3-8B (`c6-build` A: `reached = 18/36`, `edited_gold = 10/36`, `FinalCov = 0.336`, `RawCov = 0.460`) increases gold-symbol reach from `30.6%` to `50.0%` and raw fix-line coverage from `0.246` to `0.460`.
2. **Where `ken`'s structural and semantic tools (`search`, `symbol`) have the largest headroom:**
   - All calibration runs through C6 ran **without `[ken]`** (`grep`, `glob`, `read`, `edit`, `write`, `bash` only).
   - Across Tasks 1, 3, 4, 7, 8, and 9 (`326` calibration runs), `grep`/`glob`/`read` failed to reach the gold symbol in **`291 of 326` runs (`89.3%`)** — and on Task 1, `15 of the 19` runs that *did* reach `loadflags.go:55-82` passed (`78.9%`).
   - Meanwhile, on Task 4 (`pull/pull.go:369-410`), `21.2%` of runs opened `pull/pull.go` (`FileCov = 0.212`), but because `read` caps un-offset reads at `200` lines, only `4.5%` ever reached lines `369–410` (`symbol` by name jumps directly to the function's lines).
3. **Where edit ergonomics and model reasoning remain the bottleneck after retrieval:**
   - On Task 6 (`banner.go:73-97`) and Task 10 (`build_index.go:31-164`), `42.7%` and `64.4%` of runs reached the gold symbol, but only `1.3%` and `15.6%` applied an edit inside it (`edit` by `symbol` in `ken` v1.11.0 addresses whole-symbol replacement on functions under 200 lines).
   - On Task 5 (`cmd/serve/openai.go:474-542`), `21 of 26` runs that reached the symbol applied an edit inside it (`EditLoc`), yet `0 of 21` passed the oracle test — a pure reasoning/patch-logic bottleneck rather than retrieval or edit mechanics.

