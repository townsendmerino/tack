# E1 calibration set and tack's loop changes: pre-registration

<!-- citations-at: 99534d6 -->

**Status: approved by the owner 2026-10-18 (as `808a088`), with one addition in §2: below the bar, no loop
change is measured, and the next decision is the model or the hardware (DESIGN §17, R13).** Each loop change is its
own amendment, registered here before any of its runs. The loop changes are built after the baseline has run, not
before (owner).

**Why.** The pilot's ten tasks gave 0/10 for A1 and 0/20 for opencode (`e1-pilot-a1-2026-10-18.md`), so there is no
baseline a loop change could beat. This page sets up a set of tasks a 7B can pass, measures A1 unchanged on it, then
measures each change against that baseline.

## 1. The calibration set

- **The rule, fixed before any statement or run:**
  - Take every candidate in E1's full order (`eval/e1/e1-order.jsonl`, sha256 `6e11bb61…`, the 101 verified
    candidates) whose `why.non_test_lines` is at most 10.
  - Keep E1's order.
  - Nothing else filters it. The rule is mechanical and blind to outcomes, like E1's own.
- **That gives 12 tasks: goinfer 8, ken 3, aikit 1.** None is a pilot task; the smallest pilot task has 15 lines.

| # | E1 order line | repo | commit | non-test lines / files | tests |
|---|---|---|---|---|---|
| 1 | 1 | goinfer | `490f760b` | 6 / 1 | 2 |
| 2 | 22 | goinfer | `c7ac10e9` | 5 / 1 | 1 |
| 3 | 24 | goinfer | `4dd9fd82` | 8 / 1 | 1 |
| 4 | 33 | goinfer | `bbb19952` | 6 / 1 | 1 |
| 5 | 50 | goinfer | `8d5cd708` | 9 / 2 | 1 |
| 6 | 53 | goinfer | `37e72a71` | 6 / 1 | 1 |
| 7 | 54 | ken | `376e6096` | 3 / 1 | 1 |
| 8 | 59 | goinfer | `866a7ff0` | 10 / 1 | 1 |
| 9 | 66 | goinfer | `1bfe3117` | 4 / 1 | 2 |
| 10 | 76 | ken | `4aed5e90` | 8 / 1 | 1 |
| 11 | 78 | ken | `1416be9f` | 2 / 1 | 1 |
| 12 | 92 | aikit | `4c062ede` | 8 / 1 | 1 |

- **Why not candidates outside E1.**
  - All 16 candidates of 10 lines or fewer that E1 did not take failed verification
    (`~/tack-runs/e1/mine-2026-10-16/verified.jsonl`):
    - in 9, a named test skips at the base or at the fix;
    - in 6, no named test fails at the base;
    - in 1, the tests pass with the fix stubbed.
  - E1's 101 are the only verified pool.
- **What this costs E1.**
  - All twelve were in E1's capped order, at positions 1, 18, 20, 25, 37, 38, 39, 43, 47, 54, 56 and 63.
  - Tuning tack's loop on these tasks means E1 can no longer treat them as held out.
  - **They leave E1 (owner, 2026-10-18).** E1 draws from the other 59, in the same order. This is E1 task selection's
    amendment 5, and the twelve are kept as `eval/e1/calibration-candidates.jsonl`.
- **Statements.**
  - Drafted under E1's statement rules: an issue, not a spec of the fix, and never quoting or paraphrasing the
    non-test diff.
  - The owner reviews each one for leakage before it is used. A rejected statement is rewritten once, and a second
    rejection drops the task.
  - They go in `eval/e1/calibration-statements.md` and the tasks file `eval/e1/calibration.jsonl`, each recording its
    author and review state.
- **The grader check, with no model.**
  - `tack eval pilot -oracle-check` on the Mac, over all twelve at their fix commits. Every task must pass, because A1
    is graded on the Mac.
  - Verification already showed each task's tests failing at its base commit (`verified.on_parent`) and under the stub
    of its changed functions (`verified.stub`).
  - A task whose check fails on the Mac is dropped, and the reason is recorded.

