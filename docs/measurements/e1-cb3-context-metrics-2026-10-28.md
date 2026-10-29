# E1 CB3: trajectory context metrics replayed over captured runs, 2026-10-28

<!-- citations-at: f75780d -->

CB3's replay, scored under the CB3 specification `e1-cb3-context-metrics-preregistration-2026-10-28.md` (titled and treated as a specification, not a pre-registration). **Ordering, revision & approval note:** The specification `e1-cb3-context-metrics-preregistration-2026-10-28.md`, the scorer (`internal/eval/pilot/context.go`, `tools/replaycontext/main.go`), and the initial version of this record were committed together in `57c9548` (`2026-10-28 14:45:58 -0700`, after `/tmp/e1-cb3-context-metrics.jsonl` was written at `14:37`), so git history does not establish that the specification was frozen prior to the first replay run, and the specification was revised after the initial results on 2026-10-29 (`8076b0c`) to address review findings (§3.1 failed-call token exclusion and `-include-notes` on failed edits carrying `current_symbol` notes, §3.2 removal of the unused `opencode` branch, §4 aggregate `Precision` over `SeenLines > 0`, and §4.1 `-scan` exclusions); additionally, Francis's approval of `docs/research/contextbench-leann-2026-10-28.md` §9 decisions 1–9 is not recorded in a separate commit or note in the repository.

**Zero new model calls:** every figure below comes from replaying the **625 counted E1 runs** on disk (`10` pilot + `615` calibration) against `BaseTree` and `FixGold` (`internal/eval/pilot/context.go:FixGold`, `internal/eval/pilot/context.go:ScoreContext`, `tools/replaycontext`).

**Headline decomposition of the 578 counted failing runs (out of 625 counted runs: 47 pass, 578 fail):**
- Across all **578 failing runs**, mean `FinalCov = 0.185`, `RawCov = 0.243`, and `FileCov = 0.341` (`186/578 = 32.2%` reached the gold symbol; `64/578 = 11.1%` edited inside it).
- **392 of 578 failing runs (67.8%) never saw a single line of the gold symbol** (`FirstGoldReq == 0`, which has `FinalCov = 0.000` and `RawCov = 0.000` by definition, and `FileCov = 0.061` from reading non-gold lines in the gold file).
- **122 of 578 failing runs (21.1%) reached the gold symbol but never applied a successful edit inside it** (`FirstGoldReq > 0` and `EditLoc == 0`; mean `FinalCov = 0.464`, `RawCov = 0.702`, `FileCov = 0.980`, `FirstReq = 3.61`).
- **64 of 578 failing runs (11.1%) reached and edited the gold symbol** (`EditLoc > 0`; mean `FinalCov = 0.784`, `RawCov = 0.860`, `FileCov = 0.836`, `EditLoc = 0.916`) and still failed the oracle tests.
- **All 47 passing runs (100%) reached the gold file and symbol** (`FileCov = 1.000`, `RawCov = 1.000`, `FinalCov = 0.964`, `EditLoc = 0.979`, mean `FirstGoldReq = 2.34`, `AUCCov = 0.794`).

*(When the 62 void, preflight-smoke, and superseded failing runs on disk are also included via `-include-superseded`, total runs are `687` (`47` pass, `640` fail): `452/640` (`70.6%`) have `FirstGoldReq == 0` (`FinalCov = 0.000`), `123/640` (`19.2%`) have `FirstGoldReq > 0 && EditLoc == 0` (`FinalCov = 0.469`), and `65/640` (`10.2%`) have `EditLoc > 0` (`FinalCov = 0.788`), with overall failing mean `FinalCov = 0.170` and `FileCov = 0.316`; see §1.1.)*

---

## 1. What ran

- **Code:** `internal/eval/pilot/context.go` (`FixGold`, `ScoreContext`, `MergeSpans`) and `tools/replaycontext/main.go`.
- **Command:**
  ```
  go run ./tools/replaycontext -mirrors ~/tack-runs/e1/mirrors -scan ~/tack-runs/e1
  ```
