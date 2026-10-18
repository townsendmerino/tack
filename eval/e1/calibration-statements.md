# Calibration task statements — drafts for the owner's review

<!-- citations-at: goinfer, ken and aikit at each task's base commit, below -->

**The tasks.** These are the 12 calibration tasks (`eval/e1/calibration-candidates.jsonl`; the rule is in
`docs/measurements/e1-calibration-preregistration-2026-10-18.md` §1), in E1's order.

**How they were drafted.** Under E1's statement rules (`docs/measurements/e1-task-selection-preregistration-2026-10-16.md`,
"Statement and oracle"):

- each statement is drafted only from the named tests' bodies, the signatures those tests call, the commit message,
  and the code at the base commit, which the agent sees;
- the non-test diff was not opened.

**What the agent sees.** Only the statement, in a repository at the base commit. The tests are applied after its run.

**Review state:** drafted by Claude Code, 2026-10-18; **not yet reviewed.** A rejected statement is rewritten once, and
a second rejection drops the task.

**Notes** flag a statement that sits close to the fix, or an oracle that needs care.

**The check command** for every task is
`GOWORK=off GOFLAGS=-mod=mod CGO_ENABLED=0 go test -json -count=1 -timeout 120s -buildvcs=false -run '^(<tests>)$' <pkg>`,
run from the repository root after the commit's test files are copied in.

---

## 1. goinfer `490f760b` — base `f2b90014`

Tests: `TestOptions_everyFlagReachesOptions`, `TestWindowedKV_defaultOnAndOptOut` in `./internal/loadflags`.

> Windowed KV has passed its quality gate and should now be on by default in `serve` and `chat`. Today it is opt-in:
> `--windowed-kv` sets `Flags.WindowedKV`, which `Flags.Options()` passes on as `decoder.Options.ResidentWindowedKV`,
> and it defaults to false. Make it on by default for the flags `Register` installs, so that parsing no arguments gives
> `Options().ResidentWindowedKV == true`. `--windowed-kv=false` must still turn it off.

## 2. goinfer `c7ac10e9` — base `9f4ecdc3`

Tests: `TestBanner_contextAndKV` in `./internal/serveapp`.

> `serve`'s model banner reports the KV cache's precision on its `context:` line (`modelBannerFrom(bannerFacts,
> config)`). Since `--kv` became `auto` by default, a load that is not on the resident path prints "KV auto (lossy)".
> That reports the flag's value as if someone had chosen a precision, over a cache that is actually f32 and not lossy.
>
> Off the resident path, with `--kv auto` (the default), the line should say `KV f32` and not call it lossy. A resident
> load still reports the precision the resident runner actually uses (`bannerFacts.kvPrec`), as it does now.

## 3. goinfer `4dd9fd82` — base `94749c2f`

Tests: `TestTranscodeTimed_reportsBothPhases` in `./internal/prequant`.

