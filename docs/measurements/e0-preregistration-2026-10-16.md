# E0 — model survey: pre-registration

<!-- citations-at: written before the runner; the runner's commit is cited in each result record -->

**Registered 2026-10-16, before any E0 run.**

**Amendment 2026-10-16, still before any timed run.** The TTFT sizes were 4096, 8192 and 16384, each skipped above
the window minus 1024. That never times a 16384-window model above 8192, which is short of the full-window turns P3
grades. The sizes are now 4096, 8192 and the window minus 1024. No other part changed.

**Amendment 2, 2026-10-16, before any timed run.** Two Mac arms added: goinfer's curated `demo:0.5b` tier
(Qwen2.5-Coder-0.5B) and gemma-4-E2B, both used by goinfer's `demo/agent`. The floor moves from the 1.5B to the
0.5B, the model least likely to pass. With the 1.5B as the floor, a good 1.5B result would have voided the smoke set
rather than counted. The 1.5B (`demo:1.5b`) is now an ordinary candidate.

**Amendment 3, 2026-10-16, after nobara's first four arms and before any Mac run.** Three changes, each with its
mechanism:

- **The goinfer build is main.** Every arm is now served by goinfer main `0e598d3f`. The binaries until now were
  `41e13b26`, the head of the unmerged `op3-seams-2026-10` branch (merge-base with main `c0a3cc1c`). That branch rewrites
  the forward pass of Granite-4.0-H and Gemma 4, both E0 arms, and lacks 25 of main's commits. It was used because
  that is where the checkout happened to be, not by choice. nobara's runs on it (the 7B, granite, phi3 and the
  killed gpt-oss) are kept in `~/tack-runs/e0/2026-10-16-night` on nobara, named as superseded, and graded against nothing.
- **A GPU arm served on another backend fails at fit.** goinfer's planner admitted gpt-oss-20b at 16384 on the
  8 GB card. goinfer then hit a CUDA out-of-memory error and fell back to the CPU, decoding at 7.8 tok/s, and had
  produced no TTFT cell after 29 minutes when the owner had it stopped. The fit phase now also records the
  banner's `decode path:`. When an arm asked for `-backend cuda` or `metal` and the decode path names another
  backend, the arm records `fell_back` and a **fail** verdict, and nothing else is measured for it
  (`internal/eval/runner.go:fellBackFrom`).
- **nobara gets the Mac's small arms.** gemma-4-E4B, Qwen2.5-Coder-1.5B, gemma-4-E2B and the 0.5B floor are added
  on CUDA, so each box's default is chosen from the same candidates. gpt-oss-20b and gemma-4-26b stay in the
  manifest but run last, where the fallback rule records them in minutes.
- **Order.** On the Mac, the 7B, coder-1.5B and phi3 jobs on 41e13b26 had finished (14:58, 15:07 and 15:18) and
  gemma-4-E4B was mid-run when this amendment was committed (15:29:53, `352fba0`). When the build question was put to
  the owner, the Mac was said, wrongly, to have run nothing; the queue status listing those jobs was first read at 15:30,
  after the commit, and no Mac result file has been opened. Before the amendment, on nobara, only the queue's status
  lines (job, exit, minutes) were read, plus the owner's report of the gpt-oss run (CUDA out of memory, the CPU at
  7.8 tok/s, no TTFT cell in 29 minutes), which is the fallback rule's mechanism. After it, at 15:35, granite's
  decode-phase heartbeats on 41e13b26 were read to see which backend it had used. The amendment's reason is the
  clean build of main, not any result. The superseded E4B run on 41e13b26 was left to its timeout.

**Amendment 4, 2026-10-16, after the Mac's run on main** (owner). **An arm that was not measured is void, and a run
with a void arm is not a pass.** The mechanism: the Mac's phi3 arm asked for Metal, goinfer declined Metal for its
`ActQuantGroup 32` and served it on the CPU, and the arm recorded **fail** under amendment 3 and stopped. The job still
exited 0, so goinfer's `night.py` listed it as ok, and only `results.jsonl` showed that it had no data. Before this
amendment, goinfer's note on that run (`~/tack-runs/e0/NOTE-from-goinfer-2026-10-16-gemma4-metal-ttft.md`) and the
phi3 arm's records were read; the reason is the exit status, not any arm's numbers.