- **Runs scored:** **625 counted runs** (`10` pilot runs in `a1-2026-10-18-day2` + `615` calibration runs across 14 calibration/C6 jobs) across **22 completely disjoint `(repo, commit)` tasks** (`10` pilot tasks in `eval/e1/pilot.jsonl`, tasks 1–7 and 9–11 where task 8 was dropped before the pilot, and `12` calibration tasks in `eval/e1/calibration.jsonl`, tasks 1–12; zero commits overlap between the pilot and calibration suites): **47 passing, 578 failing, 0 missing (`VERDICT: PASS`)**.

### 1.1 Exclusions in `-scan` (625 counted runs vs 687 with `-include-superseded`)

By default, `tools/replaycontext -scan ~/tack-runs/e1` excludes runs that prior E1 measurement records marked void, ungraded preflight smoke, or superseded, as well as post-CB3 runs:

1. **Always excluded in `-scan`:**
   - Any record with `rec.Run.Void != ""`.
   - `a1-2026-10-18/` (`10` runs): void pilot run where the sandbox denied workspace writes (`e1-pilot-a1-2026-10-18.md`).
   - `cal-c4c5-2026-10-18/c4/pass-1-aborted-0654/` (`1` run): void aborted launch (`e1-calibration-c6-45-amended-replay-2026-10-20.md`).
   - `cal-baseline-2026-10-18/pass-3/results.jsonl` tasks `5..12` (`8` runs): void tasks replaced by `pass-3-rerun/` (`e1-calibration-baseline-2026-10-18.md`).
   - `c6-bundle-2026-10-28/`: post-CB3 job started after CB3.
2. **Excluded by default in `-scan` (`62` failing runs; included when `-include-superseded` is passed for `687` total runs):**
   - `a1-2026-10-18-day/` (`6` runs): void pilot capture where the sandbox denied the Go module cache, replaced by `a1-2026-10-18-day2/` (`e1-pilot-a1-2026-10-18.md`).
   - `a1-smoke-*` (`4` runs) and `*/smoke-t1/*` (`3` runs): 1-task preflight smoke runs ("not graded").
   - `c6-41-2026-10-20/runs` task `4` (`5` runs) and `c6-41-2026-10-20/runs-resume` task `6` (`3` runs): `8` runs from incomplete interrupted task blocks superseded by `runs-resume/` (tasks 4–5) and `runs-resume2/` (tasks 6–12), marked "recorded and not counted" under the §12.10 completeness rule (`e1-calibration-c6-41-2026-10-20.md`). Meanwhile, the `30` complete runs in `c6-41-2026-10-20/runs` (tasks 1–3, `18` runs) and `runs-resume` (tasks 4–5, `12` runs) are combined with `runs-resume2` (tasks 6–12, `42` runs) into the `72` counted runs of `c6-41-2026-10-20` (`36 A`, `36 B`).
   - `c6-42-2026-10-20/runs/` (`41` runs): stopped screen before the `instructions_head` fix, superseded by `c6-42-2026-10-20/runs-restart/` (`72` runs) and marked "recorded, not counted" (`e1-calibration-c6-42-2026-10-20.md`, `e1-calibration-c6-42-stopped-2026-10-20.md`).

---

## 2. `FixGold` vs `RawGold` across E1 tasks

For each task, `RawGold` is the non-test `.go` diff hunks at `base` (`internal/eval/mine/static.go:isNonTestGo`), and `FixGold` widens each hunk's boundary lines to their innermost enclosing function, method, or type in `ken` v1.11.0's tree-sitter index at `base` (`internal/kensearch/symbols.go:SymbolAt`).

### 2.1 Calibration tasks (12 tasks, `eval/e1/calibration.jsonl`)

