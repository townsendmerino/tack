# E1 pilot — A0 against A0k: pre-registration

**Registered 2026-10-16, before the runner exists and before any agent run.** DESIGN §15.1 and §15.2 define the
pilot: a first read on whether ken helps a local agent on real tasks, before tack exists. **It informs; it decides
nothing.** Its other job is to size E1: minutes per run, A0's pass rate (the difficulty band of
`e1-task-selection-preregistration-2026-10-16.md`), and the spread D13 needs.

**Amendment 2026-10-16, before any agent run** (owner). ken's index stays outside both the clock and the grade:

- **Warm-up, untimed.** For A0k, ken's index for the task's base commit is built before the timed run starts. The
  runner starts the same ken-mcp binary with the same configuration, sends one `search` call over MCP, waits for its
  answer, and stops it. ken-mcp has then built the index and written its snapshot under the task repository's
  `.ken/`, which the ken-mcp opencode starts loads instead of rebuilding. The build time is recorded separately
  (`ken_index_secs`) and is never part of the run's wall time or capped time.
- **A fresh index per task.** Every A0k task repository is new, so its index is built at its own base. No index is
  reused across commits.
- **Out of the grade.** ken's index directory (`.ken/`) and `opencode.json` are excluded from the graded diff, and
  paths under `.ken/` are excluded from the `left_repo` check. Go's tools ignore dot-directories, so the oracle's
  `go test` never sees them.

**Amendment 2, 2026-10-16, after the smoke and before any task run** (owner). **The window is 32768.** The
night smoke stopped both arms on their first request: goinfer refused it before decoding
(`~/tack-runs/e1/smoke-night-2026-10-16` on nobara, each arm's `events.jsonl`). opencode 1.19.0's system prompt and
tool definitions take **16,494 tokens for A0 and 20,008 for A0k**, which already exceeds the 16384 the pilot served,
before any task text or a reply. A smaller model does not help: the 1.5B and 0.5B use the same tokenizer. So:

- the server is `-ctx 32768 -kv-sessions 1`, one KV slot, so that the 7B's weights and one 32768 window share the
  8 GB card (fit-checked again with these flags before the smoke is re-queued);
- opencode's `limit.context` is the served 32768, and `limit.output` is 2048 instead of 4096
  (`internal/eval/pilot/pilot.go:replyLimit`), leaving A0k about 10,700 tokens of conversation on the smoke task (amendment 3 has the
  per-task picture);
- the server is goinfer main `0e598d3f`, as for E0 (its amendment 3).

With one slot, a request on opencode's `small_model` (its title call) takes the slot from the task's conversation,
which then re-prefills unless goinfer restores a prefix snapshot. The `reused` field of `-log-requests` records which
happened, and the smoke is re-run before any task run to read it. A0 is still stock opencode. Both figures are a finding
in their own right: the incumbent harness's fixed prompt alone does not fit the window tack is built for.

**Amendment 3, 2026-10-16, before any task run** (owner). What the arms carry, and the title calls:

- **A0k is "opencode + ken-mcp as shipped".** On the smoke task A0k's first request was 3,514 tokens longer than
  A0's (20,008 against 16,494): ken-mcp's tool schemas. A0k pays for them in the window on every task, so the pilot
  measures the shipped combination, not whether retrieval helps (see "What it does not decide").
- **opencode's fixed prompt includes the repository's instruction file.** opencode reads the first of `AGENTS.md`,
  `CLAUDE.md` and `CONTEXT.md` it finds at the task repository's root into its system prompt (no global file exists
  on nobara). So the smoke's 16,494 is opencode's own prompt plus ken's 19,199-byte `CLAUDE.md`. Across the ten
  task bases: the three aikit bases and three of the four goinfer bases have none; goinfer `a9b6b190` has a 32,375-byte
  `CLAUDE.md`; the ken bases have 11,750 to 24,503 bytes. Every run is fit-checked by goinfer itself: a run whose fixed
  prompt and 2048-token reply exceed 32768 is refused, as the smoke was, and counts as a fail, listed separately.
- **Recorded per run** (`internal/eval/pilot/run.go:PromptStats`), from the run's `-log-requests` window and the
  server's `count_tokens`: the first prompt on the task's model; the statement's tokens; their difference, the
  arm's **fixed-prompt tokens**; the instruction file's tokens; and the fixed prompt less the instruction file, which
  is opencode's own prompt (plus ken-mcp's schemas for A0k) and is what compares with tack's. Also: requests on the
  task's model and on the `-fast` alias, **re-prefills** (a request on the task's model that reused less than half of
  the previous one's prompt), and how many reused rows came from a host snapshot.
