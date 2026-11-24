# Task: E1 calibration C14, Inkling 2 Mini on nobara through llama.cpp (2026-11)

> **Status 2026-11-24: open; nothing built or run.** Written by Cowork at Francis's request (chat, 2026-11-24 12:06 PST: "can
> you write a task doc in tack for the Inkling 2 Mini eval run?"), after he deferred the run on 2026-11-20 ("Tomorrow we
> can"). Facts read at tack main `7be6fc5`.
>
> This chooses a model, so it takes full measurement discipline (CLAUDE.md, "Rigor matched to stakes"). The registration
> below is a draft. C14-1 commits it, with its pins, as §20 of `docs/measurements/e1-calibration-preregistration-2026-10-18.md`
> before E0 runs, in §19's shape.
>
> **Why.** Thinking Machines released Inkling 2 Mini on 2026-11-20.
> - **The model:** an Apache-2.0 looped MoE, 26.4B total and 3.4B active (5.4B weight activations counting the three
>   passes), text and image input.
> - **Its claims:** 48.2% on DeepSWE v1.1 and 77.9% on SWE-bench Pro v2 (Hard), against Qwen3.8-27B's 42.2% and 73.8% in
>   its launch table. It is weaker on multi-turn chat and many-constraint instructions (50.0% on MultiChallenge).
> - **The question:** whether it beats the 35B (Qwen3.6-35B-A3B), E1's model since C8, in tack's own loop.
>
> **The obstacle.** goinfer does not support the family, and tack's eval runner (`internal/eval`) drives `goinfer-serve`
> only: a manifest names `goinfer_serve` and `goinfer_rev`, and an arm's `args` are goinfer flags.
> - tack itself can already talk to llama.cpp's server: `kind = "openai-remote"` (`internal/server/openai`, DESIGN §6.4).
>   llama.cpp has GGUF support for the model from launch (Unsloth publishes the quants).
> - So C14 runs Inkling on llama.cpp's `llama-server`, after the runner learns to drive an external server.
> - Because that changes the server as well as the model, C14 also runs the 35B on the same llama-server build as a control
>   (C14-3). Without it, a difference could be the server's tool parsing or prefix reuse rather than the model.
>
> **Not in scope:**
> - goinfer support for the family. That is a goinfer-notes doc, and only if C14 makes Inkling a candidate (C14-6),
>   because tack bundles goinfer.
> - The Mac: TML puts Ollama on a Mac at 32 GB or more, and the M1 Pro has 16 GB.
> - Images, and the P1 screen's 24,576 window (C14 runs at 16,384 so its rows sit beside C8's and C13's).

<!-- ledger -->
| id | state | next | evidence |
|---|---|---|---|
| C14-0 | open | the eval runner drives an external OpenAI-compatible server (§1), red-first tests, a PR | — |
| C14-1 | open | on nobara: pin llama.cpp, the Inkling GGUF and the flags (§2); run the setup read; commit the registration as calibration §20 with the pins | — |
| C14-2 | open | step 1, E0 on nobara at 16,384 under E0's rule, as one job of nobara's `night.py` by day | — |
| C14-3 | open | step 2, the first set: tasks 11, 1, 10 × 3 runs, Inkling and the 35B-on-llama.cpp control interleaved | — |
| C14-4 | open | expansion block by block, only if a first-set run passes (§19's order and cut-offs) | — |
| C14-5 | open | the record: `docs/measurements/e1-c14-inkling-2-mini-<date>.md`, rows beside C8's 16k row and the control | — |
| C14-6 | decision | Francis: E1's model stays the 35B, or Inkling becomes a candidate (and then whether goinfer takes on the family) | — |

## 1. The runner change (C14-0)

This is ordinary engineering: a red-first test, `go run ./tools/touched` and `go run ./tools/lint`, and a PR.
- **A new arm kind.** An external server, launched by the runner from a pinned binary and a recorded command line, reached
  over `openai-remote`:
  - the manifest records the binary's path, sha256 and revision in place of `goinfer_serve` / `goinfer_rev`;
  - every result carries them, as it carries goinfer's revision today.
- **Readiness and identity.** The runner waits for the server's `/health`, then checks that `/v1/models` names the pinned
  file. That takes the place of goinfer's banner check of the decode path.
- **A swap guard the runner owns.** llama-server has no swap guard. The runner samples the server's VmSwap and system swap
  every second, with goinfer's Linux thresholds (+512 MB over the process's baseline, a +4,096 MB system backstop). A trip
  voids the cell, exactly as goinfer's 503s void a goinfer cell.
  - The guard logs its own samples, since C13 could not tell which source tripped.
- **Forced calls.** The arm's forced-call mode maps to `tool_choice: "required"`. The runner records whether the server
  honored it on every request: a free-text answer where a call was required counts as malformed.
- **Token counts.** They are estimates on this adapter (DESIGN §6.4), and reuse comes from `cached_tokens`. The record
  says which numbers are estimated.
- **Tests.** Tier 0, against a fake server and hand-written wire fixtures:
  - the identity check, the guard's trip (planted), the void path and the forced-call accounting;
  - each new check shown failing first.

## 2. Pins on nobara (C14-1)

These are read and fixed before E0 by a setup run on nobara's timing lock, then copied into §20:
- **llama.cpp:** a pinned commit with Inkling support, a CUDA build for sm_75, the binary at a durable path under
  `~/tack-runs/bin/`, with its sha256.