| task | repo | commit | raw lines | `FixGold` lines | `FixGold` spans (`base` coordinates) |
|---:|---|---|---:|---:|---|
| 1 | `goinfer` | `490f760b` | 3 | 30 | `internal/loadflags/loadflags.go:55-82`, `internal/loadflags/loadflags.go:316-317` |
| 2 | `goinfer` | `c7ac10e9` | 2 | 155 | `internal/serveapp/banner.go:128-282` |
| 3 | `goinfer` | `4dd9fd82` | 4 | 161 | `internal/prequant/prequant.go:68-142`, `internal/prequant/prequant.go:150-233`, `internal/prequant/prequant.go:453-454` |
| 4 | `goinfer` | `bbb19952` | 2 | 42 | `pull/pull.go:369-410` |
| 5 | `goinfer` | `8d5cd708` | 7 | 225 | `internal/loadflags/loadflags.go:297-297`, `internal/serveapp/banner.go:24-72`, `internal/serveapp/banner.go:74-103`, `internal/serveapp/banner.go:114-258` |
| 6 | `goinfer` | `37e72a71` | 1 | 25 | `internal/serveapp/banner.go:73-97` |
| 7 | `ken` | `376e6096` | 2 | 2 | `mcp/log.go:20-21` |
| 8 | `goinfer` | `866a7ff0` | 3 | 36 | `chat/templates.go:116-151` |
| 9 | `goinfer` | `1bfe3117` | 2 | 44 | `internal/serveapp/tower_reserve.go:53-95`, `internal/serveapp/tower_reserve.go:152-152` |
| 10 | `ken` | `4aed5e90` | 2 | 134 | `cmd/ken/build_index.go:31-164` |
| 11 | `ken` | `1416be9f` | 1 | 9 | `mcp/server.go:74-82` |
| 12 | `aikit` | `4c062ede` | 2 | 26 | `ann/flat_i8_persist.go:97-122` |

### 2.2 Pilot tasks (10 tasks, `eval/e1/pilot.jsonl`)

| task | repo | commit | raw lines | `FixGold` lines | `FixGold` spans (`base` coordinates) |
|---:|---|---|---:|---:|---|
| 1 | `aikit` | `8419cf34` | 2 | 4 | `linalg/weightmat.go:50-53` |
| 2 | `ken` | `3aa62a29` | 7 | 160 | `internal/structural/extract_file.go:4-5`, `internal/structural/extract_file.go:32-39`, `internal/structural/index.go:468-617` |
| 3 | `aikit` | `b2a3cb91` | 5 | 5 | `ann/flat_i8_persist.go:4-6`, `ann/flat_i8_persist.go:68-69` |
| 4 | `goinfer` | `6b4ef501` | 7 | 16 | `internal/serveapp/tower_reserve.go:163-178` |
| 5 | `goinfer` | `09ff5ec4` | 7 | 73 | `cmd/serve/openai.go:7-8`, `cmd/serve/openai.go:474-542`, `cmd/serve/openai.go:555-556` |
| 6 | `ken` | `5c185feb` | 7 | 41 | `internal/structural/extract_file.go:74-104`, `internal/structural/index.go:410-412`, `internal/structural/index.go:414-420` |
| 7 | `ken` | `3961c347` | 10 | 144 | `internal/sql/parser.go:46-189` |
| 9 | `aikit` | `38b97c97` | 11 | 83 | `vision/gemma4_encoder.go:367-399`, `vision/preprocess.go:29-34`, `vision/preprocess.go:38-45`, `vision/preprocess.go:58-93` |
| 10 | `goinfer` | `a34ffaa5` | 19 | 50 | `decoder/serialize.go:302-351` |
| 11 | `goinfer` | `e9b76046` | 2 | 2 | `constrain/constrain.go:105-106` |

---

## 3. Pass vs fail and failure-mode decomposition across the 625 counted runs

Aggregate `Precision` is computed over runs with `SeenLines > 0` (`seenRuns`), so runs that quit without reading any base file lines do not dilute or inflate precision.