- **Title calls.** opencode names each session with a request on `small_model`. On one KV slot that request can
  evict the task's conversation. opencode's own switch, `"agent": {"title": {"disable": true}}`, removes the call
  (`internal/eval/pilot/pilot.go:Arm.NoTitle`, `tack eval pilot -title=false`). The smoke runs twice in one
  night, stock and with titles off, and the re-prefill counts of both are reported. **Decision rule:** if the stock
  smoke has more re-prefills than the title-off smoke, summed over both arms, every task run has titles off, on both
  arms; otherwise the pilot runs stock. The cap stays 10 minutes either way.

**Amendment 4, 2026-10-16, before any task run** (owner). **A server not decoding on `cuda-resident` voids the job.**
goinfer declines a GPU quietly, serving the load on the CPU and answering every request, and only its banner says so
(E0's amendment 4 has the case). The runner reads the banner's decode path once the server is up; if it does not begin
with `cuda-resident`, no task starts, both arms are void, and `tack eval pilot` ends with `VERDICT: INCONCLUSIVE` and
a non-zero exit (`internal/eval/runner.go:VoidPath`). The pilot's server was `cuda-resident (int4)` at its nobara fit
check on 2026-10-16, so no run so far is affected.

**Amendment 5, 2026-10-16, before any task run** (owner). **Every result is reported twice:** over all tasks, and
over the tasks without a **weak-oracle** note in `eval/e1/pilot-statements.md`. A weak oracle is a named test that a
shortcut implementation can pass, so a pass on it must not carry the result alone. Today one task has the note: task
11 (goinfer `e9b76046`), whose test's one-byte-per-token vocabulary lets a wrapper around `ForcedRun` pass. Task 8,
the other task with the note, was removed by the stub check. The set is fixed by the notes as they stand now; a note
added after a run does not move a task out of it. Task 11 is the stub check's replacement for task 8, and the owner
approved its statement as drafted.

**Correction, 2026-10-17, after job 1** (not a rule change). The `left_repo` check now matches the registered
definition, "any tool call that reads or writes a path outside the task repository" ("Recorded per run").

- **What was wrong.** It scanned every string in opencode's events, tool outputs included. In job 1 it flagged task
  5's runs for `/v1/chat/completions`, `/tmp/a` and `/admin/models`. Those are names in the contents of files read
  from inside the repository, not paths any call touched.
- **The fix.** The check now looks only at a call's arguments (tack `e778e26`,
  `internal/eval/pilot/run.go:SummarizeEvents`).
- **Effect on job 1.** It was found after job 1. Job 1's ten runs were re-scanned with the corrected check: no flags,
  and the same tool counts.

**Re-run plan, 2026-10-17** (owner).

- **The first run.** Job 1 ran on 2026-10-17 from 08:06 to 08:17, by day, on goinfer `0e598d3f` and tack `95ed38c`
  (`~/tack-runs/e1/pilot-2026-10-17-job1` on nobara).
- **Why it is re-run.** In 3 of its 10 runs the model's tool call came back as text, so the run ended on goinfer's
  tool-call parsing rather than on the agent
  (`~/tack-runs/e1/NOTE-for-goinfer-2026-10-17-tool-calls-as-text.md`). Job 2 was stopped about 30 s after it started
  and has no result.
- **The re-run.** Both jobs, tasks 1–5 and 6–11, run again on one goinfer build, the one with the tool-call fix, and
  one tack revision.
- **The first run's place.** This morning's job 1 stays as a recorded first run on `0e598d3f`, superseded by the
  re-run. It is reported alongside the re-run as such, and is never pooled into its results.

**Amendment 6, 2026-10-17, before the re-run** (owner). **Sampling, and what each run keeps.** Applied to both re-run
jobs alike.

