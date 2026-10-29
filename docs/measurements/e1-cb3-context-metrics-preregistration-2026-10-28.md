# E1 CB3: trajectory context metrics on captured runs — pre-registration

<!-- citations-at: tack f75780d, ken 5ab3888 (v1.11.0), ContextBench cd5f62e -->

**Status: committed 2026-10-28 in `57c9548` (`14:45:58 -0700`) in the same commit as `internal/eval/pilot/context.go`, `tools/replaycontext/main.go`, and the results record `docs/measurements/e1-cb3-context-metrics-2026-10-28.md` (after `/tmp/e1-cb3-context-metrics.jsonl` was written at `14:37`).** Because the specification, scorer, and results landed in a single commit, git history does not establish that this specification was frozen before the first replay run. In addition, while `docs/research/contextbench-leann-2026-10-28.md` §9 marked decisions 1–9 as approved in `57c9548`, Francis's approval of those decisions is not recorded in a separate commit or note in the repository.

## 1. Purpose

Pass rates on the 12 calibration tasks sit between 0 of 36 (`e1-calibration-baseline-2026-10-18.md`) and 4 of 36 (`e1-calibration-c5-qwen3-8b-2026-10-19.md`), so binary task outcomes alone cannot show whether a run found the right code and failed on the edit, or never reached the fix site at all.

CB3 brings `DESIGN.md` §15.1's "retrieval recall at the edit site" and ContextBench's trajectory metrics (`arXiv:2602.05892`, §1.3 of `contextbench-leann-2026-10-28.md`) to `tack`'s already-captured runs, with **zero new model runs**.

No adoption decision rides on CB3 (`contextbench-leann-2026-10-28.md` §8.3). This document specifies the gold derivation, span extraction rules, scan exclusions, and metric formulas used by `internal/eval/pilot/context.go` and `tools/replaycontext`.

---

## 2. Gold spans for E1 tasks

Every E1 task in `eval/e1/pilot.jsonl` and `eval/e1/calibration.jsonl` has a base commit (`base`), an oracle fix commit (`commit`), and a list of oracle test files (`test_files`).

### 2.1 Fix-site gold (`FixGold`, automatic, default)

For a task `(repo, base, commit, test_files)`:

1. **Non-test diff hunks at `base`:**
   - Run `git --git-dir <mirrors>/<repo>.git diff -U0 --no-renames <base> <commit>`.
   - Keep only files `f` that are non-test Go files outside `testdata` (`strings.HasSuffix(f, ".go")` and `!strings.HasSuffix(f, "_test.go")` and `!strings.Contains("/"+f, "/testdata/")` and `f` not in `test_files`, matching `internal/eval/mine/static.go:isNonTestGo`).
   - For each hunk header `@@ -s[,n] +... @@` in file `f`:
     - If `n > 0` (omitted `,n` means `n = 1`), the changed base span is `[s, s+n-1]` (1-based, inclusive), and the probe lines for symbol lookup are `s` and `s+n-1`.
     - If `n == 0` (a pure insertion after line `s` in `base`), the raw base span is `[max(s, 1), min(s+1, fileLines)]`, and the probe lines are `max(s, 1)` and `min(s+1, fileLines)`.
2. **Widening to the enclosing symbol at `base` (via `ken` K12):**
   - Materialize the repository at `base` (or the changed files) and open `kensearch.Open` (`Mode: "bm25"`, `Chunker: "treesitter"`, `Watch: false`, `Snapshot: false`).
   - For each probe line `p` of a hunk in file `f`, call `SymbolAt(ctx, f, p)`.
   - `SymbolAt` returns enclosing symbols innermost first (`internal/kensearch/symbols.go:SymbolAt`). If non-empty, take the innermost symbol's `[StartLine, EndLine]` (inclusive) and union it with the hunk's raw base span `[rawStart, rawEnd]`.
   - If `SymbolAt` returns an empty slice (the change is at file scope, such as a package-level `var` or `const`) or `ErrNotIndexed`, keep the hunk's raw base span `[rawStart, rawEnd]`. Any other error from `SymbolAt` is returned by `FixGold`.