| group | runs | reached gold | edited gold | `FinalCov` | `RawCov` | `FileCov` | `CovReq1` | `Cov4k` | `Precision` (`SeenLines>0`) | `FirstReq` | `AUCCov` | `Util` | `EditLoc` |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| **passing runs** | 47 | 47/47 (100%) | 46/47 (97.9%) | 0.964 | 1.000 | 1.000 | 0.388 | 0.612 | 0.164 | 2.34 | 0.794 | 0.076 | 0.979 |
| **failing runs (all)** | 578 | 186/578 (32.2%) | 64/578 (11.1%) | 0.185 | 0.243 | 0.341 | 0.008 | 0.106 | 0.161 | 3.77 | 0.135 | 0.017 | 0.101 |
| — `FirstGoldReq == 0` (never saw gold) | 392 | 0/392 (0.0%) | 0/392 (0.0%) | 0.000 | 0.000 | 0.061 | 0.000 | 0.000 | 0.000 | — | 0.000 | 0.000 | 0.000 |
| — `FirstGoldReq > 0 && EditLoc == 0` | 122 | 122/122 (100%) | 0/122 (0.0%) | 0.464 | 0.702 | 0.980 | 0.010 | 0.239 | 0.183 | 3.61 | 0.355 | 0.000 | 0.000 |
| — `EditLoc > 0` (edited gold, failed test) | 64 | 64/64 (100%) | 64/64 (100%) | 0.784 | 0.860 | 0.836 | 0.054 | 0.504 | 0.452 | 4.08 | 0.543 | 0.150 | 0.916 |

---

## 4. Per-task breakdown across the 12 calibration tasks (615 counted calibration runs)

Grouping the 615 counted calibration runs by task separates the 12 calibration tasks into four descriptive regimes:

| task | repo | gold symbol / file (`FixGold`) | runs | pass | reached gold | edited gold | `FinalCov` | `RawCov` | `FileCov` | `Precision` | `FirstReq` | observed regime |
|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 1 | `goinfer` | `loadflags.go:55-82, 316-317` | 48 | 15 | 18/48 (37.5%) | 17/48 (35.4%) | 0.375 | 0.375 | 0.396 | 0.092 | 4.33 | **Retrieval-gated, solved when found:** 15 of the 18 runs that found the gold symbol passed (83.3%), vs 0/30 when not found. |
| 2 | `goinfer` | `banner.go:128-282` | 63 | 3 | 39/63 (61.9%) | 17/63 (27.0%) | 0.401 | 0.516 | 0.635 | 0.384 | 2.77 | **Mixed read-to-edit & edit logic:** 61.9% reach `banner.go:128-282`, 17/63 edit inside it, 3 pass. |
| 3 | `goinfer` | `prequant.go:68-142, 150-233, 453-454` | 49 | 0 | 11/49 (22.4%) | 2/49 (4.1%) | 0.117 | 0.128 | 0.265 | 0.070 | 6.00 | **Retrieval + read-to-edit drop:** 77.6% never reach `prequant.go`'s target functions; 9 of 11 that do never apply a successful edit inside them. |
| 4 | `goinfer` | `pull/pull.go:369-410` | 55 | 0 | 3/55 (5.5%) | 1/55 (1.8%) | 0.035 | 0.055 | 0.236 | 0.018 | 15.67 | **Retrieval-bound:** 23.6% open `pull.go` (usually the top 200 lines), only 5.5% ever reach lines 369–410. |
| 5 | `goinfer` | `loadflags.go:297, banner.go:24-72, 74-103, 114-258` | 56 | 0 | 26/56 (46.4%) | 21/56 (37.5%) | 0.281 | 0.311 | 0.232 | 0.406 | 6.19 | **Edit-logic / multi-file bound:** 21 of 26 runs that reach the gold spans edit inside them (`EditLoc = 0.338`), and all 21 fail the oracle test (`FileCov = 0.232` across the 2 gold files). |
| 6 | `goinfer` | `banner.go:73-97` | 66 | 0 | 32/66 (48.5%) | 1/66 (1.5%) | 0.119 | 0.485 | 0.485 | 0.056 | 3.22 | **Read-to-edit drop:** 32 of 66 runs read the raw hunk line (`RawCov = 0.485`), while only 1 of 66 applies a successful edit inside `banner.go:73-97`. |
| 7 | `ken` | `mcp/log.go:20-21` | 45 | 0 | 1/45 (2.2%) | 0/45 (0.0%) | 0.011 | 0.011 | 0.133 | 0.000 | 22.00 | **Retrieval-bound:** 44 of 45 runs never reach `mcp/log.go:20-21`. |
| 8 | `goinfer` | `chat/templates.go:116-151` | 49 | 0 | 0/49 (0.0%) | 0/49 (0.0%) | 0.000 | 0.000 | 0.020 | 0.000 | — | **Retrieval-bound:** 0 of 49 runs ever reach `chat/templates.go:116-151`. |
| 9 | `goinfer` | `tower_reserve.go:53-95, 152` | 49 | 0 | 1/49 (2.0%) | 0/49 (0.0%) | 0.020 | 0.020 | 0.020 | 0.045 | 9.00 | **Retrieval-bound (early quit):** 48 of 49 runs quit before reaching `tower_reserve.go`. |
| 10 | `ken` | `cmd/ken/build_index.go:31-164` | 45 | 0 | 29/45 (64.4%) | 7/45 (15.6%) | 0.146 | 0.178 | 0.644 | 0.143 | 1.59 | **Read-to-edit drop:** 64.4% reach `build_index.go` in ~1.6 requests, but most read only a slice (`FinalCov = 0.146`) and only 7/45 edit inside it. |
| 11 | `ken` | `mcp/server.go:74-82` | 45 | 27 | 44/45 (97.8%) | 37/45 (82.2%) | 0.951 | 0.933 | 1.000 | 0.218 | 1.27 | **Solved:** 97.8% reach the 9-line symbol at request 1.27; 27 of 37 runs that edit it pass. |
| 12 | `aikit` | `ann/flat_i8_persist.go:97-122` | 45 | 2 | 24/45 (53.3%) | 6/45 (13.3%) | 0.533 | 0.533 | 0.578 | 0.048 | 4.17 | **Read-to-edit + edit-logic drop:** 53.3% read the full symbol (`FinalCov = 0.533`), 6/45 edit inside it, 2 pass. |