- **Sampling.** opencode sends Qwen2.5's own recommended settings with every request: temperature 0.7, top_p 0.8,
  top_k 20 (`internal/eval/pilot/capture.go:Sampling`, in each model's `options` in `opencode.json`).
  - **Why.** The first run sent no sampling fields, so goinfer sampled at temperature 1.0 over the whole vocabulary.
    goinfer's session reproduced task 5's malformed call from its recorded request: 26 of 30 samples parsed at those
    defaults, and 9 of 10 at Qwen's settings, the tenth hitting the length cap.
- **The check that the settings arrive.** opencode's provider drops its own `topK` setting, so the run relies on it
  passing the model's options into the request body as written. The job's first run must show all three in its first
  recorded request, or the job stops there and nothing after it is measured (`CheckSampling`).
- **Raw bodies.** Every request opencode sends and every response, the stream as streamed, are recorded under each
  run's `requests/` directory and kept locally on nobara, never committed (`Capture`). goinfer may replay first
  requests to check snapshot restore. Headers, and so the API key, are not recorded.
- **The goinfer build.** goinfer main `d5dd01ef`, which holds the tool-call fix (`8abd8057`): a tool list with
  schemas the call grammar cannot fully hold no longer drops the constraint for the whole list. The binary's sha256
  goes in each job's record.
  - **Timings from this build form their own series** (goinfer, 2026-10-17). `d5dd01ef` predates goinfer's masker
    prefilter. On nobara it adds about 940 ms of decode to each 19-tool call: for the 7B and a 121-token call,
    66.3 → 43.8 tok/s, constraint on vs off. On `e0b1a966` / main `31418112` and later the cost is about 207 ms.
  - So the re-run's timings (turn latency, task wall time, tok/s) cannot be compared with any later build's.
  - Its pass count is unaffected. The mask, and so every greedy output, is identical with and without the prefilter,
    by construction and by test (goinfer's `TestMasker_firstBytePrefilterExact`).
  - **Superseded by amendment 7** before any job ran on it.

**Amendment 7, 2026-10-17, before the re-run** (owner). **The goinfer build is main `31418112`.** Both jobs start a
goinfer-serve built from main at or after `31418112`, so it has the tool-call constraint and the masker prefilter.

- **The build.** goinfer main `31418112`:
  - built with `git archive`, `GOOS=linux`, `-tags cuda`, `./cuda/cmd/serve`;
  - binary `~/tack-runs/bin/goinfer-serve-cuda-31418112` on nobara, sha256
    `f8e8b18d3211c0fac5d2702c958cc6b361a7a39da9e007384f2f318829899db0`, the same at both ends of the copy;
  - it contains `d5dd01ef` (the tool-call fix `8abd8057`);
  - its `constrain/` is identical to `e0b1a966`, the build goinfer measured for the prefilter, and to main `e482e394`.
- **Why `31418112` and not newer main.** It is the commit goinfer named for the prefilter. Main after it changes
  1,007 lines of non-test serve code that the pilot does not need: the swap guard, sampling from the model, model
  loading and the fit route. `31418112`'s own CI run was cancelled when a newer push superseded it; its `constrain/`
  is the code `e482e394`'s green run tested.
- **Timings.** On this build each 19-tool call costs about 207 ms of decode on nobara (goinfer's tier-1 figure for
  `e0b1a966`), not the ~940 ms of `d5dd01ef`. No run used `d5dd01ef`, so the re-run is one series on `31418112`.
- **Sampling and raw bodies,** as amendment 6. Each job's record carries the hash, the sampling and where the bodies
  are kept.
- **The jobs.** `tack-e1-pilot-rerun-job1-746fb8c-g31418112` (tasks 1–5) and `-job2-…` (tasks 6–11). They replace
  the `d5dd01ef` queueing, which was dropped unrun. The queue starts tonight, not by day, because goinfer's peer
  re-run has nobara by day.
- **Order: queued before the fit check.** The design (below) fit-checks the server before queueing. Here the jobs
  were queued first, because the box is reserved by day. The fit check (`tack eval models -fit-only`,
  `~/tack-runs/e1/pilot-server-31418112.toml`, about 15 s under the timing lock) runs when the box is free, before the
  queue starts. If it does not admit `-ctx 32768` on `cuda-resident`, both jobs are dropped. Its result goes in
  `~/tack-runs/e1/fitcheck-pilot-server-31418112`.

## Tasks

The 10 tasks in `eval/e1/pilot.jsonl`, chosen by the registered selection rules with amendment 2, each with the
statement the owner approved in `eval/e1/pilot-statements.md`. A task whose statement the owner drops is left out. The
pilot then runs with 9, and records that.

## Box, server and model

- **nobara** (RTX 2070 SUPER 8 GB). Its night queue (goinfer's `scripts/night.py`) runs the job under the timing lock.
- **`goinfer-serve` CUDA 0e598d3f** (main), cross-built (`eval/e0/nobara.toml`): `-backend cuda -ctx 32768
  -kv-sessions 1 -log-requests` (amendment 2; it was 41e13b26 at `-ctx 16384`).
- **Qwen2.5-7B-Instruct `q4_k_m`** from `~/models/qwen2.5-7b-instruct-q4_k_m.gguf`. It was fit-checked on 2026-10-16
  at 13:33 (`~/tack-runs/e0/fitcheck-2026-10-16-nobara`), admitted at 16384, `cuda-resident (int4)`, using 6.3 GB of
  VRAM.
- One server for the whole job, started by the runner. Every run of both arms talks to it.

## Arms

- **A0: opencode 1.19.0**, as installed in `~/harnesses` on nobara, with a clean configuration. Its project-local
  `opencode.json` is the one goinfer's `connect opencode` writes:
  - the `@ai-sdk/openai-compatible` provider at the server;
  - `limit.context` set to the served window, and `limit.output` 2048 (amendment 2);
  - `small_model` set to the `-fast` alias.

  It runs as `opencode run --format json --auto -m goinfer/<model> --dir <task repo> "<statement>"`. opencode's own
  tools and agent are used unchanged.
- **A0k: A0 plus ken-mcp v1.9.0**, cross-built from the tagged ken (`2853c7b`). It is configured as a local MCP server
  in the same `opencode.json`, over the task repository, with its embedding model in `~/.ken/model`. Nothing else
  differs.
- **Unmodified repositories.** Both arms see each repository's instruction files (`CLAUDE.md`, `AGENTS.md`) exactly
  as they are at the base commit, as a user's setup would (DESIGN §15.1).

## A run

- **The task repository** holds only the base commit and its ancestors: `git init`, then `git fetch` of the base from a
  mirror on nobara. No later commit is reachable from its refs. It is built before the timed run starts, so its setup
  is not counted.
- **The cap** is 10 minutes of wall time, measured from launching opencode until it exits. At the cap the runner kills
  opencode's process group, and the run counts as failed.
- **The oracle.** After the run, the commit's named test files are copied into the task repository and its check
  command runs (`eval/e1/pilot.jsonl`, `check_cmd`). The task passes only if every oracle test reports `pass`; a skip
  or a build failure is a fail.
- **One run per task per arm: 20 runs** (owner, 2026-10-16). Repeats belong to E1.
- **Order.** Tasks run in `pilot.jsonl` order. Within a task, A0 goes first on odd-numbered tasks and A0k first on
  even-numbered ones, so neither arm always meets a cold server.

## Recorded per run

- the tack revision the runner was built from, read from its build info; `tack eval pilot` refuses to start without a
  clean one (`internal/eval/buildrev.go:Revision`);
- pass or fail;
- wall time and **capped time** (a failed run counts as the full 10 minutes, PAR-1);
- whether the cap was hit;
- opencode's JSON event stream, kept whole;
- from the server's `-log-requests` lines for the run's window: per request prompt tokens, reused tokens, time to first
  token and total time;
- the number of tool calls by tool name;
- the edit's diff against the base;
- the prompt figures of amendment 3: fixed-prompt, instruction-file and own-prompt tokens; requests per alias;
  re-prefills and snapshot restores;
- **any tool call that reads or writes a path outside the task repository.** opencode runs unsandboxed as the owner's
  user, and nobara's clones of goinfer, ken and aikit hold the fixes, so such a call is a possible leak. A run that
  makes one is marked `left_repo` and reported both ways: counted, and excluded.

## What it reports

- A0's and A0k's pass rates over the tasks, with their paired difference. Bootstrap intervals are shown, but at 10
  tasks they are wide, and no claim is made from them.
- Each figure in this list twice: over all tasks, and over the tasks without a weak-oracle note (amendment 5).
- Mean capped time per arm, and the paired ratio on the tasks both solved.
- Minutes per run, which replace DESIGN §15.2's 5–10-minute placeholder for E1's machine-time plan (D10).
- Whether A0's pass rate falls inside 20–80%. Outside it, the selection rules' single registered difficulty adjustment
  applies before E1.
- Per repository, and per task.

## Queue plan, against the worst case

- Every run reaching its 10-minute cap is 200 minutes of agent time. The oracle, at most 2 minutes, makes 12 minutes
  per run worst case: **240 minutes for all 20**.
- That is split into **two night jobs of 5 tasks each, both arms, 120 minutes each worst case**. Each is queued with
  `--est 120` and a 150-minute timeout, so neither breaks goinfer's three-hour job cap.
- Repository setup and the server's load (about 30 s) are done at a job's start, before the first timed run, and fit
  inside each job's margin.
- Before queueing, the job's server configuration is fit-checked again on the day, with the same binary, model and
  flags.

## What it does not decide

- Whether ken helps. E1 decides that, with A2 and A3 against A1, at a margin sized from variance this pilot supplies.
- Whether retrieval helps opencode. A0k carries about 3,500 more fixed-prompt tokens than A0, ken-mcp's tool schemas,
  so the pilot compares opencode with and without ken-mcp as shipped (amendment 3).
- Whether ken finds code. Eight of the ten statements name the function to change, because the tests call it directly.
  So the pilot measures ken on tasks where the location is mostly given, and is expected to understate it. A small or
  zero A0k-A0 gap is not evidence that ken does not help. E1's task mix should include tasks where finding the code is
  part of the work (owner, 2026-10-16).
- Anything about tack's loop, which is not an arm here.