- **Two kinds of no.** A **fail** is a result: the arm ran on the decode path it names and goinfer refused every load,
  or the window, `check` or smoke failed the rule below. A **void** is no result: goinfer decoded on another path than
  the arm names, memory pressure did not settle, a load or request got no answer, `check` did not finish, a timing
  repeat errored, or the run was stopped before or during the arm. A server that answers with an error, such as a 400
  for a missing tool-calling template, has answered.
- **The path is named, not inferred from the backend.** Each arm names its decode path (`decode_path` in the manifest,
  by default `metal-resident` or `cuda-resident` for a GPU and `cpu` for the CPU), and goinfer's banner must begin with
  it. A banner with no decode path is void. This replaces amendment 3's prefix match on the backend
  (`internal/eval/runner.go:VoidPath`).
- **The verdict line.** `results.jsonl` ends each arm with one `verdict` record: the grade, or `void` with the reasons and
  any grade reached before the gap. `tack eval models` ends with one `VERDICT:` line: **PASS** when every arm was
  measured, whatever its grade, and **INCONCLUSIVE** with a non-zero exit when any arm is void, naming each.
- **No grade moves.** A void arm, like a failed one, is never eligible and never the default. The amendment changes what
  a run reports, and a void arm is re-run before its numbers count.
- **Applied to the Mac's run of 2026-10-16** (goinfer `0e598d3f`). The phi3 arm is void on Metal. Its four failed `check`
  rows (three 400s for a missing tool-calling template, and a long prompt that produced no tokens) were taken on the
  CPU, and a write-up reports them as observations, never as the Metal arm's result. The gemma-4-E2B and E4B arms
  are re-run only after goinfer reports its P28 fix (goinfer `docs/queue-performance.md`); their prefill on this build is
  a goinfer defect, not a property of the models.

**Amendment 5, 2026-10-16** (owner). **phi3-mini-4k is retired on both boxes.** goinfer answers every tool request
for it with a 400, "this model has no tool-calling template" (three of its `check` rows on the Mac), so it cannot be
tack's model on any backend, and its 4,096 window already kept it from being eligible. Its arms leave both manifests.
Its records stay as observations, graded against nothing: the Mac's run on main (`~/tack-runs/e0/2026-10-16-main/mac`,
void under amendment 4), the void re-check at tack `f209e4b` (`~/tack-runs/e0/2026-10-16-phi3-void-recheck`), the
superseded Mac run on 41e13b26 (`~/tack-runs/e0/2026-10-16-night/mac`), and nobara's runs. No Metal support for its
quantization is sought.

**Amendment 6, 2026-10-16** (owner). **Fit probes.** gpt-oss-20b and gemma-4-26b do not fit nobara's 8 GB, and
goinfer serves each on the CPU. Under amendment 4 that made both void every night, and an INCONCLUSIVE that is always
there teaches everyone to ignore INCONCLUSIVE. A manifest can now mark an arm as a fit probe with its expected outcome
(`expect_fallback = "cpu"`): the arm stops at the fit, and falling back to that path is its measurement, recorded as
`fallback`, so the run passes. Anything else is reported and the run is INCONCLUSIVE: a crash, a timeout, a refusal of
every load, another decode path, or a load that now fits the GPU. The last is good news and is labelled so; it means
the arm can become a measured one. Three nobara arms are probes. gpt-oss and gemma-26b are probes because of memory.
granite is a probe because of goinfer's gate, not memory: goinfer's resident runner excludes its architecture
("bring-up gate (GOINFER_SSM_RESIDENT unset)"), so it falls back although it fits. An `unexpected` on granite means
the gate has opened; check goinfer's change log, then make it a measured arm.

**Clarification, 2026-10-17, before the Gemma 4 re-run** (not a rule change). **E0's prompt and tool schemas are
frozen as they were sent.**

- **Why.** The smoke and TTFT requests sent tack's own `loop.SystemPrompt`, its `read`/`glob`/`grep` schemas and
  `loop.UserText` "exactly as `tack run` sends them". P1 changes those, starting with the tool-path rules in its step
  1. Re-running an arm after that would grade it against a different instrument from the arms already run.
- **What changed.** The three are now frozen copies in `internal/eval/instrument.go`, byte-identical to what every E0
  run so far sent (tack `352fba0`).
- **How that was checked.** A test at the freezing commit (`c4b9457`) compared them with tack's own, and
  `TestInstrumentIsFrozen` holds them to their sha256 from then on.