---

## 5. Job-by-job context metrics across E1 pilot, calibration, and C6 screens (625 counted runs)

Every row below comes directly from `tools/replaycontext -mirrors ~/tack-runs/e1/mirrors -scan ~/tack-runs/e1`, and the `runs` column sums to **625** (`10` pilot + `615` calibration):

| job | arm | runs | pass | reached | edited gold | `FinalCov` | `RawCov` | `FileCov` | `CovReq1` | `Cov4k` | `Precision` | `FirstReq` | `AUCCov` | `Util` | `EditLoc` |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `a1-2026-10-18-day2` (pilot tasks 1–7, 9–11) | `A1` | 10 | 0 | 5/10 | 1/10 | 0.276 | 0.303 | 0.600 | 0.000 | 0.276 | 0.132 | 3.00 | 0.194 | 0.010 | 0.100 |
| `cal-baseline-2026-10-18` (Qwen2.5-7B, passes 1–3) | `A1` | 36 | 0 | 8/36 | 2/36 | 0.106 | 0.083 | 0.250 | 0.009 | 0.051 | 0.039 | 5.00 | 0.054 | 0.008 | 0.044 |
| `c4` (Qwen3-4B, passes 1–3) | `A1` | 36 | 0 | 6/36 | 0/36 | 0.084 | 0.083 | 0.222 | 0.009 | 0.084 | 0.132 | 1.50 | 0.083 | 0.000 | 0.000 |
| `c5` (Qwen3-8B, passes 1–3) | `A1` | 36 | 4 | 11/36 | 6/36 | 0.205 | 0.246 | 0.278 | 0.059 | 0.129 | 0.189 | 2.73 | 0.158 | 0.038 | 0.156 |
| `c6-46-2026-10-19/runs` (`fit_window`: A=off, B=on) | `A` | 36 | 1 | 11/36 | 2/36 | 0.167 | 0.194 | 0.306 | 0.033 | 0.098 | 0.089 | 2.09 | 0.143 | 0.006 | 0.056 |
| `c6-46-2026-10-19/runs` (`fit_window`: A=off, B=on) | `B` | 36 | 2 | 11/36 | 8/36 | 0.263 | 0.298 | 0.278 | 0.028 | 0.214 | 0.250 | 4.09 | 0.200 | 0.038 | 0.203 |
| `c6-44-2026-10-19/runs` (`result_notes`: A=off, B=on) | `A` | 36 | 2 | 8/36 | 3/36 | 0.140 | 0.139 | 0.250 | 0.031 | 0.088 | 0.092 | 2.25 | 0.119 | 0.007 | 0.083 |
| `c6-44-2026-10-19/runs` (`result_notes`: A=off, B=on) | `B` | 36 | 3 | 15/36 | 8/36 | 0.315 | 0.315 | 0.458 | 0.001 | 0.205 | 0.174 | 5.53 | 0.203 | 0.039 | 0.212 |
| `c6-41-2026-10-20` (`edit_messages`: all 72 counted) | `A` | 36 | 2 | 9/36 | 4/36 | 0.159 | 0.167 | 0.278 | 0.031 | 0.134 | 0.112 | 4.33 | 0.121 | 0.011 | 0.111 |
| `c6-41-2026-10-20` (`edit_messages`: all 72 counted) | `B` | 36 | 3 | 11/36 | 4/36 | 0.180 | 0.194 | 0.306 | 0.001 | 0.152 | 0.120 | 3.18 | 0.137 | 0.010 | 0.111 |
| `c6-48-2026-10-20/runs` (`instructions`: A=none, B=full) | `A` | 36 | 2 | 9/36 | 6/36 | 0.217 | 0.222 | 0.250 | 0.007 | 0.162 | 0.232 | 1.89 | 0.178 | 0.011 | 0.167 |
| `c6-48-2026-10-20/runs` (`instructions`: A=none, B=full) | `B` | 36 | 4 | 19/36 | 9/36 | 0.400 | 0.409 | 0.569 | 0.087 | 0.160 | 0.194 | 3.74 | 0.298 | 0.040 | 0.231 |
| `c6-head-t1-2026-10-20/qwen3-4b` (`instructions_head`, tasks 3, 4) | `A1` | 6 | 0 | 0/6 | 0/6 | 0.000 | 0.000 | 0.333 | 0.000 | 0.000 | 0.000 | — | 0.000 | 0.000 | 0.000 |
| `c6-head-t1-2026-10-20/qwen3-8b` (`instructions_head`, tasks 3, 4) | `A1` | 6 | 0 | 4/6 | 0/6 | 0.387 | 0.667 | 0.833 | 0.002 | 0.286 | 0.204 | 2.00 | 0.327 | 0.000 | 0.000 |
| `c6-42-2026-10-20/runs-restart` (`verify_done`: A=off, B=on) | `A` | 36 | 6 | 14/36 | 8/36 | 0.293 | 0.389 | 0.417 | 0.084 | 0.150 | 0.122 | 2.79 | 0.226 | 0.040 | 0.199 |
| `c6-42-2026-10-20/runs-restart` (`verify_done`: A=off, B=on) | `B` | 36 | 5 | 16/36 | 11/36 | 0.310 | 0.425 | 0.431 | 0.084 | 0.121 | 0.173 | 3.25 | 0.239 | 0.016 | 0.303 |
| `c6-4b-bundle-2026-10-21/runs` (Qwen3-4B + bundle, tasks 1, 2, 11, 12) | `A1` | 12 | 0 | 2/12 | 0/12 | 0.001 | 0.000 | 0.417 | 0.000 | 0.001 | 0.009 | 5.00 | 0.001 | 0.000 | 0.000 |
| `c6-build-2026-10-25/runs` (`build_after_edit`: A=off, B=on) | `A` | 36 | 5 | 18/36 | 10/36 | 0.336 | 0.460 | 0.486 | 0.084 | 0.212 | 0.170 | 3.28 | 0.257 | 0.013 | 0.278 |
| `c6-build-2026-10-25/runs` (`build_after_edit`: A=off, B=on) | `B` | 36 | 4 | 18/36 | 10/36 | 0.429 | 0.485 | 0.500 | 0.084 | 0.181 | 0.184 | 4.39 | 0.334 | 0.042 | 0.249 |
| `c6-t1-2026-10-25/edit-lines-qwen3-8b` (tasks 2, 5) | `A1` | 6 | 1 | 5/6 | 2/6 | 0.453 | 0.833 | 1.000 | 0.002 | 0.055 | 0.145 | 4.60 | 0.286 | 0.007 | 0.333 |
| `c6-t1-2026-10-25/restate-qwen3-4b` (tasks 3, 4, 8, 9) | `A1` | 12 | 0 | 1/12 | 0/12 | 0.001 | 0.000 | 0.250 | 0.000 | 0.001 | 0.006 | 2.00 | 0.000 | 0.000 | 0.000 |
| `c6-cursym-2026-10-27/runs` (tasks 1, 2, 5) | `A1` | 9 | 3 | 9/9 | 4/9 | 0.555 | 0.889 | 0.833 | 0.000 | 0.111 | 0.294 | 4.89 | 0.368 | 0.014 | 0.444 |
| `c6-cursym2-2026-10-27/runs` (tasks 2, 5, 6) | `A1` | 24 | 0 | 23/24 | 12/24 | 0.403 | 0.821 | 0.812 | 0.004 | 0.306 | 0.328 | 3.04 | 0.307 | 0.060 | 0.464 |