3. **Canonicalization:**
   - For each file `f`, sort its intervals by `Start` and merge any intervals where `next.Start <= cur.End + 1` into disjoint 1-based inclusive `[Start, End]` spans.

### 2.2 Annotated gold (`-gold <path>`, optional)

- When an external JSONL file of `{task, repo, base, spans: [{file, start_line, end_line}]}` is passed via `-gold`, its canonicalized spans are scored alongside `FixGold` and emitted in the `annotated_gold` field of `-dump`.
- `FixGold` numbers are internal to `tack`'s E1 tasks and are never compared directly to ContextBench's published numbers on SWE-bench (`contextbench-leann-2026-10-28.md` §4.4, §10).

---

## 3. Seen and used spans from a captured run

Each run is scored in **base-commit line coordinates** over the files that exist in `BaseTree(mirrors, repo, base)` (`internal/eval/pilot/basetree.go:BaseTree`).

To prevent a run's own mid-run edits from shifting line numbers when it later reads the same file, the scorer tracks a per-file line-offset table (`trackedFile`) from the run's successful `edit` and `write` calls:
- On a successful `edit` replacing current lines `[start, end]` with `newCount` lines, lines newly inserted by the run are assigned base line `0` (unless `start == end` and `newCount == 1`, which preserves that single base line index) so subsequent reads of newly inserted text cannot inflate base coverage or precision, while lines after `end` shift by `newCount - (end - start + 1)`.
- On a successful whole-file `write` to an existing base file with `newCount` lines, `trackedFile` replaces `curLines` with positional indices `1..newCount` (clamped to `len(baseLines[path])`, or `0` for lines beyond `len(baseLines[path])`), a positional approximation since `write` overwrites the entire file rather than diffing against `base`.

### 3.1 `tack` (`A`, `A1`, `B`) runs (`events.jsonl`)

Request index `r` starts at `0` and increments on each `proto.RequestSent` (`r = 1, 2, ...`). Each `proto.ToolFinished` with `Outcome == proto.OutcomeOK` (or a failed `edit` carrying a `current_symbol` note when `-include-notes` is enabled) paired with its `proto.ToolCall` yields spans at request `r` and adds `engine.ApproxTokens(stripTackNotes(ToolFinished.Result))` to cumulative tool-output tokens (`cumTokens`):

| tool | seen code spans (`S_r`) | used spans (`U_r`) | rule |
|---|---|---|---|
| `read` | `[start, end]` in `path` | — | From input `{path, offset, limit}` against the base file's line count (`len(baseLines)`), applying `contextReadCapLines = 200` when `offset == 0 && limit == 0` (`internal/tools/fs.go:readTool.Run`), then mapped through `trackedFile.mapRange`. |
| `search` | each `<file>:<start>-<end>` block in `Result` | — | Parsed from the header lines `<file>:<start>-<end>` emitted by `internal/tools/search.go:searchTool.Run`. |
| `symbol` | definition span `[shownStart, shownEnd]` in `<file>`, plus 1-line spans `[line, line]` for each listed caller (`  <file>:<line>`) and test (`  <file>:<line> <name>`) | — | When `input.name != ""` and a single definition is shown (`<file>:<start>-<end> <kind> <name>` followed by numbered `\t` lines), the seen definition span is the range of numbered lines actually printed (which starts at `DocLine` when present and is capped at `readMaxLines = 200`). Outline-only calls (`input.name == ""`) and multi-definition ambiguity listings print signatures/ranges without code bodies and add **no** seen code spans. |
| `grep` | `[line, line]` for each `<file>:<line>:<text>` match line in `Result` | — | Parsed from `internal/tools/fs.go:grepTool.Run` output. |
| `edit` | `[start, end]` of each replaced region | `[start, end]` of each replaced region | When `Outcome == OutcomeOK`: the base lines replaced by `old_string` (including `replace_all: true` and `MatchTolerances` matches), `symbol`, or `start_line..end_line`. Recording replaced base spans in both `S_r` and `U_r` guarantees `|G \cap U| \le |G \cap S_{\text{final}}|` (`Utilization <= 1.0`) even when an edit targets a symbol or line range without a prior `read`. |
| `write` | `[1, len(baseLines[path])]` if `path` existed at `base` | `[1, len(baseLines[path])]` if `path` existed at `base` | Creating a brand-new file not in `base` adds no base spans. Overwriting an existing base file marks all base lines of `path` as seen and used. |
| `bash` | file lines printed by simple file-reading commands (`cat`, `sed -n '<M>,<N>p'`, `head`, `tail`, `nl`) on workspace files | — | Parsed by walking `syntax.CallExpr` nodes with `mvdan.cc/sh/v3/syntax`. Each simple file-reading command in a pipeline is credited independently (so `cat f | head -n 20` credits `cat f` with the whole file rather than modeling pipe truncation). In-place shell modifications (`sed -i`, `gofmt -w`, output redirection) are not tracked in `trackedFile` or `U_r`. Build, test, git, and other command output adds no code spans. |

