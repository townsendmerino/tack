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