- **What it does not change.** The tasks, the scoring and the rule. "As `tack run` sends them" now means as it sent
  them when E0 was registered.

**Amendment 7, 2026-10-17 — approved by the owner the same day,** with the arms settled as recorded under "Approved"
at its end. **Coder-tuned 7–8B models on nobara.**

- **Why.** The E1 pilot re-run of 2026-10-17 passed 0 of 20 with the incumbent, Qwen2.5-7B-Instruct, though every
  tool call parsed and ran. 13 runs answered in prose instead of editing (`e1-pilot-2026-10-17.md`; DESIGN §17, R13).
  The owner asked for coder-tuned models that goinfer can run on nobara's 8 GB card.
- **The arms**, all nobara, `cuda`, `q4_k_m`, read from `~/models`:

  | checkpoint | architecture | why |
  |---|---|---|
  | Qwen2.5-Coder-7B-Instruct | qwen2 | **required** (owner): the incumbent's architecture and size, coder-tuned |
  | Seed-Coder-8B-Instruct | llama, per its model card | a coder-tuned 8B of another family |
  | OpenCoder-8B-Instruct | llama, per its model card | a coder-tuned 8B of another family |

  - The two 8Bs are the owner's to keep or drop. goinfer serves `qwen2` and `llama`; the architecture is read from
    each GGUF's header at the fit. An architecture goinfer refuses makes the arm a recorded refusal, not a fail.
  - Not included: deepseek-coder-6.7b-instruct. Its 16k native window is below the 32768 the pilot's opencode needs,
    and its full multi-head KV is the costliest of the candidates.
