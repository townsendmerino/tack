# Brief for ken: exact hit text, symbol spans, the symbol at a line, an index that follows edits, qualified names, and a symbol's tests (tack's K10–K15)

<!-- citations-at: ken d63f507, tack d14cbfe -->

**From:** tack (DESIGN §12, asks K10–K15, added to the table with this brief). Drafted in the claude app on the owner's
word, 2026-10-22.

**For:** ken's Claude Code, the sole editor of ken (ken's `CLAUDE.md`). The work lands on a branch. It becomes a tagged
release only on the owner's yes.

**Order:** K10 first, in its own commit, because it is a fix and can ship alone. Then K11, K13, K12, K14, K15, and the
measurements. Release grouping is the owner's call; a suggestion is at the end.

**tack's state:** paused for a few days while the owner works on goinfer
(`docs/measurements/e1-calibration-c6-status-2026-10-21.md`). tack pins ken v1.9.0 and moves only when this lands and the
owner says so. Nothing in tack waits on a date.

## Why

tack has never run with ken in a measured run. Its eval config has no `[ken]` section (`internal/eval/pilot/tack.go`,
`tackConfig`), so every calibration run found code with `grep`, `glob` and whole-file reads, in a 16k window. The next
screen puts ken in the loop. The runs so far show what it has to fix.

- **Searching the wrong place, then no room to read.** The 4.1 screen's task 2, arm A, run 1 (goinfer at base `9f4ecdc3`;
  `docs/design/mocks/Stopped.dc.html` shows it step by step):
  - The statement names the function: "`modelBannerFrom(bannerFacts, config)`".
  - The model searched `internal/sampling` about ten times, then the whole repository, which found
    `internal/serveapp/banner.go:128`.
  - It read lines 1–200 (10,957 bytes). tack withheld the read: about 500 tokens of the window were left, after the
    instruction file had taken 65% of it.
  - It then sent the same edit 8 times, from a guess, and `repeat_loop` stopped the run at 68 s.
  - One definition lookup would have put it at `banner.go:128–282`. That function is 155 lines (8,226 bytes) of a
    418-line file (20,574 bytes), and the fix is at lines 183–195. tack would still read only part of it, but a part of
    a known function rather than the first 200 lines of the file.
- **Edits written from memory.** The 4.1 replay (`docs/measurements/e1-calibration-c6-41-replay-2026-10-20.md`):
  - Of 370 failed edits, 217 had old text that is nowhere in the repository.
  - The most common leave out code the file has. The model writes `return "No repo specified…` where the file has
    `return "", fmt.Errorf("No repo specified…`. No matching tolerance rescued any of the 370.
  - tack's next answers are to show the exact current lines of the symbol being edited and, later, to edit by line range
    (C6 candidate 4.9). Both need spans, and the spans must still be right after the run's own edits.
- **Unrelated test failures.** In task 11 (ken at base `ac71bae4`; `docs/design/mocks/Terminal.dc.html`):
  - The model ran `go test ./...`. It failed in `internal/modelfetch` (`TestFetch_SuccessDownloadsAllThree`: the sandbox
    refused a bind on `::1`).
  - The oracle test is in `./mcp`. The model then said the tests passed.
- **Names in the statements.** 10 of the 12 calibration statements (`eval/e1/calibration-statements.md`) name an
  identifier that exists at the base (the table under K14). DESIGN §7.4 already plans to "expand to `symbol` when the
  message names an identifier ken knows".
- **The app's step summaries** (`docs/design/mocks/README.md`, "What tack would need to emit") say which symbol each step
  touched: "Edited mcp/server.go:81, in resolveRepo". That is a lookup of the symbol at a line, over an index that is
  current.

## What ken has today

Read at `d63f507` (v1.10.0 plus the go 1.27.3 bump). The outputs below come from `engine.Open` over small fixtures in a
temporary directory outside ken's tree, 2026-10-22, in bm25 mode. Kind 1 is a function, 2 a class, 3 a method.

- **The spans exist inside.**
  - `structural.FuncDef` and `ClassDef` carry `StartLine` and `EndLine` from every extractor (Phase 0,
    `docs/internal/structural-call-graph-plan.md`).
  - `CallRef` carries `Callee`, `Receiver`, `Line` and `EnclosingSymbol`.
