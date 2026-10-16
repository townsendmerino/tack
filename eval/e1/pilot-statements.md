# Pilot task statements — drafts for the owner's review

<!-- citations-at: goinfer 5d597aca, ken 2853c7b, aikit fd12e14 -->

The 10 pilot tasks, chosen by the registered rules with amendment 2
(`docs/measurements/e1-task-selection-preregistration-2026-10-16.md`, verified 2026-10-16). Each statement is drafted
from the named tests' bodies, the signatures those tests call, and the commit message, never from the non-test diff.
The agent sees only the statement, in a repository holding the base commit and its ancestors; the tests are applied
after its run.

**Review state:** the owner approved 1, 2, 3, 4, 5, 6, 7, 9 and 10 as written (2026-10-16); they are in
`eval/e1/pilot.jsonl` with `reviewed_by`. Task 6 is kept because its test checks real skip, count and log behaviour
through the seam, so a do-nothing seam fails it. Task 8 waits on the stub check (selection amendment 3) rather than
being dropped by hand. A rejected statement is rewritten once, and a second rejection drops the task.
**Notes** flag a weak oracle, or a "what is wanted" that sits close to the fix.

The check command for every task is
`GOWORK=off GOFLAGS=-mod=mod CGO_ENABLED=0 go test -json -count=1 -timeout 120s -buildvcs=false -run '^(<tests>)$' <pkg>`,
run from the repository root after the commit's test files are copied in.

---

## 1. aikit `8419cf34` — base `4b9a9b49`

Tests: `TestWeightMat_wrapConstructors` in `./linalg`.

> `linalg` can build a `WeightMat` only by quantizing float32 weights (`QuantizeInt8`, `QuantizeInt4`). A caller that
> already holds quantized arrays, such as weights read from a memory-mapped file, has to rebuild them from float32 and
> copy. Add two constructors to package `linalg` that wrap caller-owned, already-quantized arrays without copying or
> re-quantizing:
>
> - `WrapInt8(q8 []int8, scales []float32, rows, cols int, w8a8 bool) WeightMat`: row-wise int8 with one scale per
>   row, as `QuantizeRowsInt8` returns them;
> - `WrapInt4(q4 []byte, q4s []float32, rows, cols, group int) WeightMat`: group-wise int4, as `QuantizeGroupsInt4`
>   returns them.
>
> A wrapped matrix must behave exactly like the one `QuantizeInt8` or `QuantizeInt4` builds from the same values:
> `MatmulBT` and `Row` give bit-identical results. Its `Int8()` and `Int4()` accessors must return the very slices
> passed in (aliased, not copied) and report `w8a8` and `group` as given. Arrays whose lengths do not match the shape
> must panic.

Notes: a clean interface task.

## 2. ken `3aa62a29` — base `14796618`

Tests: `TestExtractFile_UppercaseExtension` in `./internal/structural`.

> `structural.ExtractFile` returns nil for a file whose extension is upper-case. `ExtractFile("foo.py", src)` extracts
> the Python functions, but `ExtractFile("FOO.PY", src)` finds no grammar. Windows-authored and legacy files such as
> `Foo.PY` or `MAIN.C` lose all their structure. The lookup from extension to grammar should ignore case, so both
> spellings extract the same functions.

Notes: the fix is small (a case-folded lookup), and the statement says what is wrong without saying where.

## 3. aikit `b2a3cb91` — base `6a052365`

Tests: `TestFlatI8_WriteToMatchesMarshal` in `./ann`.

> Saving an `ann.FlatI8` index means calling `MarshalBinary`, which builds the whole serialized blob in memory before
> any of it can be written. Add `func (f *FlatI8) WriteTo(w io.Writer) (int64, error)` (`io.WriterTo`), which streams
> the index to `w`. It must:
>
> - write exactly the bytes `MarshalBinary` returns, for every shape, an empty index included;
> - return the number of bytes written;
> - produce output that `LoadFlatI8` loads back into an index answering queries the same way.

Notes: a clean interface task.

## 4. goinfer `6b4ef501` — base `a9b6b190`

Tests: `TestTowerVRAMEstimate` in `./internal/serveapp`.

> The serve code's VRAM estimate for a vision tower, `towerVRAMEstimate(mt string, d towerDims, maxPixels int) int64`
> in `internal/serveapp`, comes out too high for Qwen2.5-VL. For qwen2.5-vl-3b at the 8192-patch ceiling, the
> footprint measured on the card is 2758 MiB of tower weights plus 270 MiB of scratch per 4096 patches: about 3.46 GB.
> The estimate is about 0.27 GB above that, which costs the text model context on an 8 GB card. Make the qwen2.5-vl-3b
> estimate land between 3.40 and 3.60 GB without changing the estimate for any other model type.

Notes: the measurement in the statement is the commit message's, and the band is the test's. The agent still has to
find which term over-reserves.

## 5. goinfer `09ff5ec4` — base `dc2e1e06`

Tests: `TestPrepare_topPAndSeed` in `./cmd/serve`.