*(Note: On `c6-44-2026-10-19/runs` arm `B`, excluding failed tool calls from `cumTokens` per §3.1 of the specification changes `Cov4k` from `0.178` to `0.205`.)*

### 5.1 Sensitivity to `-include-notes` on `c6-cursym` and `c6-cursym2`

Running `tools/replaycontext -include-notes` on `c6-cursym-2026-10-27/runs` (9 runs) and `c6-cursym2-2026-10-27/runs` (24 runs) credits `[tack: the current text of <sym> in <file>, lines <start>-<end>, ...]` notes attached to failed `edit` calls as seen spans:
- `c6-cursym-2026-10-27/runs` (`A1`, 9 runs): metrics are unchanged (`FinalCov = 0.555`, `RawCov = 0.889`, `FileCov = 0.833`, `Cov4k = 0.111`, `Precision = 0.294`) because those runs had already read the enclosing symbol before attempting the failed edits.
- `c6-cursym2-2026-10-27/runs` (`A1`, 24 runs): `FinalCov` increases by `+0.001` (`0.403 -> 0.404`), with all other rounded columns unchanged (`RawCov = 0.821`, `FileCov = 0.812`, `Cov4k = 0.306`, `Precision = 0.328`).

---

## 6. Observations and hypotheses from CB3

