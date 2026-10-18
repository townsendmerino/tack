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
4. Run the baseline: A1 as is, 36 runs.
5. If the baseline reaches the bar, build the changes, each with tier-0 tests broken once, in the order 4.1, 4.2, 4.3,
   4.4. Each is registered as an amendment and measured before the next is built. Below the bar, stop at §2's rule.
