# E1 calibration C6, change 4.6: the window-sized output cap, 2026-10-19

<!-- citations-at: f384182 -->

This is change 4.6 under amendment C6 (`e1-calibration-preregistration-2026-10-18.md` §12, registered in §12.5 before
any run). A is A1 as is; B is A with `[run] fit_window = true`. The two arms ran interleaved, A B B A A B per task.

**Verdict, as registered: not adopted, d = +1.** B passed 2 of 36 and A passed 1 of 36. §12.3's rule adopts at
d ≥ 3 and parks at d = 2.

**Mechanism, beside it: met.** Window-full stops fell from 25 in A to 12 in B, counting prompt-too-long refusals,
against a prediction of at least 5 fewer. It did not turn into passes.

**After this result, the owner made `fit_window` a background condition** for the rest of C6 (§12.6). It is on in both
arms of each later change. That is not an adoption, makes no claim for it, and does not change this verdict.

## What ran

- **When:** 18:08:32–20:23:11 PDT by day (the owner's evening approval), 2 h 14 min, holding both machines' locks for
  this comparison only.
  - 72 runs, 127 minutes of run time.
  - That matches the registered "about 2¼ hours". The start waited for the Mac to come off battery (about 18:05) and for
    a goinfer job on nobara (until 18:08). My mid-run projection of 21:00–21:30 was too pessimistic.
- **Builds:**
  - tack `c7d5c05`, clean, on both machines;
  - goinfer `b2b5e816`;
  - Qwen3-8B `Q4_K_M` at 16,384, `cuda-resident (int4)`, `-serve-option thinking-off`.
- **Cells:**
  - **0 void.**
  - At every cell's start: nobara at load 0.00–2.33 of 16 cores with 55.5–56.0 GB available, and the Mac on mains.
    The Mac's load peaked at 7.42, recorded only; it drives the runs.
  - The cell check's memory rule double-counts a loaded model. It is known and inert here (§12.5) and is fixed before
    4.5.
- **The cell check before the start, model loaded and idle:** nobara at load 2.00 of 16 cores (limit 8.0), 56.0 GB
  available (limit 8.5 GB), the Mac on mains. Clean, so the load rule needed no fix.
- **Records:** `~/tack-runs/e1/c6-46-2026-10-19/runs/` on the Mac.

## Task by task

| task | A passes | B passes | d | A window-full | B window-full |
|---|---|---|---|---|---|
| 1 | 0/3 | 0/3 | 0 | 2 | 0 |
| 2 | 0/3 | 0/3 | 0 | 3 | 3 |
| 3 | 0/3 | 0/3 | 0 | 3 | 3 |
| 4 | 0/3 | 0/3 | 0 | 3 | 1 |
| 5 | 0/3 | 0/3 | 0 | 3 | 2 |
| 6 | 0/3 | 0/3 | 0 | 2 | 0 |
| 7 | 0/3 | 0/3 | 0 | 3 | 0 |
| 8 | 0/3 | 0/3 | 0 | 0 | 0 |
| 9 | 0/3 | 0/3 | 0 | 2 | 0 |
| 10 | 0/3 | 0/3 | 0 | 1 | 2 |
| 11 | 1/3 | 1/3 | 0 | 1 | 1 |
| 12 | 0/3 | 1/3 | **+1** | 2 | 0 |
| **all** | **1/36** | **2/36** | **+1** | **25** | **12** |

Over the 9 tasks without tasks 3, 8 and 9: A 1/27, B 2/27.

**Stop reasons:**

| | done | prompt-too-long | window_full | budget | max_tokens |
|---|---|---|---|---|---|
| A | 8 | 20 | 5 | 2 | 1 |
| B | 20 | 4 | 8 | 4 | 0 |

## Against the predictions (§12.5)

| prediction | outcome |
|---|---|
| B has at least 5 fewer window-full stops | **13 fewer** (25 → 12): held |
| d between −1 and +3 | **+1**: held |
| about 25% adopted, 25% parked, 50% not adopted | not adopted |
| A about 4 passes (3–6), like C5 | **1**: missed |

## What it means

- **The cap does its job and the model does not use it.** With room left, B's runs went on and ended with `done` 20
  times against A's 8, but 18 of those 20 were wrong or empty fixes. Room was not what B lacked. It is the same picture
  as C5: the model's fixes are the limit, not the window, once the window stops failing first.
- **B still filled the window 12 times.** The cap holds back only tool results. The model's own replies, edits and loops
  (task 11's `read`/`edit` pairs, 34–52 rounds) still fill it.
- **The passes are genuine:**
  - task 11: A-2 and B-1 lowercased the error string, as C5's passes did;
  - task 12: B-1 rejects an inconsistent shape in `ann/flat_i8_persist.go`.

  No passing diff touches a test file.
- **A's 1 pass against C5's 4.** §12.1 registered a difference of 3 or more as a build effect, to be reported to
  goinfer, and it is reported. But it may well be chance:
  - The drop is task 11 (3/3 in C5; 1/3 in A and 1/3 in B, so 2/6 across both arms) and task 2 (1/3 in C5, 0/6 here).
  - The failures are C5's own kinds: two `read`/`edit` loops, and two edits that rewrote the error string wrongly or not
    at all.
  - tack's A arm is unchanged with the setting off (`TestFitWindowOffIsUnchanged`), so the differences are goinfer
    `b2b5e816` against `f20f0d93`, and sampling at temperature 0.7.
  - Three runs per task cannot tell those apart.
- **For C6 going on:** A's 1/36 is below §2's bar of 4. The registered rule for each change is d against its own A, and
  that stands. But a do-nothing arm this close to zero leaves the rule able to see only changes that create passes. The
  owner may want C5's arm re-checked on `b2b5e816` before 4.5, so as not to measure the next change on a baseline that
  has moved.

## goinfer's answer on a build effect (2026-10-19, after the run)

goinfer's Mac session read `git diff f20f0d93 b2b5e816`. It found no non-test change under `chat/`, the sampler, the
grammar, or serve's tool, think or Anthropic stream code. What did change should not reach Qwen3-8B:
- the windowed-KV `PrefillPartial` fix acts only on windowed models, and Qwen3-8B is not one;
- the kv3-tiled split-KV prefill is routed to Gemma 2 and dense Gemma 4 only;
- the CUDA stream-pack, load-time, fit and swap-guard changes affect loading, not numerics.

Bit-identity of Qwen3-8B across the two builds is not verified. One greedy request with a fixed prompt on each build
(same `-ctx 16384`, thinking off) would separate a build effect from sampling in minutes. Until that runs, A's drop to
1/36 is read as sampling: 4/36 against 1/36 over 3 runs per task is within its noise.

## The determinism check (owner, 2026-10-19, 20:31–20:33 PDT)

Qwen3-8B `Q4_K_M` at `-ctx 16384`, thinking off, through pilot-serve (tack `c7d5c05`), holding nobara's lock.
- **Two cold loads per build,** f20f0d93 and b2b5e816. Each answered two fixed requests greedily (temperature 0).
- **A text request:** about 1.7k tokens, a Go file and a question, 200 tokens allowed, with the top 5
  log-probabilities at every token.
- **A tool request** with `tool_choice` required, the grammar path A1 uses.
- **The card at the start held nothing but the desktop compositor (17 MiB).**
- **Records:** `~/tack-runs/e1/determinism-2026-10-19/` on nobara.

**Result: each build repeats itself exactly, and the two builds agree.**
- The text reply is the same 121 tokens on all four loads, with **bit-identical log-probabilities**. The full top-5
  lists, 605 entries, hash to `1518c46108b09c40` on every load. The smallest gap between the top two tokens along the
  way is 0.086, so a numeric difference large enough to matter would likely have changed a token.
- The tool reply is the same call on all four loads: `grep {"pattern": "applyTokenBudget", "path": "tack"}`, 27
  tokens. goinfer returns no log-probabilities for a grammar-forced call, so the tool path is compared by output only.
- **No build effect is shown, so A's 1/36 against C5's 4/36 is read as sampling** at temperature 0.7 over 3 runs per
  task.
- **The check's limit:** one prompt of about 1.7k tokens. A1's prompts run 10–16k, and a kernel path that only
  longer prefills take would not be exercised. goinfer's reading of the diff (above) finds no such change for Qwen3-8B.