- **Download** (the network use the owner asks for). Into `~/models` on nobara, from the publisher's own GGUF where
  one exists (Qwen's for Qwen2.5-Coder), otherwise a named quantizer. Each file's source URL, revision and sha256 go
  in the arm's record before its fit.
  - `~/models/qwen2.5-coder-7b-instruct-gguf-dl/` holds no model today, only a download's cache. It is not reused.
- **Fit check first, by day,** with `tack eval models -fit-only`, as the run discipline below already requires. It
  runs only with the timing lock free and no night runner going.
  - It records E0's sizes, 16384 then 8192, and additionally whether 32768 is admitted, which the pilot's opencode
    needs. The 32768 row informs the pilot and is not part of E0's rule.
  - An arm the fit does not admit at 8192 stops there, recorded. It is not re-shaped to fit.
- **Graded by the registered rule, unchanged:** fit, `check`, smoke at least 17 of 20 on both runs with 0 malformed,
  then the timed phases at night under `night.py`. The incumbent stays the do-nothing arm.
- **What E0 cannot see here.** E0's smoke is one request per task and scores the tool chosen. The incumbent already
  makes good single calls, and R13 is about what a model does over a whole task. So E0 screens these arms for fit,
  speed and clean calls, but cannot say whether one edits.
  - *Proposed:* an eligible coder arm becomes nobara's default only after the pilot, re-run with that model beside the
    incumbent under a new pilot amendment, shows it editing.
  - Until then, E0's "highest smoke score" rule ranks it, but the owner decides any change of default.
- **Approved (owner, 2026-10-17):**
  - **Qwen2.5-Coder-7B-Instruct:** required, as written.
  - **Seed-Coder-8B-Instruct:** kept.
  - **OpenCoder-8B-Instruct: dropped**, as deepseek-coder was, because its native window is under 32768. Its
    `config.json` at `infly/OpenCoder-8B-Instruct@01badbbf` has `max_position_embeddings: 8192`, and its model card
    lists "Sequence Length: 8K".
  - Both kept models declare 32768 natively (`max_position_embeddings`): Qwen2.5-Coder-7B-Instruct at `c03e6d35`
    (`Qwen2ForCausalLM`, 28 layers, 4 KV heads) and Seed-Coder-8B-Instruct at `1c5af72a` (`LlamaForCausalLM`,
    32 layers, 8 KV heads).
- **Downloads, 2026-10-17 by day,** into nobara's `~/models`. Each was fetched at a pinned revision and checked
  against the sha256 Hugging Face publishes before it was renamed into place
  (`~/tack-runs/e0/download-coder-2026-10-17.{sh,log}`).

  | file | source | sha256 |
  |---|---|---|
  | `qwen2.5-coder-7b-instruct-q4_k_m.gguf` (4,683,073,536 bytes) | Qwen's own, `Qwen/Qwen2.5-Coder-7B-Instruct-GGUF@13fb94bf` | `509287f78cb4d4cf6b3843734733b914b2c158e43e22a7f4bf5e963800894d3c` |
  | `seed-coder-8b-instruct-q4_k_m.gguf` (5,071,410,592 bytes) | `mradermacher/Seed-Coder-8B-Instruct-GGUF@852502f4`, a static llama.cpp quant | `6d1496eee941b58266e4d73581de963d0f69411aa433801a7ad29efd66e3d99f` |

  ByteDance publishes no GGUF. mradermacher's static quant was chosen over unsloth's because unsloth labels its files
  "Dynamic 2.0", a different quantization recipe from the static `q4_k_m` the incumbent and Qwen's file use.
- **Fit manifest:** `~/tack-runs/e0/manifests/nobara-coder-fit-a5a786e.toml` on nobara.
  - It holds E0's two arms and a `-32k-probe` arm per model with `ctx = [32768]`. The probes are not E0 arms.
  - It uses E0's pinned goinfer build, `0e598d3f`, so these arms compare with the incumbent's E0 rows.
- **Fit checks, 2026-10-17, 16:25–16:26 by day.** tack `295d1aa`, with the timing lock free, no runner going and the
  GPU idle. Output: `~/tack-runs/e0/fitcheck-coder-2026-10-17` on nobara.

  | arm | `-ctx` | verdict | decode path | KV beside weights |
  |---|---|---|---|---|
  | `qwen2.5-coder-7b-q4km-cuda` | 16384 | admitted | `cuda-resident (int4)` | 0.9 GB beside 4.0 GB |
  | `seed-coder-8b-q4km-cuda` | 16384 | admitted | `cuda-resident (int4)` | 2.0 GB beside 4.3 GB |
  | `qwen2.5-coder-7b-q4km-cuda-32k-probe` | 32768 | admitted | `cuda-resident (int4)` | 1.8 GB beside 4.0 GB |
  | `seed-coder-8b-q4km-cuda-32k-probe` | 32768 | not on the GPU | `cpu (int4)` | needs 4.29 GB; 3.21 GB free |

  - **Both E0 arms are admitted at 16384 on the GPU** and go on to E0's `check`, smoke and timed phases.
  - **Seed-Coder-8B cannot serve 32768 on nobara's card.** goinfer's own message: "resident context 32768 positions
    needs 4.29 GB of KV … but only 3.21 GB is free on the device beside the weights". goinfer fell back to the CPU,
    and the runner marked that probe void, hence the run's INCONCLUSIVE.
  - So Seed-Coder can be an E0 arm but cannot take the pilot's opencode, which needs 32768, on this card. For the
    pilot, only Qwen2.5-Coder-7B qualifies.

**Amendment 8, 2026-10-17, after the coder arms' night run** (owner). **Seed-Coder-8B-Instruct is retired, and
Qwen2.5-Coder-7B-Instruct's fail is recorded with its cause** (`e0-nobara-2026-10-17.md`).

- **Seed-Coder-8B-Instruct is retired** on the same ground as phi3-mini-4k (amendment 5). Its GGUF carries no
  tool-calling template, and goinfer refuses every tool request for such a model (`400 this model has no
  tool-calling template`), deliberately and permanently.
  - Its run is void: smoke 0/20 in 11 ms, and every TTFT request refused. It stays on record as that.
  - Its decode rate, 80.6 tok/s, is kept as a speed row only.
  - It leaves `eval/e0/nobara.toml`. It returns only if the owner asks goinfer for an opt-in template.
- **Qwen2.5-Coder-7B-Instruct fails** by the registered rule: smoke 2 of 20 on both runs, 0 malformed, `check` ok.
  - **The finding.** It calls a tool when offered one, but not when offered several. goinfer's `check`: with one tool
    (`get_weather`) it calls it in two turns; with the 12-tool schema it answers without calling, on
    `/v1/chat/completions` and `/v1/messages` alike.
  - In E0's smoke, with three tools offered, it called none on any task. Its two passes are the two tasks where the
    right answer is no call.
  - Its template is not the cause: the same tool section as the incumbent's, which calls among 12 and scores 18/20.
  - Its speed equals the incumbent's (85.6 tok/s against 85.7).
  - Its run was on E0's goinfer `0e598d3f` with E0's default `tool_choice` (auto), under which goinfer forces no call.
    The result stands as the arm's grade. Re-running it on a newer goinfer is the owner's call, and would be its own
    amendment made after this data.

**Amendment 9, 2026-10-18: E0's timing probes changed** (owner).

**When it was recorded.**

- The code was committed at 14:13 PDT in `38137f5` and `14e80de`, and pushed before C3's first run (14:14:30).
  `e1-calibration-preregistration-2026-10-18.md` §8 and §9 recorded the change before that run, also at 14:13.
- **This amendment, in E0's own record, was written after C3 started.** That is a gap in how it was recorded: the
  change should have been registered here first.

**What changed.**

- **The first-token probe** (`internal/eval/runner.go:firstToken`) allows 16 tokens instead of 1. It times the first
  streamed delta of text *or thinking*, and records which it was (`first_kind`). Before, a reply whose first token never
  streams (Harmony's channel header) read "no token was streamed".
- **The decode probe** (`decodeOnce`) times text and thinking alike. It now refuses, with an error rather than a rate,
  a reply that counted more tokens than it streamed characters. It records the characters streamed as text and as
  thinking.
- **Both probes send each arm's registered thinking setting** (`Arm.ProbeThinking`, `probe_thinking` in the manifest).
  - By default they send thinking disabled, as the smoke and every tack request do (`client.go:toWire`). goinfer reads
    that as thinking off.
  - An arm sets `probe_thinking` only for a model whose reasoning cannot be turned off (Harmony). Its probes then ask for
    thinking, which turns it on, so the reasoning streams instead of being dropped.
  - For amendment C3's five models it is off.
- **The smoke, the check, the fit and the footprint are unchanged.**

**Why.** C2 (gpt-oss-20b) read "no token was streamed" on every first-token request, and a decode rate six times its
by-day reading. Both came from these probes counting tokens that never streamed. The decode audit is in
`e0-nobara-2026-10-17.md`.

**Results before and after this amendment are not directly comparable.**

- A first-token time measured after it may include a few decode steps, up to 16 tokens' worth at most, while before it
  measured to a single token.
- A decode rate measured after it covers only replies that streamed what they counted. Before, a rate could include
  tokens that never streamed. The audit found no such case in the figures that stand, but they were judged by
  mechanism, not recomputed.
- Comparing a model across the line therefore needs a re-measurement on the new probes. The table for C3's models
  holds only post-amendment results.

**Amendment 10, 2026-10-19: E0 for Mellum2.1 on goinfer b2b5e816** (owner: "Mellum2.1 E0 now, with
-moe-cache-experts on ~/serve-cuda-b2b5e816, thinking setting stated"). Registered before the run.

- **Why.** Mellum2.1 is not out (`e1-model-candidates-2026-10-18.md`, correction of 2026-10-19). Its night load lacked
  `moe-cache-experts`, so the resident build was declined and the load fell back to CPU. With its experts streamed it
  may be resident.
- **The arm:** `mellum2.1-thinking-cuda-moecache`.
  - Model: `~/models/mellum2.1-thinking` (JetBrains/Mellum2.1-12B-A2.5B-Thinking, bf16, a mixture of experts: 12B in
    total, 2.5B active). goinfer serves it from its own int4 bundle.
  - Server: goinfer-serve `b2b5e816` (`~/serve-cuda-b2b5e816`), copied to `~/tack-runs/bin/goinfer-serve-cuda-b2b5e816`
    (sha256 `231c6e30…`).
  - Flags: `-backend cuda -moe-cache-experts -thinking off`.
  - tack: `9f4e0e0`, on E0's amendment-9 probes.
  - Manifest: `eval/e0/nobara-mellum-b2b5e816.toml`.
- **Thinking: off.** It is enforced three ways:
  - tack sends thinking disabled on every request: the smoke, the probes and A1;
  - goinfer-serve runs with `-thinking off`;
  - goinfer manages Mellum2.1's template (its `enable_thinking` control and history rule), so "off" renders the closed
    empty think block, as Mellum2.1's own template does.

  `probe_thinking` stays unset, because this model's reasoning can be turned off.

  **Why off:** A1 forces every call, and goinfer renders a forced call with thinking off. So an E0 measured with
  thinking on would measure a mode the calibration cannot use. Mellum2.1 thinks by default, so the setting is stated
  rather than left to the template.
- **Context:** one fit load at 32,768 first.
  - If it comes up `cuda-resident`, E0 runs at 32,768.
  - If not, one fit load at 16,384, tack's design window. If that is resident, E0 runs there.
  - If neither is resident, Mellum2.1 is out on this card, and the record says so.
  - No other context is tried. The fit load's `decode_path`, `fit` line, VRAM and `forced_tool_call` are recorded.
- **The rule is unchanged:** the smoke 2×20, eligible at 17/20 or better on both runs with 0 malformed calls; then the
  probes.
- **Predictions, before the run:**
  - **resident at 32,768:** 0.5. The night load's numbers are 1.07 GB of KV at 32k against 0.68 GB free with every
    expert resident. Streaming experts frees room, but goinfer's cache shrinks to what is left.
  - **resident at 16,384:** 0.95. goinfer's Gate 2 ran it resident there on 2026-10-09.
  - **eligible:** 0.7. Gate 2's harness-scale row passed on the template-aware build.
  - **forced-call mode:** grammar (its JSON tool format).
  - **cold first token:** about 170 s at 16k, at goinfer's measured 97 tok/s cold prefill. Whether a reused prefix is
    reused is not predicted: 21 of its 28 layers are sliding-window.
- **Time:** the fit loads take about 2 minutes, and E0 about 15–30 minutes (45 if the prefix is never reused). It holds
  nobara's timing lock for this amendment only and releases it at the end.

**Amendment 11, 2026-10-19: the reused-prefix probe appends** (owner). Registered now. It applies from the next E0 run,
and nothing is re-run for it.

- **What changes.** The first-token probe's reused cell (`internal/eval/runner.go:ttft`) now **appends to the previous
  request**, as tack's loop does between compactions (CLAUDE.md, "Append-only context").
  - Before, it was the previous prompt with its last 512 tokens rewritten.
  - The appended text is the same length the rewrite used (512 tokens), so the work after the reused prefix is
    comparable.
- **The 512-token rewrite stays as a recorded secondary figure, outside the gate.** It shows how much a server can roll
  back. It is reported beside the append figure, never in its place. E0's gate (the smoke) is unchanged, and neither
  figure decides eligibility.
- **Why.** The rewrite read "never reused" on Mellum2.1 (amendment 10) for a cause that does not touch appends.
  - On an expert-cache load, goinfer grants Mellum2.1's sliding-window layers no extra rollback, so a request may
    diverge only about 448–512 positions back. The rewrite diverges about 518 back.
  - goinfer's logged probe confirms that reading (owner, 2026-10-19; goinfer P34, reopened at `6ce495c4`).
  - So the rewrite measured something tack's loop does not do.
- **Results across the line are not comparable.** Every "prefix reused" figure recorded before this amendment is a
  rewrite figure:
  - C3's table;
  - `e0-nobara-c3-2026-10-18.md`;
  - `e0-nobara-mellum-2026-10-19.md`.

  A model is compared across the line only by re-measuring it.
- **Tests (tier 0, each broken once, with the code):**
  - the append cell's request is the previous request with a message added at its end, byte for byte;
  - the rewrite cell is unchanged and recorded under its own name;
  - the gate reads neither.

DESIGN §15.1 defines E0. This page fixes the arms, the instruments, the
repeat counts and the decision rule. A rule changes only with a mechanism, recorded here as a dated amendment above
the results. Results go in `e0-<box>-<date>.md` beside this page, graded against this rule.

## Question

Which local models can run tack's loop on each of the owner's machines, how fast, and in how much memory? E0 sets:

- the default model per machine;
- the window tack plans for on that machine;
- the model-registry rows (DESIGN §6.4).

E0 does not rank models that are close to one another. It is a gate with a tie-break, not a comparison with power.

## Arms

All models are read from `~/models` on the measuring machine, never the archive (goinfer `docs/benchmarks.md`, "Model
storage"). Every arm is served by `goinfer-serve` from one pinned goinfer revision, built once into a durable path.

| box | backend | checkpoint (in `~/models`) | role |
|---|---|---|---|
| Mac (M1 Pro 16 GB, macOS 26) | metal | `qwen2.5-7b-instruct-q4_k_m.gguf` | the 7B that works on nobara, on the Mac |
| Mac | metal | `gemma-4-e4b-gguf/gemma-4-E4B_q4_0-it.gguf` | the size between 3B and 7B, never tried for agent use |
| Mac | metal | `qwen2.5-coder-1.5b-instruct-q4_k_m.gguf` | goinfer's `demo:1.5b` tier |
| Mac | metal | `gemma-4-e2b-gguf/gemma-4-E2B_q4_0-it.gguf` | the small gemma `demo/agent` runs |
| Mac | metal | `qwen2.5-coder-0.5b-instruct-q4_k_m.gguf` | goinfer's `demo:0.5b` tier, and the floor: the model least likely to pass the smoke set. If it passes, the set is too easy |
| Mac | metal | `phi3-mini-4k-gguf/Phi-3-mini-4k-instruct-q4.gguf` | retired (amendment 5); was a DESIGN §8.4 candidate with a 4,096 window, never eligible |
| Mac | cpu | `qwen2.5-7b-instruct-q4_k_m.gguf` | the CPU path that did not fit at q3_k_m (opencode.md); fit decides again |
| nobara (RTX 2070 SUPER 8 GB) | cuda | `qwen2.5-7b-instruct-q4_k_m.gguf` | **the incumbent**: the one configuration measured working |
| nobara | cuda | `gemma-4-E4B`, `qwen2.5-coder-1.5b`, `gemma-4-E2B`, `qwen2.5-coder-0.5b` (the floor), as on the Mac | amendment 3: the same small candidates on both boxes |
| nobara | cuda | the granite checkpoint, `gpt-oss-20b-MXFP4.gguf`, `gemma4-26b-q4_k_m.gguf` (`phi3-mini-4k-gguf` retired, amendment 5) | DESIGN §8.4 candidates; fit decides which load. An arm goinfer serves on the CPU is void (amendment 4), except a fit probe expected to fall back: all three, gpt-oss and gemma-26b for memory, granite for goinfer's architecture gate (amendment 6) |

The **do-nothing arm** is the incumbent: keep the model on nobara. A Mac model replaces it only under the rule below.

Same weights on Ollama, for reference: Ollama is not installed on the Mac. The reference arm is recorded as not run;
it informs nothing in the rule.

## Instruments

`tack eval models` (`internal/eval`) runs each arm through these phases, in order. Each phase writes JSON lines to the
run's output directory and a heartbeat to its log at least every 30 s.

1. **fit.** Start `goinfer-serve` with an explicit `-ctx`, first 16384 and then 8192, and load the model. goinfer's
   own planner admits the load or refuses it; this is goinfer's plan, not arithmetic. Recorded per arm: the largest
   admitted `-ctx`, the banner's `fit:` line, its `decode path:`, and `/v1/models` `context_window`. An arm that asked for a GPU and is decoding
   elsewhere fails here and stops (amendment 3). If both are refused, or the window is
   under 8192, the arm stops after smoke.
2. **check.** `goinfer-serve check` against the running server. Recorded: its exit status and full output.
3. **smoke.** 20 tasks built from tack's own tool schemas: P0's `read`, `glob` and `grep`, with `loop.SystemPrompt`,
   exactly as `tack run` sends them. The tasks are `internal/eval/smoke.go:SmokeTasks`. Each task is one request,
   greedy (temperature 0), max 256 output tokens, and only the first response is scored:
   - **pass** when the response calls an expected tool, whose input decodes, carries the required fields, and contains
     the expected argument text (case-insensitive, backslashes ignored);
   - for the two no-tool tasks, **pass** when the response calls no tool;
   - **malformed** is counted separately: a call to a tool that was not offered, or input that is not a JSON object.

   The whole set runs twice. A task that scores differently across the two runs is reported as unstable.
4. **ttft.** Time to the first streamed token, at prompts of 4096 and 8192 tokens and at the window minus 1024 (15360
   for a 16384 window), skipping any size above the window minus 1024. Each prompt is tack's system prompt and tools, plus repository text sized with the server's own
   `count_tokens`. Two cells per size:
   - **cold**: a unique nonce at the very start of the system prompt, so nothing is reused;
   - **reused**: the same prompt as the previous request except its last 512 tokens, so all but the tail is a cached
     prefix.

   Three repeats per cell after one warm-up. Median, minimum and maximum are reported.
5. **decode.** 256 output tokens requested, three repeats. The rate is output tokens over the time from first to last
   token. A reply that stops early is reported with its token count.
6. **footprint.**
   - The child's peak resident set from `getrusage` after it exits. This is RSS, which reads low under memory pressure
     on Darwin, so it is labelled RSS and read only as context.
   - goinfer's `resident_bytes`.
   - On the Mac, the highest `kern.memorystatus_vm_pressure_level` seen during the arm.
   - On CUDA, peak `nvidia-smi` memory used, sampled every second.

On the Mac, each arm starts only at memory-pressure *normal*. An arm that finds *warning* or worse is recorded as
`skipped: pressure` and is not retried that night.

## Decision rule

Per arm, on its machine:

- **Eligible**:
  - fit admits a window of at least 8192;
  - `check` exits 0;
  - smoke scores **at least 17 of 20** on both runs, with **0 malformed** (goinfer constrains calls).
- **Parked**:
  - smoke scores 14 to 16 on either run, or exactly one `check` row fails.
  - A parked model is usable by explicit choice, never as a default, and is re-run after a goinfer or model change.
- **Fail**: anything else.

**The Mac's default.** The model stays on nobara (the incumbent) unless a Mac arm meets all of these:

- it is eligible;
- its reused-prefix TTFT at 8192 is at most **2.0×** the incumbent's, medians compared;
- its decode rate is at least **10 tok/s**.

Among Mac arms that pass, the highest smoke score wins; a difference of one task counts as a tie, broken by the lower
reused 8192 TTFT. A Mac arm within 10% of either speed bar is **parked**, not passed: the bars come from no
measurement, so the band around them is a judgement, and a re-run decides.

**nobara's default.** The eligible arm with the highest smoke score, ties broken as above. If no arm is eligible, the
incumbent stays and that is recorded as a finding.

**The planned window.** The default model's largest admitted window from {16384, 8192}.

## Why these numbers

- **17 of 20**: one or two misses are noise from task wording, while three or more is a model that will derail a loop.
  Binomial chances of one run scoring at least 17 of 20, by the model's true pass rate:

  | true rate | 70% | 80% | 85% | 90% | 95% |
  |---|---|---|---|---|---|
  | P(at least 17) | 0.11 | 0.41 | 0.65 | 0.87 | 0.98 |

  So the bar mostly admits models at 90% or better and parks the 80–85% band. E0 accepts that; E1 is where pass rates
  get power (DESIGN §15.3).

  The runs are greedy, so the second run is not independent evidence. It checks that the score is stable: a task that
  flips between runs exposes nondeterminism, which is a finding in itself.
- **2.0× TTFT and 10 tok/s** are what the owner will tolerate on the Mac in exchange for not needing nobara. They are
  judgements, registered as such. The 10% band exists because they are not measurements.
- **The floor arm** (0.5B) has to fail. If it passes 17 or more, the smoke set cannot separate models, and the run
  grades nothing. It is then recorded as void for the eligibility rule, and only the speed rows stand.

## Run discipline

- Timed phases (ttft, decode, footprint) run only at night, under goinfer's `scripts/night.py` on the measuring box.
  They hold the timing lock (`~/.goinfer-timing.lock`); `tack eval models` refuses a timed phase when another run holds
  it. One timed run per box.
- **Fit-checked by day** with `tack eval models -fit-only` before anything is queued, and only on an idle machine:
  - the Mac only at memory pressure *normal*;
  - nobara only when no night job holds its GPU.
- Pinned: tack at the commit that queues the run, goinfer at the revision named in each record, both as pre-built
  binaries at durable paths. Logs go under `~/tack-runs/e0/`, and the queued command lines are in each record.
- Every record carries the tack revision from the binary's own build info (`vcs.revision`). `tack eval models`
  refuses to start from a binary without one (built by `go run`, from a `git archive` tree, or with
  `-buildvcs=false`) or built from a tree with uncommitted changes, and `-tack-rev` must match it
  (`internal/eval/buildrev.go:Revision`). Build the binary with `go build` from a clean checkout of the commit.
- One copy of each run. An arm already graded at the same tack, goinfer and model revisions is not re-run.

## What E0 does not decide

- Whether tack's loop is competent on real tasks: that is E1, the P1 exit.
- Any claim against Ollama, LM Studio or llama.cpp.
- Speed claims for publication. The speed rows inform the default and the registry; a published figure needs its own
  registration.
