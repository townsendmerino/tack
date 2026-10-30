# E1 calibration pre-registration: archived sections

<!-- citations-at: 99534d6 -->

Closed sections of `e1-calibration-preregistration-2026-10-18.md`, moved here verbatim on 2026-10-30 (goinfer
`docs/tasks/task-claude-code-hygiene-2026-10.md`, DX11, option (a)). Each keeps its § number, and the live doc keeps its
heading with the outcome in one line and a pointer here. Section references inside them (§2, §3, §12.2, …) are to the live
doc. Nothing here is open.

## 8. Amendment C2: E0 for gpt-oss-20b — approved by the owner as written, 2026-10-18, before the run

**Why.** gpt-oss-20b loads `cuda-resident` at 32k on nobara's 8 GB card with its experts streamed
(`e1-model-candidates-2026-10-18.md`). Under the owner's rule, a model that loads resident at 32k takes E0's smoke
before any calibration run. C2 is that gate, and nothing more: it starts no calibration run of gpt-oss.

**What runs:**

- E0, `tack eval models` on nobara, for gpt-oss-20b alone, with the manifest `eval/e0/nobara-0de92cd9-gptoss.toml`:
  - goinfer-serve `0de92cd9`;
  - `~/models/gpt-oss-20b-MXFP4.gguf`, whose CUDA sidecar is built;
  - `-backend cuda -moe-cache-experts`.
- **How the server is started:** E0 starts goinfer-serve through the same `models.Supervisor` that `tack eval pilot-serve`
  uses. The option sits in the manifest's `args`, which is how E0 takes server flags; pilot-serve's allowlist,
  `-serve-option`, is the pilot's own.
- **All of E0, as registered:** fit, check, two smoke runs of 20, TTFT, decode and footprint. By day on the owner's
  override, after C1, holding nobara's timing lock.

**The bar is E0's rule, unchanged:** a smoke score of at least 17/20 on both runs, and 0 malformed.

**How a missing call is scored:**

- E0's smoke does not force a tool call: some of its tasks expect no tool. So goinfer's post-check does not refuse
  replies during E0.
- A reply with no call, where a tool was wanted, scores as a miss, not as malformed (`internal/eval/smoke.go:ScoreSmoke`).
- Malformed means goinfer parsed a call it could not read.

**The stop rule, the same as C1's:** gpt-oss-20b is recorded as ineligible for calibration if any of these holds:

- its load is not `cuda-resident` (E0's fit phase);
- E0 scores below 17/20 on either smoke run;
- E0 has any malformed call. At or above the bar, it is eligible, and any calibration run of it needs its own amendment.

**Prediction, before the run:**

- **Smoke:** 15–19 of 20 on each run. Harmony's channels and gpt-oss's own tool-call training should help, and nobara's
  E0 has no prior gpt-oss result to go on.
- **Speed:** decode about 35 tokens a second (the by-day sample was 36–37), about half of gemma-4 E4B's 68.

**Time:** E4B's E0 took 27 minutes, and gpt-oss decodes about half as fast, so expect 40–50 minutes. The worst case is
90 minutes.

**C2 outcome, 2026-10-18: gpt-oss-20b is ineligible on goinfer `0de92cd9`.**

- E0 ran 10:48 PDT onward (`~/tack-runs/e0/c2-gptoss-0de92cd9/` on nobara).
- **The load passed:** `cuda-resident (int4)` at E0's 16k, with 0.8 GB of KV beside 11.0 GB of streamed weights.
- **The smoke failed the gate:** 9/20 on both runs, with 8 malformed calls (6 `greplicate`, 2 `grepaste`), which is
  under E0's bar on both counts. Under C2's stop rule, no calibration run follows.
- **The malformed calls carry grep's correct arguments.** goinfer is asked, at low priority, whether gpt-oss wrote those
  names or the Harmony parser ran past `grep` (tack's note to goinfer, item 4).
  - If it was the parser, a new C2 on the fixed build is warranted.
- **E0 finished at 11:55 PDT with verdict "fail".**
  - **Time to first token:** every one of its 24 requests ended "no token was streamed". That covers 4,096, 8,192 and
    15,360 tokens, cold and with the prefix reused. They took about 2.5 minutes each, 17:53–18:54 UTC.
  - **Decode: not to be quoted** (owner, 2026-10-18). The figure is most likely a counting error; see below.
  - **Decode:** its phase measured 226–230 tok/s. That disagrees with the by-day sample of 36–37 tok/s
    (`e1-model-candidates-2026-10-18.md`), and is not investigated.
- **With its experts streamed, gpt-oss-20b is out for A1 on this card** (owner, 2026-10-18), whatever the `greplicate`
  answer turns out to be. The parser question to goinfer stays as a correctness check only.
- **Correction, 12:10 PDT: the "no token was streamed" result is very likely tack's E0 probe, not goinfer.**
  - E0's first-token probe (`internal/eval/runner.go:firstToken`) sends `MaxTokens: 1` and times the first streamed
    delta.
  - gpt-oss's first token is a Harmony channel token, which streams no delta, so the probe can see none on a Harmony
    model whatever the server does (goinfer's Mac session's reading, matched against tack's code).
  - So E0's first-token phase does not measure gpt-oss, and its verdict "fail" rests on that phase too.
- **Unaffected:** the smoke result (9/20 on both runs, 8 malformed), and with it C2's ineligibility. The 2.5 minutes per
  request remain the server's real time.
- **Filed, not built:** E0's first-token probe should count a Harmony model's first token. For example, it could time
  the first byte of content or reasoning, or allow enough tokens for the first delta.
- **The decode reading of 226–230 tok/s is most likely a counting error, and must not be quoted** (owner, 2026-10-18).
  - It is six times the by-day reading of 36–37 tok/s, with nothing in between to explain it.
  - E0's decode rate (`internal/eval/runner.go:decodeOnce`) is the output tokens the server reports, less one, over the
    time between the first and last streamed delta. On a Harmony model, tokens that never stream (channel tokens,
    analysis) count in the numerator but not in the time, the same blind spot as the first-token probe.
  - Filed with the probe fix: E0's decode timing must cover every token it counts.
  - **When those fixes are built** (owner, 2026-10-18): check every earlier E0 decode figure for tokens that never
    streamed, gemma-4 E4B's 68.3 tok/s first (thinking text, or a post-check hold-back, could be counted but not
    timed). Recompute from the raw records where they allow it; otherwise mark the figure unverified in its record.
    E0's records keep only each repetition's token count and rate, not per-delta timings, so most figures may end up
    unverified.
- **How gpt-oss-20b can return** (owner, 2026-10-18), in this order and no other way:
  1. goinfer fixes its Harmony parser, if the `greplicate`/`grepaste` names turn out to be the parser running past
     `grep` (item 4 of tack's note to goinfer, which waits until the owner names it);
  2. a new C2 runs on that build, with E0's first-token check (and decode timing) fixed to count Harmony tokens.
- **Built, 2026-10-18 (tack `38137f5`):** E0's first-token and decode probes ask for thinking, (**changed at C3's approval:** asking turns
  the template's thinking on, so the probes now send each arm's registered setting, disabled by default, §9) time text and thinking
  alike, allow 16 tokens for the first one, and refuse a decode reply that counted more tokens than it streamed
  characters.
- **The audit is done** (`e0-nobara-2026-10-17.md`, "Decode audit"). No earlier decode figure could be recomputed:
  E0's servers logged to a temporary directory.
  - gemma-4 E4B's 68.3 and E2B's 85.6 tok/s are marked unverified because they were not recomputed. **Corrected
    the same day:** the first reason given (a reasoning channel goinfer could drop) was wrong. These checkpoints are
    `thinking: unmanaged`, which goinfer never splits, so no mechanism is known.
  - The Qwen2.5, Seed-Coder and Mac figures stand: their templates have none.

## 9. Amendment C3: E0 on the five models resident on nobara's card — approved by the owner 2026-10-18, with changes made before the first run

**Why.** Five models load `cuda-resident` on the RTX 2070 SUPER (8 GB) at a context measured by day
(`e1-model-candidates-2026-10-18.md`). Under the owner's rule, each takes E0 before any calibration amendment.

- **C3 is E0 only.** It starts no calibration run.
- **It produces a table, not a decision.** Picking a model for calibration is a separate amendment, after the owner has
  seen the table.

**What runs:**

- **The command:** `tack eval models` on nobara, with `eval/e0/nobara-c3.toml`.
- **The builds** (changed by the owner at approval):
  - goinfer-serve from main **`f20f0d93`**, which carries the windowed-KV fix. It is cross-built like the others
    (sha256 `b00ed6183e2610a9…`) and keeps sidecar format v17.
    - Each model's pre-run load at its context records residency and VRAM on this build.
    - A model that does not come up `cuda-resident` there at its measured context runs on `0de92cd9` instead, where the
      context was measured, and the table says why.
  - tack's fixed E0, at the commit that follows this amendment's change:
    - the first-token probe allows 16 tokens and times the first streamed text or thinking;
    - the decode probe times both kinds and refuses a reply that held tokens back;
    - both send each arm's registered thinking setting (below).
- **The run:** all of E0, as registered, for each model. That is fit, check, two smoke runs of 20, first-token time at
  4,096, 8,192 and 15,360 tokens, decode and footprint.
- **The order** (owner): Qwen3.5-9B, Qwen3.6-35B-A3B, Qwen3-8B, Qwen3-4B, Granite-4.2-3B.
- **All five run even if an early one passes,** because the comparison table is the point.
- **When:** by day, on the owner's override, each holding nobara's timing lock.
- **Before each model's E0:** one load through pilot-serve at its context records goinfer's own `forced_tool_call` from
  `/v1/models`, which E0 does not record.

**Each model's setting:**

| # | model | context | server flags | forced-call mode: what to expect (goinfer's report recorded at the run) | thinking |
|---|---|---|---|---|---|
| 1 | Qwen3.5-9B `Q4_K_M` | 32,768 | `-thinking off` | grammar under goinfer's default Hermes JSON form; the model's native XML form (`-tool-format template`, not used) would be post-check | off (template default off) |
| 2 | Qwen3.6-35B-A3B `q4_k_m` | 32,768 | `-moe-cache-experts -thinking off` | as Qwen3.5: grammar under the default Hermes form | off (template default on) |
| 3 | Qwen3-8B `Q4_K_M` | 16,384 | `-thinking off` | grammar (chatml, Hermes JSON) | off (template default on) |
| 4 | Qwen3-4B (bf16, int4 at load) | 30,720 | `-thinking off` | grammar (chatml, Hermes JSON) | off (template default on) |
| 5 | Granite-4.2-3B (bf16, int4 at load) | 30,720 | none | **uncertain: could be "off"**, see below | none: unmanaged, no thinking control |

**Forced-call modes** (corrected at approval):

- **Granite-4.2-3B.** The owner's note is that Granite's released template is not chatml, so goinfer may serve it with
  no JSON call form and report "off". goinfer's banner at the 13:58 load named it `[chat: chatml, thinking:
  unmanaged]`, which would make it grammar. Whichever it reports is recorded.
- **Qwen3.5 and Qwen3.6.** goinfer's earlier coverage listed their native tool form as post-check. That holds only
  under `-tool-format template`. C3 serves goinfer's default Hermes form, which is grammar-constrained.
- **Every model.** goinfer's own `forced_tool_call` from `/v1/models` is recorded before each E0. **A model that
  reports "off" is flagged prominently in the table:** A1 relies on forced tool calls.

**Thinking: off for the Qwen models, the same in every request** (corrected at approval):

- **tack already sends thinking disabled on every request that does not ask for it**
  (`internal/server/goinfer/client.go:toWire`, since P0). goinfer reads that as thinking off, explicitly
  (`resolveThink`). So E0's smoke and every A1 turn run the Qwen models with thinking off, whatever the template
  defaults to. `-thinking off` in the manifest is belt-and-braces for anything that sends nothing.
- **The timing probes now send the same.** The first fix had them ask for thinking, which turns the template's thinking
  on (`thinking.type: enabled` makes `resolveThink` set it on): it made the probes unlike the smoke and A1. They now
  follow each arm's registered setting, `probe_thinking`:
  - off by default, so they send thinking disabled like everything else;
  - set only for a model whose reasoning cannot be turned off (Harmony), so that reasoning streams rather than being
    dropped.

  The decode probe still refuses a reply that counted more tokens than it streamed.
- **Why off:** with thinking on, the reasoning is text tack never reads. In E0's 256-token smoke it could use up the
  reply before the scored tool call, and in an A1 turn it costs time and tokens on every request.
- **Whether reasoning would help tool use** is a different question, not asked here. Thinking on could be a second E0
  arm under its own amendment.

**Known limit of the first-token probe:** it offers E0's tools, so its prompt matches tack's real prefix. A model that
opens with a tool call streams nothing until the call completes, because goinfer buffers tool calls, and that request
records "no text or thinking … within 16 tokens", visibly.

- **Not used: `tool_choice: "none"`,** the owner's optional suggestion. goinfer treats a request with tool_choice none as
  having no active tools (`toolsActive` in `internal/serveapp/anthropic.go`), so the tool schemas would leave the
  prompt. The probe's prompt would then be smaller than the one E0 sized it at, and unlike tack's real prefix.

**The gate, E0's rule unchanged:** a smoke score of at least 17/20 on both runs, and 0 malformed. If a model's load is
not `cuda-resident`, or its smoke falls below the bar or has any malformed call, that model is recorded ineligible.
Every model runs regardless.

**Predictions, recorded before any run.** The incumbent, Qwen2.5-7B, scored 18/18 on E0 at 85.7 tok/s, a figure that
stands after the audit.

| model | smoke (each run) | gate | decode tok/s | first token at 15k, cold |
|---|---|---|---|---|
| Qwen3.5-9B | 17–19 | pass | 50–75 | 2–8 s |
| Qwen3.6-35B-A3B | 17–19 | pass | 10–35 (experts streamed from host RAM) | 15–90 s |
| Qwen3-8B | 16–18 | on the edge | 65–85 | 2–6 s |
| Qwen3-4B | 14–17 | likely below | 90–130 | 1–4 s |
| Granite-4.2-3B | 12–16 | below | 90–140 | 1–4 s |

**Reported in one table:**

- the gate result;
- the malformed count, and the names called;
- first-token time at 4k, 8k and 15k, cold and reused, as medians;
- decode speed, as the median of three;
- VRAM, from E0's footprint;
- goinfer's reported forced-call mode.

**Time:** E0 took 27 minutes for gemma-4 E4B and about an hour for gpt-oss-20b. The four dense models should take
20–40 minutes each, and Qwen3.6-35B-A3B up to 90. That is about 2.5–4 hours by day in all, holding nobara's lock.

### 12.10 Change 4.1, edit messages: a screen (owner, 2026-10-20), registered before its runs

- **Arms:**
  - A is A1 with `fit_window` (§12.6), `stop_repeats` (4.5, adopted) and `result_notes` (4.4, in the bundle), all on in
    both arms (owner).
  - B is A plus `[run] edit_messages`, 4.1 as messages only (`e1-calibration-c6-41-replay-2026-10-20.md`):
    - a missing path names the nearest existing file;
    - text the session's own earlier edit or write replaced names that edit's line;
    - text in another file names it;
    - text found nowhere shows the 12 lines that best match it, numbered;
    - an edit that changes nothing says to verify with a build or test.
- **Builds:**
  - tack `6743122` (the messages; `95eab38`, the temp-directory leak fix, is in both arms), clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Run:**
  - `-setting fit_window -setting stop_repeats -setting result_notes -compare-setting edit_messages`;
  - the 12 tasks, A B B A A B, 72 runs, each after the cell check;
  - by day (owner), about 2 hours.
- **It is a screen.** "Not adopted" means "not shown" (§12.9).
- **Decision rule, as 4.4's.** It enters the bundle if both of its edit mechanisms move as predicted and d ≥ −1.
- **Mechanism metrics, per arm:**
  - edit failures by kind, in the replay's classes;
  - after each failed edit, whether the next edit to that file succeeds;
  - whether that next edit sends the identical old_string again.
- **Predictions, from the replay and from 4.4's runs** (without 4.1, after a failed edit, the next edit to that file
  succeeded 4 times in 36, and 31 of the 32 that failed again sent the identical text):
  - after a failed edit, B's next edit to that file succeeds at least twice as often as A's;
  - B sends the identical failing old_string again at most half as often as A;
  - the largest distinct failure kinds in A are missing paths and old equal to new, as in the replay;
  - d between −1 and +1.
- **Also checked in the same runs:** `tools/replayrepeats` confirms every `repeat_loop` stop, as in §12.8.
- **Interrupted and resumed (owner, 2026-10-20), recorded before the resumed run:**
  - The first launch (08:21:47–09:58:42) completed tasks 1–3, 18 runs, and 5 of task 4's 6.
  - The Mac went onto battery about 09:26. Task 4's B-3 cell was then void 30 times in a row, and the harness stopped
    the job.
  - **Completeness rule:** a task whose block of six runs did not complete is re-run whole.
  - Task 4's five earlier runs are recorded as an incomplete block and **not counted**.
  - The resumed run covers task 4 whole and tasks 5–12: 54 runs, A B B A A B per task, same build and settings. It
    starts once the Mac is on mains.
  - 4.1's verdict reads 72 runs: tasks 1–3 from the first launch, tasks 4–12 from the resumed run.
- **A second stop, and a warm-up (owner, 2026-10-20), recorded before the second resumption:**
  - The resumed run (10:32:06–11:16:59) completed tasks 4 and 5 and 3 of task 6's 6 runs.
  - The Mac then fell under the 20 GB disk floor, and the remaining 39 runs were void before any started.
  - Task 6's three runs are an incomplete block, not counted.
  - With the owner's yes, Go's build cache and tack's sandbox build cache were cleared (19 → 31 GB free).
  - **The warm-up, untimed** (11:19:54–11:22:00, holding the Mac's lock shared):
    - at each of tasks 6–12's base commits, `go build ./...` and `go test -count=1 -run '^$' ./...` ran as a run's sandbox
      runs them (`GOCACHE` = tack's sandbox cache, `GOPROXY=off`);
    - all seven built and compiled their tests cleanly, leaving a 1.0 GB cache;
    - Go keys a package's compile on its directory, so the standard library and every dependency are warm, but each
      run still compiles the task repository's own packages. Both arms are treated alike.
  - The second resumption covers tasks 6–12, 42 runs, task 6 re-run whole.
  - 4.1's verdict reads 72 runs: tasks 1–3 (first launch), 4–5 (first resumption) and 6–12 (second resumption).
- **Outcome (2026-10-20, ended 12:23 PDT): 4.1 does not enter the bundle ("not shown").**
  - Neither edit mechanism moved: after a failed edit, the next edit to that file succeeded 3 times in each arm, and
    every failed next edit resent the identical old_string (B 27, A 26).
  - d = +1 (B 3/36, A 2/36).
  - 4.5's live check agreed on all 18 `repeat_loop` stops, all exit 9.
  - Record: `e1-calibration-c6-41-2026-10-20.md`.

### 12.18 A no-change `done` refusal that restates the task: tier-1 check (owner, 2026-10-25), registered before its runs

- **The change, as built** (`[run] restate_task`, `loop.DoneNoChangeRestated`; tack `4b2c208`, on main at `55ecef2`): the
  refusal of an edit task's first `done` with no file changed quotes the turn's task back: "no files changed; the task
  asks for a change. The task was:", the task as the user gave it (cut at a line past 2,000 bytes), then "Make the
  change it asks for, or call done again with the reason none is needed." The rule is unchanged: once per turn, and
  the second `done` ends it. Proposal: `e1-calibration-c6-restate-task-proposed-2026-10-25.md`.
- **The tier-1 check** (owner: the draft as written). It repeats the Qwen3-4B bundle check of 2026-10-21
  (`e1-calibration-c6-4b-bundle-2026-10-21.md`) with the setting on:
  - Qwen3-4B as C4 loaded it (bf16, int4 at load), at 30,720, thinking off, goinfer `b2b5e816`;
  - `fit_window`, `stop_repeats` (as amended), `result_notes`, `instructions_head`, `refuse_duplicate_edits`,
    `verify_done` and `restate_task`; `build_after_edit` stays off until §12.17 reads;
  - tasks 2, 3, 4, 6, 8 and 9, two runs each, 12 runs;
  - tack at this registration's commit; on nobara after §12.17's screen and goinfer's jobs, holding both locks.
- **Baseline:** 7 of 12 runs were refused for no change, and 0 of the 7 made an `edit` or `write` after the refusal.
- **Pass rule:** at least 3 of the runs refused with the restated text make an `edit` or `write` after the refusal.
  - If fewer than 4 runs are refused at all, the check reads "not shown", not failed.
  - Every run's first message must match the 2026-10-21 layout; only the refusal's text changes.
- **The screen, if it passes** (owner, rule approved now, registered with its run after the pass): A is the bundle as
  adopted then; B is A plus `restate_task`; Qwen3-8B `Q4_K_M` at 16,384, thinking off, goinfer `b2b5e816`; the 12
  tasks, A B B A A B, 72 runs.
  - **Enters:** B's runs ending with no file changed are at most two thirds of A's, and d ≥ −1.
  - **Parked:** an `edit` or `write` follows at least half of B's refusals but the endings do not move, or d < −1.
  - **Not adopted:** otherwise. At 72 runs, "not adopted" means "not shown".
- **If it fails,** the setting stays in tack, off, as 4.1's did, and no screen runs.
- **Outcome (2026-10-26, 03:36–03:40 PDT, tack `a2679df`): fails.** 8 runs were refused with the restated text, and
  0 of the 8 made an `edit` or `write` after it; each called `done` again at once. First messages matched the
  2026-10-21 layout. No screen runs; the setting stays off. Record: `e1-calibration-c6-t1-restate-edit-lines-2026-10-26.md`.

### 12.19 Change 4.9, edit by line range: tier-1 check (owner, 2026-10-25), registered before its runs

- **The change, as built** (`[run] edit_lines`, `internal/tools/edit_lines.go:editTool.byLines`; tack `4b41ff3`, on main
  at `55ecef2`): `edit` also takes `start_line` and `end_line` with `new_string`, which replaces those lines. It is
  refused unless every line of the range was shown by `read` or `symbol` and is the same now as when shown. tack keeps
  that record apart from `lastSeen`, which its own edits update. The largest fixed prompt is 1,197 tokens. Proposal:
  `e1-calibration-c6-edit-lines-proposed-2026-10-25.md`.
- **The tier-1 check** (owner: the draft as written):
  - Qwen3-8B `Q4_K_M` at 16,384, thinking off, goinfer `b2b5e816`;
  - the bundle (`fit_window`, `stop_repeats` as amended, `result_notes`, `instructions_head`,
    `refuse_duplicate_edits`, `verify_done`) plus `edit_lines`; `build_after_edit` stays off until §12.17 reads;
  - tasks 2, 4 and 6, two runs each, 6 runs (§12.12's tasks);
  - tack at this registration's commit; on nobara after §12.18's check, holding both locks.
- **Pass rule:** at least 3 of the 6 runs make at least one line edit, and at least half of all line edits apply (are
  not refused). Every first message must match the bundle's layout; only `edit`'s schema and description change.
- **The screen, if it passes** (owner, rule approved now, registered with its run after the pass): A is the bundle as
  adopted then; B is A plus `edit_lines`; Qwen3-8B as above; the 12 tasks, A B B A A B, 72 runs.
  - **Enters:** B's failed edits whose text is not in the file are at most half of A's, and d ≥ −1.
  - **Parked:** the failure kind moves but d < −1, or d ≥ +2 with no change in edit failures.
  - **Not adopted:** otherwise. At 72 runs, "not adopted" means "not shown".
- **If the model never uses the range,** the setting stays off and no screen runs.
- **Outcome (2026-10-26, 03:40–03:56 PDT, tack `a2679df`): fails.** 0 of 6 runs made a line edit; the model made 21
  `edit` calls, all with `old_string`, 17 of them failing. The line form was in the schema it received; first messages
  matched the bundle's layout. No screen runs; the setting stays off. Record:
  `e1-calibration-c6-t1-restate-edit-lines-2026-10-26.md`.

### 12.20 The current text of a symbol after repeated edits to one file: tier-1 check (owner, 2026-10-27), registered before its code

The owner chose this candidate on 2026-10-27 at 15:05 PDT (from the C6 status note's list), and asked for its tier-1
check to be registered before any code. The line naming the commit it was built at is added here before the first run.

- **Built (2026-10-27, before the first run):** tack `8c29530`, on branch `c6-current-symbol` (product code, on main after
  the owner's yes). Built as registered.
  - ken's bm25 index costs about 14 s at a run's first note: 8.8 s to open and 5.0 s for the first `SymbolAt`, on
    task 5's goinfer base (7,490 files). A run with no note pays nothing.
  - A note that would not fit the room `fit_window` leaves is dropped.
  - Tier-0 tests, each broken once, are listed in the commit message.
  - Run time: about 25 minutes plus those opens.

- **The change, as it is to be built** (`[run] current_symbol`, off by default):
  - **When:** an `edit` with `old_string` fails because the text is not in the file (either of edit's two replies for
    that), and the session has already made an earlier `edit` call to the same file, whatever that call's outcome.
  - **What:** a note appended to the result, after the repeat count, like the build note. It shows the symbol the edit
    was aimed at: the function, method or type holding the line most like `old_string` (4.1's line score), with its
    span taken from ken v1.11.0's index (`SymbolAt`).
  - The lines are given **exactly as the file has them**, without `read`'s line-number prefixes, so `old_string` can be
    copied from them. The note names the symbol and its line range, and is cut to 40 lines around the best line when the
    symbol is longer.
  - **No note:** at file scope, when no line shares a word with `old_string`, or when the index cannot place the file.
  - **The fixed prompt is unchanged.** Calibration arms run without ken, so with the setting on tack opens ken's index
    in bm25 mode for the spans alone. No search or symbol tool is added, and edit gets no symbol form.
  - The repeat rule compares results without the note (`loop.StripNotes`).
- **Why the trigger is a failure, not every repeated edit.** In the 144 runs with the bundle (§12.15's restart, both
  arms; §12.17, both arms), the next edit to the file was refused for stale text after 1 of 38 repeated edits that
  applied. After the 33 repeated edits whose text was not in the file, the next edit to that file applied after 5
  (15%). Of the other 28:
  - 15 failed the same way again;
  - 7 failed in another way;
  - 2 resent the same failed call, which the loop refused without running it;
  - 4 had no next edit to that file.
  - Method and counts: `~/tack-runs/e1/c6-cursym-2026-10-27/` on the Mac, `editstats.go` and `base-rates.txt`.
- **What 4.1 already tried, and how this differs.** 4.1's message for text found nowhere showed the 12 lines most like
  `old_string`, numbered as `read` numbers them, on every such failure. It did not change what the model resent
  (§12.10). This change shows the whole symbol, unnumbered, and only once the model is editing the same file again.
  - **The prior is against it.** Qwen3-8B has ignored four kinds of added text in a row (§12.10, §12.17, §12.18,
    §12.19). In the base runs, its most-repeated failure (task 6) adds a `;` the file does not have, after reading the
    file eight times.
- **The tier-1 check:**
  - Qwen3-8B `Q4_K_M` at 16,384, thinking off, goinfer `b2b5e816`;
  - the bundle (`fit_window`, `stop_repeats` as amended, `result_notes`, `instructions_head`,
    `refuse_duplicate_edits`, `verify_done`) plus `current_symbol`; `build_after_edit`, `restate_task` and `edit_lines`
    off;
  - tasks 1, 5 and 6, three runs each, 9 runs. These hold 32 of the 33 base failures (task 1 15, task 5 4, task 6
    13). Task 1 also passes in 11 of 12 recent bundle runs, so it shows whether the note costs a passing task;
  - tack at the commit named on the line above, clean, on both machines;
  - by day, holding both machines' timing locks, started only when both are free and nobara's queue is idle. About
    20 minutes, plus ken's index build per run, which is timed in the tier-0 work and stated before the run.
- **Pass rule:**
  - after at least half of the notes, the next `edit` call to that file applies (base 5 of 33);
  - at least 4 notes are shown; with fewer, the check reads "not shown", not failed;
  - every run's first request carries the same system prompt and tool schemas as the bundle's, byte for byte.
- **Reported, not decided on:** whether the next edit's `old_string` is copied from the note's lines; the window used by
  notes; passes per task (task 1 against its 11 of 12); stops by reason.
- **The screen, if it passes** (registered with its run after the pass):
  - A is the bundle as adopted then; B is A plus `current_symbol`; Qwen3-8B as above; the 12 tasks, A B B A A B, 72
    runs.
  - **Enters:** the share of repeated not-found failures followed by an applying edit to that file is in B at least
    twice A's, and d ≥ −1.
  - **Parked:** that share moves but d < −1, or d ≥ +2 with the share unmoved.
  - **Not adopted:** otherwise. At 72 runs, "not adopted" means "not shown".
- **If it fails,** the setting stays in tack, off, and no screen runs.
- **Outcome (2026-10-27, 15:32–15:52 PDT, tack `8c29530`): not shown.**
  - The 9 runs produced 1 note; the rule needed at least 4.
  - After that note (task 5 run 1), the model's next 7 calls were the identical failed edit, refused unrun.
  - Task 1 passed 3 of 3 and carried no note: its failures' best line is a file-scope `const`.
  - A replay of the 33 base failures through the note found that 18 would carry one, all on tasks 2, 5 and 6; 6 of
    those aim at a struct whose field names match rather than the code.
  - The setting stays in tack, off, on branch `c6-current-symbol`. Record:
    `e1-calibration-c6-t1-current-symbol-2026-10-27.md`.
- **Amendment: the aim fixed, and a targeted second check (owner, 2026-10-27, option 3 of the decision after the
  first check), registered before its run.**
  - **The fix** (tack `3e80dd5`, on main with the setting off). When the line most like `old_string` is in a type, the
    note shows the function or method holding a line that scores at least as high. Of equal scores, the line whose
    shared words are longer wins. A type that scores higher than every function line stays.
    - Replayed against ken at each task's base, the 33 base failures still give 18 notes.
    - Task 6's six that showed `bannerFacts` now show `modelBannerFrom`, lines 173–212, which hold the target line.
    - `base-replay-aim.txt` in the records.
  - **The check:** as above, with three changes:
    - tack `3e80dd5`;
    - tasks 2, 5 and 6, eight runs each, 24 runs (the tasks that hold all 18 notes the replay finds);
    - by day, about 25–35 minutes, holding both machines' timing locks only for the runs.
  - **Pass rule:** unchanged.
    - After at least half of the notes, the next `edit` call to that file applies.
    - At least 4 notes; with fewer, "not shown".
    - The first requests carry no new tool or schema field.
  - **Prediction:** from the base runs, 5–8 notes. Following the four text nudges before it, the next edit applies
    after fewer than half of them.
  - **If it fails or reads "not shown" again,** the setting stays off for good in C6, and no screen runs.
- **Outcome of the second check (2026-10-27, 16:49–17:42 PDT, tack `3e80dd5`): fails.**
  - 24 runs, 0 passes, 5 notes (all task 2 run 3). After 0 of the 5 did the next edit apply.
  - The note held the exact line the model was after (`case f.kvPrec != "":`), and the model sent its
    `if f.kvPrec != ""` edit 12 times.
  - Task 5 run 7's notes were dropped by the room guard in a nearly full window; run 4's failures were at file scope.
  - As registered, `current_symbol` stays off for good in C6, and no screen runs. Record:
    `e1-calibration-c6-t1-current-symbol-2-2026-10-27.md`.