> Two sampling bugs in how the server prepares a request, `(*loadedModel).prepare(sm sampling, promptIDs []int)` in
> `cmd/serve`:
>
> - **`top_p: 0` samples from the full vocabulary.** It should mean greedy decoding: temperature 0 and top_p 0. A top_p
>   strictly between 0 and 1 sets the nucleus as given. A top_p below 0 or above 1 must be rejected with an error.
> - **A request with no seed always uses seed 0,** so "random" sampling repeats from request to request. An omitted
>   seed should be a fresh random (non-zero) seed. A supplied seed is honoured exactly.

Notes: clear behaviour, two small changes in one function.

## 6. ken `5c185feb` — base `267e6a6b`

Tests: `TestParseBudget_ExhaustionSkipsCountsLogs` in `./internal/structural`.

> The structural extractor has a parse time budget. A file whose parse exceeds it is skipped: `ExtractFile` returns
> nil, `ParseBudgetSkips()` counts it, and the logger set with `SetParseBudgetLogf` is called with the path and a
> positive duration. Today that handling can only be tested by setting a tiny wall-clock budget, which is flaky: the
> parser checks its deadline only between parse steps, so a small file can finish first, and the test fails on fast
> machines. Add a test-only seam to `internal/structural`, `parseBudgetForceTimeout` (an `atomic.Bool`), which when
> set makes the guarded extraction take the budget-exhausted path deterministically, with exactly the same skip,
> count and log behaviour as a real timeout.

Notes: the named test fails on the base only because the seam does not exist, so the task is to add a test hook. The
statement has to name it, since that is the interface. It is a weaker task than the others; review whether to keep it.

## 7. ken `3961c347` — base `c03819d0`

Tests: `TestStatementStartLine_AfterMultiLineQuoted` in `./internal/sql`.

> In `internal/sql`, a statement's `StartLine` comes out too low after a quoted string or quoted identifier that spans
> lines. In
> `CREATE TABLE foo (id INT);\nINSERT INTO foo VALUES ('a\nb\nc');\nCREATE TABLE bar (id INT);` the chunk for
> `CREATE TABLE bar` should start on line 5 but reports a lower line. The same happens with a double-quoted identifier
> containing a newline, and the error grows with each such statement before the one being cited. Dollar-quoted strings
> already count their newlines; single- and double-quoted ones must too.

Notes: a real bug with a reproducing example. The example is one of the test's cases, which is "what is wrong", not
how to fix it.

## 8. goinfer `815a513f` — base `420e0fdd`

Tests: `TestWarnThinkingTemplate_noTokenizer` in `./internal/serveapp`.

> When a model's chat template leaves thinking mode on, a thinking-mode target still answers correctly under block
> drafting, but much more slowly, and nothing in any log says why. Add
> `warnThinkingTemplate(lm *loadedModel)` to `internal/serveapp`, called at startup for each loaded model. It writes a
> warning to stderr when the template renders with thinking on by default. It must be safe for a model that has no
> tokenizer: it must not panic.

Notes: **weak oracle.** The one named test only calls `warnThinkingTemplate` on a model with no tokenizer and checks
it does not panic, so an empty function passes. It is in by the registered rules; the pilot records it either way.
Review whether to keep it.

## 9. aikit `38b97c97` — base `afd808c7`

Tests: `TestPreprocess_zeroMaxPixelsUsesDefaultCap`, `TestAveragePool_partialBucketUsesActualOccupancy` in `./vision`.

> Two defects in aikit's `vision` package, both wrong by default and silent:
>
> - **The decompression-bomb guard is off unless set.** `Preprocess` checks `cfg.MaxPixels` only when it is positive,
>   so a `Config` that leaves `MaxPixels` unset (0), or sets it negative, accepts an image of any size. Add an exported
>   constant `DefaultMaxPixels` (the 16-megapixel cap the presets use today), and a method
>   `(Config) maxPixels() int` that returns the effective cap: `DefaultMaxPixels` when `MaxPixels` is 0 or negative,
>   and `MaxPixels` itself when it is positive, however large. Presets such as `Gemma3()` carry `DefaultMaxPixels`.
> - **Partly filled pooling buckets come out too small.** `Gemma4Encoder`'s average pooling divides each pooled bucket
>   by k×k even when the patch grid leaves the bucket only partly filled. A bucket's mean must divide by the number of
>   patches actually present in it.

Notes: two tasks in one commit. The constant and method are named because the first test calls them.

## 10. goinfer `a34ffaa5` — base `a2583664`

Tests: `TestValidateShapes_catchesArchMismatch` in `./decoder`.

> `validateShapes(w *Weights, arch *Architecture) *SerializeError` in `decoder` checks a weight bundle against its
> architecture before decoding. Today it checks only `Embed`, `LMHead`, `Router` and the Q, K, V and Down projections.
> A bundle passes, and decoding then reads or writes past buffers sized from the config, when:
>
> - `OProj`, `GateProj` or `UpProj` has the wrong number of rows;
> - any MoE expert's Gate, Up or Down matrix has the wrong number of rows;
> - the number of layers differs from `arch.NumLayers`.
>
> Reject each of these. The error must name the offending weight (`OProj`, `GateProj`, `UpProj`, `expert 0 Gate`,
> `expert 0 Down` and so on), or say `layer count`.

Notes: the error names are the test's assertions, all under rule 8's 20-character threshold. This is the largest
change of the ten (65 non-test lines).