## 2. The baseline: A1 as it is

- **Configuration:** tack `1819139`, which is A1 exactly. The same server and build (goinfer `31418112`, Qwen2.5-7B-Instruct `q4_k_m`,
  32k window), sampling (0.7 / 0.8 / 20), the 10-minute cap, the failure budget of 8, `max_tokens` 2048, and the
  harness: module download and sandboxed preflight, void on failure.
- **Statements:** all 12 were approved by the owner as drafted (2026-10-18), and are in `eval/e1/calibration.jsonl`.
- **Every result is reported twice, before any run** (owner, 2026-10-18):
  - over all 12 tasks;
  - over the 9 without tasks 3, 8 and 9, whose statements sit close to the fix (`eval/e1/calibration-statements.md`,
    the notes).

  The bar and the decision rule (§3) are read on all 12. The 9-task figures are reported beside them.
- **Three runs per task: 36 runs.** At temperature 0.7 one run per task cannot tell a hard task from bad luck, and
  three give each task a pass rate of 0, 1, 2 or 3 out of 3.
- **What "above zero" means.** The set is usable if the baseline passes at least 4 of 36 runs, on at least 2 tasks.
  - **Below that bar** (fewer than 4 passes, or passes on fewer than 2 tasks), **no loop change is measured** (owner,
    2026-10-18). The next decision is then the model or the hardware, per R13 (DESIGN §17), not further loop tuning.
  - The bar does not move without a mechanism.
- **Outcome, 2026-10-18: below the bar.** The baseline passed 0 of 36 runs, and 0 of 27 over the 9
  (`e1-calibration-baseline-2026-10-18.md`). No loop change is measured on it. The next decision is the model, R13:
  gemma-4 E4B in §7.
- **Time:** A1's ten runs averaged 0.5 min each. Thirty-six runs take about 20–60 min, and the worst case is 6 h with
  every run capped. It runs by day or by night as the owner says, with nobara's timing lock held.

## 3. How a loop change is measured

- **One change per amendment.** Each amendment is registered here before any of its runs, is measured on the same 12
  tasks × 3 runs, and is compared task by task with the baseline, never pooled across arms.
- **Each change is measured alone against the baseline, not stacked.** Stacking comes later, under its own amendment,
  once each change has been read alone.
- **The decision rule, sized from the variance:**
  - Let d be the change's passes minus the baseline's passes, summed over tasks.
  - Under "no effect", each task's run passes with the baseline's rate p_t for that task, so d has variance
    2·Σ 3·p_t(1−p_t).
  - **Adopted:** d ≥ 2 sd.
  - **Parked:** 1 sd ≤ d < 2 sd. It is re-measured with 3 more runs per task in each arm, then judged by the same rule.
  - **Not adopted:** d < 1 sd. This includes d ≤ 0, so the do-nothing arm, the baseline itself, wins ties.
  - The sd is computed from the baseline's per-task rates and written here before the change's first run.
- **Also reported, not decided on:**
  - each task's oracle outcome (pass, fail, build-fail);
  - diffs left;
  - how runs ended;
  - tool-call failures, by kind.

## 4. The loop changes

### 4.1 An edit tool for small models (recommended first)

**The evidence.** I classified all 45 of A1's "text isn't in the file" edit failures, across the graded run, the void
night and the two smokes. Each `old_string` was compared with the file at the task's base commit and with the rest of
the repository:

| why old_string did not match | count |
|---|---|
| the text is in a different file, mostly the task's test file | 14 |
| the model's own earlier edit or write had already changed that text | 9 |
| the path does not exist (the `aikit/…` prefix taken from "Repository: aikit") | 8 |
| not in the repository even after undoing regex escapes; 7 of these 8 are regex-escaped | 8 |
| a grep pattern pasted as old_string (`func \(f \*FlatI8\) MarshalBinary`); unescaped, it matches | 6 |
| whitespace or indentation only | **0** |
| `read`'s line-number prefixes copied in | 0 |

**Whitespace-tolerant matching alone would have fixed none of them.** The failures are about which text and which
file, not about spacing.