All mechanism interpretations below are **hypotheses** drawn from observational correlations across 36-run per-arm calibration jobs where binary pass differences are 1–2 runs:

1. **Observed trajectory differences across C6 screens (and counter-evidence):**
   - **4.4 (`result_notes`):** Between arm `A` (off) and arm `B` (on), `reached` rises from `8/36` to `15/36`, `FinalCov` rises from `0.140` to `0.315`, and `edited_gold` rises from `3/36` to `8/36`. *Hypothesis:* when an initial search or grep returns nothing, the note nudges the model to issue follow-up searches rather than quitting early. *Counter-evidence / trade-off:* `CovReq1` drops from `0.031` (`A`) to `0.001` (`B`), and mean `FirstReq` among runs reaching gold increases from `2.25` to `5.53` requests, showing that the additional coverage arrives late in the trajectory rather than on the first turn.
   - **4.8 (`instructions`):** Between arm `A` (no instruction file) and arm `B` (full instruction file), `reached` rises from `9/36` to `19/36`, `FileCov` rises from `0.250` to `0.569`, and `CovReq1` rises from `0.007` to `0.087` (`Cov4k` is flat at `0.162` vs `0.160`, and `Precision` is `0.232` vs `0.194`). *Hypothesis:* the repository map in `CLAUDE.md`/`AGENTS.md` helps direct initial file lookups toward the target package, though `FirstReq` also rises from `1.89` to `3.74` across the larger set of runs that eventually reach gold.
   - **4.6 (`fit_window`):** `FinalCov` rises from `0.167` to `0.263`, `Cov4k` rises from `0.098` to `0.214`, and `edited_gold` rises from `2/36` to `8/36` (`reached` is `11/36` on both arms, and `FileCov` is `0.306` vs `0.278`).
   - **4.1 (`edit_messages`, all 72 counted runs):** Trajectory metrics are nearly flat between `A` and `B` (`reached` `9/36` vs `11/36`, `edited_gold` `4/36` vs `4/36`, `FinalCov` `0.159` vs `0.180`, `EditLoc` `0.111` vs `0.111`, while `CovReq1` drops from `0.031` to `0.001`).
   - **4.2 (`verify_done`, `runs-restart`):** `edited_gold` is `8/36` (`EditLoc = 0.199`) on `A` vs `11/36` (`EditLoc = 0.303`) on `B`, while `FinalCov` (`0.293` vs `0.310`) and `FileCov` (`0.417` vs `0.431`) barely move, `Cov4k` drops from `0.150` to `0.121`, `Util` drops from `0.040` to `0.016`, and passes are `6/36` (`A`) vs `5/36` (`B`).
   - Across the cumulative progression from unbundled Qwen3-8B (`c5`: `reached = 11/36`, `edited_gold = 6/36`, `FinalCov = 0.205`, `RawCov = 0.246`, `4/36` pass) to bundled Qwen3-8B (`c6-build` arm `A`: `reached = 18/36`, `edited_gold = 10/36`, `FinalCov = 0.336`, `RawCov = 0.460`, `5/36` pass), gold-symbol reach increases from `30.6%` to `50.0%` while binary passes move by `+1` run (`4/36 -> 5/36`).