- **The public results drop most of them.**
  - `DefinitionSite{File, Kind, QName, Line}`: the start line only.
  - `OutlineEntry{Name, Kind, Container, Params}`: no lines.
  - `Reference{File, Kind}` and `CallSite{File}`: no lines, one entry per file. The callers map is built from each
    file's deduped `CalleeNames()`, though `Workspace.Callers`'s comment (`engine/structural.go:246`) says a file "may
    appear more than once (once per call site)".
  - ken-mcp's structural tools show no line except `Definition`'s (`mcp/structural_tools.go`).
- **`Hit.Text` starts with the enrichment label** (K10). On `a.go` (`package a`, where `Alpha` calls `Beta`):

  ```
  hit a.go:1-8 text="# ken: func: Alpha | calls: Beta\npackage a\n\n// Alpha adds one.\nfunc Alpha() int {\n\treturn Beta() + 1\n}\n\nfunc Beta() int { return 2 }\n"
  ```

- **A Go type with methods in another file has two definitions, and package names don't resolve** (K14). Package `pull`:
  `types.go` holds `type Model struct{ n int }` and `func Select(m Model) int`; `methods.go` holds
  `func (m Model) Size() int`.

  ```
  Definition("Model")       = [{File:pull/methods.go Kind:2 QName: Line:0} {File:pull/types.go Kind:2 QName: Line:3}]
  Definition("Model.Size")  = [{File:pull/methods.go Kind:3 QName:Model.Size Line:3}]
  Definition("pull.Select") = []
  Definition("Select")      = [{File:pull/types.go Kind:1 QName: Line:5}]
  Outline("pull/methods.go") = [{File:pull/methods.go Entries:[{Name:Model Kind:2 Container: Params:[]}
                                                              {Name:Size Kind:1 Container:Model Params:[]}]}]
  ```

- **The structural index never changes** (K13). The same package, opened with `Watch: true`. Three comment lines were
  inserted above `Size`, then a 4 s wait, past the 2 s debounce:

  ```
  after edit Definition("Size") = [{File:pull/methods.go Kind:3 QName:Model.Size Line:3}]   Size is now at line 6
  after edit Search hit pull/methods.go:1-8 gen=2                                          republished
  ```

## The asks

### K10. Hit text exactly as it is in the file (a fix)

**Today.**
- `toHits` (`engine/search.go:158–159`) and `FindRelated` (`:215–216`) copy `r.Chunk.Text` and hash it.
- For a file with a structural extractor, that text begins with the Arm B label. The indexer prepends it so that BM25 and
  the embedding see it (`internal/search/index.go:475`, `enrichChunks`).
- ken-mcp strips it at its boundary (`mcp/server.go:628–634` and `:822`). The reasons are in audit R5 and §10: the label
  breaks "Chunk.Text reproduces source" and misleads a read at a line. The engine does not strip it.

**What it costs tack.**
- tack's `search` tool prints `Hit.Text` as the file's code (`internal/tools/search.go:58`). The model would see a line
  that isn't in the file, and one line more than the span says.
- A model that copies that line into an edit gets "text not in the file", the largest failure kind in the 4.1 replay.
- `Hit.Hash` covers the label, which lists the file's functions and callees. A new call anywhere in a file changes the
  hash of every hit in it, even hits whose code did not change. tack's ledger keys items on that hash (DESIGN §7.5: "Same
  hash: nothing happens"), which was K2's purpose.

**The ask.**
- `Hit.Text` is the chunk's text without the label, through `structural.StripLabel`, the helper ken-mcp uses. Put it in
  one place that `toHits` and `FindRelated` both call; `SearchFiles` goes through `toHits`.
- `Hit.Hash` is the sha256 of that text.
- `Hit`'s doc comment says it: `Text` is exactly the file's lines `StartLine..EndLine`.
- Ranking is untouched. The stored chunk keeps its label for BM25 and the embedding; only what the engine returns changes.
- CHANGELOG, under Fixed: the label reached engine callers' hits since 1.9.0. tack is the only external caller today
  (DESIGN §12).

**Checks.**
- A fixture with a Go file, a Python file, a markdown file and a file with no extractor. Every hit from `Search`,
  `SearchMode`, `SearchFiles` and `FindRelated` has:
  - `Text` equal to the file's lines `StartLine..EndLine`, read from disk;
  - no line starting with the `# ken:` sentinel that the file doesn't have;
  - `Hash` equal to the sha256 of `Text`.
- Run it with enrichment on (the default) and off.
- Ledger identity: a file with functions A and B in separate chunks. Add a call to a new function inside B, through K13's
  `Refresh` or the watcher. A's hit keeps its `Hash`. That fails today.