**Recommendation: keep old_string matching (text, not line numbers), and change three things.**

- **Matching.** An exact match is tried first, as now.
  - If there is none, two tolerances are tried in turn. Each must give exactly one match in the file:
    - with regex escapes undone (`\(`, `\*`, `\[` and the like);
    - with whitespace runs and indentation ignored.
  - Whitespace tolerance is kept even though A1 shows none, because other small models do drift on tabs and spaces.
    It costs nothing when a unique match is required.
  - The result says which tolerance matched, so the model learns.
- **A failed match locates the text.** In order:
  1. If the text, or its unescaped form, is in another file, the result names it: "this text is in
     `internal/serveapp/tower_reserve_test.go:41`, not in `tower_reserve.go`".
  2. If the session's own earlier edit or write changed this file, the result says so and names the call.
  3. Otherwise it shows the nearest region of this file: the lines most like old_string, with line numbers, at most
     12 lines.
  4. If nothing is similar, it says so and suggests a read.
- **A missing path names the nearest existing path.** For example: "`aikit/vision/preprocess.go` does not exist;
  `vision/preprocess.go` does (paths are relative to the repository root)".

**Why not line-number edits.**

- Line numbers would make the 14 wrong-file failures and the 9 stale-text failures into edits that succeed at the
  wrong place. Today those fail safely.
- A line-numbered edit is only right if the model's numbers match the file at that moment. After its own earlier
  edits they no longer do, which is exactly the stale case.
- Text anchors stay correct when lines move.
- What line numbers would fix, the pasted regex escapes, the unescape tolerance fixes directly.

**Tests (tier 0), each broken once:**

- a regex-escaped old_string edits the one place;
- whitespace drift edits the one place;
- an ambiguous tolerant match is refused and names both places;
- text from another file names that file and line;
- a stale edit names the earlier call;
- the nearest region is shown with line numbers and capped;
- a wrong-prefix path names the existing path.

`TestFixedPromptBudget` re-counts the edit tool's description.

### 4.2 done requires verification

- **The rule.** After an edit or write succeeds, `done` is refused until a verification has run since that change. A
  verification is a `bash` call whose parsed command runs `go build`, `go vet` or `go test`; tack's shell parser
  decides, not a match on the string.
- **The refusal shows the latest result.** It quotes the last verification's command, its exit status and the tail
  of its output (up to 20 lines), or says that none has run since the change at call N. Then: "run go build ./... or
  the task's tests, then call done".
- **It does not require a pass.** `done` after a failing build is accepted. The run's record notes that the last
  verification failed, so the oracle and the record can be compared.
- **No deadlock.** If bash cannot run (no sandbox, or a denied command), `done` is refused at most 3 times, and the
  4th call ends the turn. The record says "unverified".
- **It sits beside, not instead of, the existing refusal of `done` with no file changed.**
- **Tests (tier 0), each broken once:**
  - done after an edit with no build is refused, showing the call number;
  - done after edit and `go test` is accepted, and records the result;
  - a second edit after the build makes done refuse again;
  - `echo go test` is not a verification;
  - the 4th refusal ends the turn.

### 4.3 The sandbox allows loopback; the outside network stays denied

**Why.** Under A1's sandbox, `go test ./...` on ken always fails `internal/modelfetch`, because its `httptest`
server cannot bind loopback. opencode, with no sandbox, could. This is a sandbox change (DESIGN §6.6 says the network
is "denied outright" in P1), so it also needs an ADR in `DECISIONS.md`.

**The risk is local services, not just the internet.** Allowing loopback lets a sandboxed command reach anything
listening on localhost. On this Mac on 2026-10-18 that was:

- **an ssh SOCKS proxy to nobara.** VS Code Remote-SSH's `ssh -D 50814`. Through it, a command reaches the whole
  outside network.
- **four ssh port forwards** left over from today's pilot runs, a harness bug, below;
- **VS Code and Claude Code helper servers**;
- **AirPlay**.

"An outside connection still fails" is therefore not enough as the only escape test.

**The design:**

