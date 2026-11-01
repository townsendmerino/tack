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

**Thinking, for any model that goes to calibration** (owner, 2026-10-18). A calibration amendment for a model must
register its thinking setting explicitly, before any run:

- on, off, or not controllable;
- how it is enforced: what tack sends (thinking disabled, unless the amendment changes that), and any server flag;
- why.

Thinking can change an agent's results a great deal: in time, in tokens spent, and in what the model does. The setting
is part of the arm, never left to a template's default. A change to it is a new arm under its own amendment.

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
- **A glob with no matches adds one line** (owner, 2026-10-19): "glob matches file names only; to find a name inside
  files, use grep."
  - Its target is C5's task 5. Two runs globbed for the field name `kvF16Decline` as a file name 124 times
    (`**/kvF16Decline*`, `kvF16Decline.go`), each glob empty.
  - Test (tier 0, broken once): an empty glob's result carries the line, and an empty grep's does not.
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

### 4.6 A tool-output cap sized to the window (owner, 2026-10-19: its own arm)

**Filed, not registered.** Like the others, it is registered under §12 before any of its runs.

**The evidence (C5, `e1-calibration-c5-qwen3-8b-2026-10-19.md`):**
- At 16,384, 22 of C5's 32 failing runs stopped on the window.
- 10 of those 22 stopped straight after a `bash` call (`go test -v ./...`, or a CUDA build) whose 300–360 lines of
  output took a 10–12k prompt past the window.
- goinfer then refused the next request as too long, and the model never saw the output it asked for.

**What tack does now.**
- `internal/loop/loop.go:Session.runTool` caps a result at `MaxResultBytes`, 16 KiB, keeping three quarters from the
  head and one quarter from the tail.
- The full output goes to the blob store, and `read_output` pages it.
- 16 KiB is about 4–5k tokens. A fixed cap that size is right for a 32k window and too large for 16k with a 10k first
  prompt.

**The change** (revised by the owner, 2026-10-19).
- **Cap each result at the room left, less a reserve for the next reply.**
  - The cap is the smaller of 16 KiB and the room left, converted to bytes.
  - The room left is the window, less the tokens the last request used (prompt and output), less the reserve.
  - The reserve is `max_tokens` (the next reply's room) plus a margin of 512 tokens.
- **The tokens-to-bytes rate is measured, not assumed:** the bytes of the last request's JSON over its prompt tokens
  from the response's usage, **scaled by 0.85, then held to 2–6 bytes per token** (owner, 2026-10-19).
  - JSON's escapes and keys inflate the measured rate, and an inflated rate would let a result overflow the window.
  - Until a response exists, it is 3 bytes per token.
- **A cut or a note is not a failure.** The result keeps the tool's own outcome, so neither counts toward the
  consecutive-failure budget of 8.
- **Below a minimum, a note instead of a sliver.**
  - If the cap comes out under the minimum (1 KiB), the result is replaced by a short note: "[window nearly full: this
    output (N bytes) is not shown. Finish up: make your edit and call done.]"
  - The full output is still stored and paged by `read_output`.
- **Every truncation is marked in-band:** how much was cut, and how to narrow it. For example: "[… cut N of M bytes
  (K of L lines) to fit the window; narrow it with grep, or read a line range with offset and limit …]".
  - Head and tail are kept as now.
  - The note names `read_output`'s handle when that tool is present.
- **Behind a `[run]` setting** (off until adopted), so both arms run the same binary.

**Tests (tier 0), each broken once:**
- a result larger than the room left is cut to it, and the record keeps the full output;
- a result that fits is untouched;
- the cap never exceeds 16 KiB;
- under the minimum, the result is the "window nearly full" note and not a sliver;
- every cut says how much was cut and how to narrow it, and names the handle;
- with the setting off, behaviour is byte-identical to today's.

`TestFixedPromptBudget` is unaffected, because no tool description changes.

**Expected effect:**
- Fewer window stops after `bash`, and runs that go on to read their failing test's output.
- It does not address loops. It cannot make a wrong fix right.

### 4.7 Proposed, not registered: glob and grep descriptions that say which is which (owner asked for the count, 2026-10-19)

**The count.** Calls that chose glob where the smoke task wanted grep or read, in E0's smoke (40 calls per model;
records under `~/tack-runs/e0/` on nobara):

| model | glob instead of grep or read | its other misses |
|---|---|---|
| Mellum2.1 (amendment 10, `b2b5e816`) | **16 of 40** | 2 glob patterns lacking `_test.go` |
| gpt-oss-20b (C2) | **8 of 40** | 8 malformed (`greplicate`, `grepaste`), 4 others |
| Granite-4.2-3B (C3) | **6 of 40** | 22 with no tool call |
| Qwen3-4B, Qwen3-8B, Qwen3.5-9B (C3) | **0 of 40 each** | 2–4 each, all glob patterns missing a part (`_test.go`, `.github`) |