### K11. Lines on every structural result

**The ask.** Additive fields on the `internal/structural` types that `engine` aliases. Zero means "not recorded".
- **`DefinitionSite.EndLine int`:** the declaration's last line, inclusive. `Line` stays the first.
- **`OutlineEntry.StartLine int` and `OutlineEntry.EndLine int`.** A Go class entry that only groups methods whose
  receiver type is declared in another file (a "shell", see K14) has both at 0. The type's own entry is in the file that
  declares it.
- **`CallSite.Calls []Call`:** every call to the symbol in `File`, in line order. `Call` is a new type,
  `Call{Line int; Caller string}`, aliased in `engine` as `engine.Call`.
  - `Caller` is the qualified name of the enclosing function or method, from `CallRef.EnclosingSymbol` (for example
    `"Server.handle"` or `"resolveRepo"`). It is `""` at file scope.
  - There is still one `CallSite` per file, as today. Fix `Workspace.Callers`'s comment to say so, and that `Calls` holds
    the sites.
- **`Reference.Lines []int`:** for a call reference, the call lines in `File`, the same lines as `CallSite.Calls`. It is
  `nil` for imports and raises, whose lines the extractors don't record. Say so in the doc; those lines are not asked for
  here.
- **Compute at query time.** `Definition` already walks `Functions` and `Classes` for the start line (`defStartLine`), and
  the end line is on the same `FuncDef` or `ClassDef`. `Calls` comes from `ix.files[f].CallRefs`, filtered by `Callee`.
  The index doesn't grow.
- **Say what a span covers.**
  - Spans are the declaration node as tree-sitter reports it. Document per language family what that includes.
  - A Go doc comment is outside the span.
  - For a decorated Python function, check whether the extractor takes `decorated_definition` or the inner
    `function_definition`, and say which. tack would rather the span start at the first decorator, so that an edit by
    range can't cut one off, but for this release documenting what it is now is enough.
- **Optional, Go only:** `DocLine int` on `DefinitionSite` and `OutlineEntry`.
  - It is the first line of the comment block directly above the declaration (no blank line between), else equal to
    `Line`.
  - Task 11's `resolveRepo` is lines 74–82 (309 bytes), and 72–82 (444 bytes) with its doc comment.
  - tack can find the comment itself by reading upward, so skip this if it complicates the extractors.

**One observation, ken's call.** `Outline` gives methods `Kind: DefinitionKindFunction` with `Container` set
(`internal/structural/lookups.go:357`), while `Definition` gives them `DefinitionKindMethod`. tack can work with either,
since `Container` tells. If `Outline` changes, ken-mcp's outline output changes with it, so that would need a CHANGELOG
line.

**Checks.**
- A fixture per language family.
  - Go: a function, a struct, an interface, a method.
  - Python: a function, a class, a method, a decorated function, a nested class.
  - Each `DefinitionSite` and `OutlineEntry` has `1 ≤ StartLine ≤ EndLine ≤` the file's line count, and the text of its
    span contains the declaration's name.
- `Definition(x)` and `Outline(file)` agree on x's span.
- `Callers(x)`'s `Calls` lists each call line once, in order, with the right `Caller`. That includes a call inside a
  closure (it counts for the enclosing named function) and a call at file scope (`Caller` is `""`).
- The existing `extract_*_test.go` fixtures pass unchanged. A table test over them checks that every span, in all 15
  extractor languages, lies within its file.

### K12. The symbol at a line

**The ask.**

```go
// SymbolAt returns the indexed symbols whose spans contain line in path, innermost first.
// A line inside a Python method gives the method, then its class. A Go method is not nested in its
// type's span, so it gives the method alone. A line at file scope (imports, package-level
// declarations, comments between functions) gives an empty slice.
func (w *Workspace) SymbolAt(ctx context.Context, path string, line int) ([]SymbolSpan, error)

// SymbolSpan is one symbol and its lines.
type SymbolSpan struct {
	Name      string         // bare name: "Size"
	QName     string         // "Model.Size" for a method, else Name
	Kind      DefinitionKind
	StartLine int
	EndLine   int
}
```

- Names are ken's call.
- Errors match the other structural methods: `ErrStructuralBuilding` and `ErrNoStructuralIndex`.
- A path that is not in the structural index (an unsupported language, or a file not indexed) is an error, distinct from
  file scope. tack's step summaries need to tell "at file scope" from "ken doesn't know this file". The sentinel's name is
  ken's call.