- **Rules:** allow binding and inbound connections on loopback, and outbound connections to loopback.
- **Deny-list:** deny outbound to every loopback port that was already listening when the command started. Each
  command's profile is built when it starts (`sandbox.Wrap`), so the deny-list is taken then, from the kernel's
  socket table and not by running `lsof`.
- **A test's own server binds port 0** after that point, so it is allowed.
- **Everything else in the network section stays denied:** non-loopback IPv4 and IPv6, DNS, and UDP.

**Escape tests, each red first** (with the section or the deny-list removed, the escape succeeds):

1. A TCP connection to an outside address fails.
2. A connection to a loopback listener started outside the sandbox *before* the command fails, whether the listener
   is a plain TCP echo server or an ssh `-D` SOCKS proxy to the outside.
3. A UDP send or DNS lookup to the outside fails.
4. A sandboxed process binds `127.0.0.1:0`, accepts and connects to itself, and both succeed. This is `httptest`'s
   pattern, and ken's `internal/modelfetch` tests pass in the sandbox.

**A remaining hole, stated and tested.** A listener that someone outside the sandbox starts *while* a command runs is
reachable from that command.

- **Accepted by the owner, 2026-10-18.** It is recorded in DESIGN §6.6 as a known gap (ADR-005), and the test that
  shows it lands with the change.
- **Approved as designed:** every port listening at a command's start is blocked. The escape tests go red first,
  including the pre-existing listener and the ssh SOCKS proxy.

**This change is measured like the others.** On the calibration set it matters only where a task's tests, or the
packages the model tests, use loopback.

### 4.4 Small additions: repeat and empty-result messages

- **These are A1b's two items, as small additions, not a focus:**
  - an unchanged exact repeat is answered as one, with its count, and counts toward the budget of 8;
  - empty glob and grep results say what to do.
- **The evidence is in `e1-pilot-a1-2026-10-18.md`.** None of A1's ten runs looped on successful calls, and the
  longest streak was 1. So this is expected to change little, and it is measured as its own amendment for that reason.

### 4.5 Filed for after the baseline: a stop rule for repeated identical calls (owner, 2026-10-18)

**Filed, not registered.** It is registered as its own amendment before any of its runs, and built after the baseline
with the others.

**The evidence, from the baseline's first pass, while it ran:**

- **Task 4** made the same grep 340 times: `pull\.Select\(files \[\]File, ref Ref\)` in `pull/pull.go`, a regex that
  matches nothing.
  - Each call returned "no matches", outcome `ok`, so neither the repeat rule nor the failure budget fired.
  - It made 353 requests over 7.5 minutes, until the prompt itself passed the window.
- **Task 5** made one grep 213 times or more.
- **The smoke of task 2** made one glob 383 times.

How often a single call was repeated with an unchanged result, per run:

| runs | most unchanged repeats of any one call |
|---|---|
| A1's ten runs, the void night's ten, the task-9 smoke, and pass 1's tasks 1–3 | 0–14 |
| loops: baseline pass 1, tasks 4 and 5; the task-2 smoke | 213, 340, 383 |

**The rule:**

- Within a turn, tack keys each call by its tool and its input, key order aside (`callKey`), and remembers the last
  result each key gave.
- **The 8th time a call repeats with a result identical to its last one ends the turn.** The stop reason is
  `repeat_loop`, with its own exit status.
- Unlike §4.4's message, this counts that call's unchanged repeats wherever they fall in the turn, so calls in
  between do not reset it.
- Eight matches the failure budget.
- **What it would have done:** cut short the three non-loop runs that reached 9, 12 and 14 (all of them failed
  anyway), and ended each loop after 8 repeats instead of 213–383.

**Tests (tier 0), each broken once:**

- an unchanged grep repeated until the turn ends at the 8th, with stop reason `repeat_loop`;
- the same call with a changed result does not count;
- interleaved calls do not reset the count.

**Measured** as its own amendment against the baseline, like 4.1–4.4. It overlaps §4.4, which answers each repeat
and counts it toward the budget. If both are approved, they are measured separately before any stacking.

**Also filed: a prompt that fills the window is `window_full`, not `error`.**

