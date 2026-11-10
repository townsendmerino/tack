# Proposal: design review, 2026-11-09 — an explore-then-edit loop, and what else to change

<!-- citations-at: 0cf8edd -->

Status: **proposed**, for the owner's review. Nothing here is built, registered or queued. Written in the Claude app
on Francis's word (2026-11-09: review the design, then "yes please" to one consolidated task doc). One doc, by his
standing preference: the loop change with its draft registration (§2), the measurement changes (§3), the direction
questions that need an ADR (§4), smaller fixes (§5), features (§6), the order (§7), and the decisions for Francis,
each with a recommendation first (§8).

**How the facts were gathered.** From the tree at `0cf8edd` (main after PR #138): `DESIGN.md`, `DECISIONS.md`,
`p1-notes.md`, the calibration pre-registration and the C6 to C11 records, and spot checks of the code each open item
names. Nothing was run. A claim about what is not in the tree means it was searched for and not found; where that
matters it says so.

**What is not in question.** The architecture holds up: one daemon with thin clients, the append-only log and context,
the sandbox as the boundary, ken in-process, goinfer as a child, and the measurement discipline. The 16k design window
is supported by the records (§1). This doc changes the loop's shape and a few priorities, not the structure.

---

## 1. What the records say

All figures are Qwen3.6-35B-A3B `q4_k_m` on nobara with the bundle, unless a line says otherwise.

| finding | figure | record |
|---|---|---|
| The model finds the code | reaches the gold symbol in 35 of 36 runs at 16k | pre-registration §14.4's outcome |
| It still fails two runs in three | 12 of 36 pass at 16k | the same |
| A larger window does not help | 13 of 36 pass at 32k; failures stop at 31–33k as they stop at 14–16k | `e1-calibration-c8-qwen36-2026-10-30.md` |
| Reaching the "window nearly full" note is what separates pass from fail | never reached it: 22 of 26 pass; reached it: 11 of 46 (both arms) | `e1-calibration-c10-cliff-2026-11-02.md`, the mechanism table |
| Failures are not wrong edits | of 75 failed `best_of` attempts, 48 looped, 24 filled the window, 3 hit max tokens; none ended cleanly and then failed its tests | `e1-calibration-c9-screens-2026-11-01.md`, §15.3 |
| The failures happen before any edit | all 22 failures at 16k: 13 repeat loops with no edit, 9 a full window | pre-registration §14.4's outcome |
| Treating the cliff has not moved passes | `mask_observations` d = −1; `lean_results` d = +2 (parked); `compact_at_cliff` d = +1 | C9 and C10 records |
| The test verifier can be trusted | 13 verified attempts, 0 false accepts | C9 record, §15.3 |
| Text nudges go unheeded (Qwen3-8B) | restating the task: 0 of 8 refused runs acted; `build_after_edit`: the next change built after none of 7 notes | `e1-calibration-c6-status-2026-10-21.md` |

**The reading.** On the model that works, the run finds the right code early, keeps reading instead of editing, and
dies at the top of the window. Every lever screened since C8 acts at the cliff, after the run is already in that state.
None changes what the model does between finding the code and running out of room. That is a problem with the loop's
shape: one long conversation in which exploring and editing share a window, and nothing makes the model stop exploring.

**What this says about the window.** 16k and 32k pass the same, so the window's size is not the binding limit and
"small window first" stands. What binds is how the window is spent.

---

## 2. The loop change: explore, then edit in a fresh context

### 2.1 The shape

A write task runs in two phases, enforced by the harness, not asked for in text (§1's last row is why).

1. **Explore.** The loop as it is today, with one addition: an explore budget. *Proposed:* the phase ends when the
   prompt reaches 50% of the served window and the turn has applied no edit. A run that edits before then never sees
   the handoff, so short tasks run exactly as now.
2. **Findings.** At the budget, the harness sends one request that must answer with a `findings` call
   (`tool_choice`, which goinfer constrains by grammar, so the note cannot be skipped or malformed). Its fields:
   the files and symbols that matter (name and path), what is wrong or wanted in a sentence or two, the edit plan as
   short steps, and what was ruled out.
3. **Handoff.** The history before it is replaced by one message: the task word for word, the findings, and the named
   symbols re-read through ken at their current text, within a budget (*proposed* 30% of the window). The system prompt
   and tool list stay byte-identical, so the cached prefix up to the first message is reused (DESIGN §7.2).
4. **Edit.** The loop continues from there with the bundle on. The model starts with the code it needs and about
   half the window free.

**Fallback.** If the findings name no symbol ken can resolve, the handoff uses the mechanical anchor list
(`internal/loop/summary.go`, the paths and symbols the turn read most) in their place, so the step never leaves the
model with nothing.

**At most once per turn** in the first version. A second handoff, for a turn that fills the window again after
editing, is a later question.

### 2.2 Why it is cheap to build

P1 step 6 built most of the machinery for the summarizing step:

- `server.Conversation.Summarize` already replaces a run of messages with one user message;
- `proto.Compaction` already records the replacement so `loop.Rebuild` replays it (the log stays the truth);
- `internal/loop/summary_retrieve.go` already searches ken and appends hits within a share of the window.

The handoff is that step with three differences: it fires early, on the explore budget, where summarizing fires at 75%
full; it replaces every turn so far, where summarizing replaces the oldest half; and its text is the model's own
constrained findings plus re-read symbols, where summarizing calls the summarizer. *Proposed:* a `[run] handoff`
setting, off by default, a `handoff` trigger on the `compaction` event, and one new tool whose schema must fit the
fixed-prompt budget (1,316 of 3,276 tokens are used today, `TestFixedPromptBudget`). Size: M, several days with tier-0
tests.

**What it costs a run:** one re-prefill of the handoff message, about 5k to 6k tokens. On the 35B that is roughly 40 to
50 seconds at the cold prefill rate E0 measured (110–150 tokens/s, `e0-nobara-qwen36-35b-2026-10-30.md`), added to a
run that averages 3.1 minutes (pre-registration §18.2).

### 2.3 How it relates to what is already planned

- **DESIGN §17.1's candidates 3, 4 and 6** (a notes block, restart from notes, a plan turn) are this, combined and
  triggered early. None has been screened.
- **DESIGN §2.5's fixed stages** (from DevBox) are the same idea with more stages. This is the two-stage version.
- **C12** (pre-registration §18, drafted, not approved) screens summarizing at 75%. Its own prediction is that a
  large pass effect is not expected, because the failures are repeat loops a summary does not touch. *Recommended:*
  screen the handoff first. If it works, far fewer runs reach 75%, and C12's question changes.
- **Subagents** (`[run] subagents`) could carry the explore phase as a child session instead. That keeps the parent's
  window cleaner but adds a second conversation's prefill and loses the parent's own reads. The in-place handoff is
  simpler and reuses more; the subagent form is the alternative if gate 0 (below) says explore needs more than half a
  window.

### 2.4 Draft registration (for Francis, one screen; nothing runs until he approves)

**Question.** Does an enforced handoff at the explore budget raise passes on the 35B at 16k?

**Gate 0, no model, minutes.** A replay over the existing 35B runs at 16k (the C8 to C10 records' runs on nobara), with
a tool on the pattern of `tools/replaycontext`:

- of passing runs, the share whose first applied edit came before the prompt reached 50% of the window. *Pass:* at
  least 80%, so the trigger rarely interrupts a run that was going to pass;
- of failing runs, the share that had read the gold symbol by then. *Pass:* at least 70%, so the findings can name it.

If either misses, the budget is moved once (to 40% or 60%) and the gate re-read; a second miss stops the candidate.

**Tier 1, about 30 minutes by day.** Tasks 1, 3 and 5 (each fails most runs and passes some, or reaches gold and never
edits), one run each with `handoff` on. *Pass:* in at least 2 of 3, the findings name the gold symbol and the edit
phase applies an edit inside it. A fail stops the candidate before a screen, as §12.18 and §12.19 stopped theirs.

**The screen.** 72 runs: the 12 calibration tasks, 3 per arm, A B B A A B per task, each after the cell check.

- **A** is the bundle as C12's draft defines it (pre-registration §18.1), so the two screens share a base. **B** is A
  plus `handoff`.
- **Metric: passes,** out of 36 per arm, sd 2.0 as §15.2 drew it.

| d | verdict |
|---|---|
| +4 or more | adopted |
| +2 to +3 | parked: report, Francis decides on a second night |
| −1 to +1 | not shown: stays off |
| −2 or less | parked low: reported, stays off |

- **Mechanism metrics, reported and never used for adoption:** runs that reached the handoff; findings that named the
  gold symbol; runs ending with no applied edit; `repeat_loop` and window-full stops; passes among runs that handed off
  against those that did not; the fallback rate.
- **Predictions, made here before any run:** the handoff fires in 20 to 28 of B's 36 runs; runs ending with no edit
  fall by at least half against A; B − A is between +1 and +7.
- **Time:** about 4 hours at §14.4's mean of 3.1 minutes a run, plus the handoff's prefill. Over two hours, so a night
  run under the run budget.

The registration itself goes in the pre-registration file as its next amendment, written by the session that builds
the setting, once Francis approves this shape.

---

## 3. Measurement: make the screens able to see an effect

- **The calibration set has little room.** On the 35B, tasks 10, 11 and 12 pass 3 of 3 in every arm and tasks 2, 6, 7
  and 9 pass 0 of 3 in every arm of the §15.3, §16.1 and §16.2 screens. Passes move on about five tasks, so a screen's 36 runs per arm carry
  roughly 15 runs of information. DESIGN §15.1 already says tasks should sit between 20% and 80%; that was set for
  Qwen2.5-7B and has not been redone for the 35B.
- **The rule parks often by chance.** C11's own arithmetic gives 18.7% parked under no effect
  (`e1-calibration-c11-onenight-2026-11-07.md`). Five screens in a row have ended parked or not shown.

**Proposed:**

1. **A fits-the-window ceiling, no model.** For each calibration task: the tokens of the gold symbols, the named
   tests and their failing output, plus the fixed prompt. A task whose minimum does not fit in half a 16k window cannot
   pass under any loop and should not be in the set. Same kind of check as C7's injection ceiling, about a day to build
   on `internal/eval/pilot/context.go:FixGold`.
2. **Re-mine for the band.** Replace the tasks that fail the ceiling, and the three that always pass, with tasks from
   the existing miner (`internal/eval/mine`) that the 35B passes between 20% and 80% in a 3-run pilot each. Aim for 12
   informative tasks. The old 12 stay as a frozen set, so earlier records keep their meaning.
3. **Keep the house rule,** sd drawn again from the new set's per-task rates.

This is direction-settling work, so it takes full discipline; the ceiling tool itself is ordinary engineering.

### 3.1 The opencode row

The only A0 and A0k rows found are the pilot's 0 of 20 on Qwen2.5-7B (`e1-pilot-2026-10-17.md`). No opencode run on the
35B was found. A1 against A0 is the P1 exit rule (DESIGN §15.3), goal G3, and the claim that would go in the README
when the repo opens.

**Proposed:** one row, opencode at its pinned version on the 35B at 32,768 (its 11,180-token fixed prompt leaves too
little of 16k), the 12 tasks, 3 runs each, reported beside tack's 16k row and not pooled with it. About 4 to 6 hours.
It informs the P1 rule's design on this model; it decides nothing by itself.

---

## 4. Direction: questions that need an ADR

These change DESIGN §0, §2.3, §8.4, §11.12 and risks R12 and R13. They are Francis's decisions; none is edited into
DESIGN.md by this proposal.

### 4.1 Which model the design is centered on

DESIGN §0 and §2.3 name Qwen2.5-7B-Instruct on an 8 GB card as the one working configuration. The records since say
otherwise: Qwen2.5-7B passed 0 of 20, Qwen3-8B passes about 15% with the bundle, and the model that passes a third of
the calibration set is a 35B mixture-of-experts with 18.2 GB of weights and a 40.5 GB host peak (E0, 2026-10-30).

*Proposed ADR-008:* the design's reference configuration for edit tasks is the 35B at 16k on nobara; 8B-class models
are the reference for read-only questions. §0, §2.3 and §8.4 are rewritten to match, with R13 marked as observed and
answered by the model, not by loop changes.

### 4.2 What a 16 GB Mac is promised

D7's one-install promise assumes a model that fits beside the owner's apps and can edit. Nothing measured supports
that on 16 GB. *Proposed, in the same ADR:* state tiers plainly.

| Mac memory | what tack offers | model |
|---|---|---|
| 16 GB | questions about a repository, read-only (`tack run`, plan mode) | a small bundled model |
| 48 GB and up (*to be measured by E0 on such a Mac, when one is available*) | editing | the 35B class |
| any, with a server | editing | a remote goinfer |

The first-run screen (§11.12) then says this instead of offering an edit agent that will fail. R12's wording changes
from a risk to a stated limit.

### 4.3 Whether the context bets still lead

Bet 2 (the right context for a small window) was aimed at finding the code. The 35B finds it in 35 of 36 runs with
grep, read and ken as they are, so auto-context, the repo map and `test_first` have almost no room on this model
(pre-registration §14.4 says the same). *Proposed:* P2's order becomes the ledger and inspector first (bet 1, still
untested and still the product's difference), then staleness; auto-context is kept as built and not screened again
until a model that needs it is in use.

---

## 5. Smaller fixes

| # | item | evidence | size |
|---|---|---|---|
| 5.1 | **Commands get a short environment.** DESIGN §6.6 designed the allowlist on 2026-11-08; `internal/shell/shell.go` still passes `os.Environ()` whole in both places it builds an environment, so one `env` puts `GH_TOKEN` in the model's context and the log | read at `0cf8edd` | S, with a red-first test |
| 5.2 | **Edit tolerances compare tokens.** DESIGN §7.5's "To fix: string literals": `internal/tools/tolerance.go` uses `go/parser` and no `go/scanner`, so `"foobar"` can still match `"foo bar"`. The setting is in the adopted bundle | read at `0cf8edd` | S, then replay the kept rescues |
| 5.3 | **Check a request against the window before sending.** C8 traced the server's prompt-too-long refusals to requests growing past `fit_window`'s reserve; they were still 10 of B's stops in C10. No pre-send check was found in `internal/loop` | C8 and C10 records | S |
| 5.4 | **Freeze the always-ask guard.** `internal/perms/destroy.go` is 1,895 lines, the largest file in the product, for what DESIGN §6.6 says is not a boundary, after ten review rounds that each found more forms. No further review rounds; new forms are fixed only when a real run meets them | DESIGN §6.6 | none |
| 5.5 | **Snapshots hold the index and refs.** Copy the index file and record `for-each-ref` and the stash list in the snapshot's manifest, so `git reset`, `git branch -D` and the stash commands can be undone and leave the ask list. The list then shrinks instead of growing | DESIGN §6.8, "What keeps asking" | M |
| 5.6 | **Named profiles.** `[run]` has 26 on-or-off switches among about 40 settings, nearly all off by default, and "the bundle" is seven of them set by each launcher. A `profile = "bundle-v1"` setting names the tested combination, the eval arms name profiles, and what ships is a profile that was measured. No switch is removed | `internal/config/config.go` | S |

5.1 to 5.3 are ordinary engineering and should land before the owner's week of real use (P1's exit).

---

## 6. Features

1. **Overnight verified fixes.** Queue tasks with a named test; each gets up to *k* attempts in its own worktree; the
   owner sees only diffs whose test passes. It rests on §1's verifier row (0 false accepts in 13) and on `best_of`,
   which Francis kept for this kind of use. Local attempts cost time, not money, and the night is free. It needs
   worktree sessions (P5, DESIGN §16) pulled forward, or a simpler copy of the workspace per attempt as E1's runner
   already makes.
2. **`tack fix <test>`.** DESIGN §17.1's candidate 1 as a command: run the test, put its failing output in the first
   message, and use it as the `done` check. It is the entry point feature 1 queues.
3. **Task sizing before a run.** With §3's ceiling arithmetic at run time: ken resolves the names in the request, tack
   counts their tokens, and warns when the task is unlikely to fit, with the symbols listed so the owner can split it.
   DESIGN §2.5 says the app should help scope a task and gives no mechanism; this is one.
4. **Run autopsy.** A first form of `tack ctx <session>` (DESIGN §10), after the fact: the window's fill per request, when each file was first read,
   when the first edit came, and why the run stopped. `tools/calreport` already computes this for eval runs; this
   shows it for any session, and is a first slice of the P2 inspector that needs no ledger.

---

## 7. Order

1. §5.1 to §5.3, the three small fixes. Days.
2. §2's gate 0, then tier 1. A day, no night.
3. §3's ceiling tool, and the re-mined set while §2 is built.
4. §2's screen, on the set it was registered for.
5. §3.1's opencode row, on a free night.
6. §4's ADR, once 4 and 5 have read: the handoff's result and the opencode row are the evidence it should cite.
7. §6, after P1's exit, feature 2 first.

C12 waits behind step 4 (§2.3).

---

## 8. Decisions for Francis

| # | decision | recommendation |
|---|---|---|
| 1 | Build and screen the explore-then-edit handoff (§2) | yes; gate 0 and tier 1 first, which cost a day and no night |
| 2 | C12 before or after it | after: C12's own prediction is a small effect, and a working handoff changes its question |
| 3 | The explore budget and the re-read budget (50% and 30% of the window) | start there; gate 0 may move the first once |
| 4 | Re-mine the calibration set for the 35B (§3) | yes, keeping the old 12 frozen for the existing records |
| 5 | An opencode row on the 35B at 32k (§3.1) | yes, one night, reported only |
| 6 | ADR-008: center the design on the 35B, and state the Mac tiers (§4.1, §4.2) | yes, written after decisions 1 and 5 have read |
| 7 | P2 leads with the ledger and inspector, not auto-context (§4.3) | yes |
| 8 | The three small fixes before the week of real use (§5.1–§5.3) | yes |
| 9 | Freeze the guard; extend snapshots to the index and refs (§5.4, §5.5) | freeze now; the snapshot work after P1's exit |
| 10 | Profiles (§5.6) | yes, when the next arm setting is added |
| 11 | Which features, and when (§6) | `tack fix` and the autopsy after P1's exit; overnight fixes once worktrees or workspace copies exist |