Failed tool calls (`Outcome != OutcomeOK`) without `-include-notes`, `glob` listings, and `done` calls add **no** seen code spans and **0** tokens to `cumTokens`. (If a `current_symbol` note `[tack: the current text of <sym> in <file>, lines <start>-<end>, ...]` is attached to a failed `edit` result, its `<file>:<start>-<end>` span and `stripTackNotes(Result)` tokens are recorded only when `-include-notes` is set, off by default so baseline and note arms compare on tool results unless asked.)

### 3.2 `opencode` (`A0`, `A0k`) runs (not present on this host; path removed)

All captured E1 runs in `~/tack-runs/e1/` on this Mac are `tack` (`proto.Event`) runs (`A`, `A1`, `B`); the 20 pilot `opencode` (`A0`/`A0k`) runs from 2026-10-17 live on nobara. An unused `opencode` scoring branch initially committed in `internal/eval/pilot/context.go` had no callers or test fixtures in the repository and contained two defects (`strings.TrimPrefix(name, "ken_")` before `extractTackToolSpans` dropped `ken_*` tool calls, and absolute paths in `opencode` tool inputs were not stripped to repo-relative paths before `BaseTree` lookup); it was removed in favor of scoring `tack` `events.jsonl` trajectories only.

---

## 4. Metrics per run and aggregate reporting

Let $G$ be the set of `(file, line)` pairs in the task's gold spans (`|G|` total lines), $S_r$ the cumulative union of seen `(file, line)` pairs in `BaseTree` files through request $r$ ($1 \le r \le R$, with $S_{\text{final}} = S_R$), $S_{\le 4\text{k}}$ the union of seen `(file, line)` pairs across tool calls after which cumulative tool-output tokens satisfy $\text{cumTokens} \le 4{,}096$ (whole-call boundary: a tool call whose output pushes $\text{cumTokens}$ past $4{,}096$ is excluded in full from $S_{\le 4\text{k}}$ rather than sliced mid-output), and $U$ the union of used (edited/overwritten) `(file, line)` pairs across the run.

Following ContextBench (`contextbench/metrics/compute.py`, `evaluate.py` at `cd5f62e`):

1. **Final line coverage (`FinalCov`):**
   $$\text{FinalCov} = \begin{cases} 1.0 & \text{if } |G| = 0 \\ \frac{|G \cap S_{\text{final}}|}{|G|} & \text{otherwise} \end{cases}$$