- **It clusters on the same prompts.** All three non-Qwen models chose glob for smoke tasks 11 ("Which file defines the
  type Conversation?") and 12 ("Find every call to os.Exit."). Two of them also did so for task 5 ("Find all TODO
  comments in the code.").
- **In real runs:**
  - C4 (Qwen3-4B) made no glob call at all.
  - C5 (Qwen3-8B) made 186 glob calls in 17 of 36 runs. 124 of them were in two runs of task 5, globbing for the field
    name `kvF16Decline` as a file name (`**/kvF16Decline*`, `kvF16Decline.go`) in a loop.

**So it is common in the non-Qwen models and rare in Qwen.** On C6's arm it touched 2 of 36 runs, both on task 5,
which failed for other reasons too.

**tack's descriptions now** (`internal/tools/fs.go`):
- **glob:** "List files matching a glob such as **/*.go or internal/*/doc.go." Its `pattern` is "relative to the repo
  root".
- **grep:** "Search file contents with a Go regular expression. Use for every occurrence of a name."

Nothing says that glob never looks inside files.

**Proposed rewrite:**
- **glob:** "Find files by name or path pattern, such as **/*_test.go or cmd/*. It matches file names only, never
  contents: to find where a function, type or text appears, use grep."
- **grep:** "Search inside files for a Go regular expression: where a function, type, field or string is defined or
  used. Use for every occurrence of a name."

The fixed prompt now uses 1,024 of its 3,276-token budget, so the rewrite fits.

**How it would be measured, honestly.**
- Under C6, on Qwen3-8B, the expected effect is near zero: 2 runs. It is unlikely to reach d ≥ 3, so it would be
  measured last.
- Its value is for the models that make the error, which C6 does not run. **A cheaper tier-1 check answers that
  directly:**
  - re-run E0's smoke, 2×20 and a few minutes, on Mellum2.1 and gpt-oss with the rewritten descriptions;
  - against the counts above, with its mechanism metric: glob-instead-of-grep calls.
- The owner decides whether it is 4.7 under C6, that tier-1 check, or both.

**The diagnostic, registered 2026-10-19 before its runs** (owner: "run E0's smoke with the rewritten descriptions
today, as a diagnostic (not eligibility), on Mellum2.1, gpt-oss-20b and Qwen3-8B").
- **What changes:** only the glob and grep descriptions in E0's smoke requests. They come from a labelled instrument
  variant (`glob-grep-4.7`, `internal/eval/instrument.go:instrumentVariants`); E0's frozen instrument is unchanged.
- **Each model runs on its registered build, flags and context:**
  - Mellum2.1 on `b2b5e816` at 32,768 (E0 amendment 10);
  - gpt-oss-20b on `0de92cd9` with `-moe-cache-experts` (C2);
  - Qwen3-8B on `f20f0d93` at 16,384 (C3).
- **The arm stops after the smoke** (2×20, temperature 0) and is graded "diagnostic", never eligible or ineligible.
- **Read against each model's registered smoke:**

| model | registered score | registered glob-instead-of-grep/read | prediction with the rewrite |
|---|---|---|---|
| Mellum2.1 | 11/20, 11/20 | 16 | glob misses 6 or fewer; score 14–18 |
| gpt-oss-20b | 9/20, 9/20 | 8 | glob misses 3 or fewer; its 8 malformed calls unchanged, since they are a parser matter |
| Qwen3-8B | 19/20, 19/20 | 0 | unchanged within 1 (the check that it does not hurt a passing model) |

- **Time:** about 25 minutes in all. It holds nobara's lock per model, after Mellum2.1's E0 has released it.

**Outcome (2026-10-19, 17:28–17:33 PDT; tack `015b0c0` on a local side branch, unpushed; records in
`~/tack-runs/e0/diag47-2026-10-19/` on nobara): the rewrite moves the glob confusion between prompts. It does not
reduce it. Both reduction predictions missed.**

| model | glob instead of grep or read | malformed | smoke passes of 40 |
|---|---|---|---|
| Mellum2.1 | 16 → **16** (predicted 6 or fewer) | 0 → 0 | 22 → 24 |
| gpt-oss-20b | 8 → **8** (predicted 3 or fewer) | 8 → 2 | 18 → 20 |
| Qwen3-8B | 0 → 0 | 0 → 0 | 38 → 38 (unchanged, as predicted) |

- **The same count, on other prompts.** The smoke runs at temperature 0, so each difference comes from the descriptions,
  not from sampling.
  - Mellum2.1 stopped choosing glob for task 12 ("Find every call to os.Exit.") and started for task 15 ("Where in the
    code do we handle HTTP status 529?").
  - gpt-oss stopped for tasks 11 and 12, and started for tasks 5 ("Find all TODO comments") and 18 ("Which test checks
    the fixed prompt budget?").
- **gpt-oss's malformed calls fell from 8 to 2.** Its malformed names (`greplicate`, `grepaste`) are variants of grep's
  name, so grep's new description may change how it spells the call. That is one run pair, not registered as a
  measure, and the parser fix stays the remedy.
- **Reading:** the descriptions alone are not the lever for these models.
- **Decision (owner, 2026-10-19): 4.7 does not proceed.** The rewrite moved the confusion without reducing it.
  - The diagnostic's code and manifests stay unpushed on the local side branch `diag-4.7` (`015b0c0`), as its record.
  - **§4.4's glob no-match line stays the fix for C5's glob loop.** It acts after an empty glob, where task 5's 124
    globs of `kvF16Decline` happened.

### 4.9 Candidate, not registered and not built: an edit by line range (owner, 2026-10-20)

**To be considered after 4.8.**

- **The change:** `edit` also takes `start_line`, `end_line` and `new_string`, and replaces those lines.
  - It is refused unless those lines match what the model last read of the file (the workspace's `lastSeen`).
  - So a range whose numbers have shifted, after the model's own earlier edit or anything else, is caught instead of
    editing the wrong place.
  - old_string matching stays as it is. The range is an alternative way to name the text, not a replacement.
- **Why:**
  - 4.1 showed the model cannot copy text exactly, and resends identical failing text whatever the reply says. After
    a failed edit, 26 and 27 of the next failed edits in A and B sent the same old_string again
    (`e1-calibration-c6-41-2026-10-20.md`).
  - 4.4 showed it can switch tools when told: after an empty glob, B turned to grep 57% of the time against A's 9%
    (`e1-calibration-c6-44-2026-10-19.md`).
  - Line numbers are what `read` shows it, so a range asks it to copy two numbers, not a block of text.
- **What §4.1 said against line-number edits, and the answer:**
  - Numbers that no longer match the file would edit the wrong place.
  - The refusal unless the lines equal what the model last read is the guard for that.
  - The guard's test must show a shifted range refused, both after the model's own edit and after an outside change.
- **The fixed-prompt cost:**
  - Two optional integer properties and a sentence in the edit tool's description, estimated at 40–60 tokens.
  - The fixed prompt is now 1,024 of its 3,276-token budget, so it fits.
  - `TestFixedPromptBudget` measures the real cost when it is built.
- **Its mechanism metric, if registered:** edit failures by kind, and how often the model uses a range after an
  old_string failure.

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
   **Superseded 2026-10-19 by §12:** C5 reached the bar, and the changes, now including 4.5 and 4.6, are measured on C5's
   arm. §12 proposes an order for the owner.

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

**Archived, closed.** C2's outcome (2026-10-18): gpt-oss-20b is ineligible on goinfer `0de92cd9`. The registration and the full outcome are in [the archive](e1-calibration-preregistration-2026-10-18-archive.md), § 8.

## 9. Amendment C3: E0 on the five models resident on nobara's card — approved by the owner 2026-10-18, with changes made before the first run

**Archived, closed.** C3 produced E0's table for the five models (`e0-nobara-c3-2026-10-18.md`); Qwen3-4B and Qwen3-8B passed E0's gate and went on to C4 (§10) and C5 (§11). The registration is in [the archive](e1-calibration-preregistration-2026-10-18-archive.md), § 9.

## 10. Amendment C4: Qwen3-4B on the calibration set — registered 2026-10-18 by the owner, before any run

**Why.** Qwen3-4B passed E0's gate in C3 (18/20 on both runs, 0 malformed, 130 tok/s;
`e0-nobara-c3-2026-10-18.md`). Under §2, the next decision after the Qwen2.5-7B baseline's 0/36 is the model. C4
measures A1, unchanged, on Qwen3-4B. C5 follows on Qwen3-8B.

**The arm:**

- **Model:** Qwen3-4B (`~/models/qwen3-4b`, bf16, quantized to int4 at load, with its CUDA sidecar built).
- **Server:** goinfer-serve `f20f0d93`, which C3 ran on.
- **Context: 30,720.**
  - That is 2,048 under the registered 32,768: the most this model holds resident on the 8 GB card (goinfer's decline
    at 32k: 4.83 GB of KV against 5.13 GB free less 384 MB; resident at 30,720 by day and in C3).
  - A run that would have fit between 30,720 and 32,768 fills the window here. In the baseline, 8 of 36 runs passed
    30,720, and those 8 filled 32k anyway. So the difference is small, but it is a difference.
- **Thinking: off** (§3), enforced twice:
  - tack sends thinking disabled on every request (`client.go:toWire`);
  - goinfer-serve runs with `-thinking off` (pilot-serve `-serve-option thinking-off`).

  **Why off:** E0's gate measured this model with thinking off. Thinking on would be a separate arm later, under its
  own amendment.
- **Forced-call mode: grammar**, as goinfer reported in C3. No post-check resends are expected, and a resend would be a
  finding.
- **Everything else as the baseline:**
  - tack's loop, prompt, tools and limits (failure budget 8, `max_tokens` 2048, 10-minute cap);
  - sampling 0.7 / 0.8 / 20;
  - the harness: module download, sandboxed preflight, the disk floor, the Mac kept awake, and the sleep check;
  - the same 12 tasks with 3 runs each (36 runs);
  - results reported over 12 and over 9, task by task beside the baseline, never pooled.
- **tack:** a clean build of this amendment's commit or later.

**Procedure (C1's):**

1. **Fit load** at 30,720 through pilot-serve with the run's server options. It must come up `cuda-resident`, or C4
   stops.
2. **One-task smoke,** task 1. tack's session reads it, since this runs unattended (owner). It goes on to the 36 runs
   only if the smoke is clean:
   - no void;
   - no harness error;
   - request timings that make sense: every request paired; the client's total near goinfer's plus the tailnet time;
     first-token times in line with C3's.

   Otherwise C4 stops, the reason is recorded, and C5 begins.
3. **The 36 runs,** as three passes of 12.

**Before the run:** the Mac must be on mains. Each record notes its power source.

**Predictions, before any run:**

- **Passes: 0–3 of 36, below the bar.** The baseline, a larger model of the same family, passed none. Qwen3-4B's E0 smoke
  equals the baseline's, and nothing in A1 changed.
- **Window-full stops: 6–12 of 36,** counting the server's prompt-too-long refusals. The baseline filled the window in 8
  of 36 runs at 32,768, 7 of them through loops of one identical call. Qwen3-4B's context is 2,048 smaller, and its
  loops are expected at a similar rate.
- **Decision rule:** §2's bar, at least 4 passes over at least 2 tasks, out of 36.

**Outcome (2026-10-19): below the bar.** 0 of 36 passed (0 of 27 over the 9), and 11 of 36 stopped on a full window.
Both are within the predictions. In 19 runs the model called `done` without doing any work. Pass 1's tasks 1–7 ran with
the Mac on battery; they are kept and marked. Record: `e1-calibration-c4-qwen3-4b-2026-10-19.md`.

## 11. Amendment C5: Qwen3-8B on the calibration set — registered 2026-10-18 by the owner, before any run

**Why.** Qwen3-8B passed E0's gate in C3 (19/20 on both runs, 0 malformed, 79 tok/s). It runs after C4, on the same
night.

**The arm:** as C4, with these differences.

- **Model:** Qwen3-8B `Q4_K_M` (`~/models/qwen3-8b-gguf/Qwen3-8B-Q4_K_M.gguf`, sha256 `d98cdcbd…`, sidecar built).
- **Context: 16,384,** half the registered 32,768.
  - That is the most this model holds resident on the card (goinfer's decline at 32k: 4.83 GB of KV against 3.17 GB
    free; resident at 16,384 by day and in C3).
  - **It equals tack's 16k design window** (DESIGN §1, CLAUDE.md "Small window first").
  - **Window-full stops are a confound against the baseline.** A run that fills 16,384 might have passed with the
    baseline's 32,768. In the baseline:
    - first prompts ran 0.9k–11.6k tokens (median 8.7k), leaving 5–15k of room at 16k;
    - 13 of 36 runs passed 16,384 at some point;
    - tasks 3 and 5 passed it in all 3 runs, and tasks 1 and 4 in 2 of 3.

    A C5 pass rate below C4's could be the window, not the model, so C5 is read beside C4 task by task, not as a
    verdict on Qwen3-8B.
- **Thinking: off,** enforced and for the reason as C4.
- **Forced-call mode: grammar,** as goinfer reported in C3. No resends are expected.

**Procedure:** as C4. Fit load at 16,384, then the one-task smoke read by tack's session, then the 36 runs. If the
smoke is not clean, C5 stops and the reason is recorded.

**Predictions, before any run:**

- **Passes: 0–3 of 36, below the bar.**
- **Window-full stops: 12–20 of 36,** counting prompt-too-long refusals. 13 of the baseline's 36 runs grew past 16,384,
  and loops add to that.
- **Decision rule:** §2's bar. A C5 result is reported with the window confound beside it.

**Outcome (2026-10-19): at the bar.** 4 of 36 passed, over 2 tasks: task 2 once and task 11 three times (4 of 27 over
the 9). Both predictions missed: 4 passes against 0–3, and 25 window-full stops against 12–20. The window confound is
the main failure: 10 of the window stops followed a `bash` call whose output overflowed 16k. **The owner decided
(2026-10-19): the loop changes are measured on this arm (§12).** Record: `e1-calibration-c5-qwen3-8b-2026-10-19.md`.

**Order and time:** C4, then C5, the same night, each holding nobara's timing lock for its runs. The baseline's 36
runs took about 70 minutes. Expect about 1–1.5 hours each, plus the fit load and the smoke. C5 may finish sooner,
because a 16k window fills sooner.

## 12. Amendment C6: the loop changes, measured on C5's arm — owner decision 2026-10-19; registered before any run

**Why.** C5 (Qwen3-8B at 16,384) passed 4 of 36 over 2 tasks, which meets §2's bar. The owner decided that §4's loop
changes are now measured on this arm, with the effect size that would count registered here.

**This section fixes how every change is measured.** Each change still gets its own line here, before its runs, with
its commit, its setting and its predictions.

**Where C6 stands (2026-10-21; updated 2026-10-29):** `e1-calibration-c6-status-2026-10-21.md` lists what is in the
bundle, what is not, the candidates, the bundle night's plan, the triggers and the open decisions.

### 12.1 The arms

- **A, the do-nothing arm:** C5's configuration.
  - A1 as is, on Qwen3-8B `Q4_K_M` at 16,384.
  - Thinking off, enforced as in C4.
  - Forced-call mode grammar.
  - Sampling 0.7 / 0.8 / 20, `max_tokens` 2048, failure budget 8, the 10-minute cap.
  - The same harness, and the same 12 tasks.
- **B:** A with exactly one change from §4 switched on.
- **One binary.** Each change lands behind a `[run]` setting that is off by default, so A and B run the same tack
  binary, built clean at the change's commit, and differ by that one config line. The pilot writes the line.
- **The server: goinfer `b2b5e816`** (owner: anything registered from now on), the same build for both arms.
  - C5 ran on `f20f0d93`, so each change is measured against an A arm run beside it, not against C5's 36 runs.
  - C5's runs stay the evidence that the bar is met, and they supply the per-task rates for the sd.
  - A fit load of Qwen3-8B at 16,384 on `b2b5e816` comes first. It must be `cuda-resident`, or the change waits.
- **A is reported beside C5 task by task.** If A's total differs from C5's 4 by 3 or more passes, that is a build
  effect, and it is reported to goinfer. The change's d is still read against its own A.
- **Changes stack** (owner, 2026-10-19). This replaces §3's "each change measured alone, not stacked" for C6.
  - Every change adopted so far is on in **both** A and B of the next change, so each change is measured on top of
    the loop as it will ship.
  - A change that is parked or not adopted is off in both arms of later changes. A parked change is re-measured
    under its own rule before the next change runs.
  - Each change's registration line names the changes its A includes.
  - **The order therefore matters.** A change's effect is measured given those before it, and is not comparable with
    the same change measured on plain A1.

### 12.2 Interleaving (owner rule, 2026-10-19)

- **Per task, six runs back to back:** A B B A A B. Then the next task.
- **A cell is one run.** Its start records nobara's load, its free memory and the Mac's power. The Mac's load is
  recorded and never voids.
- **A cell is void if it starts under heavy load on nobara:** a 1-minute load over 8.0, or, from 4.5 on, less than 2 GB
  of memory available with the model loaded. It is also void if it starts with the Mac on battery.
  - Through 4.6, the memory rule was "under the fit's need plus 2 GB". That double-counted the loaded model, whose
    memory is already out of "available". It was inert at nobara's 55 GB (§12.5), and the owner approved the fix,
    `cmd/tack/pilot_a1.go:cellVoid`, on 2026-10-19.
- A void cell is re-run in its place and listed in the record, never averaged.

### 12.3 The decision rule and the effect size that counts

§3's rule, with the sd computed now from C5's per-task rates:
- task 2 at 1/3, task 11 at 3/3, the other ten at 0;
- under no effect, d = B's passes minus A's has variance 2·Σ 3·p(1−p) = 2·3·(1/3)(2/3) = 4/3, so **sd = 1.15**.

| d (B − A, out of 36 each) | verdict |
|---|---|
| **3 or more** (d ≥ 2 sd = 2.31) | **adopted** |
| 2 (1 sd ≤ d < 2 sd) | parked: 3 more runs per task in each arm, then the same rule on all 6 per task |
| 1 or less | not adopted; the do-nothing arm wins ties |

**What +3 means.**
- **A gain of 3 passes per 36 runs:** for example 7/36 against 4/36, an absolute gain of 8 points.
- **Task 11 is already 3/3 and task 2 can add at most 2,** so a change can be adopted only if it makes a task pass
  that C5 never passed.
- **A smaller real effect is parked or missed.** That is the price of 36 runs per arm, and it is stated rather than
  hidden.

**How often the rule errs when a change does nothing,** taking C5's rates as true:
- adopted by chance, P(d ≥ 3): 1.1%;
- parked by chance, P(d = 2): about 8%.

These rest on the ten zero-rate tasks being truly zero. A task with a small true rate adds noise and raises both.

**Also reported, not decided on:** each task's outcome in both arms, how runs ended, window-full stops, tool errors by
kind, and the cell states.

**One mechanism metric per change** (owner, 2026-10-19). Each is recorded in both arms and reported beside d. It
shows whether the change did what it is for. **It is never used for adoption.**

| change | its mechanism metric |
|---|---|
| 4.6, the window-sized output cap | window-full stops (`window_full` plus prompt-too-long refusals), with the cap's truncations and "nearly full" notes counted |
| 4.5, the stop rule | stop reasons, `repeat_loop` included |
| 4.4, repeat and empty-result messages | repeat loops (runs with 8 or more unchanged repeats of one call), empty-result repeats (an empty glob or grep repeated unchanged), and globs for a code name after an empty result (owner, 2026-10-19) |
| 4.1, the edit tool | edit failures by kind, in §4.1's table |
| 4.2, done requires verification | verification runs per run, and window use after `go test`: the prompt's growth from each verification's result, and window stops that follow one |
| 4.3, loopback | tool-output and oracle failures that name a loopback bind or dial |
| 4.7, if registered | glob calls that search for a code name as a file name |

### 12.4 Time and order

- **Each change is 72 runs:** C5's 36 took 57 minutes, so about 2 hours, plus the fit load.
  - That is at the night line of the 2026-10-19 rule. It runs by day unless the owner says otherwise.
  - A done-by time is stated at launch.
  - It holds nobara's lock only for each step, and releases it between changes.
- **Proposed order, for the owner to decide.** It replaces §6's 4.1–4.4, given C5's evidence:
  1. **4.6, the tool-output cap sized to the window.** The largest failure in C5: 10 of 22 window stops followed a
     `bash` output.
  2. **4.5, the stop rule,** then **4.4, the repeat and empty-result messages.** C5's loops were short, so they are
     expected to change little.
  3. **4.1, the edit tool.**
  4. **4.2, done requires verification.** Measured after 4.6, because it makes the model run `go build` or `go test`,
     whose output at 16k without 4.6 would add window stops.
  5. **4.3, loopback in the sandbox.** It only affects tasks whose tests use loopback.
- **The prompt-too-long label** (§4.5, "also filed") changes no outcome, so it lands as a harness fix, not an arm.
- **Each change is product code.** It is built with tier-0 tests broken once, shown to the owner, and pushed on his
  yes. Then its registration line goes here, and then its runs.

### 12.5 Change 4.6, the window-sized output cap — registered 2026-10-19, before its runs (owner-approved)

- **Arms:**
  - A is A1 as is. No change has been adopted yet, so A includes none.
  - B is A with `[run] fit_window = true`.
- **Builds:**
  - tack `c7d5c05` (the cap is `e515410`; the A/B harness is `c7d5c05`), clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Run:** `tack eval pilot -a1 -compare-setting fit_window`, the 12 calibration tasks, A B B A A B per task, 72 runs,
  each after the cell check. By day, this evening (owner).
- **Before the runs:**
  - a fit load that must come up `cuda-resident`;
  - then the cell check once on its own, with the model loaded and idle (`-cell-check`). Its numbers are recorded here.
    If it would void a loaded, idle box, the rule is fixed first, and the fix is recorded here.
- **Predictions, before any run:**
  - **Mechanism:** B has at least 5 fewer window-full stops than A, counting prompt-too-long refusals. C5 had 25, and 10
    of them followed a `bash` output.
  - **d:** between −1 and +3. The cap lets runs go on, but most of C5's failures are wrong or missing fixes that more
    room does not repair.
  - **Verdicts:** about 25% adopted (d ≥ 3), 25% parked (d = 2), 50% not adopted.
  - **A against C5:** about 4 passes (3–6), the same arm on a newer goinfer build.
- **Time:** about 2¼ hours of runs plus any void cells re-run. It holds nobara's lock for this comparison only.
- **A known, inert flaw in the cell check's memory rule** (owner, 2026-10-19). The rule compares available memory with the
  fit's need (KV plus weights) plus 2 GB.
  - For a model resident on the GPU, that need is mostly GPU memory, and the server's own host memory is already out
    of "available". So the rule double-counts the loaded model.
  - It cannot void here: nobara has about 55 GB available against about 8.5 GB.
  - **It stays as registered for 4.6's runs.** It is fixed and tested before 4.5's runs, to measure headroom with the
    model loaded: at least 2 GB available.
  - The load rule is different: if the standalone cell check shows it would void a loaded, idle box, it is fixed
    before 4.6's runs.
- **Outcome (2026-10-19, 18:08–20:23 PDT): d = +1 (B 2/36, A 1/36), so 4.6 is not adopted.**
  - **Verdict, as registered (owner):** not adopted, d = +1.
  - **Mechanism, beside it:** window-full stops fell from 25 to 12, against a prediction of at least 5 fewer: met.
  - A's 1/36 against C5's 4 was checked for a build effect. Qwen3-8B is greedy bit-identical across goinfer
    `f20f0d93` and `b2b5e816` (the determinism check, in the record), so the drop is read as sampling.
  - There were 0 void cells. Record: `e1-calibration-c6-46-2026-10-19.md`.

### 12.6 Amendment: `fit_window` is a background condition for the rest of C6 (owner, 2026-10-19)

**Decided after seeing 4.6's result, and registered before any run of 4.5.**

- **For every remaining C6 change, `[run] fit_window = true` is on in both arms,** A and B alike. In the harness it is
  `-setting fit_window`, beside the change's `-compare-setting`.
- **It is a background condition, not an adoption.** 4.6 stays "not adopted" at d = +1 (§12.5), and no claim is made for
  `fit_window`, in passes or otherwise. §12.1's stacking rule is unchanged; this is the one exception it names.
- **Why:**
  - In 4.6, window-full stops halved (25 → 12) with no cost in passes (B 2/36, A 1/36).
  - 4.2 (done requires verification) makes the model run `go build` and `go test`. At 16k without the cap, that output
    would add window stops in B that A does not suffer, so 4.2 could not be measured fairly.
  - Holding the condition the same for every remaining change keeps them comparable with one another.
- **What it changes for the remaining changes:**
  - Each A now differs from C5's arm by `fit_window`.
  - The sd in §12.3 stays as registered, from C5's per-task rates. 4.6's arms show the cap did not move passes (A 1, B
    2), so those rates are the nearest registered estimate.
  - Each change's record names this condition.

### 12.7 Change 4.5, the stop rule for repeated identical calls: judged by replay (owner, 2026-10-19)

**Registered before the replay ran.** The rule is §4.5's, built as `loop.RepeatGuard` and `[run] stop_repeats`: within a
turn, the 8th unchanged repeat of one call (same tool and input, same result as its last) ends the turn with
`repeat_loop`, exit status 9. The build is tack `898f97a` on the local branch `c6-45`, unpushed until the owner approves.

- **4.5 is judged by replay, not by a 72-run comparison. This criterion replaces §12.3's d-based rule, for 4.5 only.**
  - **Adopted** if the rule would have stopped no passing run.
  - **Not adopted** if it would have stopped any.
- **Why:**
  - A stop rule only ends runs. It never changes what a run did before its stop.
  - So applying the rule's own code to the captured requests and results of a run that went on shows exactly where
    the run would have ended.
  - Passes do not measure a guard: one that stops only failing runs cannot add passes, and the question is whether it
    ever costs one.
- **The replay** (`tools/replayrepeats`, `pilot.ReplayRepeats`):
  - It feeds every captured run's tool calls, with the results the model saw, through the same `RepeatGuard` the loop
    uses. The stop takes effect after the stopping response's results, as in the loop.
  - It runs over 4.6's 72 runs, C5's 36 and C4's 36: 144 runs.
  - For each stopped run it records where it stopped, its unchanged repeats, and the requests and wall time it would
    have saved.
  - A test (`TestReplayRepeatsMatchesTheLiveRule`) holds the replay to the live rule: replaying a run made with the
    rule off stops at the request where the same run with it on ended.
- **Prediction, before the replay:** it stops 25–40 of the 144 runs and no passing run. In 4.6 alone, 21 of 72 runs
  reached 8 or more repeats by the input-and-summary approximation, and none of them passed.
- **A live check for correctness, not the verdict:** two runs with `stop_repeats` on, on tasks where loops occurred
  (task 2 and task 5, with `fit_window` per §12.6). They confirm that `repeat_loop` ends the turn and `tack run` exits 9,
  end to end against goinfer. Done by day under nobara's lock, in about 20 minutes, after the replay.
- **Outcome (2026-10-19, replayed 21:22 PDT): adopted.**
  - Over 144 captured runs, the rule would have stopped 36 and no passing run. It would have saved 4,243 requests and
    102 minutes.
  - The prediction held: 25–40 runs stopped, none passing.
  - Record: `e1-calibration-c6-45-replay-2026-10-19.md`.
- **Adopted (owner, 2026-10-19).** The code was pushed as `3e2da1e`, a cherry-pick of `898f97a` with the same code.

| job | runs | stopped | passing runs stopped | requests saved | wall time saved |
|---|---|---|---|---|---|
| 4.6 (A and B) | 72 | 18 | 0 | 1,150 | 24 min |
| C5 | 36 | 6 | 0 | 314 | 6 min |
| C4 | 36 | 12 | 0 | 2,779 | 72 min |
| **all** | **144** | **36** | **0** | **4,243** | **102 min** |

- **The separate live check is dropped (owner).** `stop_repeats` is on in both arms of 4.4, as an adopted change under
  stacking, and 4.4's runs are the live check.
  - 4.4's record runs `tools/replayrepeats` over its live runs and confirms that every `repeat_loop` stop came at a real
    8th unchanged repeat. It also counts the exit-9 runs.
  - Any disagreement is reported before 4.4's verdict.

### 12.8 Change 4.4, repeat and empty-result messages: a screen (owner, 2026-10-19), registered before its runs

- **Arms:**
  - A is A1 with `fit_window` (§12.6, background) and `stop_repeats` (4.5, adopted).
  - B is A plus `[run] result_notes`: the glob-only line on an empty glob ("glob matches file names only; to find a
    name inside files, use grep."), a line on an empty grep, and an unchanged-repeat note with its count.
  - The repeat note does not count toward the error budget, because `stop_repeats` ends loops. The owner accepted
    that departure from §4.4 as filed.
- **Builds:**
  - tack `987eb2f`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Run:**
  - `-setting fit_window -setting stop_repeats -compare-setting result_notes`;
  - the 12 tasks, A B B A A B, 72 runs, each after the cell check (memory rule: at least 2 GB available with the model
    loaded);
  - starts about 22:20 PDT (owner).
- **It is a screen.** At 72 runs, passes can show only gains of 25 points or more (§12.9), so "not adopted" means "not
  shown".
- **Decision rule, replacing §12.3's for 4.4.** It enters the bundle (§12.9) if both hold:
  - its glob mechanism moves as predicted: after an empty glob, B's next call is glob at most half as often as A's;
  - d ≥ −1.
- **Mechanism metrics, recorded per arm:**
  - for each empty glob result, whether the run's next call is a glob (the confusion) or a grep (the hint taken);
  - `repeat_loop` stops;
  - empty-result repeats: an empty glob or grep repeated unchanged.
- **Predictions:**
  - after an empty glob, B's next call is glob at most half as often as A's;
  - B has fewer `repeat_loop` stops than A (4.6's replay: about 9 a side);
  - d between −1 and +1.
- **4.5's live check, in the same runs:** `tools/replayrepeats` over all 72 confirms that every `repeat_loop` stop came
  at a real 8th unchanged repeat. The exit-9 runs are counted. A disagreement is reported before 4.4's verdict.
- **Time:** about 1 h 50 m, done by about 00:10 PDT.
- **Outcome (2026-10-20, 22:21–00:15 PDT): 4.4 enters the bundle.**
  - Glob mechanism: B's next call after an empty glob was a glob 8 times out of 35 (23%), against A's 16 out of 22
    (73%). Held.
  - d = +1 (B 3/36, A 2/36), so d ≥ −1.
  - `repeat_loop` stops: 6 against 14. Held.
  - 4.5's live check: 20 `repeat_loop` stops, all exit 9, all at a real 8th unchanged repeat by the replay. No
    disagreement.
  - 0 void cells after a disk-floor relaunch.
  - Record: `e1-calibration-c6-44-2026-10-19.md`.

### 12.9 The pass-rate question: one bundle arm against the baseline (owner, 2026-10-19), registered before 4.4 runs

- **The baseline.** C5's 36 runs and 4.6's arm A ran the same configuration on bit-identical builds (§12.5). Pooled,
  they are the Qwen3-8B baseline: **5 of 72, about 7%**, with task 11 at 4/6, task 2 at 1/6, and the other ten tasks at
  0/6.
- **The screens.** 4.4, 4.1, 4.2 and 4.3 are each measured as screens of 72 runs. At that size passes can show only
  gains of about 25 points (7% → 33% at 80% power), so a screen decides by its change's mechanism metric and a floor on
  d. A screen that does not adopt means "not shown".
- **The bundle comparison.** Once 4.1–4.3 are built and screened:
  - one bundle arm (every adopted change) against the baseline arm;
  - 14 runs per task per arm, 336 runs, interleaved per task, each after the cell check;
  - run as a night job, about 9 hours.
- **Power: about 80% to see 7% → 17%,** a gain of 10 points, about one more task in ten. That is the smallest gain
  that would matter for E1's A0-against-A1 comparison. At 7%, the set sits below the 20–80% band where E1 has power
  (DESIGN §15.1).
- **Pass credit cannot be split between changes.** The bundle's d belongs to the bundle. Each change's own effect is
  read only through its mechanism metric, from the same runs.
- **The bundle runs on Qwen3-8B** (owner, 2026-10-19). The choice is fixed before any screen result.
  - **Its failures are mostly the window,** which the adopted changes target. It filled the window in 47 of its 72
    baseline runs; `fit_window` halved those stops in 4.6, and `stop_repeats` (4.5) ends its loops.
  - **It has passes to build on:** task 11 at 4/6, and task 2 at 1/6.
  - **Qwen3-4B (C4) never produced a passing fix:** 0/36, with 19 runs calling `done` with no work done.
    - It gives the bundle no baseline to improve on.
    - Its main failure is one that only 4.2 addresses.
    - A starting rate of 0 would need still more runs.
    - C4 also ran on goinfer `f20f0d93` without `fit_window` or `stop_repeats`, and the bit-identity check covers only
      Qwen3-8B.
- **The Qwen3-4B question: a mechanism check run beside 4.2's screen** (owner, 2026-10-19, replacing an earlier
  condition). 4.2's screen runs on Qwen3-8B, which rarely calls `done` without working, so it cannot show 4.2's
  effect on Qwen3-4B. Registered before 4.2 runs:
  - **Runs:** Qwen3-4B at 30,720 on goinfer `b2b5e816`, thinking off.
    - Settings: 4.2 (`done` requires verification) on, plus `fit_window` and `stop_repeats`.
    - Two runs on each of tasks 2, 3, 4, 6, 8 and 9: 12 runs, about 20–30 minutes, beside 4.2's screen.
    - These are the tasks where C4's Qwen3-4B called `done` without working in all three runs: 18 of 18.
  - **Metric:** runs that make a real edit attempt before `done`, meaning at least one `edit` or `write` call before
    the first `done` call, whatever its outcome.
  - **Rule:** a full Qwen3-4B screen (72 runs) follows only if most of the 12 do, 7 or more. Otherwise Qwen3-4B is not
    run again in C6.
  - **It is a mechanism check, not a pass-rate measure.** Its passes are recorded, not judged.
  - **It needs the pilot to run one arm with settings**, which it refuses today. That small harness change is built
    with 4.2 and shown to the owner with it.
  - **Dropped (owner, 2026-10-20):** 4.2 cannot reach a run that changes nothing. After 4.2's screen, the bundle runs
    on Qwen3-4B as an exploratory point instead (§12.15).
- **The decision rule** for the bundle is registered with its run, before it.

### 12.10 Change 4.1, edit messages: a screen (owner, 2026-10-20), registered before its runs

**Archived, closed.** Outcome (2026-10-20): 4.1 does not enter the bundle ("not shown"). The registration and its reading are in [the archive](e1-calibration-preregistration-2026-10-18-archive.md), § 12.10.

### 12.11 Change 4.8, instruction files, whole against none: a screen (owner, 2026-10-20), registered before its runs

- **Arms:**
  - A is the bundle as 4.1's verdict leaves it (`fit_window`, `stop_repeats`, `result_notes`, and `edit_messages` if
    4.1 enters), with the repository's instruction file loaded whole (`--instructions whole`), as now.
  - B is A plus `[run] no_instructions`: no instruction file is loaded.
- **Build:**
  - tack at or after `1ea11e7`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Run:** the 12 tasks, A B B A A B, 72 runs, each after the cell check. By day, about 2 hours, after 4.1's verdict
  (owner).
- **What "none" is for.** It measures what the whole file costs in window. It never enters the bundle: it drops the
  repositories' own rules, and the calibration's oracles do not check whether rules are followed.
- **Exact first-prompt tokens per task.** These are goinfer's own counts, from 4.4's 72 runs; they are identical in every
  run of a task. The owner's byte estimates are replaced by these:

| task | repo | A: first prompt | of it, `CLAUDE.md` | B predicted (tack's own prompt + statement) | A leaves of 16,384 |
|---|---|---|---|---|---|
| 1 | goinfer | 10,580 | 9,693 | 887 | 5,804 |
| 2 | goinfer | 11,626 | 10,702 | 924 | 4,758 |
| 3 | goinfer | 10,674 | 9,693 | 981 | 5,710 |
| 4 | goinfer | 6,894 | 5,959 | 935 | 9,490 |
| 5 | goinfer | 10,643 | 9,693 | 950 | 5,741 |
| 6 | goinfer | 8,685 | 7,748 | 937 | 7,699 |
| 7 | ken | 5,282 | 4,360 | 922 | 11,102 |
| 8 | goinfer | 6,463 | 5,533 | 930 | 9,921 |
| 9 | goinfer | 9,693 | 8,685 | 1,008 | 6,691 |
| 10 | ken | 6,229 | 5,327 | 902 | 10,155 |
| 11 | ken | 4,064 | 3,180 | 884 | 12,320 |
| 12 | aikit | 906 | 0 | 906 | 15,478 |

  Task 12 loads no instruction file, so its two arms are identical: a control.
- **Mechanism metrics, per arm:**
  - first-prompt tokens per task;
  - window-full stops (`window_full` plus prompt-too-long refusals);
  - requests per run before the stop.
- **Predictions:**
  - B's first prompt equals the predicted figure, within 20 tokens, on every task;
  - B has fewer window-full stops than A, mostly on tasks 1, 2, 3, 5 and 9, which carry the most instruction tokens;
  - task 12's two arms end alike;
  - d between −1 and +2.
- **Decision rule (owner):** the window cost of whole instructions is shown if B's window-full stops fall as predicted and
  d ≥ −1, the same harm guard as the other screens. At 72 runs, d cannot carry more than that.
  - Shown: section loading (4.8b: ken K9 sections, which needs ken v1.10.0) is built and screened against whole.
  - Not shown: section loading waits for its place in P1's schedule (DESIGN §16).
  - Either way, "none" never enters the bundle.
- **A noted fallback, not an arm (owner).** If the window matters but section loading must wait, a budgeted head is the
  cheap stand-in: the file's headings plus its first section, capped at 15% of the window (2,457 tokens at 16k), with
  no ranking.
  - **Chosen by the owner after the outcome:** adopted as §12.12, with a mechanism check only.
- **Outcome (2026-10-20, 12:50–14:23 PDT): the window cost of whole instructions is shown, thinly.**
  - Window-full stops fell from 4 in A to 1 in B.
  - d = +2 (B 4/36, A 2/36).
  - B's first prompts were 9 tokens under the predicted figures on every task.
  - Task 12, whose arms are identical, differed by a pass and a window-full stop: that is the noise at this size.
  - 4.5's live check agreed on all 19 stops (all exit 9). One stopped run, task 12 B-3, passed: an edit whose
    new_string contains its old_string repeated with the same reply while the file grew. The stop cost no pass.
  - Next, under the rule: section loading (4.8b), or the owner's cheaper fallback, the budgeted head.
  - Record: `e1-calibration-c6-48-2026-10-20.md`.

### 12.12 The budgeted head of the instruction file: adopted on 4.8's evidence, with a mechanism check only (owner, 2026-10-20)

- **What it is:** `[run] instructions_head` (`internal/core/instructions.go:instructionHead`). Under
  `--instructions whole`, tack loads the instruction file's head instead of the whole file:
  - a line naming the file and saying to read it for any section;
  - the file's first section, which runs to the first heading after some body text, so a file that opens with only
    its title (goinfer's) takes in the section after it;
  - an outline of the file's other headings (headings inside code fences are not headings).
- **The cap:** 15% of the served window, 2,457 tokens at 16k.
  - It is enforced in bytes at 3 bytes per token, 7,371 bytes. The calibration's `CLAUDE.md` files measure 3.6–3.8
    bytes per token (§12.11's table against their sizes).
  - Over the cap, the outline is cut from its end first, then the first section at a line. Each cut says so in the
    text the model reads.
- **Adopted without a screen (owner).** The evidence is 4.8's (§12.11):
  - neither "whole" nor "none" beat the other on passes (A 2/36, B 4/36);
  - whole's window cost is shown, thinly;
  - the head sits between them.
- **It enters the bundle** from the next screen (4.2) on, in both arms, and in §12.9's bundle arm.
- **Section loading (4.8b, ken K9) stays in P1's schedule** (DESIGN §16). The head stands in until then.
- **Mechanism check only, in 4.2's arm A:**
  - A task's head share is its first prompt (goinfer's token count) less its first prompt with no instructions (4.8's
    arm B, §12.11's outcome).
  - **It must be under the cap, 2,457 tokens, on every task.** A share over the cap on any task is a defect, fixed
    before the next screen.
  - The predictions below are reported against, not judged.
- **Predictions.** Heads were measured through `instructionHead` itself on each task's `CLAUDE.md` at its base commit;
  none is cut. The predicted share is bytes / 3.7, and the band is bytes / 4.0 to bytes / 3.4.

| task | repo | whole file's share (§12.11) | head, bytes | head share, predicted | first prompt, predicted (4.8 B + share) |
|---|---|---|---|---|---|
| 1 | goinfer | 9,693 | 2,108 | 570 | 1,448 |
| 2 | goinfer | 10,702 | 2,142 | 579 | 1,494 |
| 3 | goinfer | 9,693 | 2,108 | 570 | 1,542 |
| 4 | goinfer | 5,959 | 2,027 | 548 | 1,474 |
| 5 | goinfer | 9,693 | 2,108 | 570 | 1,511 |
| 6 | goinfer | 7,748 | 2,070 | 559 | 1,487 |
| 7 | ken | 4,360 | 642 | 174 | 1,087 |
| 8 | goinfer | 5,533 | 2,027 | 548 | 1,469 |
| 9 | goinfer | 8,685 | 2,091 | 565 | 1,564 |
| 10 | ken | 5,327 | 642 | 174 | 1,067 |
| 11 | ken | 3,180 | 616 | 166 | 1,041 |
| 12 | aikit | 0 | none | 0 | 906 |

- **What it does not check:** whether the model follows rules that are now only headings. As §12.11 says, the oracles
  do not check rules.
- **Found in 4.2's screen (2026-10-20): the head as built breaks goinfer's tasks.** Qwen3-8B called `done` with no work on
  35 of 36 goinfer runs, reading the head as its task (`e1-calibration-c6-42-stopped-2026-10-20.md`). The token check
  above cannot see this. The head is not used again until it is fixed and checked at tier 1.
- **The fix (owner, 2026-10-20): the notes come first, the task last.**
  - The head now opens the first message, framed "Project notes from CLAUDE.md, for reference:". Its outline is
    headed "Other sections of CLAUDE.md, to read when you need one:".
  - The task statement follows it and ends the message, under an explicit "Task:" label.
  - The cap stays at 15% of the window.
- **Adopting any prompt change now needs a tier-1 live check (owner, 2026-10-20),** not only a size check. A change to
  what the model reads first (system prompt, tool descriptions, first message) is run live on a small set of tasks
  before it enters a screen or the bundle.
- **The head's tier-1 check, registered before it runs** (by day, under nobara's lock):
  - **Qwen3-8B** (`Q4_K_M`, 16,384, thinking off, goinfer `b2b5e816`): tasks 2, 4 and 6, two runs each, with the
    bundle settings (`fit_window`, `stop_repeats`, `result_notes`, `instructions_head`, `refuse_duplicate_edits`).
    **It passes if at least 5 of the 6 make a call other than `done` before their first `done`.** If it fails, the head
    stays out and 4.2's screen does not restart.
  - **Qwen3-4B, exploratory** (as C4 loaded it, at 30,720, thinking off, goinfer `b2b5e816`): the same three tasks, two
    runs each, with the same settings and the new layout. It records how many runs make an edit attempt (an `edit` or
    `write` call before the first `done`). C4 quit on all of these tasks with the old layout, possibly for the same
    reason. No rule follows from it.
  - 4.2's screen restarts only after the Qwen3-8B half passes.
- **Outcome (2026-10-20, 17:02–17:23 PDT, tack `9b71ee0`, goinfer `b2b5e816`): the Qwen3-8B half passes, 6 of 6.**
  - Every run made other calls before any `done`. Three reached `done` after an edit and a bash call (task 4 once,
    task 6 twice). Two ended on `repeat_loop` (task 2, task 4), and one on a window-full refusal (task 2: 16,392
    tokens). None passed.
  - Both halves' first messages opened with "Project notes from CLAUDE.md, for reference:" and ended "Task: …".
  - **Qwen3-4B, exploratory: 0 of 6 made an edit attempt.** Unlike C4, all 6 searched first (grep, read) and none
    began with `done`. Three ended on `repeat_loop` after 10 greps, and three on `done` refused once, then accepted.
  - Cells: all clean. goinfer-3f reported a read-only file scan on nobara, 17:02–17:05:30, beside task 2's two
    Qwen3-8B runs. This check counts calls, not time, so they were kept.
  - Records: `~/tack-runs/e1/c6-head-t1-2026-10-20/` on the Mac.
- **A candidate, not registered:** a refusal of `done` with no change that restates the task.

### 12.13 Amendment to 4.5: the repeat rule compares the file's state after edit and write; adopted (owner, 2026-10-20)

- **The finding** (4.8, task 12 B-3, §12.11's outcome):
  - An edit whose new_string contains its old_string applied 9 times with the same reply while the file grew.
  - The rule read "unchanged" from the reply and ended the run at the 8th repeat. The run passed anyway.
- **The amendment:**
  - For edit and write, the rule compares the reply together with the file's content after the call, failed calls
    included (`tools.Result.State`, a hash of the file; `loop.RepeatResult`). A call that changed its file is never an
    unchanged repeat.
  - Every other tool, and every call the tool did not run (a "not run" reply), is compared on the reply alone, as
    before.
  - `result_notes`' "same result" note reads the same guard's count, so it follows the amendment.
  - A write that succeeds always leaves the same content, so for write the state changes only a failed write's
    comparison.
- **Judged as 4.5 was, by replay (§12.7).**
  - **Adopted** if the amended rule stops no passing run.
  - The replay is re-run over all captured runs, and every stop that moves is reported.
- **The replay** (`tools/replayrepeats -mirrors`, `pilot.ReplayRepeats` with a base tree):
  - It rebuilds each file from the task's base commit (the bare mirrors) plus the run's own successful edits and
    writes.
  - A file a bash call changed is not rebuilt. A successful edit whose text the rebuilt file lacks is counted as lost,
    and each stop is printed with its losses.
  - `TestRepeatRuleSeesTheFileChange` holds the replay to the live rule. Replaying a run made with the rule off stops at
    the request where the same run with it on ended. Replayed on replies alone, the same run stops three requests
    earlier.
- **Outcome (2026-10-20, replayed 15:23 PDT): adopted under the criterion.**
  - Over 406 counted runs (23 passing), the amended rule stops 98 runs and no passing run.
  - The registered rule stops 102 over the same runs, one passing: 4.8's task 12 B-3.
  - 4 stops move. Each is an edit stop that goes to "does not stop". No new stop appears. No stopped run has a lost
    rebuild.
  - Record: `e1-calibration-c6-45-amended-replay-2026-10-20.md`.
- **Adopted (owner, 2026-10-20), together with the duplicate-edit guard (§12.14).** Without the guard the amendment
  leaves re-applying loops unstopped. Both are in the bundle from 4.2 on, in both arms, and in §12.9's bundle arm.

| run | registered rule | amended rule | passed | note |
|---|---|---|---|---|
| 4.6 task 5 B-2 | stops after request 16 | does not stop (26 requests) | no | 17 re-applications of one edit |
| 4.8 task 10 A-1 | stops after request 26 | does not stop | no | live-stopped: what follows is not seen |
| 4.8 task 10 A-3 | stops after request 24 | does not stop | no | live-stopped |
| 4.8 task 12 B-3 | stops after request 18 | does not stop | **yes** | live-stopped; the finding above |

- **What the amendment gives up.** All four moved runs are loops of an edit that applies itself again. The registered
  rule ended them; the amended rule lets them run until another stop or the budget. 4.6's task 5 B-2 shows the cost: 10
  more requests, in a failing run. The duplicate-edit guard (§12.14) is what ends that loop without a stop.

### 12.14 The duplicate-edit guard: counted by replay, adopted with §12.13 (owner, 2026-10-20)

- **What it is:** `[run] refuse_duplicate_edits` (`tools.Options.RefuseDuplicateEdits`), off by default.
  - Edit refuses a call whose path, old_string and new_string equal those of an edit that already succeeded on that
    file this session.
  - The reply is `error: this exact edit was already applied to "P" at line N earlier in this session; read "P" to see
    it`.
  - The check comes after the file is read and before old_string is looked for. So it answers both cases: a
    re-application (new_string contains old_string), and a resend whose text is gone, which is now told "this text
    isn't in …".
- **The count** (`pilot.ReplayDuplicateEdits`, run by `tools/replayrepeats -mirrors`). Over the same 406 counted runs,
  the guard would have:
  - touched 21 runs, 8 of them passing;
  - refused 92 calls, 61 of which the run applied again.
- **The 8 passing runs it touches:**
  - **Seven are task 11** (4.1 A-3, 4.4 A-2 and A-3, 4.6 A-2, 4.8 B-2, C5 pass-1 and pass-3).
    - The model resent its successful edit at request 5, once or several times between reads.
    - Each resend was told "this text isn't in … the edit may already have been made".
    - The guard would have said "already applied at line N".
  - **The eighth is 4.8's task 12 B-3:** 8 re-applications, which the guard would have refused.
  - The replay cannot say what the model does after a different reply.
- **Adopted (owner, 2026-10-20), together with §12.13's amendment,** which without it leaves re-applying loops
  unstopped. A replay cannot judge it: it changes replies in passing runs, and a replay cannot say what follows a
  different reply.
- **It enters the bundle from 4.2 on,** in both arms, and in §12.9's bundle arm.
- **Mechanism check, in 4.2's runs (both arms):**
  - the guard's refusals, per task and arm;
  - on task 11, what the model does after "already applied at line N": its next call, and whether the run passes;
  - the stops of the amended rule, checked against `tools/replayrepeats -mirrors`, as 4.5's live check is.

### 12.15 Change 4.2, done requires verification: a screen (owner, 2026-10-20), registered before its runs

- **The rule, as built** (`[run] verify_done`, `internal/loop/verify.go`; §4.2):
  - After an edit or write succeeds, `done` is refused until a verification has run since.
  - A verification is a bash call whose command line, as tack's shell parser reads it (`perms.Engine.Analyze`), runs
    `go build`, `go vet` or `go test` (also through `cd … &&`, pipes and `go -C`). `echo go test` is not one.
  - The refusal names the call that made the change, quotes the latest verification before it (command, exit status,
    the last 20 lines of its output), and ends "Run go build ./..., or go test on the package you changed, then call
    done." (owner: §4.2's "or the task's tests" is replaced. ken's whole-repository go test fails on the sandbox's
    loopback refusal, task 11, and the model should not be pushed into a failure it cannot fix.)
  - A pass is not required. The 4th `done` in a turn ends it whatever the state.
  - The record says how the run ended: `done`, `done (unverified …)` or `done (the last build or test … failed)`
    (`pilot.TackSummary.Done`), and counts the refusals (`VerifyRefused`).
  - It sits beside the refusal of `done` with no file changed, which is unchanged.
- **Arms:**
  - A is the bundle as §12.12–12.14 leave it: `fit_window`, `stop_repeats` (as amended), `result_notes`,
    `instructions_head` and `refuse_duplicate_edits`, under `--instructions whole`.
  - B is A plus `verify_done`.
- **Build:**
  - tack at the commit that carries `verify_done`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Run:** the 12 tasks, A B B A A B, 72 runs, each after the cell check, holding both machines' locks. By day, about
  1 h 45 m (4.8 took 1 h 33 m; B adds build and test calls). The sandbox cache is warmed untimed first, and free disk
  is checked against the 20 GB floor.
- **Base rates,** from the captured Qwen3-8B bundle screens (4.1 with its incomplete blocks, 4.4, 4.8: 224 runs), with
  the rule's own parser:
  - 37 runs ended in `done` after a change.
  - 27 of them (73%) had run a verification after the last change. The last verification failed in 25 of those 27,
    often from the model's own flags (`-mod=vendor` with no vendor directory, `-race` with `-covermode=set`).
  - 10 ended unverified, 6 of them passing. In 4.8, all three of B's unverified passes are task 11's.
- **Mechanism metrics:**
  1. Of each arm's runs that end in `done` after a change, the share with a verification after the last change.
  2. In B, the model's next call after each refusal: a verification, an edit, or `done` again.
  3. In B, refusals per run.
  4. B runs that end at the 4th `done`, unverified.
  5. Which verification command each B run used: its build and test command lines.
  6. The cost: requests per run and window-full stops, per arm.
- **Predictions:**
  - metric 1: B at least 90%, A between 55% and 90%;
  - metric 2: a verification after at least half of B's refusals;
  - metric 3: refusals in 3–12 of B's 36 runs;
  - metric 4: at most 2 B runs end unverified;
  - d between −2 and +1. The risk is task 11, whose passing runs often finish without a build: a refusal there sends
    the model to tests that may fail for reasons of their own.
- **Decision rule,** the same as the other screens: 4.2 enters the bundle if metric 1 moves as predicted (B at least
  90%, and above A) and d ≥ −1. At 72 runs, "not adopted" means "not shown".
  - **B's 90% is close to certain by construction** (owner): every B run that ends in `done` after a change has
    verified, unless it reached the 4th `done`. So d ≥ −1 is the effective test.
- **Checks that ride on these runs** (§12.12–12.14):
  - **The head,** in arm A: each task's first prompt less 4.8's arm B's first prompt is under 2,457 tokens. Reported
    against §12.12's predicted figures.
  - **The duplicate-edit guard,** in both arms: its refusals per task and arm. On task 11, what the model does after
    "already applied at line N": its next call, and whether the run passes.
  - **The amended stop rule:** `tools/replayrepeats -mirrors` over the 72 runs stops exactly the runs that ended live
    with `repeat_loop`.
- **The harness:** `tack eval pilot -a1 … -setting fit_window -setting stop_repeats -setting result_notes -setting
  instructions_head -setting refuse_duplicate_edits -compare-setting verify_done`.
- **The Qwen3-4B check (§12.9) is dropped from 4.2 (owner).** 4.2's rule starts only after a change, and every one of
  C4's 18 runs on tasks 2, 3, 4, 6, 8 and 9 was `done`, refused once as "no files changed", then `done` again, with no
  other call. 4.2 never reaches such a run.
- **After 4.2's screen: the bundle on Qwen3-4B, an exploratory point** (owner).
  - The bundle as 4.2's verdict leaves it, as one arm: `-only 2,3,4,6,8,9 -reps 2 -ctx 30720`, 12 runs. Qwen3-4B as
    C4 loaded it (bf16, int4 at load), on goinfer `b2b5e816`, thinking off. By day, about 20–30 minutes.
  - It counts the runs that make an edit attempt before `done`: at least one `edit` or `write` call before the first
    `done`, whatever its outcome. Passes are recorded, not judged.
  - **It is exploratory.** There is no A arm, and C4, the only comparison, ran an older setup: goinfer `f20f0d93`,
    without `fit_window` or `stop_repeats`, the whole instruction file. No rule follows from it.
- **A candidate for Qwen3-4B, not registered:** how `done` with no change is refused. Today it is refused once, and the
  next `done` ends the turn.
- **Outcome (2026-10-20, 16:39–16:51 PDT): stopped at 41 of 72 runs, no verdict.**
  - The budgeted head (§12.12), as built, led Qwen3-8B to call `done` with no work on 35 of 36 goinfer runs, in both
    arms. Its `done` reasons read the instructions as the task.
  - 4.2's rule acts only after a change, so it never fired.
  - Record: `e1-calibration-c6-42-stopped-2026-10-20.md`. The head is to be fixed, and checked at tier 1, before
    4.2 is screened again.
- **Outcome of the restart (2026-10-20, 20:12–21:51 PDT, tack `9b71ee0`): 4.2 enters the bundle, at the floor.**
  - Metric 1: B verified in 10 of its 10 runs ending in `done` after a change, A in 7 of 9.
  - d = −1 (B 5/36, A 6/36), exactly the floor. The one difference is task 11 B-2, wrong from its first edit and
    never refused.
  - The rule refused `done` in one B run of 36, and that run verified next. No run ended unverified.
  - Riding checks:
    - the head's shares are under the cap on every task;
    - the duplicate-edit guard refused 10 calls in 8 runs, and on task 11 every refusal was followed by a read (5 of 6
      passed);
    - the amended stop rule's 26 live stops match the replay exactly.
  - Record: `e1-calibration-c6-42-2026-10-20.md`.

### 12.16 Change 4.3, loopback in the sandbox: held, no screen (owner, 2026-10-21)

- **Built on branch `c6-43-loopback`, held before its screen.** That branch holds the code, the escape tests, the
  proposed registration and the ADR-005 draft.
- **Why it is held.** Seatbelt cannot restrict listening to 127.0.0.1: its `localhost` covers every address of the
  machine. A test server inside the sandbox, started by a `go test` command, was reachable from nobara over Tailscale.
- **Record:** `e1-calibration-c6-43-held-2026-10-21.md`. The sandbox keeps loopback denied. Its known cost is that
  tests which start a local server fail in the sandbox, as ken's `internal/modelfetch` does.
- **No screen runs,** and 4.3 is not in the bundle. The open design question, for the owner: run `go test` outside
  Seatbelt, in a VM, or under a port-scoped firewall rule.

### 12.17 Build after every edit: a screen (owner, 2026-10-25), registered before its runs

- **The change, as built** (`[run] build_after_edit`, `internal/loop/build.go:Session.buildNote`; tack `8ce3bcc`, on
  main at `0a60b61`):
  - After a successful `edit` or `write` of a `.go` file, tack builds that file's package in the session's own shell,
    inside the sandbox. The command is `go -C <module root> build -o /dev/null ./<package>`, or `go test -c -o
    /dev/null ./<package>` for a `_test.go` file. Nothing is run.
  - When the package does not build, the result gains a note: "[tack: this change does not build (go build ./p):", then
    go's first 10 error lines, each cut at 200 characters.
  - It adds nothing when the package builds, when the file is in no module, when the build runs past 60 s or is
    stopped, or when the permission engine would not run the same `bash` call unasked. Every A1 run is in auto mode in
    the sandbox, where it runs.
  - It is not a verification for `verify_done`, and the repeat rule compares results without the note
    (`loop.StripNotes`).
  - The fixed prompt is unchanged.
- **Arms** (owner, 2026-10-25):
  - A is the adopted bundle: `fit_window`, `stop_repeats` (as amended), `result_notes`, `instructions_head`,
    `refuse_duplicate_edits` and `verify_done`, under `--instructions whole`. This measures what `build_after_edit`
    adds on top of it.
  - B is A plus `build_after_edit`.
- **Build:**
  - tack at this registration's commit, clean, on both machines;
  - goinfer `b2b5e816`, as §12.15's screen;
  - Qwen3-8B `Q4_K_M` at 16,384, `-serve-option thinking-off`.
- **Run:**
  - the 12 tasks, A B B A A B, 72 runs, each after the cell check, interleaved per task;
  - holding both machines' timing locks, started only when nobara's queue is idle and its lock free, behind every job
    already queued or waiting there;
  - about 1 h 45 m, as §12.15's (1 h 39 m), plus about a second per edit in B. If it cannot start by day, it runs that
    night (owner).
  - Before it: Go's build cache cleared if the Mac is under the 23 GiB floor (owner's standing permission), and tack's
    sandbox cache warmed untimed for all 12 tasks.
- **Base rate:** across the recorded C6 runs, 62 changed code and still failed, and 23 of those left the model's own
  code not compiling, over 8 of the 12 tasks (`e1-calibration-c6-status-2026-10-21.md`). §12.15's arm B, the closest
  arm to A, had 5 passes in 36.
- **Mechanism metrics:**
  1. In B, the share of `.go` edits and writes whose result carries the note.
  2. In B, after each note, whether the run's next `.go` edit or write builds (no note), carries the note again, or
     never comes.
  3. **Per arm, the runs that end with code not compiling:** the oracle's `go test` for the run reports a build failure
     (`[build failed]` or `[setup failed]`).
  4. The cost per arm: wall time per run, requests per run, window-full stops; and in B, the build time per edit.
- **Predictions:**
  - metric 3: B at most half A's count;
  - metric 2: the next `.go` change builds after at least half of B's notes;
  - d between −1 and +3.
- **Decision rule** (owner, 2026-10-25, the draft as written):
  - **Enters the bundle:** B's count on metric 3 is at most half A's, and d ≥ −1 (B passes at most one task fewer
    than A).
  - **Parked** (examined, not adopted): metric 3 moves but d < −1; or d ≥ +2 with metric 3 unmoved, since a gain with
    no mechanism needs one before it moves a bar.
  - **Not adopted:** neither. At 72 runs, "not adopted" means "not shown".
  - Read literally at the edge: if A has no run ending uncompiled, "at most half" asks B for none either. The record
    reports that case as it falls.
- **The harness:** `tack eval pilot -a1 … -setting fit_window -setting stop_repeats -setting result_notes -setting
  instructions_head -setting refuse_duplicate_edits -setting verify_done -compare-setting build_after_edit`.
- **Checks that ride on these runs:** `tools/replayrepeats -mirrors` over the 72 runs stops exactly the runs that ended
  live with `repeat_loop`, which also shows the build note leaves the repeat rule as it was.
- **Outcome (2026-10-25, 14:43–16:11 PDT, tack `e3fdf99`, goinfer `b2b5e816`): not adopted, "not shown."**
  - Metric 3: A 6, B 6; the rule needed B at most 3. d = −1 (B 4/36, A 5/36).
  - Metric 2: after 0 of B's 7 notes did the next `.go` change build.
  - The oracle's `[build failed]` also counts an API the model never added; with that taken out, the model's own code
    failed to compile in A 4 runs and B 2. That split was read after the runs and decides nothing.
  - The replay check: `tools/replayrepeats -mirrors` stops exactly the 30 runs that ended live with `repeat_loop`, and
    no passing run.
  - Record: `e1-calibration-c6-build-2026-10-25.md`. The setting stays in tack, off.

### 12.18 A no-change `done` refusal that restates the task: tier-1 check (owner, 2026-10-25), registered before its runs

**Archived, closed.** Outcome (2026-10-26, tack `a2679df`): fails. The registration and its reading are in [the archive](e1-calibration-preregistration-2026-10-18-archive.md), § 12.18.

### 12.19 Change 4.9, edit by line range: tier-1 check (owner, 2026-10-25), registered before its runs

**Archived, closed.** Outcome (2026-10-26, tack `a2679df`): fails (0 of 6 runs made a line edit). The registration and its reading are in [the archive](e1-calibration-preregistration-2026-10-18-archive.md), § 12.19.

### 12.20 The current text of a symbol after repeated edits to one file: tier-1 check (owner, 2026-10-27), registered before its code

**Archived, closed.** Outcome (2026-10-27): not shown, and the second check fails. The registration and both readings are in [the archive](e1-calibration-preregistration-2026-10-18-archive.md), § 12.20.

### 12.21 The bundle against A1, at screen size (owner, 2026-10-27), registered before its run

Francis chose this at 17:54 PDT, with §12.3's rule as drafted. It is §12.9's question, asked at screen size; the
336-run night stays unregistered.
- **Arms:**
  - **A, the do-nothing arm:** A1 as C5 ran it, with no `[run]` settings (`fit_window` off). Qwen3-8B `Q4_K_M` at
    16,384, thinking off, forced-call mode, sampling 0.7 / 0.8 / 20, `max_tokens` 2048, failure budget 8, the
    10-minute cap, `--instructions whole`.
  - **B, the bundle:** A plus `fit_window`, `stop_repeats` (as amended), `result_notes`, `instructions_head`,
    `refuse_duplicate_edits` and `verify_done`.
  - Not in either arm: `build_after_edit`, `restate_task`, `edit_lines`, `current_symbol`, `edit_messages`.
- **Build:**
  - tack `5d54f3c` (`-compare-setting` is repeatable, so B adds all six to A's none), clean, on both machines;
  - goinfer `b2b5e816`.
- **Run:**
  - the 12 tasks, A B B A A B, 72 runs, each after the cell check (§12.2);
  - holding both machines' timing locks, about 1.5–2 hours;
  - tack's sandbox cache warmed untimed first, as §12.17's was.
- **When: by day, after the power outage of 2026-10-28** (owner). The launcher starts it at the first moment, from
  09:00 to 19:30 on 2026-10-28 or later, that the Mac is on mains, both queues are idle and both timing locks free.
  - It is submitted by hand once the power is back (a `launchctl submit` job does not survive the restart).
  - Files and the command: `~/tack-runs/e1/c6-bundle-2026-10-28/notes.txt` on the Mac.
- **Decision rule (§12.3's, owner, 2026-10-27).** d = B's passes − A's, out of 36 each:

| d | verdict |
|---|---|
| **+3 or more** | **the bundle beats A1** at screen size |
| +2 | parked: 3 more runs per task in each arm, then the same rule on all 6 per task |
| +1 or less | not shown; the do-nothing arm wins ties |

  - The sd under no effect is §12.3's, 1.15, from C5's rates. False adoption: about 1%.
  - **Pass credit is the bundle's** (§12.9). No change's share is read from it.
- **Predictions:**
  - task 1: B at least 2 of 3, A at most 1 of 3. Over the bundle runs since the head was fixed it passed 11 of 12;
    over the earlier Qwen3-8B runs, 1 of 27.
  - task 11 alike in both arms, about 2 of 3;
  - d between +2 and +5;
  - window-full stops: A at least twice B's.
- **Reported, not decided on:** each task's passes in both arms, stops by reason, window-full stops, requests per
  run, `done` refusals, and the cell states.
- **Riding check:** `tools/replayrepeats -mirrors` over B's runs stops exactly B's runs that ended live with
  `repeat_loop`.
- **Outcome (2026-10-28, 19:01–21:19 PDT, tack `5d54f3c`, goinfer `b2b5e816`; record
  `e1-calibration-c6-bundle-2026-10-28.md`): not shown, d = +1.**
  - B passed 4 of 36 and A 3 of 36. Task 1 went from 0/3 in A to 3/3 in B, and task 11 from 2/3 to 1/3.
  - 0 cells were void. The parked row is not triggered, so no runs follow.
  - Window-full stops: A 27, B 0.
  - The riding check holds: the replay stops exactly B's 16 live `repeat_loop` runs.
  - **Francis, 2026-10-29 07:35 PDT:** the bundle, with `edit_tolerances` added (§12.22), stays C6's loop and is the
    loop for §12.23's row. The powered comparison is deferred until §12.23 step 2's model question is settled.

### 12.22 Safe edit tolerances: judged by replay (owner, 2026-10-27), registered before their code

Francis chose this at 18:31 PDT, from `e1-calibration-c6-next-direction-proposed-2026-10-27.md`. Fuzzy matching is
dropped. DESIGN §7.5 is unchanged: edit still requires the file to have been read.
- **The change, as it is to be built** (`[run] edit_tolerances`, off by default; the fixed prompt is unchanged):
  - **When:** an `edit` with `old_string` finds no exact match in a `.go` file.
  - **What:** the tolerances are tried in a fixed order.
    1. Whitespace removed entirely.
    2. Then each kept punctuation kind, one at a time: that character dropped where it ends a line of `old_string`.
  - **Each must find exactly one place**, or nothing applies.
  - The edit applies `new_string` as given, and **only if the file still parses as Go afterwards**. Otherwise it fails
    as it does today.
  - The result names the tolerance that matched.
- **The kinds replayed, each on its own:** `;` `,` `)` `]` `}` `.` `:`. Francis widened the rule from `;` alone to
  trailing punctuation, with care, since a trailing `,` or `)` can carry meaning.
- **The replay:** every captured failed edit in set A (the 4.1 replay's 216 runs) and set B (the bundle-era Qwen3-8B
  screens and checks, 335 runs). Each file is rebuilt as it was at the failed edit (`tools/replayedits`'
  reconstruction), and the built tolerance code is used to match.
- **Adoption rule, per tolerance and per kind:**
  - Kept only if **every** edit it would apply across both sets is unique, leaves a file that parses, and makes the
    change the model meant: its `new_string`'s difference from its `old_string`, at the place the fix-blind match
    found.
  - Each applied edit is listed in the record and read case by case.
  - **One wrong edit and that kind is left out,** with the case named.
  - A kind that applies nothing is left out as unshown.
- **Kept tolerances enter the bundle after §12.21's run.** §12.21's arms stay as registered.
- **Reported:** per kind, the calls and distinct failures it rescues in each set, and how many of those are on the
  task fix's lines.
- **Outcome (2026-10-27, replayed 18:35 PDT; record `e1-calibration-c6-tolerances-2026-10-27.md`):**
  - **Kept:** all whitespace ignored, and a trailing `;` ignored. Every edit either would apply across 551 runs is
    unique, parses and is the one the model meant (61 and 19 calls).
  - **Left out:** `)` (1 distinct, does not parse) and `:` (3 distinct, none parses), whose would-be edits are wrong;
    `,` `]` `}` `.` apply nothing.
  - `[run] edit_tolerances` stays off by default and joins the bundle after §12.21's run.

### 12.23 Qwen3.5-9B: the E0 append probe, then a calibration row (owner, 2026-10-27), registered before its runs

Francis chose direction 2 at 18:31 PDT, in this order: after §12.21's run, by day.
1. **E0 again for Qwen3.5-9B `Q4_K_M` at 32,768,** with amendment 11's append probe.
   - goinfer at or after B35's merge (#112, 2026-10-21), pinned at the commit named in a line added here before the
     run.
   - Thinking off, nobara, holding its timing lock, about 45 minutes. E0's decision rule is unchanged.
   - **The gate for step 2, besides eligibility:** with the prefix reused by appending, the first-token time at 8k and
     at the top cell is at most a fifth of the cold time. Qwen3-8B reads 1.9 s against 26.7 s at 15k.
   - A probe that fails the gate is reported to goinfer, and no row follows.
   - **Pinned (2026-10-29, before the run):**
     - goinfer main `95bc3c7b`, green in goinfer's CI, 1,760 commits after B35's merge `5cb1925d`. It is built from
       `git archive` with `GOOS=linux`, `-tags cuda`, `./cuda/cmd/serve`, to `~/tack-runs/bin/goinfer-serve-cuda-95bc3c7b`
       on nobara.
     - tack at the commit that adds this line, built clean.
     - The gate compares medians of the three repeats, as E0's decision rule compares first-token times.
     - It runs as one job through nobara's `night.py`, whose queue is running: a fit-only load at 32,768 first, then E0
       only if that comes up `cuda-resident`.
     - **Expected time: about 60–75 minutes, not 45.** C3's cold top cell took 421 s per repeat, and E0 times it three
       times. The rewrite cell at that size may be as slow.
   - **Outcome (2026-10-29, 08:44–09:31 PDT; record `e0-nobara-qwen35-2026-10-29.md`): eligible, and the gate
     passes.**
     - Smoke 19/20 on both runs, 0 malformed, `check` ok, resident at 32,768.
     - Append against cold, medians: 6.1 s against 39.9 s at 8k (0.15), and 18.9 s against 280.3 s at 31,744
       (0.07).
     - The build note above is narrowed in the record: GitHub's archive of `95bc3c7b`, with a `go.work` joining the
       root and `cuda` modules.
2. **If it passes:** a C5-style calibration row, registered here with its own predictions and timing before it runs.
   - Qwen3.5-9B with the bundle as adopted then, the 12 tasks, 3 runs each.
   - Its per-run time is estimated from step 1's figures. It runs by day if that comes to 2 hours or less, else as a
     night job.
### 12.24 Qwen3.5-9B on the calibration set, with the bundle (§12.23 step 2) (owner, 2026-10-29), registered before its runs

Drafted by tack's session on 2026-10-29, after §12.23 step 1 passed its gate (`e0-nobara-qwen35-2026-10-29.md`), as
§12.23 step 2 calls for.
- **Francis approved it as drafted at 11:35 PDT** (relayed by the coordinator): 32,768 context and C5's 10-minute cap.
- It is queued tonight on the Mac's `night.py`. It starts no earlier than 22:00, and is retried until 03:00 if nobara
  is busy.
- tack is `3382aa8`, the commit that added this section.
- **The arm:** one arm, C5-style.
  - **Model and context:** Qwen3.5-9B `Q4_K_M` (`~/models/Qwen3.5-9B-Q4_K_M.gguf`) at **32,768**, the context E0
    admitted and the reason this model was chosen (double Qwen3-8B's 16,384). *Francis's choice.* The alternative is
    16,384, which matches §12.21 and tack's design window.
  - **The loop:** the bundle as adopted (Francis, 2026-10-29, §12.21's outcome): `fit_window`, `stop_repeats`,
    `result_notes`, `instructions_head`, `refuse_duplicate_edits`, `verify_done` and `edit_tolerances`. Each is passed
    as `-setting`.
  - **Sampling and limits as C5:** thinking off, forced-call mode, sampling 0.7 / 0.8 / 20, `max_tokens` 2048, failure
    budget 8, the 10-minute cap. *The cap is Francis's choice:* step 1's slow appends make it likelier to bind than in
    C5 (below).
- **Build:**
  - goinfer `95bc3c7b`, the build step 1 measured (`~/tack-runs/bin/goinfer-serve-cuda-95bc3c7b` on nobara);
  - tack at the commit that adds this section, built clean on both machines.
- **Run:**
  - the 12 tasks, 3 runs each (`-reps 3`): 36 runs, each after the cell check (§12.2);
  - tack's sandbox cache warmed untimed first, as §12.21's was;
  - both machines' timing locks held by the pilot.
- **Time, estimated from step 1's figures:**
  - **The model:** each run costs its tool time plus its model time. Qwen3-8B's bundle runs in §12.21 (B) averaged
    104 s each, 16.9 requests, 8,255 prefilled tokens and 609 generated. About 75 s of that was tools and harness.
  - **Qwen3.5-9B's model time:** the prefill at step 1's 8k cold rate (205 tokens/s) is about 40 s, plus about 3 s
    per request of append overhead (step 1's append cells cost 5–19 s for about 600 tokens), plus decode at 65 tok/s
    (9 s). That comes to about 100 s, so **about 3 minutes per run**.
  - **The range:** 36 runs at 3–6 minutes is **1.9–3.7 hours**, plus the warm-up and fit load. A larger window lets
    runs go longer than Qwen3-8B's at 16k, so the upper end is plausible.
  - **That is over 2 hours, so it runs as a night job** (§12.23), queued on the Mac's `night.py`. Its launcher waits
    for both queues to be idle and both locks free, as §12.21's did.
- **Predictions (tack's session, before any run):**
  - passes: 2–8 of 36; task 1 at least 2 of 3; task 11 at least 1 of 3;
  - window-full stops: 0–2;
  - 10-minute cap hits: 2–10;
  - median run time: 2.5–6 minutes.
- **The reading:**
  - **§2's bar:** usable if it passes at least 4 of 36 runs, on at least 2 tasks.
  - **Reported beside §12.21's B arm, task by task.** That arm is the same bundle, on Qwen3-8B at 16,384.
  - **It is not a model verdict.** The window, the model and the sampling defaults' fit all differ, and §12.21's B
    is 36 runs on a different night. Whether C6 moves to Qwen3.5-9B is Francis's decision after this row.
- **Also reported, not decided on:** each task's passes, stops by reason, window-full stops, cap hits, requests per
  run, first-token times per request, `done` refusals and the cell states.
- **The read uses `tools/calreport`** (Francis, 2026-10-29 14:27 PDT), with §12.21's job and C5's three passes beside
  it. It decides nothing:

  ```
  E=~/tack-runs/e1
  go run ./tools/calreport \
    -compare $E/c6-bundle-2026-10-28/runs/results.jsonl \
    -compare $E/cal-c4c5-2026-10-18/c5/pass-1/results.jsonl,$E/cal-c4c5-2026-10-18/c5/pass-2/results.jsonl,$E/cal-c4c5-2026-10-18/c5/pass-3/results.jsonl \
    $E/c6-qwen35-row-2026-10-29/runs/results.jsonl
  ```
- **Outcome (2026-10-29 22:53 to 2026-10-30 03:03 PDT, tack `3382aa8`, goinfer `95bc3c7b`; record
  `e1-calibration-c6-qwen35-row-2026-10-29.md`): usable, at the bar.**
  - 4 of 36 passed, on 2 tasks: task 10 once and task 11 three times. Task 1 passed 0 of 3.
  - 0 cells were void.
  - Window-full stops: 5 (4 prompt-too-long, 1 `window_full`). Cap hits: 11. Median run time: 7.7 minutes. The run
    took 4 h 10 min.
  - Of the predictions, the pass count and task 11 held. Task 1, the window-full stops, the cap hits and the median
    run time missed.
  - Whether C6 moves to Qwen3.5-9B is Francis's decision.
  - **Francis, 2026-10-30 08:01 PDT:** C6 stays on Qwen3-8B at 16,384, and the powered comparison runs there.
    Qwen3.5-9B stays a candidate for when goinfer's long-prefill time drops.
### 12.25 The powered comparison: the bundle against A1 on Qwen3-8B, 14 runs per task per arm (§12.9) (owner, 2026-10-30), registered before its runs

This is §12.9's bundle comparison at its registered size. It follows §12.24 and Francis's decision at 08:01 to keep C6
on Qwen3-8B at 16,384. Francis chose the four marked choices at 08:20 PDT (relayed by the coordinator), each as tack's
session recommended them. The rest was drafted by tack's session and goes to him with the harness change it needs.
- **Francis approved the drafted rest and the harness change at 08:46 PDT** (relayed by the coordinator). The harness
  change is `16c7c3e`, and this section is `f5f7ecc`, the build the runs use.
- **The arms:**
  - **A, the do-nothing arm:** A1 as §12.21's A ran it, with no `[run]` settings. *Francis's choice:* not A1 with
    `fit_window` on (§12.6), so that A matches §12.21's A and §12.9's pooled baseline.
  - **B, the bundle as adopted:** A plus `fit_window`, `stop_repeats`, `result_notes`, `instructions_head`,
    `refuse_duplicate_edits`, `verify_done` and `edit_tolerances`. These are §12.24's seven, passed as
    `-compare-setting`.
  - **Both:**
    - Qwen3-8B `Q4_K_M` (`~/models/qwen3-8b-gguf/Qwen3-8B-Q4_K_M.gguf`) at 16,384;
    - thinking off, forced-call mode;
    - sampling 0.7 / 0.8 / 20, `max_tokens` 2048, failure budget 8, the 10-minute cap;
    - the 12 tasks.
- **Build:**
  - **goinfer `b2b5e816`** (`~/tack-runs/bin/goinfer-serve-cuda-b2b5e816` on nobara), the build §12.21 ran, as §12.1
    fixes. *Francis's choice:* not `95bc3c7b`, on which Qwen3-8B has no bit-identity check.
  - **tack at the commit that adds this section,** built clean on both machines. It includes the harness change
    that lets `-reps` set the runs per arm in compare mode (`internal/eval/pilot/run.go:abOrder`), shown to Francis
    with this section.
- **Size and order: 336 runs, over two nights of 168.** *Francis's choice:* not one night of 336 (about 11 hours), nor
  8 per task per arm.
  - Each night runs the 12 tasks, 14 runs per task back to back: A B B A A B B A A B B A A B, 7 per arm (`-reps 7`).
  - Each run follows the cell check (§12.2).
  - The two nights are the same job, written to `~/tack-runs/e1/c6-powered-2026-10-30/night-1/` and `night-2/` on
    the Mac.
- **Before night 1, the fit check:** one fit-only load of Qwen3-8B at 16,384 on `b2b5e816`, judged by the server's
  own plan, holding nobara's timing lock. It must come up `cuda-resident`, or the comparison waits.
  - It is needed because nobara took a system update on 2026-10-30, which may change the card's free memory.
  - It is the pilot's cell check (`tack eval pilot -a1 -cell-check`), run by day after nobara's other timed work.
- **When:** each night is a job on the Mac's `night.py`, as §12.24's was.
  - It starts no earlier than 22:00. It is retried until 00:30 while nobara's queue or lock is busy or the Mac is on
    battery.
  - Its launcher waits for both queues to be idle and both locks free. It clears Go's build cache once if the disk is
    under 21 GiB (the owner's standing permission), warms tack's sandbox cache untimed, and starts only after a clean
    cell check.
  - Night 2 is queued after tack's session has checked night 1. That check covers only the night itself: that it
    completed, its void cells, and harness errors. No passes are tabulated between the nights.
- **Time:** §12.21's 72 runs took about 115 s each with their cell checks. So 168 runs take about 5.4 hours, and
  about 5.5–6 hours per night with the warm-up and the server load. That is about 22:00–04:00.
- **Void and stopping:**
  - A void cell is re-run in its place and listed, never averaged (§12.2).
  - A night that ends before its 168 runs, through a crash, the power or a lock, is not counted. It is listed and
    re-run whole on a later night. The comparison is read only when two whole nights exist.
- **The decision rule (§12.3's form; *Francis's choice*, as drafted).** d = B's passes − A's, out of 168 each.
  - The sd is from §12.9's pooled baseline: task 11 at 4/6, task 2 at 1/6, the other ten at 0.
  - Under no effect, d has variance 2 · 14 · Σ p(1−p) = 28 · (8/36 + 5/36) = 10.1, so **sd = 3.18**.

| d (B − A, out of 168 each) | verdict |
|---|---|
| **+7 or more** (d ≥ 2 sd = 6.36) | **the bundle is shown to beat A1** |
| +4 to +6 (1 sd ≤ d < 2 sd) | parked: reported, with no more runs unless Francis says so |
| +3 or less | not shown; the do-nothing arm wins ties |

  - **How often the rule errs when the bundle does nothing,** taking those rates as true and d as normal: shown by
    chance about 2%, parked by chance about 12%. A task with a small true rate adds noise and raises both.
  - **Power:** about 80% to see 7% → 17% (§12.9).
  - **Pass credit is the bundle's** (§12.9). No change's share is read from it.
- **Predictions (tack's session, before any run),** from §12.21 (A 3/36, B 4/36; task 1 A 0/3, B 3/3; task 11 A 2/3,
  B 1/3):
  - A passes 8–20 of 168, and B 12–32;
  - d between +3 and +14;
  - task 1: B at least 10 of 14, A at most 3 of 14;
  - task 11: A at least 7 of 14; B within 4 of A;
  - window-full stops: A at least 100 of 168, B at most 6.
- **Reported, not decided on,** as §12.21: each task's passes in both arms, stops by reason, window-full stops, cap
  hits, requests per run, first-token times, `done` refusals and the cell states. It is also reported beside §12.21's
  arms and C5, task by task.
- **The read uses `tools/calreport`** over both nights' results together, with §12.21's job and C5 beside them. It
  decides nothing:

  ```
  E=~/tack-runs/e1; P=$E/c6-powered-2026-10-30
  cat $P/night-1/runs/results.jsonl $P/night-2/runs/results.jsonl > $P/both.jsonl
  go run ./tools/calreport \
    -compare $E/c6-bundle-2026-10-28/runs/results.jsonl \
    -compare $E/cal-c4c5-2026-10-18/c5/pass-1/results.jsonl,$E/cal-c4c5-2026-10-18/c5/pass-2/results.jsonl,$E/cal-c4c5-2026-10-18/c5/pass-3/results.jsonl \
    $P/both.jsonl
  ```
- **Night 1 (2026-10-30 23:07 to 2026-10-31 04:02 PDT): complete, checked at 07:07.** It started after four retries
  (the Mac on battery at 22:01 and 22:17, nobara busy at 22:35 and 22:51).
  - 168 runs, every task at 7 A and 7 B, 0 void, every run with tack's summary, 0 oracle errors.
  - tack `f5f7ecc`, all on mains, 3 cap hits, exit 0.
  - Passes are not tabulated before night 2, as registered.
- **Night 2 runs by day on 2026-10-31, not from 22:00** (coordinator, 10:47: Francis is away until Sunday, the Mac
  stays plugged in, and the day/night limits relax).
  - **Nothing here ties night 2 to night hours for a measurement reason.** The 22:00 start came from the run-budget
    rule (CLAUDE.md, jobs over about two hours go to night), not from the comparison.
  - **d is read within each task's interleaved A B B A … block,** so the hour of a block biases neither arm. The cell
    check still voids a run that starts under heavy load on nobara.
  - **It starts once §14.4's row ends,** as a day job on the Mac's `night.py`, retried on exit 75 until 20:00. The
    timing lock, the void rule and its own cell check, which is the fit-only load, are unchanged.
- **How the read handles voids, short arms and a parked result (2026-10-31 17:17 PDT, tack's session, written
  before either night's passes are tabulated).** A read-only audit of this rule against the analysis code (Gemini,
  relayed by the coordinator at 17:20) found three questions the rule leaves open. The coordinator recommended these
  answers, and they are adopted here:
  - **Disclosure:** while checking night 2's progress at 14:01, tack's session saw its first 3 records and 2 log
    lines, all task 2 and all fails. Nothing else of either night's passes has been read or tabulated.
  - **(a) A void's remedy.** A run voided inside a run counts as void, not as a fail. That covers disk under the
    floor, a module download failure, a failed preflight, the host sleeping, and an oracle error (a harness error
    grading the run, which is not a model failure). After the night, each void run is re-run once in its place:
    the same task, arm and build, and the same settings. The re-runs are listed. A night that ends early (a crash,
    the power, a lock) is still re-run whole, as above.
  - **(b) d when an arm is short.** If an arm has fewer than 168 counted runs after the re-runs, d is rate-scaled to
    168: d = 168 · (B/n_B − A/n_A). The read says so, and it is judged on the sd edges: shown at d ≥ 6.36 (2 sd),
    parked at 3.18 ≤ d < 6.36 (1 sd), not shown below. With 168 counted runs in each arm, d is the integer B − A,
    and those edges are the table's +7, +4 to +6 and +3.
  - **(c) A parked result** (+4 to +6) is reported, and nothing continues it without Francis. Any continuation he
    asks for is registered first: its size, and how it combines with these 336 runs.
  - **The command:** `tools/calreport` takes each night's results file as its own input, so each record keeps its
    run directory, and it prints d, the sd, the verdict and each arm's n (`-rule` below). The concatenated
    `both.jsonl` above would point every record at a run directory that does not exist, so it is not used.
    `tools/calreport` also counts an oracle error as a void run, as (a) says. The read is:

    ```
    E=~/tack-runs/e1; P=$E/c6-powered-2026-10-30
    go run ./tools/calreport -rule sd=3.18,n=168 \
      -compare $E/c6-bundle-2026-10-28/runs/results.jsonl \
      -compare $E/cal-c4c5-2026-10-18/c5/pass-1/results.jsonl,$E/cal-c4c5-2026-10-18/c5/pass-2/results.jsonl,$E/cal-c4c5-2026-10-18/c5/pass-3/results.jsonl \
      $P/night-1/runs/results.jsonl,$P/night-2/runs/results.jsonl
    ```
  - **The nights' voids:** night 1 and night 2 each have 168 records, 84 per arm, 0 void and 0 oracle errors
    (checked 2026-10-31 after this amendment was committed, reading those fields only). So no re-run is needed, and
    d is the integer B − A.
- **Outcome (read 2026-10-31; record `e1-calibration-c6-powered-2026-10-31.md`): A 9 of 168, B 26 of 168, d = +17.
  The bundle is shown to beat A1** (+7 or more, against 2 sd = 6.36).
  - The gain is on tasks 1 (+12), 11 (+3) and 12 (+2); the other nine tasks pass in neither arm.
  - Five of the seven predictions held. d was predicted at +3 to +14 and came out above it. B's window-full stops
    were predicted at 6 or fewer and were 12.
  - **The voids and short arms rules did not decide this read.** Both arms have 168 counted runs and 0 voids. d = +17
    is far past the 6.36 edge. So neither rule (a) nor rule (b) could have changed the band.
- **The order of the amendment and the read (recorded 2026-10-31 after the read, at the coordinator's question).**
  Times are PDT, from git's reflog and the files' modification times, all on the Mac's one clock:
  - 17:11:58: the audit's answer file is written (`~/goinfer-work/gemini-tasks/tack-1225-rule/answer.txt`). The
    coordinator's message relaying it is labeled 17:20. It reached tack's session before 17:17:19, when the
    amendment's branch was made, so the label is not its send time.
  - 17:17:44: the amendment is committed. Its content came from that message (the coordinator's recommended
    answers to (a) to (c)) and the answer file.
  - 17:17:50: it is amended once, only to correct its own time from "17:25" to "17:17". That is `71c95aa`.
  - **Its text is unchanged since then.** It was not amended after 17:20, nor after any tabulation.
  - After 17:17:50: night 2's records are checked for voids and oracle errors, reading only those fields.
  - 17:20:26: `calreport -rule` is committed (`8ff336f`).
  - About 17:22: the first tabulation, the read, runs.
  - 17:23:33: the read is committed (`c4d2f4b`).
  - The only pass data seen before the amendment is the 14:01 glimpse it discloses: 3 records of task 2, all fails.
- **Rule (b) refined after the read (2026-10-31, at Gemini's review of the tool).** An arm short of 168 rate-scales
  d, and now its sd as well: sd · √(168/2 · (1/n_A + 1/n_B)). Holding the edges fixed would loosen them for short
  arms.
  - `tools/calreport` compares unrounded and prints d in full.
  - The refinement does not apply to this read, which has 168 in each arm.
  - It does apply to later reads that use (b).

## 13. Amendment C7: context before and during the turn — drafted 2026-10-30 for the owner, before any run

**Why.** CB3 (`e1-cb3-context-metrics-2026-10-28.md`) found that every passing run reached the gold symbol, and that
392 of 578 failing runs never saw a line of it. C6's loop changes act after the model has found the code. C7 measures
three ways of putting the code in front of it. Francis set them as one plan (2026-10-30 09:04, relayed by the
coordinator), from his suggestions of that morning, with this section as the one place it lives.

**The three arms, in order.** Each runs on top of the loop as adopted when it runs, as C6's changes stack (§12.1):
1. **The turn-1 injection,** C7's main arm (§13.1).
2. **The read outline at 200 lines** (DESIGN §7.3; §13.2).
3. **The generated repository map** (§13.3).

Each has a no-model gate first. A screen with the model follows only if that gate passes. Every screen is registered
here with its own rule and predictions before its runs.
- **Screens are read by CB3's reach numbers, not passes** (Francis): runs that reach the gold symbol, and
  `FinalCov`, from `tools/replaycontext`. Passes are reported beside them.
- **The server and model are C6's:** Qwen3-8B `Q4_K_M` at 16,384 on goinfer `b2b5e816`, unless Francis moves them.

**Every record logs every guard's firings** (Francis, 2026-10-30). `tools/calreport` gains a per-setting table: for
each setting, how often it fires per run and whether progress followed within a few requests (new lines of gold code
seen, an edit applied, a build passing). What today's records carry, and what tack must start logging, is in §13.4.

### 13.1 The turn-1 injection (C7's main arm)

**What.** Before the first request on an edit task, tack runs ken over the task statement. The top items go into the
request's tail, under a budget of **1,600 tokens** (DESIGN §7.4's proposed 10% of a 16k window).
- They go in the tail, not the stable prefix: they are per-task bytes ("Append-only context", CLAUDE.md).
- The fixed prompt does not change.

**Why not a tool.** A tool the model must choose often goes unused: none of `edit_lines`' 6 runs made a line edit (§12.19).

**Selection, fixed for the ceiling check and the arm alike:**
1. **Named code first.** Each backquoted span in the statement yields the qualified identifiers in it: `a.b.c` and
   `(*T).m`, which is read as `T.m`. Go keywords and predeclared names are dropped.
   - For each identifier, in the statement's order, ken's `Definition` is looked up. If it finds nothing, the last
     component is dropped and the shorter name is tried.
   - At most 3 definitions are kept per identifier.
2. **Then the statement's text:** ken's search over the whole statement, k = 8, in rank order.
3. **Packing:**
   - Each item is the base tree's lines of its span.
   - An item whose lines are all already packed is skipped.
   - An item is packed whole if it fits the remaining budget, and otherwise skipped, and packing goes on with the
     next item.
   - Tokens are counted as bytes / 3, `loop`'s default rate before a response reports one (`fitDefaultRate`). At
     that rate 1,600 tokens is 4,800 bytes.
4. **ken's mode is `hybrid`,** tack's default (`internal/config`), with ken's Model2Vec model. `bm25` is reported beside
   it.

**Gate 0, the ceiling, with no model.** It runs over the 22 E1 tasks, the 12 calibration tasks and CB3's 10 pilot
tasks, at each task's base commit.
- **A task lands** if every raw span of its fix overlaps the injected lines. A raw span is the non-test diff hunk at
  base (`internal/eval/pilot/context.go:FixGold`'s first result).
- **Also reported, not decided on:**
  - the share of each task's `FixGold` lines injected;
  - whether any gold line is injected ("reached", CB3's sense);
  - the same at no budget, which separates the budget from the retrieval;
  - `bm25` beside `hybrid`;
  - the injection's size.
- **The rule, read on the 12 calibration tasks at 1,600 tokens, `hybrid`:**

| calibration tasks that land | verdict |
|---|---|
| **6 or more of 12, including at least 3 of the 8 whose runs reached gold under half the time (tasks 1, 3–9)** | **pass: the arm goes to a screen** |
| 4 or 5 of 12, or 6 or more but fewer than 3 of those 8 | parked: reported to Francis with the no-budget figure |
| 3 or fewer | the arm as specified is not screened; what next is Francis's |

  - **The 8 tasks are where an injection could add reach.** CB3's runs reached gold on them in 0–48.5% of runs.
    Tasks 2, 10, 11 and 12 already reached it in 53–98%.
  - The pilot tasks are reported beside the calibration tasks, not decided on.
- **Gate 1, the screen:** the loop as adopted against it plus the injection, 72 runs, A B B A A B. It is registered
  here, with its rule from CB3's per-task reach rates, after gate 0 passes.
- **Outcome of gate 0 (2026-10-30, 09:13–09:20 PDT; record `e1-c7-injection-ceiling-2026-10-30.md`): parked.**
  - 4 of 12 calibration tasks land (4, 6, 8 and 11), 3 of them among tasks 1 and 3–9.
  - With no budget, 6 of 12 land, 3 of them among those 8. The injections then run 2,811–13,994 tokens.
  - What follows is Francis's decision.

**Gate 0b: a second selection, POST HOC** (Francis, 2026-10-30 09:38, option (a)). This was written after gate 0's
report and before its run. It is shaped by what that report showed, so it does not stand as a clean test of the
12 calibration tasks. The pilot tasks carry the check: no one looked at them for this change.
- **The selection is §13.1's, with two changes** (`tools/injectceiling -selection posthoc`):
  1. **Search hits are dropped** if their file is a `_test.go` file or is not a `.go` file. ken still returns k = 8
     hits, and the dropped ones are not replaced.
  2. **Single-word, all-lower-case identifiers are dropped:** one with no dot and no upper-case letter, such as
     `serve`, `kv`, `ctx` or `mcp`.
  - Everything else stays: the 1,600-token budget, bytes / 3, `hybrid` as the one read with `bm25` beside it, and
    the packing.
- **The rule, read at 1,600 tokens with `hybrid`:**
  - **It passes, and the arm goes to a screen, only if it lands on at least 6 of the 12 calibration tasks and on at
    least 5 of the 10 pilot tasks.**
  - Otherwise it does not pass, and what next is Francis's.
  - Reported, not decided on: the count among tasks 1 and 3–9, the no-budget figures, `bm25`, and each task's
    packed spans.
- **The first selection's result stands as recorded.** Gate 0b does not replace it.
- **Outcome of gate 0b (2026-10-30, 09:40–09:46 PDT; record `e1-c7-injection-ceiling-2026-10-30.md`, "Gate 0b"): does not
  pass.**
  - 4 of 12 calibration tasks land (the same 4), and 2 of 10 pilot tasks.
  - Reaching any gold line rose from 7 to 9 of 12.
  - What follows is Francis's decision.

**The screen, run on Francis's override (2026-10-30 10:00, relayed by the coordinator), registered before its runs.**
- **This overrides both registered gates.** Gate 0 read parked and gate 0b did not pass. Francis chose to screen the
  arm anyway, read by reach rather than by the ceiling. The two gates' results stand as recorded. This screen does not
  turn either into a pass.
- **The arms**, one tack binary, interleaved A B B A A B per task, each run after the cell check (§12.2):
  - **A: the bundle as adopted.** That is `fit_window`, `stop_repeats`, `result_notes`, `instructions_head`,
    `refuse_duplicate_edits`, `verify_done` and `edit_tolerances`, as in §12.24 and §12.25's B.
  - **B: A plus `inject_context`** (`internal/window/inject.go`), gate 0b's selection.
    - ken runs in `bm25`, which is what tack opens without `[ken]`. That keeps the model's tools unchanged, and gate
      0b's `bm25` column (5 of 12 landing, 9 of 12 reaching gold) is at least `hybrid`'s.
    - The injection sits in the first message after the head, before the task, at most 1,600 tokens.
  - **The model and the server:** Qwen3-8B `Q4_K_M` at 16,384 on goinfer `b2b5e816`. Thinking off, forced-call mode,
    sampling 0.7 / 0.8 / 20, `max_tokens` 2048, failure budget 8, the 10-minute cap, the 12 tasks: 72 runs.
- **Build:** tack at the commit that adds this block, built clean on both machines.
- **The metric, which decides: reach.** A run reaches gold when it sees a line of its task's `FixGold`
  (`tools/replaycontext`, `FirstGoldReq > 0`, without `-include-notes`). Let d = B's reaching runs − A's, out of 36
  each.
  - The sd under no effect comes from §12.21's B per-task reach, the bundle on this model and window: tasks 1, 2, 6
    and 11 at 3/3; 3, 5 and 12 at 2/3; 4 at 1/3; 7–10 at 0/3. So Σ p(1−p) = 8/9, the variance of d is
    2 · 3 · 8/9 = 16/3, and **sd = 2.31**.

| d (B − A reaching runs, out of 36 each) | verdict |
|---|---|
| **+5 or more** (d ≥ 2 sd = 4.62) | **the injection raises reach** |
| +3 or +4 | parked: reported; more runs only if Francis says so |
| +2 or less | not shown; the do-nothing arm wins ties |

  - The tasks at 0/3 or 3/3 add no variance to that sd. A task with a true rate between them adds noise, so the rule
    errs more often than the sd says.
- **Reported, not decided on:**
  - passes in each arm, task by task;
  - `FinalCov`, `CovReq1` and the first request that reaches gold;
  - window-full stops, with the injection's extra tokens;
  - each B run's injection size, from its first message;
  - the guard table (§13.4);
  - the cell states.
- **Stopping and void:**
  - A void cell is re-run in its place (§12.2).
  - The launcher runs one task's six runs per invocation, and starts no task block after 19:15. A block can take up
    to 60 minutes, so the runs end by about 20:15, before §12.25's night 1 at 22:00.
  - If any task is left, it runs whole on the next day slot that does not touch §12.25's nights, and the screen is
    read only when all 72 runs exist.
  - A block cut short by a crash or a lock is re-run whole (the §12.10 completeness rule).
- **Predictions (tack's session, before any run):**
  - A reaches gold in 15–22 of 36 runs, near §12.21's B at 19;
  - d is between +2 and +8;
  - passes in each arm 3–7 of 36, with B within 2 of A;
  - B has at least as many window-full stops as A.
- **Time:** §12.21's 72 runs took 2 h 18 min. With ken's index opened in each B run and the warm-up, expect about
  2.5 hours.

### 13.2 The read outline at 200 lines

**What.** When `read` stops a whole-file read at 200 lines, its result ends with the file's outline from ken: each
symbol with its line range, as `current_symbol` already opens ken (bm25, no model). Today it says only "Read more with
offset and limit." DESIGN §7.3 specified the outline.

**Evidence.** On calibration task 4, 23.6% of CB3's runs opened `pull/pull.go`, usually its first 200 lines. Only 3 of
55 reached lines 369–410.

**Gate 0, by replay, with no model.** Over CB3's 625 counted runs, count the runs with a whole-file read that stopped
at 200 lines on a gold file whose gold span starts past line 200. The outline would have named the gold symbol's line
range in each of them.
- **The rule:** it goes to a screen if that is **at least 15 runs over at least 3 tasks.** Otherwise it is built only
  as the product feature DESIGN §7.3 already specifies, unmeasured.
- **The outline's size is reported for each gold file.** It must fit the read's own result cap.
- **Outcome of gate 0 (2026-10-30, 09:48–09:50 PDT; record `e1-c7-injection-ceiling-2026-10-30.md`, "§13.2 gate 0"):
  not screened.** 11 runs over 1 task (task 1), against 15 over 3. The outline is built only as DESIGN §7.3's feature,
  unmeasured.

### 13.3 The generated repository map

**What.** tack builds a map of the repository from ken's structural index, about 1,000 tokens: packages, files and their
main symbols, ranked. It works in any repository. Aider's repository map is the prior art.
- It goes beside the fixed prompt, in the slot the instruction file's head now uses. It is per-repository, not
  per-session, so it may sit in the stable prefix.
- Today's fixed prompt is 1,024 tokens without ken; with ken and the map it must still fit the 3,276-token budget
  (DESIGN §1).

**Evidence.**
- In CB3, the whole instruction file roughly doubled how often runs reached the right code (9/36 → 19/36), and CB3
  guessed the map the file carries was why.
- The bundle now loads only the file's first section and headings (`instructions_head`). goinfer's first section is
  the model-archive rule, so most of any map in the file does not reach the model.

**Gate 0, with no model, on E1's historical trees.** For each of the 22 tasks, the map is built at the base commit, so
nothing written later leaks in.
- **Counted:** whether it names the gold file, and whether it names the gold symbol. The same is counted for the
  instruction head each task's run carried today.
- **The rule:** it goes to a screen if the map names the gold file on **at least 6 more of the 22 tasks than the
  head does.**
- **The map's size is reported:** at most 1,000 tokens at bytes / 3.
- **The map and the counts, fixed before gate 0 runs** (2026-10-30). tack's session drafted this, filling in "ranked"
  with a construction that needs no ken change (`tools/repomapgate`):
  - **Files:** every `.go` file at base, except `_test.go` files and anything under `testdata/` or `vendor/`.
  - **Symbols:** each file's exported definitions from ken's structural index (`Outline`, `bm25`). A method is kept
    when its own name is exported, and is written `Type.Method`.
  - **Rank:**
    - a symbol's references are the other `.go` files in which its name (a method's own name) occurs as a whole word;
    - a file's score is the sum of its symbols' references;
    - files go in descending score, with ties broken by path.
  - **Text:**
    - the line `Repository map (the most-referenced Go files and their exported symbols):`;
    - then one line per file, `path: A, B, …`, with at most 8 symbols in descending references.
    - Files are added in rank order while the map stays within 3,000 bytes (1,000 tokens at bytes / 3).
  - **The head it is compared with** is the head `instructions_head` gives at 16,384 tokens
    (`internal/core/instructions.go:instructionHead`). It is taken over the first of `AGENTS.md`, `CLAUDE.md` and
    `CONTEXT.md` at the repository root at base. A repository with none has an empty head.
  - **"Names the gold file"** means the text contains the repository-relative path of at least one file holding a
    `FixGold` span.
  - **"Names the gold symbol"**, reported only, means it lists a symbol whose definition overlaps a `FixGold` span. For
    the head, it means the head contains that symbol's name as a whole word.
- **Outcome of gate 0 (2026-10-30, 09:53–10:00 PDT; record `e1-c7-injection-ceiling-2026-10-30.md`, "§13.3 gate 0"):
  goes to a screen.** The map names a gold file on 7 of 22 tasks, and the head on 0. The screen needs the map built
  into tack behind a `[run]` setting, for Francis's yes, and is registered here before its runs.

**Gate 1, the screen (Francis, 2026-10-30 10:14, relayed by the coordinator), registered before its runs.** It runs only
after §12.25's night 2.
- **The arms**, one tack binary, interleaved A B B A A B per task, each run after the cell check (§12.2):
  - **A: the loop as adopted when the screen runs.** Today that is the bundle (`fit_window`, `stop_repeats`,
    `result_notes`, `instructions_head`, `refuse_duplicate_edits`, `verify_done`, `edit_tolerances`). If Francis adopts
    `inject_context` after §13.1's screen, A carries it too. The arms are fixed in a line here before the first run.
  - **B: A plus `repo_map`** (`internal/window/repomap.go`), §13.3's construction as gate 0 measured it. ken runs in
    `bm25`, and no tool is added. The map sits in the first message after the head, at most 3,000 bytes.
  - **The model and the server:** Qwen3-8B `Q4_K_M` at 16,384 on goinfer `b2b5e816`, with C5's sampling and limits
    and the 10-minute cap, on the 12 tasks: 72 runs.
- **Build:** tack at the commit that fixes the arms line, built clean on both machines.
- **The metric, which decides: reach,** as §13.1's screen reads it. d = B's runs that see a `FixGold` line − A's, out
  of 36 each.
  - **The sd is §13.1's, 2.31,** from §12.21's B per-task reach. If A carries `inject_context`, the sd is recomputed
    in the arms line from §13.1's B arm before the first run.

| d (B − A reaching runs, out of 36 each) | verdict |
|---|---|
| **+5 or more** (d ≥ 2 sd) | **the map raises reach** |
| +3 or +4 | parked: reported; more runs only if Francis says so |
| +2 or less | not shown; the do-nothing arm wins ties |

- **Reported, not decided on:**
  - passes in each arm, task by task;
  - `FinalCov`, `CovReq1`, `FileCov` and the first request that reaches gold;
  - window-full stops;
  - the map's size in each B run;
  - the guard table (§13.4);
  - the cell states.
- **Stopping and void:** as §13.1's screen.
  - It runs by day, one task's six runs per invocation, and starts no task block after 19:15.
  - A void cell is re-run in its place.
  - A block cut short is re-run whole.
  - It is read only when all 72 runs exist.
- **Predictions (tack's session, before any run):**
  - d is between +1 and +6. The map named a gold file on 7 of 22 tasks, mostly in ken and aikit, and those tasks
    (calibration 7, 11 and 12) reach gold on 0/3, 3/3 and 2/3 in §12.21's B.
  - Passes in each arm are within 2 of each other.
- **Time:** about 2.5 hours by day.
- **The model changes (Francis, 2026-10-30 20:10, relayed by the coordinator):** this screen runs on Qwen3.6-35B-A3B at
  16,384 (§14), after §14.4's 16k row, not on Qwen3-8B. Its arms line names the model, the build and A's settings, and
  takes its sd from the 35B's 16k per-task reach (§14.4). The line is written before the first run. The predictions
  above were made for Qwen3-8B and are restated in that line.
- **Closed on the 35B (Francis, 2026-10-31 11:35, relayed by the coordinator): not built or run here.** The 35B at 16k
  reaches gold in 35 of 36 runs (§14.4), so a reach-decided screen cannot show an effect on it. Revisit on a model whose
  failures are retrieval.

### 13.4 Guard firings in every record

Owned by `tools/calreport`'s per-setting table. Measuring it decides nothing. A setting whose table shows it never
fires, or fires with no progress after, is a candidate for removal, and that removal is Francis's call. What records
carry today, and what tack must start logging, is recorded with the table's commit.
- **First reading (2026-10-30):** over §12.21's B and §12.24, with what records carry today and what tack must start
  logging, in `e1-c7-injection-ceiling-2026-10-30.md` ("Guard firings").

## 14. Amendment C8: a bigger MoE model, Qwen3.6-35B-A3B — owner, 2026-10-30, registered before its runs

**Why.** C4, C5, §12.21 and §12.24 put every arm at or under §2's bar, and the loop changes moved little. Francis
(10:35 PDT, relayed by the coordinator) read the calibration as saying the model is the limit. He asked for a bigger
MoE model today:
- Qwen3.6-35B-A3B first, its experts streamed from nobara's 64 GB of RAM;
- gpt-oss-20b second, registered on its own when its turn comes.

It goes ahead of C7's injection screen on nobara's card. §12.25's fit-only load and its night 1 at 22:00 stay as
planned, so **nobara is idle by 21:45**.
- **The earlier run:** C3's E0 of 2026-10-18 found this model void. goinfer's swap guard tripped in `check`, and host
  RSS peaked at 33.1 GB (`e0-nobara-c3-2026-10-18.md`).
- **The fix:** nobara-big01 is fixing goinfer's load, and will post the exact `goinfer-serve` command once the model
  serves cleanly.

### 14.1 Step 1: E0, by day

- **The model:** Qwen3.6-35B-A3B `q4_k_m` from `~/models` on nobara, at **16,384**, C6's window and tack's design
  window. The server command and goinfer commit are the ones nobara-big01 posts. They are pinned in a line here before
  the run, with the decode path goinfer's banner must name.
- **The rule:** E0's, unchanged (`e0-preregistration-2026-10-16.md`, amendments 4 and 11):
  - admitted at the window;
  - `check` ok;
  - smoke at least 17 of 20 on both runs, with 0 malformed.

  A swap-guard halt, an unanswered request or a decode on another path than the pinned one is **void**, not a fail.
- **The append probe** (amendment 11) is reported, not gated. A slow prefill only makes step 2 slower, and the cap
  below allows for it.
- **Holding nobara's timing lock, after nobara's other timed work.** Expected 60–120 minutes, since the timed phases
  prefill on a model whose experts stream from host memory.
- **Step 2 follows only if E0 is eligible.** A fail or a void is recorded, and gpt-oss-20b's registration comes next.
- **Pinned (2026-10-30, before the run; posted by nobara-big01, relayed by the coordinator):**
  - **goinfer `00584c35`:** branch `big01-moe-serve-2026-10-30`, which is main plus goinfer PR #521's bit-identical
    shared-expert prefill fix. It is used before #521 merges, under Francis's standing rule.
    - Binary: `/home/francis/goinfer-bench/big01-2026-10-30/goinfer-serve-cuda-00584c35`, sha256 `b25f68126426b1d6…`.
  - **The model:** `~/models/qwen3.6-35b-a3b-q4_k_m.gguf`, served from its v17 `.giw` sidecar.
  - **Server flags:** `-backend cuda -moe-cache-experts -thinking off`, at `-ctx 16384` as registered (nobara-big01
    served it at 32,768). The decode path is `cuda-resident (int4)`, as nobara-big01's banner shows. E0 and the pilot
    check the banner's path (`internal/eval/runner.go:VoidPath`) in place of goinfer's `-require-backend`.
  - **tack `5704cba`,** the commit that added this section, built clean on both machines.
  - **What nobara-big01 measured** (goinfer `docs/measurements/big01-moe-serve-2026-10-30/README.md`):
    - load in 21 s, peak RSS 38.8 GB, no swap;
    - decode 43.5 tok/s, not the 11–16 assumed above;
    - forced tool calls correct;
    - cold first token 33.5 s at 3.6k and 150 s at 13.2k, about 90 tokens/s;
    - with the prefix reused by appending, 6.9–7.1 s at about 12k.
  - **So §14.2's 30-minute cap stays.** Decode is about half of Qwen3-8B's (79 tok/s), and cold prefill is several
    times slower, so a run's model time is roughly 2–6 times Qwen3-8B's. A 10-minute run of C5 maps to 20–60 minutes,
    and 30 covers most of that.
  - **Order on the card:** after nobara-big01's gpt-oss-20b check (about 11:42). §12.25's fit-only load first (a few
    minutes, from the Mac), then this E0, as a job on nobara's `night.py` holding its timing lock.
- **Outcome (2026-10-30, 11:38–12:09 PDT; record `e0-nobara-qwen36-35b-2026-10-30.md`): eligible.**
  - Smoke 20/20 on both runs, 0 malformed, `check` ok, admitted at 16,384 on `cuda-resident (int4)`.
  - Decode 51.5 tok/s; host RSS peak 40.5 GB.
  - In the event, the §12.25 fit-only load ran after this E0 (12:09–12:10, passed), not before it.
  - §14.2 follows.

### 14.2 Step 2: a small calibration run, expanded if it passes

- **The loop:** the bundle as adopted (§12.24's seven settings), thinking off, forced-call mode, sampling 0.7 / 0.8 /
  20, `max_tokens` 2048, failure budget 8, at 16,384.
- **The cap: 30 minutes per run,** not C5's 10.
  - E0 at C3 put Qwen3-8B's decode at 79 tok/s; this model's is about 11–16 (nobara-big01's estimate). That is about
    a fifth.
  - Prefill streams experts too. Three times C5's cap gives a run about the model time a 10-minute run had on
    Qwen3-8B, if prefill slows no more than decode.
  - Cap hits are reported as such.
- **Build:** tack at the commit that adds this section, built clean on both machines. goinfer as pinned in §14.1.
- **The first set, at most 3 tasks** (Francis's limit), **3 runs each,** 9 runs in task order:
  1. **Task 11, the sanity check.** Every Qwen3-8B arm passed it (C5 3/3, §12.21 A 2/3 and B 1/3), and CB3's runs
     reached its gold 97.8% of the time. If a 35B model cannot pass it, the harness or the server is suspect before the
     model.
  2. **Task 1.** The bundle passed it 3/3 on Qwen3-8B (§12.21 B), and A1 never did. It shows whether the loop's best
     case carries to the larger model.
  3. **Task 10, the capability probe.** Runs reach its gold 64% of the time (CB3), but Qwen3-8B never passed it, and
     Qwen3.5-9B passed it once (§12.24). It is where a better model should turn reach into a pass.

  Three runs give each task a rate of 0–3 of 3, comparable with the rows' per-task cells.
- **The expansion rule** (Francis, 10:36):
  - **If any run of the first set passes,** more tasks follow, 3 runs each, one task at a time, in this order: **2,
    12, 6, 5, 3, 4, 7, 9, 8.**
  - The order is CB3's per-task reach, highest first (61.9%, 53.3%, 48.5%, 46.4%, 22.4%, 5.5%, 2.2%, 2.0%, 0%), so
    the tasks where a model could most often turn reach into a pass come first.
  - **If no run of the first set passes,** the run stops after its 9 runs, and gpt-oss-20b comes next.
- **The stopping point today:**
  - **No task block starts after 20:15.** A block is 3 runs of at most 30 minutes, so the last one ends by 21:45.
  - The launcher checks the clock before each block.
  - Tasks left continue on a later day by day, on the same build and in the same order, until all 12 tasks have 3
    runs.
- **Void:** §12.2's cell rule. A run void on the server (a swap-guard halt, or a request with no answer) stops the run.
  It is recorded and re-run in its place on the next slot.
- **The reading:**
  - **Reported, not decided on:** each task's passes beside §12.21's B and §12.24, task by task, plus stops, cap hits,
    window-full stops, requests and first-token times per run, from `tools/calreport` with the guard table.
  - **If all 12 tasks reach 3 runs** (36 runs), §2's bar is read as for §12.24: usable at 4 or more passes on 2 or
    more tasks.
  - **It is not a model verdict.** The tasks are a subset until all 12 run, and the window, the cap and the speed
    differ from the Qwen3-8B rows. Whether C6 or C7 moves to this model is Francis's decision.
- **Predictions (tack's session, before any run):**
  - task 11 at least 2 of 3; task 1 at least 1 of 3; task 10 0–2 of 3;
  - the median run 8–20 minutes;
  - cap hits 0–3 of the first 9;
  - window-full stops 0–2.
- **First attempt (2026-10-30, 12:14–12:16 PDT): void, disk.** All 9 runs were refused by the disk floor (5.1 GB free,
  under 20) and reached no model. The launcher counted them as non-passes, which is a defect, now fixed: a void run
  stops it. They are recorded as void (`e1-calibration-c8-qwen36-2026-10-30.md`), and the first set re-runs from 12:25.

### 14.3 The 32k arm (Francis, 2026-10-30 12:47), registered before its runs

Francis chose a 32,768 window for the expanded set ("32 sounds good", relayed by the coordinator). That replaces
§14.2's expansion at 16,384.
- **The first set's 16,384 runs stand as their own read** (tasks 11, 1 and 10, 3 runs each, from 12:28). They are not
  pooled with the 32k runs.
- **The trigger is met.** The first set had passing runs: task 11 at 3/3 and task 1 at 1/3.
- **Why task 1 runs again at 32k:**
  - its passing run ended on the server refusing a 16,532-token request, after its edit was applied;
  - its two failures ended on `repeat_loop` with no edit, at 14.7k and 15.0k tokens.

  So the window bound task 1's one pass, but not its failures, which the record classifies by stop.
- **The arm:** §14.1–14.2's, except the window.
  - Qwen3.6-35B-A3B `q4_k_m` on goinfer `00584c35`, with `-moe-cache-experts` and `-thinking off`, at **`-ctx 32768`**.
  - The bundle, sampling 0.7 / 0.8 / 20, `max_tokens` 2048, failure budget 8, tack `5704cba`.
- **The window's admission:** E0 admitted the model at 16,384, the window it was given.
  - Before the first 32k run, the pilot's cell check loads the model at 32,768. It must come up `cuda-resident (int4)`
    with a fit line and a clean cell, or the arm does not run. That is the same fit-only load E0 makes, at the new
    window.
  - E0's smoke and `check` do not depend on the window, so E0 is not re-run.
  - nobara-big01's serve check held 32,768 with no swap (38.8 GB peak).
- **Tasks, 3 runs each, one task at a time, in this order: 1, then 2, 12, 6, 5, 3, 4, 7, 9, 8** (§14.2's expansion
  order), **then 11 and 10.**
  - Ending with 11 and 10 means that, if time allows, all 12 tasks have 3 runs at 32k, and §2's bar can be read at
    that window.
- **The cap stays 30 minutes.** At 16,384, the first set's runs took 1–4 minutes.
  - At 32k a run can grow longer, and a cold prefill near the top costs about 6 minutes (nobara-big01's figure at 31.7k).
    With the prefix reused, appends cost seconds (7 s at 12k).
  - A 45-minute cap would move the last block's start to 19:30. The cap is kept, and cap hits are reported.
- **Stopping and void:** as §14.2.
  - No task block starts after 20:15, so nobara is idle by 21:45.
  - Each block needs 21 GiB free; the guard is not lowered.
  - A block with any void run stops the arm and is reported, and nothing in it is counted.
  - Tasks left continue on a later day, by day, on the same build and in the same order.
- **The reading:**
  - **Reported, not decided on:** each task's passes at 32k, beside this row's 16k first set, §12.21's B and §12.24,
    with stops (window-full and prompt-too-long counted), cap hits, requests, first-token times and the guard table.
  - **If all 12 tasks have 3 runs at 32k,** §2's bar is read on those 36 runs: usable at 4 or more passes on 2 or more
    tasks.
  - It is not a model verdict. What follows is Francis's.
- **Predictions (tack's session, before any run):**
  - task 1 at least 2 of 3 at 32k;
  - over the expansion's 9 tasks, passes on 1–4 tasks;
  - the median run 3–12 minutes;
  - cap hits 0–4;
  - window-full and prompt-too-long stops 0–3 in all.
- **The run in progress:** §14.2's launcher was suspended (SIGSTOP) at about 12:50, while task 10's block ran. It
  started no 16k expansion block, and is removed once that block ends.
- **The owner's battery override (Francis, 2026-10-30 18:05, relayed by the coordinator):** "go ahead and start tack's
  run, I will watch battery."
  - The Mac went onto battery at about 17:05, during task 4's block. The launcher then waited, and the pilot voids a
    cell that starts on battery (§12.2).
  - **The remaining blocks run with `-allow-battery`** (`cmd/tack/pilot_a1.go:cellVoid`), on tack at the commit that
    adds this line, built clean on both machines. Every other cell rule stays, and each run records its power.
  - **Mac-side timings from these blocks are reported only.** Pass and fail, stops and nobara-side figures stand.
  - **No new block starts with the battery under 30%.** The 21 GiB guard and the 20:15 cut stay.
- **Outcome (2026-10-30, 12:59–19:46 PDT; record `e1-calibration-c8-qwen36-2026-10-30.md`): usable, 13 of 36 on 7
  tasks** (corrected 2026-10-31 from "8 tasks").
  - Tasks 11 and 12 at 3/3, tasks 4 and 10 at 2/3, tasks 1, 3 and 8 at 1/3, the other five at 0/3.
  - 0 void, 0 cap hits.
  - Of the 23 failures, 10 were repeat loops with no edit, 9 a full window, 2 `max_tokens` and 2 wrong edits. 17 of
    them stopped at 30k tokens or more.
  - On the tasks both windows ran (1, 10, 11): 16k 7/9, 32k 6/9.
  - The override blocks all ran on mains.
  - The window for the next screens and the model for §13.3's screen are Francis's decisions.
- **Francis, 2026-10-30 20:10 (relayed by the coordinator):**
  - the 35B's window for the next screens is 16,384, confirmed first by §14.4's 16k row;
  - §13.3's repo-map screen runs on the 35B at 16k after that row, not on Qwen3-8B;
  - C9's three screens (§15) are approved.

### 14.4 The 35B's 16k row on the other 9 tasks (Francis, 2026-10-30 20:10), registered before its runs

- **What:** tasks 2, 12, 6, 5, 3, 4, 7, 9 and 8, 3 runs each (27 runs), at `-ctx 16384`. With §14.2's first set (tasks
  11, 1 and 10), that makes the 35B's 16k row on all 12 tasks.
- **The arm:** §14.2's, on the same goinfer `00584c35` with `-moe-cache-experts`.
  - tack `31a89c2`, the build of §14.3's last blocks. The commits since then change no code.
  - It runs with `-allow-battery` (§14.3), each run recording its power. In practice it will be on mains.
  - The bundle, C8's sampling and limits, and the 30-minute cap.
- **When:** by day on 2026-10-31.
  - It starts only after §12.25's night 1 has ended (`night-1/done`) and not before 07:00.
  - Before its first block, a cell check at 16,384 must come up `cuda-resident` and clean.
  - No block starts after 20:15, so both machines are idle for night 2 at 22:00.
  - Each block yields to another run waiting on either machine's lock (§14.3's yield), and needs 21 GiB free.
  - A block with any void run stops it.
- **The reading:**
  - **The 12-task row at 16k against §14.3's at 32k,** per task and by stop cause, with tokens at stop. §2's bar is
    read at 16k: usable at 4 or more passes on 2 or more tasks.
  - **It confirms 16k** if the 16k row's passes are at least the 32k row's (13) minus 3. Fewer than that is reported
    to Francis before any C9 screen runs.
  - **It also fixes the per-task pass rate and reach** from which §13.3's and §15's screens take their sd, in their
    arms lines.
- **Predictions (tack's session, before any run):**
  - the 9 tasks pass 4–9 of 27, so the full 16k row is 11–16 of 36;
  - window-full stops are more frequent than at 32k, and repeat loops fewer.

## 15. Amendment C9: loop changes on the 35B — drafted 2026-10-30 for the owner, before any run

**Approved by Francis, 2026-10-30 20:10** (relayed by the coordinator): all three screens, each after §14.4's row and
§12.25's two nights. Each setting lands in its own product PR, off by default.

**Why.** Francis (16:59, relayed by the coordinator) asked for screens of the cheapest loop candidates in DESIGN §17.1,
aimed at what C8 showed on Qwen3.6-35B-A3B (`e1-calibration-c8-qwen36-2026-10-30.md`):
- the runs read until the window is nearly full, at 16k and 32k alike;
- they repeat-loop without editing (10 of the 32k arm's 23 failures);
- the harder tasks pass about one run in three.

This drafts three screens, one registration each, with rigor matched to a screen's stakes.

**Common to all three:**
- **The arm:** the 35B on goinfer `00584c35` with `-moe-cache-experts`, at **16,384** (C8's read recommends it;
  Francis decides), with the bundle and C8's sampling and limits. A is the bundle; B is A plus the screen's one
  setting, `[run]`, off by default.
- **A prerequisite: the 35B's 16k row on the 9 tasks C8's first set did not run** (27 runs, about 1–1.5 hours by day).
  - With C8's first set, that gives a per-task pass rate and reach at 16k on all 12 tasks.
  - Each screen's sd comes from those rates, as §12.3's came from C5's. It is fixed in its arms line, with the
    build, before its first run.
- **Size and order:** 72 runs, A B B A A B per task, each after the cell check (§12.2). At the 16k first set's 1–4
  minutes a run, that is about 3–4 hours by day, after §12.25's two nights.
- **The decision rule:** §12.3's form. d = B − A out of 36 each.
  - d ≥ 2 sd: adopted.
  - 1 sd ≤ d < 2 sd: parked, with more runs only if Francis says so.
  - Otherwise not shown, and the do-nothing arm wins ties.
- **Each screen also reports one mechanism metric,** never used for adoption (§12.3).
- **Product code:** each setting is built with tier-0 tests broken first, and merged on Francis's yes before its
  screen runs.

### 15.1 Failing test first (`test_first`), decided by reach

- **What:** before the first request, the harness runs the task's named test (the oracle's test, at the base, as the
  user would see it fail) and puts its output in the first message beside the instruction head: the failing
  assertion and the stack trace, capped at 2,000 tokens.
- **The decision metric: reach.** Runs that see a `FixGold` line (`tools/replaycontext`), as in §13.1's screen.
- **The mechanism metric:** the first request that reaches gold, and the tokens spent before it.
- **Predictions:** d on reach of +3 to +8; passes up by 0–4.
- **Closed on the 35B (Francis, 2026-10-31 11:35, relayed by the coordinator): not built or run.** The 35B at 16k reaches
  gold in 35 of 36 runs (§14.4), so a reach-decided screen cannot show an effect on it. Revisit on a model whose
  failures are retrieval.

### 15.2 Observation masking (`mask_observations`), decided by passes

- **What:** when a file is read again, the earlier reads of it in the request become a one-line stub ("read pull.go
  1–200 at request 4; read again below"). A command's output older than its last 3 requests becomes a stub of its
  first line and its exit. The append-only prefix (CLAUDE.md) is kept: the stubs replace text only at a compaction
  point the loop already has, and the registration line names that point once it is built.
- **The decision metric: passes.**
- **The mechanism metric:** window-full stops (`window_full` plus prompt-too-long), and tokens at stop.
- **Predictions:** window-full stops halved or better; passes up by 1–4.
- **Arms line (2026-10-31, tack's session, before any run of this screen):**
  - **The arm:** Qwen3.6-35B-A3B `q4_k_m` on nobara at `-ctx 16384` with `-moe-cache-experts` and `thinking-off`, on
    goinfer `00584c35` (`goinfer-serve-cuda-00584c35`), with sampling 0.7 / 0.8 / 20 and a 30-minute cap per run, as
    in §14.4.
  - **The build:** tack `5212608` (main with #29, #30 and #34, the sandbox read fix; see "The answer-leak audit"
    below). It is built for both machines from a clean clone at that commit (`~/tack-runs/src/tack-5212608`).
    - It replaces `dc5a7ae`, on which no run of either screen took place. Two starts were stopped before any run: at
      17:16 a cell check lost nobara's lock, and at 17:35 the coordinator stopped the screen for the leak. Their
      directories are `s152-stopped-*`.
    - The launcher is `~/tack-runs/e1/c9-screens-2026-10-31/launch.sh` (`152`, or `153` with `1-6` and `7-12`), a job
      on the Mac's night.py.
    - Every run's config carries `[sandbox] read = ["~/models"]`, and the first block checks that it does.
  - **A:** the bundle as in §14.4: `fit_window`, `stop_repeats`, `result_notes`, `instructions_head`,
    `refuse_duplicate_edits`, `verify_done`, `edit_tolerances`. **B:** A plus `mask_observations`.
  - **The setting as built** (DESIGN §7.6; `internal/loop/mask.go`):
    - **When:** before a request, once the last prompt has reached 75% of the served window. After a compaction,
      the next waits until the prompt has grown by 10% of the window.
    - **What it masks:** a read that a later whole, successful read of the same file version covers, and command
      output older than the last 3 requests, over 300 bytes.
    - **The record:** each compaction is a `compaction` event naming every stub.
  - **The sd: 2.0,** from the 35B's 16k per-task pass rates (§14.4's row with C8's first set): tasks 1, 3 and 8 at
    1/3, tasks 10, 11 and 12 at 3/3, the other six at 0/3. Under no effect, var(d) is the sum over tasks of
    3 · 2 · p(1 − p), which is 4.0. So **d ≥ +4 is adopted, +2 ≤ d ≤ +3 is parked, and d ≤ +1 is not shown**, with A
    winning ties.
  - **Size and order:** the 12 tasks, A B B A A B per task, 72 runs, each after the cell check.
  - **Mechanism metric, reported and never used for adoption:**
    - window-full stops (`window_full` plus the server's prompt-too-long refusal) per arm, and tokens at stop;
    - in B, the compactions per run, the bytes they masked, and the request each ran before.
  - **Check on the first block:** every B run's tack config must have `mask_observations = true`, and no A run's
    may. Otherwise the screen stops, since the setting did not reach the arm. The launcher logs each B run's
    compactions, which are reported, not checked.
  - **Stopping and void:** as §13.1's screen. One task's six runs per invocation. A void cell is re-run in its
    place. A block cut short is re-run whole. It is read only when all 72 runs exist.
  - **When:** after §12.25's night 2 ends, as §15 registers. A night.py job on the Mac, like night 2. It is over
    two hours, so it may run overnight (run budget).
  - **Predictions (restated for the 35B at 16k):**
    - window-full stops in B are half of A's or fewer (§14.4 had 9 of 22 failures stop on a full window);
    - passes up by 0 to +3. Most of the 35B's failures are repeat loops without an edit (13 of 22), and masking does
      not touch those.
  - **Time:** 72 runs at about 3.1 minutes each (§14.4's mean, 186 s), plus cell checks and grading: about 4–5 hours.
- **Outcome (read 2026-11-01; record `e1-calibration-c9-screens-2026-11-01.md`): A 13 of 36, B 12 of 36, d = −1.
  Masking is not shown; the do-nothing arm wins.**
  - 72 runs, 0 void, 0 oracle errors, 0 cap hits, on tack `5212608`.
  - **Masking barely fired:** 11 compactions in 7 of the 36 B runs, masking about 14.7 KB in all. The 35B fills its
    window with grep output and whole first reads, which the rule does not mask. So the screen did not test what
    masking does to the window.
  - Window-full stops: A 7, B 12. The prediction of half or fewer missed.
  - A wider rule (grep and glob output and old first reads) would need its own registration; it is not started.
  - **Decided (the coordinator, 2026-11-01 07:40): masking stays off, with no wider rule for now.**

### 15.3 Best of N with the task's test as verifier (`best_of`), decided by any-of-k passes

- **What:** up to k = 3 independent attempts per run, each in a fresh context with a different seed. The run keeps the
  first attempt whose edit makes the task's named test pass. The verifier is only that named test, as a user would
  have it. The hidden grader is never consulted before the run ends.
- **Reported:** the per-attempt pass rate and any-of-k, both. Attempts run one after another, inside one 30-minute cap
  per attempt.
- **The decision metric: any-of-k passes per run.** B is best-of-3; A is a single attempt.
- **The mechanism metric:** the verifier's agreement with the grader (false accepts and false rejects).
- **Predictions:** any-of-k passes on tasks at 1/3 per attempt rise toward 2/3; overall d of +4 to +10; wall time
  about 2–3 times A's.
- **Arms line (2026-10-31, tack's session, before any run of this screen):**
  - **The arm and build:** as §15.2's arms line, except the cap is **90 minutes per run in both arms** (Francis,
    relayed by the coordinator).
  - **A:** the bundle, plus `mask_observations` if §15.2 adopted it (d ≥ +4). The launcher applies §15.2's rule
    from §15.2's 72 records before its first run and logs which A it chose. **B:** A plus `best_of_3`
    (`best_of = 3`).
  - **The setting as built** (`cmd/tack/bestof.go`; #29):
    - **Attempts:** up to 3 sessions, one after another, each fresh, at the registered sampling (0.7 / 0.8 / 20).
      tack refuses `best_of` at temperature 0 or top_k 1.
    - **Kept:** the first attempt that ends with exit 0, keeps HEAD, deletes no test file, builds
      (`go build ./...`), and passes `go test` of every package it changed. That is the user's own tests at the
      base, which includes the task's named test when it is in a changed package. The grader is never consulted.
    - **Between attempts:** the tree is reset to the base (`git reset --hard`, `git clean -fd`).
    - **With none verified:** the last attempt that changed a Go file is restored and graded.
  - **The per-attempt cap differs from §15.3's text.** tack's pilot config sets `max_wall = "24h"`, so an attempt is
    bounded only by the run's 90-minute cap, not by its own 30 minutes. C8's 72 runs at 16k and 32k hit no
    30-minute cap; the longest took 15 minutes (at 32k), and 4.3 minutes at 16k. A cap hit is reported per arm.
  - **The sd, the rule, size and order:** as §15.2's arms line. sd 2.0; d ≥ +4 adopted; +2 ≤ d ≤ +3 parked; d ≤ +1
    not shown, with A winning ties. 72 runs, A B B A A B.
  - **Reported:**
    - **Pass rates:** A's pass rate is the per-attempt rate; B's is any-of-k as the verifier picks it.
    - **Attempts:** in B, the attempts per run and each attempt's verifier verdict, from `stderr.log`'s
      `best_of attempt` lines.
    - **Wall time** per arm.
  - **The mechanism metric, the verifier against the grader:**
    - **False accepts** are measured: a kept, verified attempt the grader fails.
    - **False rejects** are measured only for the attempt graded when none verified (the grader passes it). Attempts
      reset before the run ends are never graded.
  - **Check on the first block:** every B run's tack config must have `best_of = 3`, and no A run's may. Each B
    run's `stderr.log` must have a `best_of attempt 1 of 3` line. If one has none (best_of refused its workspace or
    its sampling), the screen stops, since that run measured nothing.
  - **Stopping and void:** as §15.2's arms line.
  - **When:** after §15.2's 72 runs, overnight if needed.
  - **Predictions (restated for the 35B at 16k):** d between +2 and +6. The tasks at 1/3 (1, 3 and 8) rise toward
    2/3, the 0/3 tasks stay near 0, and B's wall time is 2–3 times A's.
  - **Time:** A about 2–2.5 hours; B about 4–5 hours (two of three runs fail, so they make all 3 attempts). About
    6–7 hours in all.
- **Outcome (read 2026-11-01; record `e1-calibration-c9-screens-2026-11-01.md`): A 14 of 36, B 17 of 36, d = +3.
  Parked: reported, with no more runs unless Francis says so.**
  - 72 runs, 0 void, 0 oracle errors, 0 cap hits. A was the bundle alone, since §15.2's d was −1.
  - **The verifier:** 0 false accepts in the 13 verified runs. 4 false rejects: a kept, unverified attempt that the
    grader passes, counted in B's 17.
  - **Why attempts failed:** all 75 failed attempts failed on their exit code (48 repeat loops), none on the tests.
  - B took about 2.2 times A's wall time. All four predictions held.
  - **Decided (the coordinator, 2026-11-01 07:40): best_of stays parked and off, with no continuation.**

**The answer-leak audit (2026-10-31, coordinator's read-only audit, relayed at 17:32), before §15.2's first run.**
- **What it found:**
  - **A hole in the sandbox.** In every A1 run so far, a sandboxed `bash` could read anything outside the workspace.
    That includes the E1 mirrors (fix commits and oracle tests), the task file and other runs' records.
  - **FETCH_HEAD.** The workspace's `.git/FETCH_HEAD` named the mirror's path.
  - **No record of it.** `left_repo` was not recorded for tack's runs.
- **No run used the hole.** The coordinator grepped all 2,428 `events.jsonl` under `~/tack-runs/e1`. No run read
  the mirrors, the task file or another run's files. The only hit was an `ls` in §12.25's task 10 A-5, on both
  nights, that listed FETCH_HEAD.
  - The exposure was the same in both arms of every comparison, so A/B reads stand.
- **The fix lands before §15.2 and §15.3 run**, in its own PR, and their arms lines move to its build:
  - reads become an allowlist (DESIGN §6.6), with `[sandbox] read = ["~/models"]` in every A1 run's config;
  - `PrepareRepo` removes FETCH_HEAD;
  - each A1 record carries `left_repo`.
  - Both arms of each screen run on that build, so the screens compare like with like. Their runs are not pooled
    with earlier jobs' runs.
- **A known limit, recorded and not changed:** four task statements state part of their fix.
  - Task 3 names `transcodeSelfCheck`'s signature.
  - Task 5 names `kvF16Decline` on `bannerFacts`.
  - Task 8 lists the exact stop tokens.
  - Task 9 states the conclusion that Qwen3-VL's tower is the Qwen3.5 tower.
  - These statements can inflate absolute pass rates on those four tasks. A/B differences are unaffected, since both
    arms see the same statement.
  - Changing the task set in the middle of the calibration is Francis's decision.

**Order:** 15.1, then 15.2, then 15.3, each stacked on what the one before adopted (§12.1). The three are drafted for
Francis's yes, and their arms lines are fixed before any run.
- **Outcome of §14.4 (2026-10-31, 07:07–11:05 PDT; record `e1-calibration-c8-qwen36-2026-10-30.md`): 5 of 27, so the
  full 16k row is 12 of 36 (32k: 13 of 36). 16k is confirmed** under §14.4's rule.
  - §2's bar at 16k: usable, 12 passes on 6 tasks.
  - 0 void and 0 cap hits. All 22 failures stopped at 14.4k tokens or more: 13 repeat loops with no edit, 9 a full
    window.
  - **The 35B at 16k reaches gold in 35 of 36 runs.** So the reach-decided screens (§13.3's repo map and §15.1's
    `test_first`) have almost no room: only task 7, at 2/3, can rise.
  - Whether to run them as registered, re-register them on passes, or drop them, is Francis's decision.

## 16. Amendment C10: the 35B's repeat loops — Francis, 2026-11-01 07:55, registered before any run

**Why.** In C9's 144 bundle runs on the 35B at 16k (§15.2 and §15.3, both arms), 56 ended in `repeat_loop`
(`e1-calibration-c9-screens-2026-11-01.md`):
- In 54 of the 56, the repeated call was answered with `fit_window`'s "window nearly full" note.
- 55 of the 56 never made an edit.
- Before the first such note, those runs made 8.6 calls on average and took in about 34 KB of results: 22.7 KB of
  reads and 11.6 KB of grep and glob.
- `stop_repeats` ends such a turn at the 8th unchanged repeat; it does not break the loop.
- At 32k, 10 of 23 failures were still repeat loops (§14.3), so a bigger window moves the cliff without removing it.

**Francis's order (07:55, relayed by the coordinator):**
- Lever 1 first.
- If lever 1 reads "not shown", lever 2 follows with its own registration, with no need to ask again.
- If lever 1 is shown, the work stops and is reported before lever 2.

### 16.1 Leaner exploration results (`lean_results`), decided by passes

- **What:** a `[run]` setting, off by default. With it on:
  - a whole-file `read` returns its first 80 lines instead of 200, with the existing "Read more with offset and
    limit" hint, and the tool's description says 80;
  - `grep` stops at 30 matches instead of 100, with the existing "narrow the pattern or the path" hint.
  - Nothing else changes, explicit `offset` and `limit` reads included.
- **The arms:** the 35B as in §15.2's arms line (Qwen3.6-35B-A3B `q4_k_m` at 16,384, `-moe-cache-experts`, goinfer
  `00584c35`, sampling 0.7 / 0.8 / 20, a 30-minute cap). A is the bundle as in §14.4; B is A plus `lean_results`.
  The tack build is named in an arms line, with the setting merged, before the first run.
- **Size and order:** the 12 tasks, A B B A A B per task, 72 runs, each after the cell check (§12.2).
- **The decision rule:** §12.3's form, with sd 2.0 as for C9 (§15.2's arms line). d = B − A out of 36 each.
  - d ≥ +4: adopted.
  - +2 ≤ d ≤ +3: parked.
  - d ≤ +1: not shown; the do-nothing arm wins ties.
  - Voids are re-run in place, and an arm short of 36 is rate-scaled with its sd, as §12.25's rule (b) as refined.
- **The mechanism metric, reported and never used for adoption:**
  - calls and result bytes before the first "window nearly full" note, per arm;
  - `repeat_loop` stops, and repeat loops answered with the note;
  - window-full stops, and tokens at stop.
- **Predictions (tack's session, before any run):**
  - B's calls before the first nearly-full note rise from about 9 to 12 or more;
  - B's repeat-loop stops fall by a third or more against A's;
  - d between 0 and +4. A later cliff gives more chances to edit, but the 32k arm suggests more room alone converts
    few loops into passes.
- **Stopping and void:** as §15.2's arms line. It runs when both machines' queues are idle and their locks free,
  overnight if needed (about 4–5 hours).
- **Then:**
  - If not shown: §16.2.
  - If shown: stop and report to Francis before §16.2.
  - If parked: report. Lever 2's condition is "not shown", so a parked result also stops for Francis.
- **Arms line (2026-11-01, tack's session, before any run of this screen):**
  - **The build:** tack `2f2870d` (main with #39, `lean_results`, and #40, its review follow-ups), built for both
    machines from a clean clone at that commit (`~/tack-runs/src/tack-2f2870d`).
  - **The arms:** as above. A is the bundle (`fit_window`, `stop_repeats`, `result_notes`, `instructions_head`,
    `refuse_duplicate_edits`, `verify_done`, `edit_tolerances`); B is A plus `lean_results`. Both run on the fixed
    sandbox with `[sandbox] read = ["~/models"]`.
  - **The launcher:** `~/tack-runs/e1/c10-lean-2026-11-01/launch.sh 161`, a job on the Mac's night.py. On the first
    block it checks that `lean_results` is in B's configs and not A's, and that every config has `~/models`.
  - **The read:** `calreport -rule sd=2.0,n=36` over its 72 records, after the void, oracle-error and `left_repo`
    checks.
- **Outcome (read 2026-11-01; record `e1-calibration-c10-lean-2026-11-01.md`): A 13 of 36, B 15 of 36, d = +2.
  Parked: reported, with no more runs unless Francis says so.**
  - 72 runs, 0 void, 0 oracle errors, 0 cap hits. The +2 is all on task 8 (1/3 to 3/3).
  - **The mechanism:** B reached the nearly-full note in 21 runs against A's 26, after 12.2 calls against 8.0, but
    with about the same 33 KB of results. Repeat-loop stops fell from 12 to 10, and every loop in both arms was still
    answered with the note.
  - Predictions: 2 of 3 held. Repeat loops did not fall by a third.
  - **Parked, so §16.2 is not started:** its condition is "not shown", and a parked result stops for Francis.
- **The continuation (Francis, 2026-11-01 13:47, relayed by the coordinator; registered by tack's session before
  any of its runs).** Francis chose both: continue §16.1 with 72 more runs, and build lever 2 meanwhile.
  - **Size and arms:** 72 more runs, exactly as §16.1's arms line.
    - The same build (`2f2870d`), arms, model, server, sampling, cap and order.
    - The same launcher, writing to `s161b`.
  - **How it combines:** the read is over all 144 runs (72 per arm), the first 72 and these together, with
    `calreport -rule sd=2.83,n=72` over both results files.
    - The sd for 6 runs per task per arm, from the same 35B rates as §16.1: var(d) = Σ 6 · 2 · p(1 − p) = 8.0, so
      sd = 2.83.
    - **d ≥ +6 (2 sd = 5.66) is adopted, +3 ≤ d ≤ +5 parked, and d ≤ +2 not shown.** A wins ties.
    - The continuation is not read on its own.
  - **A known cost:** extending a parked result after its read raises the chance that a setting with no effect is
    shown. The rule above is fixed now, before any continuation run, and is not changed after.
  - **What the read decides:**
    - If `lean_results` is shown on the combined runs, it is adopted, and §16.2 runs with it in both arms (§12.1).
    - Otherwise §16.2 runs against the bundle alone.
    - §16.2's screen is not queued until this read.

### 16.2 Compact at the cliff (`compact_at_cliff`), decided by passes — registered 2026-11-01, before it is built

Francis chose (a) and (b) at 13:47 (relayed by the coordinator): build lever 2 while §16.1's continuation runs.
- **The base:**
  - If §16.1's combined read shows `lean_results`, it is adopted, and both arms here carry it (§12.1).
  - Otherwise both arms are the bundle alone.
  - The screen is not queued until that read.
- **What:** a `[run]` setting, off by default. It makes the loop compact once at the cliff:
  - **The trigger:** when a response's tool result gets `fit_window`'s "window nearly full" note, a compaction runs
    before the next request.
  - **What it stubs:** each result over 300 bytes from before the last 3 requests that is:
    - a `grep` or `glob` output; or
    - a whole-file read, one with no range (already read, and readable again by range);
    - or what `mask_observations` already stubs: a read covered by a later whole read of the same file version, and
      old command output.
  - Each stub is a one-line note saying what was there and how to get it back (a narrower grep, a read by range).
  - **One-shot:** after a compaction, the next one waits for another nearly-full note, and for the prompt to have
    grown by 10% of the window past the first prompt after the last one, as in `mask_observations`.
  - **Recorded:** each compaction is a `compaction` event that names every stub, with the trigger `cliff`, so a
    replay of the log rebuilds the request.
- **The arms:** the 35B as in §16.1's arms line. A is the base; B is A plus `compact_at_cliff`. The tack build is
  named in an arms line, with the setting merged, before the first run.
- **Size and order:** the 12 tasks, A B B A A B per task, 72 runs, each after the cell check.
- **The decision rule:** sd 2.0 as for §16.1. d ≥ +4 is adopted, +2 to +3 parked, +1 or less not shown, with A
  winning ties.
- **The mechanism metric, reported and never used for adoption:**
  - compactions per B run, and the bytes they freed;
  - `repeat_loop` stops, and loops answered with the nearly-full note;
  - the calls made after the first note;
  - window-full stops.
- **Predictions (tack's session, before it is built):**
  - B's repeat-loop stops fall by half or more against A's. In §16.1 all 22 loops were the model asking again at the
    note.
  - d between 0 and +5.
  - Compactions in most of the B runs that reach the note (about 21–26 of 36 in §16.1).
- **Stopping and void:** as §16.1. It runs when both locks are free, overnight if needed (about 4–5 hours).
- **Amended before any §16.2 run (Francis, 2026-11-01 14:08, relayed by the coordinator): react when stuck.**
  - **What changes:** besides the one-shot compaction at the first nearly-full note, `compact_at_cliff` changes what
    the repeat rule does at the cliff.
    - When `stop_repeats` would end the turn and the call that reached the limit was last answered with the
      nearly-full note, tack compacts with the scope above and lets the turn continue.
    - The cliff's 10% regrowth gate does not apply to it. That call's repeat count starts again from zero.
    - **At most 2 such compactions per turn.** A third, or one that finds nothing left to stub, ends the turn with
      `repeat_loop`, as today.
    - Each is a `compaction` event with the trigger `stuck`.
  - **A repeat away from the cliff still ends the turn as today.** There the call got its whole, unchanged result
    each time: the model has the data, and compaction would change nothing it is shown. Letting that turn go on would
    spend requests without giving it anything new.
  - **The rest is unchanged:** the arms, base, size, rule and stopping.
  - **The mechanism metric adds:**
    - `stuck` compactions per B run;
    - the turns that continued past a would-be `repeat_loop`, and what they did next (an edit, `done`, or another stop).
  - **Predictions, restated:**
    - B's `repeat_loop` stops fall by two-thirds or more against A's;
    - d between 0 and +5;
    - B's wall time per run rises.