- Task 4's run ended when goinfer refused the request: "400 invalid_request_error: prompt is too long: 32784 tokens >
  32768 maximum".
- tack recorded that as stop reason `error` (exit 1). `window_full` covers only a reply the window cut short.
- **The fix** maps the server's prompt-too-long refusal to `window_full` (exit 7). It changes no run's outcome, only
  its label.
- In the baseline's grading, a run ended this way is counted as window-full, and its record keeps `error`.

## 5. A harness fix, not a loop change

**The ssh port forward the A1 harness opens leaked into ssh's shared connections.**

- `pilot_a1.go` starts `ssh -N -L …` with the owner's ssh config, which shares connections. The shared master
  connection takes over the forward and keeps it after the harness kills its ssh.
- Four forwards from today's runs are still listening on the Mac, at 127.0.0.1:52947, 53045, 54711 and 55646. They
  point at nobara's server port, where nothing listens now.
- **Fix:** start the forward with `-o ControlPath=none`, so it has its own connection and dies with it.
- **Test:** after a pilot run with a fake remote, no listener remains on the forward's port.
- This changes no measurement. Cancelling the four leftover forwards (`ssh -O cancel`) waits for the owner's word,
  because the shared connections may be in use by other sessions.

## 6. Order

1. ~~Approve: this page, the E1 decision in §1, and the harness fix in §5.~~ Approved 2026-10-18. The harness fix
   is `54c2b31`. The four leftover forwards had already closed with their master connection when the cancel was
   tried.
2. Draft the 12 statements, and the owner reviews them.
3. Run the grader check on the Mac.
4. ~~Run the baseline: A1 as is, 36 runs.~~ Done 2026-10-18: 0 of 36, below the bar (`e1-calibration-baseline-2026-10-18.md`).
5. If the baseline reaches the bar, build the changes, each with tier-0 tests broken once, in the order 4.1, 4.2, 4.3,
   4.4. Each is registered as an amendment and measured before the next is built. Below the bar, stop at §2's rule.

## 7. Amendment C1: gemma-4 E4B on the calibration set — approved by the owner 2026-10-18, before any run

**Why.** The Qwen2.5-7B baseline passed 0 of 36 runs, and 0 of 27 over the 9 (`e1-calibration-baseline-2026-10-18.md`), below §2's bar. Under §2's rule the next decision is the model or the hardware (R13). This amendment
measures one other model, gemma-4 E4B, with tack's loop unchanged except for the post-check resend below. It is the
model the owner named.

**The arm, A1-E4B:**

- **Model:** gemma-4-E4B-it `q4_0` (`~/models/gemma-4-e4b-gguf/gemma-4-E4B_q4_0-it.gguf` on nobara). Its CUDA int4
  sidecar (format v17) is already built.
- **Server:** goinfer-serve from goinfer main `0de92cd9`, which contains `33532c3b`. It is cross-built on the Mac
  from a git archive the same way `31418112` was (sha256 `34348720e94bea1b…`), at `~/tack-runs/bin/goinfer-serve-cuda-0de92cd9`.
  - gemma-4's call form is native, `<|tool_call>call:NAME{…}`. goinfer cannot constrain it and checks it after
    generation, so `/v1/models` reports `forced_tool_call` "post-check". Each record records the mode from
    `/v1/models`.
- **tack:** a clean build of `dd39263`, which holds the post-check resend below (`bd6d026`), the 20 GB disk floor and
  the cleanup of repository copies (`dd39263`). For a grammar-mode model none of these changes the loop.
- **Everything else as the Qwen baseline:**
  - tack's loop, prompt, tools and limits;
  - the 32k window, the 10-minute cap, the failure budget of 8, and `max_tokens` 2048;
  - the harness: module download, sandboxed preflight, and void on failure;
  - the same 12 tasks with 3 runs each (36 runs), reported over 12 and over 9.
- **Sampling:** the registered 0.7 / 0.8 / 20. This is not gemma-4's own recommended setting (1.0 / 0.95 / 64).
  Keeping the registration's values keeps the arms comparable task by task, and the sampling check on the first
  request holds either way.