2. **File coverage (`FileCov`):** fraction of distinct gold files in $G$ that have at least one line in $S_{\text{final}}$ (`1.0` if $|G| = 0$).
3. **Request-1 line coverage (`CovReq1`):** $\frac{|G \cap S_1|}{|G|}$ (coverage after the first model request's tool calls finish; `0` if $R = 0$ and $|G| > 0$).
4. **4k-token line coverage (`Cov4k`):** $\frac{|G \cap S_{\le 4\text{k}}|}{|G|}$ (coverage across tool calls with cumulative tool-output tokens $\le 4{,}096$).
5. **Final line precision (`Precision`):**
   - Per run (`ScoreContext`):
     $$\text{Precision} = \begin{cases} 1.0 & \text{if } |S_{\text{final}}| = 0 \\ \frac{|G \cap S_{\text{final}}|}{|S_{\text{final}}|} & \text{otherwise} \end{cases}$$
   - Aggregate across a group of runs (`tools/replaycontext`): averaged over runs in the group with $\text{SeenLines} = |S_{\text{final}}| > 0$ (`0.0` if no run in the group has $\text{SeenLines} > 0$), so runs that quit without reading any base file lines do not inflate failing-run precision.
6. **First gold request (`FirstGoldReq`):** smallest request index $r \in \{1, \dots, R\}$ with $|G \cap S_r| > 0$, or `0` if $|G \cap S_{\text{final}}| = 0$. Aggregate `FirstReq` is averaged over runs with `FirstGoldReq > 0`.
7. **Trajectory efficiency (`AUCCov`):**
   $$\text{AUCCov} = \begin{cases} 0.0 & \text{if } R = 0 \\ \frac{1}{R} \sum_{r=1}^{R} \frac{|G \cap S_r|}{|G|} & \text{otherwise} \end{cases}$$
8. **Edit utilization (`Utilization`):**
   $$\text{Utilization} = \begin{cases} 0.0 & \text{if } |G \cap S_{\text{final}}| = 0 \\ \frac{|G \cap U|}{|G \cap S_{\text{final}}|} & \text{otherwise} \end{cases}$$
   *(Note: $1 - \text{Utilization}$ is ContextBench's `UsageDrop` on the edit site.)*
9. **Edit location precision (`EditLoc`):**
   $$\text{EditLoc} = \begin{cases} 0.0 & \text{if } |U| = 0 \\ \frac{|G \cap U|}{|U|} & \text{otherwise} \end{cases}$$
   *(Whether the run edited the gold symbol/lines when it made an edit.)*

### 4.1 Scan exclusions (`tools/replaycontext -scan`)

When scanning `~/tack-runs/e1/` (`-scan`), `tools/replaycontext` scores the **625 counted E1 runs** (`10` pilot + `615` calibration) and excludes void, preflight-smoke, superseded, and post-CB3 runs documented as uncounted in the E1 measurement records:

1. **Always excluded in `-scan`:**
   - `rec.Run.Void != ""` (any run marked void in `results.jsonl`).
   - `a1-2026-10-18/` (`10` runs): void pilot capture where the sandbox denied workspace writes (`e1-pilot-a1-2026-10-18.md`).
   - `cal-c4c5-2026-10-18/c4/pass-1-aborted-0654/` (`1` run): aborted launch (`e1-calibration-c6-45-amended-replay-2026-10-20.md`).
   - `cal-baseline-2026-10-18/pass-3/results.jsonl` tasks `5..12` (`8` runs): void tasks replaced by `pass-3-rerun/` (`e1-calibration-baseline-2026-10-18.md`).
   - `c6-bundle-2026-10-28/`: post-CB3 job started after CB3.
2. **Excluded by default in `-scan` (included when `-include-superseded` is passed, adding `62` failing runs for `687` total):**
   - `a1-2026-10-18-day/` (`6` runs): void pilot capture where the sandbox denied the Go module cache, replaced by `a1-2026-10-18-day2/` (`e1-pilot-a1-2026-10-18.md`).
   - `a1-smoke-*` (`4` runs) and `*/smoke-t1/*` (`3` runs): 1-task preflight smoke runs ("not graded").
   - `c6-41-2026-10-20/runs/results.jsonl` task `4` (`5` runs) and `c6-41-2026-10-20/runs-resume/results.jsonl` task `6` (`3` runs): incomplete interrupted task blocks superseded by `runs-resume/` (tasks 4–5) and `runs-resume2/` (tasks 6–12), marked "recorded and not counted" under the §12.10 completeness rule (`e1-calibration-c6-41-2026-10-20.md`). The `30` complete runs in `c6-41-2026-10-20/runs` (tasks 1–3, `18` runs) and `runs-resume` (tasks 4–5, `12` runs) are combined with `runs-resume2` (tasks 6–12, `42` runs) into the `72` counted runs of `c6-41-2026-10-20` (`36 A`, `36 B`).
   - `c6-42-2026-10-20/runs/results.jsonl` (`41` runs): stopped screen before the `instructions_head` fix, superseded by `c6-42-2026-10-20/runs-restart/` (`72` runs) and marked "recorded, not counted" (`e1-calibration-c6-42-2026-10-20.md`, `e1-calibration-c6-42-stopped-2026-10-20.md`).

---

## 5. Tier-0 gates (broken first)

1. **`TestScoreContext_RedAndExact` (`internal/eval/pilot/context_test.go`):**
   - A capture whose only `read` is outside $G$ scores `FinalCov == 0`, `Precision == 0`, `FirstGoldReq == 0`, `AUCCov == 0`.
   - A capture whose only `read` is $G$ exactly scores `FinalCov == 1`, `Precision == 1`, `FirstGoldReq == 1`, `AUCCov == 1`.
   - **Break-it-first perturbation record:** Changing `for line := sp.Start; line <= sp.End; line++` to `line < sp.End` in `internal/eval/pilot/context.go:addSpansToSet` fails all three `ScoreContext` unit tests:
     ```
     --- FAIL: TestScoreContext_RedAndExact (0.00s)
         context_test.go:89: exact metrics = {Requests:1 GoldLines:4 GoldFiles:1 SeenLines:4 ...}, want GoldLines=5 SeenLines=5 and 1 across coverage/precision/first_gold_req/auc
     --- FAIL: TestScoreContext_AllToolsAndMetrics (0.00s)
         context_test.go:156: counts = {Requests:3 GoldLines:6 GoldFiles:2 SeenLines:3 UsedLines:1 ...}, want req=3 gold=8/2 seen=9 used=3
     --- FAIL: TestScoreContext_Cov4kFailedOutcomesWriteAndEdits (0.00s)
         context_test.go:341: noNotes Cov4k=0.3333333333333333 FinalCov=0.3333333333333333, want Cov4k=0.5 FinalCov=0.75
     ```
2. **`TestScoreContext_AllToolsAndMetrics` and `TestScoreContext_Cov4kFailedOutcomesWriteAndEdits` (`internal/eval/pilot/context_test.go`):**
   - Multi-request synthetic captures exercising `search`, `symbol` (definition + callers + outline-ignored), `grep`, `read` (with 200-line cap), `bash` (`cat`/`head`/`tail`/`sed -n` vs `go test`), `edit` (`old_string` with line shift + subsequent `read`, `replace_all: true`, `start_line..end_line`, and `symbol`), `write` (new file + existing base file with subsequent `read`), failed tool outcomes (`OutcomeError` adding 0 spans and 0 tokens), `-include-notes` on a failed `edit` carrying `[tack: the current text of ...]`, `Cov4k` budget binding (`cumTokens > 4096` excluding the overflowing call from `Cov4k` while keeping it in `FinalCov`), and empty gold (`len(gold) == 0`).
3. **`TestFixGold_WidensToEnclosingSymbol` (`internal/eval/pilot/context_test.go`):**
   - On a fixture git repo with two commits, `FixGold` excludes `_test.go` and non-Go changes, widens a 1-line change inside a function to that function's `[StartLine, EndLine]` via `SymbolAt`, keeps a file-scope `var` change at its raw diff line, and returns an error when `SymbolAt` fails on a canceled context.
4. **`TestReplayContext_EndToEndAndPrecisionOverSeenRuns` and `TestScanExclusionsAndJobLabels` (`tools/replaycontext/main_test.go`):**
   - End-to-end CLI test on a bare mirror and `results.jsonl` verifying `Void` skipping, aggregate `Precision` over runs with `SeenLines > 0` (undiluted by `SeenLines == 0` runs), `-dump` JSONL output, `-scan` exclusions, and `VERDICT: PASS`.
5. **Gates:** `go run ./tools/touched` and `go run ./tools/lint` both print `VERDICT: PASS`.