- Closures and anonymous functions aren't indexed, so a line inside one gives the named function around it.
- Cost: proportional to the functions and classes in that one file. No corpus scan.
- Answers are as current as the structural index, so K12 is only as good as K13.

**Checks.**
- Every line of a fixture:
  - inside a function gives the function;
  - inside a Python method gives `[method, class]`;
  - between functions gives an empty slice;
  - past the end of the file gives an empty slice;
  - in an unindexed path gives the error.
- For every `Outline` entry with a span, `SymbolAt(path, StartLine)` and `SymbolAt(path, EndLine)` both include it.

### K13. A structural index that follows edits

**Today.**
- `structuralState` caches a successful build for the Workspace's lifetime (`engine/structural.go:98`).
- The watcher republishes the search index (ADR-012), but nothing updates the structural index. The fixture output above
  shows a three-line shift that `Definition` never sees.
- With `Watch: false`, which is how `tack run` (`cmd/tack/run.go:164–170`) and the eval loop open ken, neither index
  changes.

**Why it matters more with spans.** Once tack reads and edits by span, a stale span points at the wrong code. Three lines
inserted above a function move it, and a read of the old range shows other lines. That is worse than having no span.

**The ask.**

```go
// Refresh re-reads the given repo-relative files and brings both indexes up to date before it
// returns: changed files are re-chunked, re-enriched, re-embedded and re-extracted; deleted files
// are dropped. It is the synchronous form of what the watcher does after its debounce, for a caller
// that knows which files it has just written.
func (w *Workspace) Refresh(ctx context.Context, changed, deleted []string) error
```

- **The search side exists.** `WatchedIndex.ReconcileFiles(changed, deleted)` (`internal/search/corpus.go:133`) does it
  synchronously, through the flush path ("a boot reconcile and a live edit take the identical, already-tested code"),
  and is safe beside a running watcher. `Generation` goes up, and `Hit.Gen` shows it.
- **The structural side:**
  - Re-extract each changed file with the same guarded parse `BuildWithContext` uses (`extractGuarded`), drop deleted
    files, rebuild the lookup maps, and publish the new index atomically.
  - A reader sees the old index or the new one, never a mix.
  - Rebuilding pass 2 over the files map is the simplest way. Patching the maps is the faster one, if the measurements
    say it's needed.
