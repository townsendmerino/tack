# DRAFT, not registered: a thinking-on arm for Qwen3-8B (calibration)

<!-- citations-at: 4c8fc42 (tack), f20f0d93 (goinfer) -->

**Status: a draft for the owner to decide on after C4 and C5 report** (asked 2026-10-19). Nothing here is registered.
If approved, it becomes an amendment in `e1-calibration-preregistration-2026-10-18.md`, with its predictions and sd
written there before any run.

C5 (`§11`) runs Qwen3-8B at 16,384 with thinking off, as §3 requires. Both C4 and C5 predict 0–3 passes of 36, under
the bar (at least 4 passes over at least 2 tasks). This arm would change one thing: thinking on.

## 1. As built, a thinking-on arm cannot think

Three facts at the commits above. Together they mean that sending thinking "enabled" today would produce a run
identical to C5, labelled thinking-on.

- **A1 forces every call.** `tack run --edit` sends `tool_choice: any` (`internal/loop/loop.go:New`). goinfer maps it to
  the `required` union, which constrains the output from its first token (goinfer
  `internal/serveapp/tools.go:toolsConstrainedFromStart`).
- **goinfer renders a constrained request with thinking off** (goinfer `docs/server.md`, "Constrained requests render
  thinking-off"; `internal/serveapp/think.go:constrainedTemplate`). The grammar demands `<tool_call>` at token 1, so
  the model can never open a think block, whatever the request asks.
- **tack never asks for thinking.** The loop starts every turn with thinking false (`internal/loop/loop.go:Session.Run` and
  `internal/loop/rebuild.go:Rebuild` both call `BeginTurn` with false).

**Build (owner, 2026-10-19):** anything registered from now on runs on goinfer `b2b5e816` or later, named in its
amendment. This arm would need a later build still, one with the change in 1 below.

So the arm needs two builds before any run:

1. **goinfer: "think, then a forced call."** For `required` / Anthropic `any` with thinking on, let the model think,
   then arm the union grammar when the think block closes, the way the lazy `auto` union arms on the call opener.
   goinfer's reasoning budget already forces the block closed (default `auto`: at 3/4 of `max_tokens`), so a call is
   still guaranteed. This is goinfer's change, through goinfer's process (DESIGN §13).
   - **Not recommended:** sending `tool_choice: auto` from tack for this arm instead. That changes two things at once
     (thinking, and calls no longer forced), against §3's one change per amendment. A prose reply would also end the
     turn rather than be resent.
2. **tack: a thinking setting for the run.** A `[run] thinking` field in `config.toml`, false by default, passed to
   `BeginTurn` and to `rebuild`. The pilot's `tackConfig` would write it, and the pilot would drop `-serve-option
   thinking-off` for this arm. Product code, so it waits for the owner's yes.

## 2. The window cost at 16k

**Thinking stays in the window for the whole task.**
- goinfer replays the model's reasoning the way Qwen3's own template does: a think block is re-rendered for every
  assistant turn after the last user query (goinfer `chat/history.go`, the Qwen3 rule).
- A tack task is one user message followed by the tool loop, so every request's thinking is part of every later prompt.
- tack keeps thinking blocks as received and resends them (`internal/server/conversation.go`).

So a run of n requests that thinks T tokens on average adds about n·T tokens to its last prompt.

**Measured on the baseline's trajectories**, as a proxy (`e1-calibration-baseline-2026-10-18.md`, records under
`~/tack-runs/e1/cal-baseline-2026-10-18/`):
- Qwen2.5 and Qwen3 share a tokenizer family, so the prompt sizes carry over. The trajectories themselves are Qwen2.5's,
  not Qwen3-8B's.
- 28 of the 36 runs ended by `done` or budget; the other 8 were loops that filled 32k.
- Over those 28, a run still fits 16,384 if its largest prompt plus n·T stays under 16,384:

| average thinking per request, T | runs that still fit 16k (of 28) |
|---|---|
| 0 (C5 as registered) | 23 |
| 300 | 21 |
| 500 | 20 |
| 800 | 14 |
| 1,000 | 13 |
| 1,536 (goinfer's default ceiling at `max_tokens` 2048) | 3 |

- The median room is about 800 thinking tokens per request.
- Tasks 1, 3 and 9 already pass 16k in some runs with no thinking at all.
- Tasks 6 and 11 are short runs, with room for about 1,400 tokens or more per request.

**Qwen3-8B's T in tack's loop is not known.** It is the number that decides whether this arm is worth running, so it is
measured first (§5, step 1), not assumed.

## 3. How thinking counts against the caps

- **The per-reply cap (`max_tokens` 2048).** Thinking and the answer share it.
  - goinfer's default reasoning budget closes the think block at 1,536 tokens, leaving 512 for the call (goinfer
    `chat/budget.go:BudgetRoom`).
  - A call longer than 512 tokens ends at `max_tokens`. That could be an `edit` or `write` with a large body after a
    long think. tack ends that turn with `max_tokens`.
  - If T turns out long, the arm should send a smaller `budget_tokens` (say 768), so a large edit keeps its room.
    That is a parameter of the arm, registered with it, not tuned after.
- **The window.** As §2. A reply that runs into the window stops short of `max_tokens`, and tack records it as
  `window_full` (`internal/loop/loop.go`, the `max_tokens` stop). Thinking makes this stop more frequent, never less.
- **The 10-minute cap.**
  - At C3's 79 tok/s, T = 800 over a 10-request run is about 100 s more decode.
  - Prefill costs little extra, provided the replayed think block matches the generated one token for token, so the
    prefix is reused. That is checked in §5.
  - The cap binds only on loops, and those fail anyway.
- **tack's token budgets** count prefilled plus output tokens. The pilot sets them out of reach, so they do not bind.

## 4. What pass rate would make it worth running

The arm is worth its build and about 90 minutes of nobara only if it can plausibly reach the bar where C5 did not.
Proposed rule, decided on C5's result and on the measured T:

- **C5 at or above the bar (4 or more passes, 2 or more tasks):** thinking becomes a change measured against C5 by §3's
  rule (adopted at d ≥ 2 sd, sd from C5's per-task rates). Worth running if T leaves most of C5's passing runs inside
  16k.
- **C5 at 1–3 passes:** worth running. The model solves something, and thinking needs to add 4 − C5 passes on tasks
  that fit. Run it only if, at the measured T, at least half of C5's non-loop runs still fit 16k (by §2's arithmetic on
  C5's own records).
- **C5 at 0, with most failures a wrong fix that reached `done`:** worth running for the same reason, under the same
  window condition. Wrong reasoning is what thinking can change.
- **C5 at 0, with most failures `window_full` or loops:** not worth running at 16k. Thinking only fills the window
  sooner.
  - The thinking arm then belongs on Qwen3-4B at 30,720, if C4 shows the 4B can solve tasks. That would be a separate
    draft.
  - Otherwise it waits for a larger window on Qwen3-8B, which this card does not hold.
- **The prediction**, to be written into the amendment with numbers once C5 reports: the arm's passes are at most C5's
  plus the passes the window allows. Its `window_full` stops are at least C5's.

## 5. Order, if approved

1. **Measure T, tier 1, about 5 minutes.**
   - Replay about 20 of C5's captured requests (from its non-loop runs) against Qwen3-8B, with thinking on and the
     goinfer build from §1.1.
   - Record each reply's thinking tokens and whether a call followed.
   - Also check that a replayed think block costs what it should, from one `count_tokens` with and without it.
2. **E0's smoke with thinking on** (2×20, `probe_thinking = true`). E0's gate measured this model with thinking off
   (C3), and a forced call after thinking is a new path.
3. **Register the amendment:** the setting, `budget_tokens`, the predictions, the sd, and the stop rule as C5's.
4. **Then:** fit load, one-task smoke, and the 36 runs, by day under the timing lock (about 1–1.5 hours, under the
   2-hour night line).