**tack's design for post-check models, registered before any E4B run** (owner, 2026-10-18; tack `bd6d026`):

- **When it applies:** goinfer refuses a reply because a forced tool call came back without one. This happens under
  "post-check" only; under "grammar" the reply cannot be generated, so Qwen's runs are unchanged.
- **What tack does:** it sends the request again, at most twice for one request, with "a tool call is required; call
  done if you're finished" added to the request's last user message.
- **Counting:** each resend is a request of its own, recorded as an error event of class `no_tool_call`. A third
  refusal of the same request ends the turn as a server error.
- **Records:** each run's record counts its resends (`tack_run.resends`).
- **Test:** `TestNoToolCallIsResent`, broken once in two ways (resend off; the rebuild without the nudge).
- **Without it,** A1 would end a run at the first reply without a call. E0 measured 3 such replies in 20, so about 9
  in 10 runs of 15 requests would end that way (0.85¹⁵ ≈ 0.09).

**Before the calibration run, in order:**

1. **Fit at 32k on the new build.** One load through `tack eval pilot-serve` with goinfer `0de92cd9`, by day once the
   lock is free. It must come up `cuda-resident`. The record keeps the context held, the fit line, and `nvidia-smi`'s
   VRAM.
2. **E0 on the new build, E4B only, by day** (owner override; it includes E0's timed phases, and took 27 minutes on
   2026-10-16).
   - It uses its own manifest, `eval/e0/nobara-0de92cd9.toml`, which is the existing one with the new goinfer build
     and the E4B arm alone.
   - **It must pass E0's rule:** a smoke score of at least 17/20 on both runs, with 0 malformed. If it does not, the
     calibration run does not start, and it goes back to the owner.
3. **A one-task smoke** of A1-E4B, task 1, through the full path. It is a smoke and not a result. It shows that the
   forced-call mode arrives as "post-check", that the resend works end to end, and that the records carry resends.

**The stop rule, before any run** (owner, 2026-10-18):

- **When C1 stops:** C1 stops at the first failed gate.
  - Gate 1 fails if the 32k fit load does not come up `cuda-resident`.
  - Gate 2 fails if E0 on goinfer `0de92cd9` scores under 17/20 on either of its two smoke runs, or has any malformed
    call.
- **What happens then:** E4B is recorded as ineligible, with the gate that failed, and none of the 36 runs happen.

**Prediction, recorded before any E4B run:**

- **Resends:** E0's 3 in 20 replies without a call gives about 0.15 resends per request. That is about 1–3 per run
  in runs of the baseline's typical length (5–20 requests), and more in a loop.
- **Runs ended by a third refusal:** about 0.15³ ≈ 0.003 per request, so at most 1 of the 36 runs.
- **Passes: 0–2 of 36, below the bar.** E4B passed E0 only at the bar (17/20), it is smaller than Qwen2.5-7B, and it
  runs the same loop, which made Qwen 0 of 36. Nothing in this arm addresses the baseline's failures: giving up, wrong
  edits and loops. The resend only keeps runs going. A pass count of 4 or more over 2 or more tasks would contradict this
  prediction, and would make E4B the model the loop changes are measured on.
- **Loops:** gemma-4 E4B is no more likely than Qwen to avoid repeated identical calls. Expect window_full or
  prompt-too-long endings in about the share the baseline showed (the baseline: 8 of 36 runs filled the window, 7 of them through loops of one identical call).

**The decision rule:** §2's bar applies to this arm as to the baseline: at least 4 passes over at least 2 tasks, out
of 36.

- **If E4B reaches the bar:** it becomes the model the loop changes in §4 are measured on, each against E4B's own
  baseline.
- **If it does not:** the next decision is again the model or the hardware, and goes to the owner. No loop change is
  measured.

**Time:**

- the fit load: about 1 minute;
- E0: about 27 minutes;
- the smoke: a few minutes;
- the 36 runs: about 1 hour at the baseline's pace.

That is about 1.5 hours by day, with nobara's lock held throughout.

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