- **The watcher uses the same code.** Its flush updates the structural index for the files in its batch, so a long-lived
  `Watch: true` workspace (tack's daemon, ken-mcp) stays right too.
- **No eager build.** If the structural index hasn't been built yet, `Refresh` leaves it unbuilt; the lazy build will read
  the current files.
- **Broken files.** A file that no longer parses keeps whatever tree-sitter recovers, as in the initial build. A file
  `extractGuarded` rejects leaves the structural index, with a log line.
- **Containment.** `Refresh` reads files from disk, and `NormalizePath` is not a sanitizer (its TRUST BOUNDARY comment).
  Reject a path that escapes the root after cleaning, before reading anything.
- **The snapshot.** With `Snapshot` on, every flush rewrites the snapshot today (`Open`'s `SetOnFlush` hook), and a
  `Refresh` would too. tack refreshes after every edit, and goinfer's snapshot holds every vector. Measure the cost; if
  it's slow, defer the write (debounce it, or write at `Close`).
- **The watcher may flush the same file again** once its debounce fires. That's harmless, since the content is the same.
  Skipping it is optional.
- **State after the call.** When `Refresh` returns, both indexes reflect the files, and `Status().Generation` has moved.
  If the structural index keeps its own counter, expose it; tack only needs to know that both are current when `Refresh`
  returns.

**Checks.**
- `Watch: false`. Call `Definition` (which builds the structural index), insert lines above a function, then call
  `Refresh([file])`. `Definition`, `Outline` and `SymbolAt` give the moved span, `Search` finds the new text, and
  `Generation` went up.
- `Watch: true`, no `Refresh`: after the debounce, the same holds for the structural results. This fails today.
- `Refresh` with a deleted file: its symbols and its hits are gone.
- A path with `..` that escapes the root: an error, and nothing is read.
- Under `-race`, readers loop over `Definition`, `SymbolAt`, `Outline` and `Search` while `Refresh` runs repeatedly: no
  errors and no partial state.
- `Refresh` before any structural call doesn't start a structural build.

### K14. Qualified names

**Today.**
- `Definition` reads a dotted name as `Type.method` and splits it at the first dot
  (`internal/structural/lookups.go:211`).
  - `pull.Select`, a function in package `pull`, gives nothing (the fixture above).
  - `decoder.Model.KVF16DeclineReason` is looked up as method `Model.KVF16DeclineReason` of a type named `decoder`, so it
    gives nothing.
- A Go method whose type is declared in another file creates a class "shell" in its own file (`linkMethodToClass`,
  `internal/structural/extract_go.go:328`).
- Pass 2 of `BuildWithContext` adds every class to `defs`, shells included (`internal/structural/index.go:633–634`). So
  `Definition("Model")` returns the declaration and, with line 0, every file that holds only methods of `Model`.

**The payoff in tack's statements.** The backticked names in the 12 calibration statements, by whether ken finds a
definition at the task's base:

| task | repo | ken finds today | with K14 | struct fields (later) | added by the task itself |
|---|---|---|---|---|---|
| 1 | goinfer | `Register`, `Options`, `Flags.Options` | | `Flags.WindowedKV`, `decoder.Options.ResidentWindowedKV` | |
| 2 | goinfer | `modelBannerFrom` | | `bannerFacts.kvPrec` | |
| 3 | goinfer | `TranscodeTimed`, `selfCheck`, `TestTranscodeTimed_reportsBothPhases` | | `Phases.SelfCheck`, `Phases.Stream` | `wrapSinkForTest` |
| 4 | goinfer | | `pull.Select` | `Ref.File` | |
| 5 | goinfer | `modelBannerFrom`, `bannerFacts`, `factsOf` | `decoder.Model.KVF16DeclineReason` | | |
| 6 | goinfer | `factsOf` | `decoder.Model.ResidentDecline` | | `residentDecline`, `bannerFacts.residentDecline` |
| 7 | ken | | | | `TestPublicSurfaceDocumented` (a new test) |
| 8 | goinfer | | `chat.Harmony` | | |
| 9 | goinfer | `towerReserve`, `towerVRAMEstimate` | | | |
| 10 | ken | (the statement names none) | | | |
| 11 | ken | `resolveRepo` | | | |
| 12 | aikit | `LoadFlatI8`, `NewFlatI8` | `ann.LoadFlatI8` | | |
| **all** | | **16 names, 8 tasks** | **+5 names, +2 tasks** | **6** | **4** |

- The table comes from grepping each base commit for the declarations, not from running ken over it. K14's optional check
  below confirms it with ken.
- Bare `Options` in task 1 matches in five packages. The qualified forms narrow that kind of collision.

**The ask.**
- **Record a Go file's package.** Add the `package` clause name to `FileStruct`: Go only, empty elsewhere. The Arm B label
  must not change, and it doesn't if it keeps reading only `Functions`, `CalleeNames` and `Raises`.
- **`A.B`** gives the union of:
  - methods `B` on type `A` (today's behavior);
  - for Go, a top-level function or type `B` in a file whose package is `A`.
- **`A.B.C`**, for Go: method `C` on type `B`, in a file whose package is `A`.
- **Packages that share a name** all match, by name, as today. An `x_test` package is its own name.
- **Shells are not definitions.** `Definition(T)` returns the files that declare `T`, not files that only hold its
  methods.
  - Shells still group methods in `Outline`, with span 0 (K11).
  - `Symbols` and `SymbolsInPath` read `defs`, so they drop shells too. For Go that removes no names, since a type's
    methods live in its package.
- **Qualified callers, where the receiver text allows** (a "should", if cheap):
  - For `pkg.F`, count calls recorded with `Receiver == "pkg"`, plus bare calls (`Receiver == ""`) in files of package
    `pkg`. Leave out `x.F()` on other receivers.
  - Import aliases (`p "…/pull"`) won't match; matching stays by name. Say so.
  - For methods, the receiver is a variable, so calls stay matched by bare name. Say so too.
- **Other languages** are unchanged in this release (Python's `module.func`, for example). Document it.

**Checks.**
- On the fixture above:
  - `Definition("pull.Select")` gives `types.go:5`;
  - `Definition("pull.Model.Size")` gives `methods.go:3`;
  - `Definition("Model")` gives `types.go` only;
  - `Definition("Model.Size")` is unchanged.
- Two packages named `config`, both with `Load`: `config.Load` gives both, and `other.Load` gives none.
- If qualified callers is done: `Callers("pull.Select")` counts `pull.Select(m)` from another package and `Select(m)`
  inside `pull`, but not `r.Select(…)` elsewhere.
- Optional, outside ken's tree, because it reads tack's data: resolve the table's names at their bases and confirm the
  five package-qualified names resolve. Use a throwaway script, not a ken test.
  - The bare mirrors are in `~/tack-runs/e1/mirrors/`.
  - The bases are in `~/tmcode/tack/eval/e1/calibration.jsonl`, and the statements in `calibration-statements.md`.
  - goinfer is large, so take the timing lock shared for it (see Process).

### K15. The tests and the callees of a symbol

**The ask.**

```go
// TestsFor returns the test functions that call symbol: directly (Depth 1), or, when maxDepth >= 2,
// through one non-test function (Depth 2). Sorted by Depth, then File, then StartLine. Matching is by
// name, as in Callers, with K14's qualifier narrowing where it applies.
func (w *Workspace) TestsFor(ctx context.Context, symbol string, maxDepth int) ([]TestSite, error)

// TestSite is one test function and where to run it.
type TestSite struct {
	File      string // "internal/serveapp/banner_test.go"
	QName     string // "TestBanner_contextAndKV"; for a Python method, "TestK.test_y"
	StartLine int
	EndLine   int
	Dir       string // the directory to run it from: for Go, the package directory
	Depth     int    // 1: calls symbol; 2: calls Via, which calls symbol
	Via       string // Depth 2 only: the intermediate function's qualified name
}

// Callees returns the calls made inside symbol's span, in line order.
func (w *Workspace) Callees(ctx context.Context, symbol string) ([]Callee, error)

// Callee is one call made inside a symbol.
type Callee struct {
	Name     string // the callee's leaf name, as CallRef.Callee
	Receiver string // the receiver text; "" for a bare call
	Line     int
}
```

- **Test conventions:**
  - Go: files `*_test.go`, top-level functions `Test*`, `Benchmark*`, `Fuzz*` and `Example*`. Calls inside closures,
    such as `t.Run` subtests, count for the enclosing test.
  - Python: files `test_*.py` and `*_test.py`; functions `test_*`, and methods `test_*` of classes `Test*`.
  - Other languages: an empty result, no error, documented.
- **Depth 2:** a call to the symbol inside a non-test function `f`, then a test that calls `f`.
  - `f` is matched by its leaf name, so a common name (`Run`, `Get`) fans out.
  - Cap the work and the results, and say what the caps are. A missing Depth 2 is better than a slow answer.
- **A name with several definitions** gives the tests of all of them, as `Callers` does.
- **`Callees`** comes from the `CallRefs` whose `EnclosingSymbol` is the symbol's qualified name, in its files. It is what
  K5's callee sites need, and it costs little once K11 is in.

**The runs this serves.**
- Task 2: the oracle test, `TestBanner_contextAndKV`, calls `modelBannerFrom` directly (`banner_test.go:123` at base
  `9f4ecdc3`). So do 11 other tests in that file. Depth 1 finds all 12, and tack runs
  `go test -run '^(TestBanner_contextAndKV|…)$' ./internal/serveapp`.
- Task 11: the oracle test, `TestServer_NoRepoNoDefault_ReturnsValidationText` (`mcp/server_test.go:128`), doesn't call
  `resolveRepo`.
  - `resolveRepo` is called by `handleSearch` and `handleFindRelated` (`mcp/server.go:91` and `:125`). The test reaches
    them through the MCP SDK (`sess.CallTool`), so Depth 2 misses it too.
  - `Dir` is what tack uses there. `go test ./mcp` alone avoids the `internal/modelfetch` failure that `go test ./...`
    hit.

**Checks.**
- A Go fixture: `F`; `TestA` calls `F`; `TestB` calls a helper `h`, which calls `F`; `TestC` calls neither; `TestD` calls
  `F` inside `t.Run`.
  - `TestsFor(F, 1)` gives `TestA` and `TestD`.
  - `TestsFor(F, 2)` adds `TestB`, with `Via` set to `h`.
  - `Dir` is the package directory.
- A Python fixture: `test_x` calls `f`, and the method `test_y` of class `TestK` calls `f`. Both are found.
- `Callees(F)` lists `F`'s calls in order, including one inside a closure.

### Also asked: numbers (the structural half of K8)

- **The structural build:** wall time and peak RSS (`/usr/bin/time -l`) for goinfer, aikit and ken at their current heads.
  Measure on the Mac, cold (no snapshot), as `engine.Open` in bm25 mode plus one `Definition` call, so the model load
  doesn't dominate. Then a full hybrid `Open` of each, for comparison.
- **`Refresh` of one changed file on goinfer:** the search side and the structural side separately, with and without
  `Snapshot`.
- **Where:** ken's choice (`docs/internal/PERF.md`, or a results file), plus the numbers in the final note.
- **What they decide in tack:** whether the eval loop can open ken per run inside the clock, and whether to ask, later,
  for the structural index to be persisted with the snapshot.
- **When:** only on a quiet Mac, with the load average under 2 and no goinfer timed run. Record the load. If the Mac
  isn't quiet today, leave the measurements for the owner to schedule, and say so.

## Constraints on every item

- **No ranking change.** No BM25, embedding, fusion or rerank code changes, and the stored chunks keep their label. So
  the semble NDCG benchmark needs no re-run; say so in the CHANGELOG, as 1.9.0 and 1.10.0 did.
- **The Arm B label stays byte-identical.** The extractors may record more, but `EnrichFromFileStruct` keeps reading the
  same fields. The existing enrichment tests pass unchanged.
- **The snapshot format and its config key don't change.** Nothing here touches the search corpus format. If something
  has to, bump the key per ADR-039.
- **Public changes are additive:** new fields, types, methods and sentinel errors. The only behavior changes are the
  fixes named here: K10's `Text` and `Hash`, K14's shells, and the `Callers` comment. Each gets a CHANGELOG line.
- **Documentation.**
  - `engine`'s doc audit (`TestPublicSurfaceDocumented`) covers new exported names in `engine`.
  - Fields added to `internal/structural` types reach callers through the aliases, so document them on those types and in
    `docs/DEVELOPERS.md`.
- **ken-mcp is optional in this release.** If its `outline`, `references` and `callers` tools show the new lines, the
  JSON changes are additive only (the `*Response` types are 1.0-stable). DESIGN §12 wants the two faces not to drift, so
  a follow-up note is fine if it isn't done now.
- **Pure Go, no new dependencies.**
- **Cost bounds.**
  - Per-file operations (`SymbolAt`, `Outline`, the structural side of a one-file `Refresh`) cost in proportion to that
    file.
  - Lookups (`Definition`, `Callers`, `TestsFor` at Depth 1) cost in proportion to the matches, not the corpus.
  - `TestsFor` at Depth 2 is capped.

## Not asked: tack does these itself

- **Packing hits into a token budget.** ken-mcp's `applyTokenBudget` (`mcp/token_budget.go`) is a short greedy fill over
  the estimator `engine` already exports (`ApproxTokens`, K6). tack packs with it, and trims an over-budget top hit at a
  line boundary itself, which ken-mcp deliberately doesn't. A ken-side budget was on the 10-22 list of ideas before that
  file was read, and is withdrawn.
- **Fuzzy symbol lookup** for `@` completion: tack filters `Symbols(ctx, "")` itself.
- **Exact-text search:** tack's `grep` tool. With K12, tack labels each grep line with its symbol.
- **Sorting an outline by line:** tack sorts once the lines exist.
- **The K5 bundle** (`Symbol(name, with)`): tack composes it from K11, K12, K14 and K15 first, and asks for K5 only if that
  proves awkward.

## Later: separate briefs, not this work

- **Snapshots outside the repo, keyed by content.**
  - tack's eval copies the repo for each run, so a `<root>/.ken` snapshot is never reused.
  - goinfer and aikit don't ignore `.ken/` (only ken's own `.gitignore` does), so a snapshot written into a task's copy
    shows up in `git status`.
  - An `Options.SnapshotDir` and a content key (a git tree, or file hashes) would let a task's 28 runs share one build.
  - Decided after the K8 numbers.
- **Persisting the structural index with the snapshot,** if the K8 numbers show the parse is slow.
- **Go struct fields, package-level consts and vars, and non-struct named types** (`type Mode string`) as definitions.
  - Six of the statements' names are struct fields (the table).
  - It is an extractor change across languages, and must keep the label unchanged.
- **Lines for import and raise references.**
- **Tests in TypeScript, JavaScript and Rust,** which are closure-based or attribute-based.
- **K3, change events** (DESIGN §12). tack needs them for staleness in P2.

## Process for ken's session

- **ken's `CLAUDE.md` governs.** This brief was written in the claude app, which doesn't edit ken. Where the brief is wrong
  about ken's code, ken's reading wins; say so in the note.
- **Read first:** this brief. For context, tack's `docs/design/DESIGN.md` §7.3–§7.5 and §12. Don't edit anything in
  tack's repository.
- **Branch:** a new branch from main at `d63f507`, named as you like. One commit per item, K10 first, with subjects in
  ken's style (`engine: … (K10)`). Commit as ken's history does.
- **No push, tag, release or merge** without the owner's yes.
- **The gate.** GitHub Actions minutes are exhausted, so the local gate is the only CI. Run `GOWORK=off make check`
  before each commit, under the timing lock (below).
- **`GOWORK`.** ken's local `go.work` (gitignored) says `go 1.27.2` and uses `../aikit`. Since `d63f507` requires go
  1.27.3, `go` refuses inside ken while `go.work` is active: "module . listed in go.work file requires go >= 1.27.3,
  but go.work lists go 1.27.2". This work needs no aikit change, so run everything with `GOWORK=off`, which uses the
  pinned aikit, as CI does. Don't edit `go.work`; it is the owner's local setup.
- **goinfer's timing lock.** The Mac also runs goinfer's timed measurements, and another Claude Code session
  (`mac-mem-b`) was measuring on 2026-10-22.
  - Hold the lock shared for anything that loads the machine: builds, `make check`, `go test ./...`, the measurements.
    For example:
    `python3 ~/tmcode/goinfer/scripts/timing_lock.py run --shared --wait 3600 --label ken-k10 -- env GOWORK=off make check`.
  - It waits while a timed run holds the lock, and a timed run waits for you.
  - Never delete the lock file.
  - A single package's `go test`, a few seconds long, can run without it.
- **By day, no benchmarks.** The measurements run only on a quiet Mac.
- **Docs:**
  - the CHANGELOG (Keep a Changelog: Added, Fixed, Changed);
  - ADR-048 in `docs/internal/DECISIONS.md`, for K11–K15, with the alternatives rejected;
  - `docs/DEVELOPERS.md`'s public API section;
  - the `engine` bullet in `CLAUDE.md`;
  - the `engine` package doc.
- **At the end, or if blocked:** write `~/tack-runs/NOTE-from-ken-2026-10-22-k10-k15.md` with:
  - what landed, by commit;
  - what didn't, and why;
  - the K8 numbers;
  - anything in this brief that turned out wrong;
  - the proposed release: version, and the commit to tag.

  Then stop. The owner decides on push, merge and tag.

## How tack will use it, when it resumes

- **`search`** prints `Hit.Text` (K10).
- **Names in the task.** Before the first model call, tack looks up the backticked names in the task or message with
  `Definition` (K14). It adds one line per site, such as "`modelBannerFrom`: internal/serveapp/banner.go:128–282
  (155 lines)" (K11).
- **`read`** over its size threshold returns the outline with lines (DESIGN §7.3; K11).
- **After every edit and write,** tack calls `Refresh` on the path (K13), so the next lookup is right.
- **Step summaries** in the app and the terminal say which symbol each edit and read touched (K12; the mocks).
- **Verification** runs `go test -run` over `TestsFor`'s tests in their `Dir`, or else the package of the edited file
  (K15).
- **The `symbol` tool** (DESIGN §7.3, P2) is composed from the above.
- **The pin** moves from v1.9.0 to the new tag in one commit, with tack's `kensearch` wrapper extended. The eval config
  gets a `[ken]` section only inside a registered screen.

## How tack will check it

- tack's `kensearch` tests against the new tag: `Hit.Text` equals the file's lines, and `Refresh` after a tack edit moves
  the spans.
- A registered C6-style screen with ken in the loop and name lookup, against the current bundle, compared task by task and
  never pooled. Its mechanism metrics:
  - searches before the first edit;
  - reads withheld for size;
  - edit failures where the text isn't in the file;
  - test runs outside the edited package.
- Nothing is adopted without that screen, and without the tier-1 live check that any prompt change needs.

## Until it lands

- tack stays on v1.9.0, with no ken in eval runs.
- K10's stray label line shows only in a `tack run` configured with a `[ken]` section, which no measured run uses.

## Suggested release

- K10 alone as v1.10.1, if the owner wants it early: a fix with no API change.
- K11–K15 as v1.11.0, all additive.
- Or all of it as v1.11.0. It's the owner's call.