2. **Where retrieval (`search`, `symbol`) has the largest potential headroom (hypothesis for C7):**
   - All calibration runs through C6 ran **without `[ken]`** (`grep`, `glob`, `read`, `edit`, `write`, `bash` only).
   - Across Tasks 1, 3, 4, 7, 8, and 9 (`295` counted calibration runs), `grep`/`glob`/`read` failed to reach the gold symbol in **`261 of 295` runs (`88.5%`)** — and on Task 1, `15 of the 18` counted runs that *did* reach `loadflags.go:55-82` passed (`83.3%`).
   - On Task 4 (`pull/pull.go:369-410`), `23.6%` of counted runs opened `pull/pull.go` (`FileCov = 0.236`), whereas only `5.5%` (`3/55`) ever reached lines `369–410`. *Hypothesis:* un-offset `read` calls cap at `200` lines, so `symbol` lookup by function name could reach symbols past line 200 without requiring manual offset pagination.
3. **Where post-retrieval edit application and patch logic remain bottlenecks:**
   - On Task 6 (`banner.go:73-97`) and Task 10 (`build_index.go:31-164`), `48.5%` (`32/66`) and `64.4%` (`29/45`) of counted runs reached the gold symbol, while only `1.5%` (`1/66`) and `15.6%` (`7/45`) applied a successful edit inside it (`EditLoc > 0`). (Because `EditLoc` only counts `OutcomeOK` edits, it does not separate runs that never called `edit` on the symbol from runs whose `edit` calls failed.)
   - On Task 5 (`loadflags.go:297`, `banner.go:24-72, 74-103, 114-258`), `21 of 26` counted runs that reached the gold spans applied a successful edit inside them, yet `0 of 21` passed the oracle test (`FileCov = 0.232` across the two gold files), indicating an edit-completeness / patch-logic bottleneck rather than single-file line reach.