- **The model:** an Unsloth Inkling 2 Mini GGUF in `~/models`, sha256 recorded.
  - Prefer the 4-bit file closest to the 35B's `q4_k_m` (a UD-Q4_K_XL or Q4_K_M).
  - Say why if the fit forces another.
- **Flags:**
  - `--jinja` with the model's own template, `-c 16384`;
  - `--fit` placement, never a forced full offload (goinfer's September finding about `-ngl 99`), with experts in RAM as
    needed (`--cpu-moe` / `--n-cpu-moe`);
  - prompt caching on, and one slot.
- **Thinking.** tack sends thinking disabled.
  - Record how this template turns reasoning off, or how far down it goes.
  - **If reasoning cannot be turned off, stop at C14-1 and bring it to Francis**: a reasoning arm under a 2,048-token
    `max_tokens` would not be the arm the 35B ran.
- **The setup read:** load at 16,384 with VmSwap 0; decode tok/s on a short prompt; VmHWM and VRAM; one tool-call probe
  in each of auto and required modes, both well-formed.
- **The 35B control** uses the same build and flags on its own `q4_k_m` from `~/models`. It is pinned the same way and read
  the same way.
- **tack:** the commit that adds §20, built clean on the machines the calibration uses, as §19.

## 3. The registration (draft, one screen; C14-1 commits it as §20)

- **The question.** On the loop as adopted, does Inkling 2 Mini pass the calibration tasks at least as often as the 35B does
  at 16k?
- **Step 1, E0 on nobara at 16,384,** with E0's rule unchanged (`e0-preregistration-2026-10-16.md`, amendments 4 and 11):
  - admitted at the window, the server's identity check passing, smoke at least 17 of 20 on both runs with 0 malformed;
  - a swap-guard trip, an unanswered request or the wrong file served is void;
  - a job of nobara's `night.py`, holding its lock, by day;
  - step 2 runs only if E0 is eligible.
  - The 35B control takes E0 on the same server too. Its 20/20 on goinfer (C8) is reported beside it, not decided on.
- **Step 2, the first set:** tasks 11, 1 and 10, 3 runs each, in that order.
  - **The two arms interleave** per task: A B B A, then B A for the third runs, so each arm runs 3 times (CLAUDE.md, "Comparisons are interleaved").
  - **Settings, §19's arm:** the bundle as adopted, forced-call mode, sampling 0.7 / 0.8 / 20, `max_tokens` 2048, failure
    budget 8, the 30-minute cap, snapshots on (ADR-006).
  - **Cell check:** each block follows a clean check at 16,384.
- **Expansion, cut-off and stopping** as §19, applied to Inkling with the control running alongside:
  - expansion only if a first-set run passes;
  - tasks 2, 12, 6, 5, 3, 4, 7, 9 and 8, one 3-run block at a time;
  - no block starts after 20:00;
  - no first-set pass stops the run after its 9;
  - a void run is recorded and re-run in its place.
- **The metric:** passes per task out of 3, task by task, never pooled, beside the control's row and C8's goinfer row
  (task 11 3/3, task 1 1/3, task 10 3/3; the full row 12 of 36 on 6 tasks). C8 ran with snapshots off; the record says so.
- **The bar,** read only when all 12 tasks have 3 runs: §2's, usable at 4 or more passes on 2 or more tasks, and judged
  against the control on the same server.
- **What each outcome means** (the choice is Francis's):
  - **E0 void or not eligible:** no step 2, and the 35B stays.
  - **First set 0 of 9:** the 35B stays.
  - **All 12 tasks run, but Inkling is under the bar, or below the control:** the 35B stays.
  - **All 12 tasks run, at the bar and at or above the control:** Inkling is a candidate. Its row, beside the control's and
    C8's, with decode speed and host memory, goes to Francis (C14-6).
- **Reported, not decided on:**
  - stops by cause, tokens at stop, requests, first-token times, cap hits and the guard table (`tools/calreport`);
  - decode rate, host RSS and VRAM;
  - malformed and required-but-unanswered calls per hundred;
  - the reuse share from `cached_tokens`.
- **Predictions** (Cowork, before any run; the session refines them in §20):
  - E0 eligible if llama.cpp parses this model's tool calls, otherwise not eligible on malformed calls;
  - first set 6 to 9 of 9;
  - the control at or a little under C8's 7 of 9 on goinfer;
  - the median run 2 to 6 minutes.

## Machines and rules

- **Where:** nobara (RTX 2070 SUPER 8 GB, 62 GB RAM) serves both arms, from `~/models`, never `/srv/models`. Calibration
  runs use the same machines as §19.
- **When:** by day under the timing lock, with the expected wall time stated first.
  - Estimated machine time: setup and E0 about 1 hour; the first set about 1.5 to 2 hours with the control; full expansion
    about 6 to 8 hours over later days.
  - Nothing of tack's runs beside a timed goinfer job.
- **Changes:** every one lands through a PR. C14-0 is eval tooling, not product code; the coordinator reads the diff and
  sets auto-merge.
- **Decisions:** each goes to the coordinator. Francis gets C14-6, a reasoning model that cannot turn reasoning off
  (C14-1), and any parked E0 that someone wants to run anyway.

<!-- doc-reviewed: 2026-11-24 -->
