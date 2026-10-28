# E1 CB3: trajectory context metrics on captured runs — pre-registration

<!-- citations-at: tack f75780d, ken 5ab3888 (v1.11.0), ContextBench cd5f62e -->

**Status: registered 2026-10-28, before CB3's scorer (`internal/eval/pilot/context.go`) and replay (`tools/replaycontext`) run on captured E1 directories.** Approved by the owner in `docs/research/contextbench-leann-2026-10-28.md` §9 (decision 3).

## 1. Purpose

Pass rates on the 12 calibration tasks sit between 0 of 36 (`e1-calibration-baseline-2026-10-18.md`) and 4 of 36 (`e1-calibration-c5-qwen3-8b-2026-10-19.md`), so binary task outcomes alone cannot show whether a run found the right code and failed on the edit, or never reached the fix site at all.

CB3 brings `DESIGN.md` §15.1's "retrieval recall at the edit site" and ContextBench's trajectory metrics (`arXiv:2602.05892`, §1.3 of `contextbench-leann-2026-10-28.md`) to `tack`'s already-captured runs, with **zero new model runs**.

No adoption decision rides on CB3 at first (`contextbench-leann-2026-10-28.md` §8.3). Pre-registering the gold derivation, span extraction rules, and metric formulas before running the scorer over `~/tack-runs/e1/` guarantees the definitions are not tuned to favor any arm.

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
   - If `SymbolAt` returns an empty slice (the change is at file scope, such as a package-level `var` or `const`) or `ErrNotIndexed`, keep the hunk's raw base span `[rawStart, rawEnd]`.
3. **Canonicalization:**
   - For each file `f`, sort its intervals by `Start` and merge any intervals where `next.Start <= cur.End + 1` into disjoint 1-based inclusive `[Start, End]` spans.

### 2.2 Annotated gold (`-gold <path>`, optional)

- When an external JSONL file of `{task, repo, base, spans: [{file, start_line, end_line}]}` is passed via `-gold`, its canonicalized spans replace `FixGold` for those tasks and are reported in a separate column (`annotated`).
- `FixGold` numbers are internal to `tack`'s E1 tasks and are never compared directly to ContextBench's published numbers on SWE-bench (`contextbench-leann-2026-10-28.md` §4.4, §10).

---

## 3. Seen and used spans from a captured run

Each run is scored in **base-commit line coordinates** over the files that exist in `BaseTree(mirrors, repo, base)` (`internal/eval/pilot/basetree.go:BaseTree`).

To prevent a run's own mid-run edits from shifting line numbers when it later reads the same file, the scorer tracks a per-file line-offset table from the run's successful `edit` and `write` calls, mapping any read of a modified file back to the corresponding lines of the `base` file (lines newly inserted by the run that did not exist at `base` cannot be in `base` gold and are ignored if outside a replaced base range; lines in `base` only count toward precision if `1 <= line <= len(baseLines[file])`).

### 3.1 `tack` (`A1`) runs (`events.jsonl`)

Request index `r` starts at `0` and increments on each `proto.RequestSent` (`r = 1, 2, ...`). Each `proto.ToolFinished` with `Outcome == proto.OutcomeOK` paired with its `proto.ToolCall` yields spans at request `r` and adds `engine.ApproxTokens(ToolFinished.Result)` to cumulative tool-output tokens:

| tool | seen code spans (`S_r`) | used spans (`U_r`) | rule |
|---|---|---|---|
| `read` | `[start, end]` in `path` | — | From input `{path, offset, limit}` against the file's line count, applying `readMaxLines = 200` when `offset == 0 && limit == 0` (`internal/tools/fs.go:readTool.Run`). |
| `search` | each `<file>:<start>-<end>` block in `Result` | — | Parsed from the header lines `<file>:<start>-<end>` emitted by `internal/tools/search.go:searchTool.Run`. |
| `symbol` | definition span `[shownStart, shownEnd]` in `<file>`, plus 1-line spans `[line, line]` for each listed caller (`  <file>:<line>`) and test (`  <file>:<line> <name>`) | — | When `input.name != ""` and a single definition is shown (`<file>:<start>-<end> <kind> <name>` followed by numbered `\t` lines), the seen definition span is the range of numbered lines actually printed (which starts at `DocLine` when present and is capped at `readMaxLines = 200`). Outline-only calls (`input.name == ""`) and multi-definition ambiguity listings print signatures/ranges without code bodies and add **no** seen code spans. |
| `grep` | `[line, line]` for each `<file>:<line>:<text>` match line in `Result` | — | Parsed from `internal/tools/fs.go:grepTool.Run` output. |
| `edit` | `[start, end]` of the replaced region | `[start, end]` of the replaced region | When `Outcome == OutcomeOK`: the base lines replaced by `old_string` (including when matched via `MatchTolerances`), `symbol`, or `start_line..end_line`. |
| `write` | `[1, len(baseLines[path])]` if `path` existed at `base` | `[1, len(baseLines[path])]` if `path` existed at `base` | Creating a brand-new file not in `base` adds no base spans. |
| `bash` | file lines printed by simple file-reading commands (`cat`, `sed -n '<M>,<N>p'`, `head`, `tail`, `nl`) on workspace files | — | Parsed with `mvdan.cc/sh/v3/syntax`. Build, test, git, and other command output is not file source text and adds no code spans. |