> `TestTranscodeTimed_reportsBothPhases` asserts that both of `TranscodeTimed`'s phase spans, `Phases.Stream` and
> `Phases.SelfCheck`, are greater than zero. On Windows, a tiny fixture's self-check finishes inside one clock tick and
> reads exactly zero, so the test fails there.
>
> The test is being rewritten to hold each span open with a pause of its own:
>
> - the stream's, through the existing `wrapSinkForTest` seam (`readback.go`);
> - the self-check's, through a package-level seam that does not exist yet: `var transcodeSelfCheck func(path string)
>   error`. It defaults to `selfCheck`, and `TranscodeTimed` must run its self-check phase through it, timed as
>   `Phases.SelfCheck`.
>
> The test sets the seam and restores it to `selfCheck` afterwards. A transcode that fails during the stream must still
> report a self-check of zero.

**Note:** close to the fix. The task is to add the seam the test needs, and the seam is the interface the test calls.

## 4. goinfer `bbb19952` — base `069a1e3c`

Tests: `TestSelect_exactShardFilenameIsRefusedToo` in `./pull`.

> `pull.Select(files []File, ref Ref)` refuses a quant that resolves to a split GGUF (pieces named
> `…-00001-of-0000N.gguf`): its error names it as a split GGUF and lists the shards. But when the reference names one
> shard's filename exactly in `Ref.File`, as in `pull owner/repo:model-00001-of-00004.gguf`, Select selects it. pull then
> downloads one piece of a checkpoint that no goinfer loader can assemble.
>
> An exact filename that is a shard of a split GGUF should be refused the same way, with the same error. An exact name
> of a single-file GGUF must still select that file.

## 5. goinfer `8d5cd708` — base `76abc5bc`

Tests: `TestBanner_f16KVDecline` in `./internal/serveapp`.

> On CUDA, an f16 KV cache that `--kv` asks for can be held at f32 for a model whose attention has no f16 cache form.
> The decoder records why (`decoder.Model.KVF16DeclineReason()`), but `serve`'s banner only says `KV f32`, so the user
> cannot tell their request was not applied.
>
> The banner's `context:` line should say why:
>
> - add a field `kvF16Decline string` to `bannerFacts`, filled from the model by `factsOf`;
> - when it is set, `modelBannerFrom` puts `-kv f16 held at f32: <reason>` on the `context:` line;
> - the line still says `KV f32` and does not call the f32 cache lossy.

## 6. goinfer `37e72a71` — base `871e46a8`

Tests: `TestFactsOf_aPlainCPULoadDeclinedNothing` in `./internal/serveapp`.

> On a plain CPU load, with no GPU in play, `serve`'s banner says the model declined a GPU-resident load: "a
> GPU-resident load caps it with --ctx, but this model declined one (see decode path)". It comes from
> `bannerFacts.residentDecline`, which `factsOf` fills from `decoder.Model.ResidentDecline()`. The decoder records a
> decline for an ordinary CPU load too.
>
> The banner should report a decline only when a GPU backend was in use and the model did not go resident. A plain
> CPU load should have no `residentDecline` in its facts and no "declined" in its banner, and it must keep the note
> that the CPU path has no `--ctx` cap.

**Note:** the test skips if the decoder stops recording a decline for a CPU load, and a skip fails the oracle. At this
base the decoder does record one, so the test runs.

## 7. ken `376e6096` — base `0a8b44ad`

Tests: `TestPublicSurfaceDocumented` in `./mcp` (a new file, `mcp/doc_audit_test.go`).

> ken's `mcp` and `mcp/db` packages are the 1.0-stable SDK surface (`DEVELOPERS.md`), so every exported identifier in
> them should be documented on pkg.go.dev. Make sure each exported const, var, func, type and method in packages `mcp`
> and `mcp/db` has a doc comment as `go/doc` sees it. For a grouped const or var block, the comment above the block
> counts.
>
> A new test, `TestPublicSurfaceDocumented` in package `mcp`, will parse both directories' non-test files with
> `go/parser` and `go/doc`, and fail for any exported symbol whose doc is empty.

**Note:** this task has no stub check, because its fix changes no function. Verification showed the test failing at
the base (`verified.on_parent`).

## 8. goinfer `866a7ff0` — base `95922889`

Tests: `TestHarmony_stopsAreUpstreams` in `./chat`.

> Through `serve`, gpt-oss replies end after the model's analysis (thinking) channel and never reach the final answer.
> The stream finishes with finish_reason "stop" right after the thinking.
>
> A gpt-oss reply is several harmony messages in one turn, analysis and then final. The harmony template's stop list
> (`chat.Harmony().Stops()`) includes `<|end|>`, which closes each *message*, not the turn.
>
> The stops should be exactly the set gpt-oss-20b's own `generation_config.json` lists as `eos_token_id`:
> `<|return|>`, `<|call|>` and `<|endoftext|>`. They must not include `<|end|>`.

**Note for the owner's decision:** this statement names the three tokens, which the test asserts.

- Without them the task is close to unsolvable. The base stops are `<|return|>` and `<|end|>`, and gpt-oss's
  `generation_config.json` is not in the repository, so `<|call|>` and `<|endoftext|>` cannot be found there.
- With them, the statement says what is wanted almost exactly.
- The alternative is to drop this task from the set, leaving 11.

## 9. goinfer `1bfe3117` — base `50233201`

Tests: `TestTowerReserve_everyDeviceTower`, `TestTowerVRAMEstimate` in `./internal/serveapp`.

> On CUDA, `serve` plans device memory for a vision model's tower, but Qwen3-VL's tower (`model_type` `qwen3_vl`) is
> priced at zero. The decoder takes every free byte, the tower's first allocation fails on the first image, and from
> then on it runs on the CPU: 18.5 s for one image on Qwen3-VL-2B.
>
> Qwen3-VL's vision tower is the Qwen3.5 tower, with the same serve cap of 1024 tokens. Price it:
>
> - `towerVRAMEstimate("qwen3_vl", dims, 0)` should be about 1.55–1.75 GB for Qwen3-VL-2B's vision config (depth 24,
>   hidden 1024, intermediate 4096, patch 16, spatial merge 2);
> - on a CUDA build, `towerReserve` reserves memory for it, as for every other device tower;
> - an unknown model type still prices at zero.

**Note:** "the Qwen3.5 tower" comes from the commit message and points toward the fix. Without it, the 1.55–1.75 GB
range can only be reached by working out the tower's cost from scratch.

## 10. ken `4aed5e90` — base `3e06867c`

Tests: `TestBuildIndex_HappyPath` in `./cmd/ken`.

> `ken build-index` writes its index, for example `.ken/index.bin`, through a temp file and a rename. The file it
> leaves is mode 0600, readable only by its owner, because of how the temp file is created.
>
> The index is an operator artifact, baked into container images and `//go:embed`ed. A container built as root and run
> as another user cannot read it, and silently falls back to the full cold build the prebuilt index exists to avoid.
>
> `ken build-index`'s output should be readable by group and others (0644).

## 11. ken `1416be9f` — base `ac71bae4`

Tests: `TestServer_NoRepoNoDefault_ReturnsValidationText` in `./mcp`.

> The error `resolveRepo` (`mcp/server.go`) returns, when a tool call names no `repo` and the server has no default
> index, breaks Go's style for error strings: it starts with a capital letter and ends with punctuation, though error
> strings are wrapped and combined with others.
>
> Make it follow the style, not capitalized and with no trailing punctuation, keeping what it says. A tool call without
> `repo` on a server with no default must still be answered as text, not as a protocol error.

## 12. aikit `4c062ede` — base `773ab509`

Tests: `TestLoadFlatI8_rejectsInconsistentShape` in `./ann`.

> `ann.LoadFlatI8` accepts a FlatI8 blob whose header says `n = 0` vectors but a nonzero `dim`. The payload-size check
> passes because `n*dim` is 0, so it returns an "empty" index carrying a dimension of about 2.1e9, which could drive a
> huge query-vector allocation later. The fuzzer found it.
>
> A blob should describe either an empty index, with `n = 0` and `dim = 0` as `NewFlatI8(nil)` marshals, or one where
> both are nonzero. `LoadFlatI8` should return an error for any other shape.