Failed tool calls (`Outcome != OutcomeOK`), `glob` listings, and `done` calls add no seen code spans. (If a `current_symbol` note is attached to a failed `edit` result, its `<file>:<start>-<end>` numbered lines are recorded only when `-include-notes` is set, off by default so baseline and note arms compare on tool results unless asked.)

### 3.2 `opencode` (`A0`, `A0k`) runs (`events.jsonl` and `requests/*.req.json`)

For pilot runs captured from `opencode`, tool calls and their outputs are read from the recorded message history in the last `requests/NNNN.req.json` (or `events.jsonl` when `requests/` is absent), mapping each assistant tool-call turn to request index `r = 1, 2, ...`:
- `read` (`filePath`, `offset`, `limit`, default 2,000 lines in opencode);
- `grep` (`<file>:<line>:` or `Line <n>:` lines in output);
- `edit` / `write` / `patch` (matched region in `filePath` as seen and used);
- `bash` (same `mvdan.cc/sh/v3/syntax` rule as `A1`);
- `ken_search` / `ken_definition` / `ken_callers` / `ken_references` (file and `start_line..end_line` or `line` fields in `ken-mcp`'s JSON/text tool results).

---

## 4. Metrics per run

Let $G$ be the set of `(file, line)` pairs in the task's gold spans (`|G|` total lines), $S_r$ the cumulative union of seen `(file, line)` pairs in `BaseTree` files through request $r$ ($1 \le r \le R$, with $S_{\text{final}} = S_R$), $S_{\le 4\text{k}}$ the union of seen `(file, line)` pairs across tool calls whose cumulative tool-output tokens are $\le 4{,}096$, and $U$ the union of used (edited/overwritten) `(file, line)` pairs across the run.

Following ContextBench (`contextbench/metrics/compute.py`, `evaluate.py` at `cd5f62e`):

1. **Final line coverage (`FinalCov`):**
   $$\text{FinalCov} = \begin{cases} 1.0 & \text{if } |G| = 0 \\ \frac{|G \cap S_{\text{final}}|}{|G|} & \text{otherwise} \end{cases}$$
2. **File coverage (`FileCov`):** fraction of distinct gold files in $G$ that have at least one line in $S_{\text{final}}$.
3. **Request-1 line coverage (`CovReq1`):** $\frac{|G \cap S_1|}{|G|}$ (coverage after the first model request's tool calls finish; `0` if $R = 0$).
4. **4k-token line coverage (`Cov4k`):** $\frac{|G \cap S_{\le 4\text{k}}|}{|G|}$ (coverage within the first 4,096 tokens of tool output).
5. **Final line precision (`Precision`):**
   $$\text{Precision} = \begin{cases} 1.0 & \text{if } |S_{\text{final}}| = 0 \\ \frac{|G \cap S_{\text{final}}|}{|S_{\text{final}}|} & \text{otherwise} \end{cases}$$
6. **First gold request (`FirstGoldReq`):** smallest request index $r \in \{1, \dots, R\}$ with $|G \cap S_r| > 0$, or `0` if $|G \cap S_{\text{final}}| = 0$.
7. **Trajectory efficiency (`AUCCov`):**
   $$\text{AUCCov} = \begin{cases} 0.0 & \text{if } R = 0 \\ \frac{1}{R} \sum_{r=1}^{R} \frac{|G \cap S_r|}{|G|} & \text{otherwise} \end{cases}$$
8. **Edit utilization (`Utilization`):**
   $$\text{Utilization} = \begin{cases} 0.0 & \text{if } |G \cap S_{\text{final}}| = 0 \\ \frac{|G \cap U|}{|G \cap S_{\text{final}}|} & \text{otherwise} \end{cases}$$
   *(Note: $1 - \text{Utilization}$ is ContextBench's `UsageDrop` on the edit site.)*
9. **Edit location precision (`EditLoc`):**
   $$\text{EditLoc} = \begin{cases} 0.0 & \text{if } |U| = 0 \\ \frac{|G \cap U|}{|U|} & \text{otherwise} \end{cases}$$
   *(Whether the run edited the gold symbol/lines when it made an edit.)*

---

## 5. Tier-0 gates (broken first)

Before running `tools/replaycontext` on `~/tack-runs/e1/`:

1. **`TestScoreContext_RedAndExact`:**
   - A capture whose only `read` is outside $G$ scores `FinalCov == 0`, `Precision == 0`, `FirstGoldReq == 0`, `AUCCov == 0`.
   - A capture whose only `read` is $G$ exactly scores `FinalCov == 1`, `Precision == 1`, `FirstGoldReq == 1`, `AUCCov == 1`.
   - Perturb the span intersection boundary (`<` vs `<=`) first to confirm the test fails.
2. **`TestScoreContext_AllToolsAndMetrics`:**
   - A multi-request synthetic capture exercising `search`, `symbol` (definition + callers + outline-ignored), `grep`, `read` (with 200-line cap), `bash` (`cat` vs `go test`), and `edit` (with line shift followed by a second `read`) matches hand-computed `FinalCov`, `CovReq1`, `Cov4k`, `Precision`, `FirstGoldReq`, `AUCCov`, `Utilization`, and `EditLoc`.
3. **`TestFixGold_WidensToEnclosingSymbol`:**
   - On a fixture git repo with two commits, `FixGold` excludes `_test.go` changes, widens a 1-line change inside a function to that function's `[StartLine, EndLine]` via `SymbolAt`, and keeps a file-scope `var` change at its raw diff line.
4. **Gates:** `go run ./tools/touched` and `go run ./tools/lint` both print `VERDICT: PASS`.

