# E0 on nobara: Mellum2.1 on goinfer b2b5e816, 2026-10-19

<!-- citations-at: 263a64f -->

E0 amendment 10 (`e0-preregistration-2026-10-16.md`), registered in `e0f6aa6` before the run. By day, holding nobara's
timing lock for the amendment only, 16:24–17:28 PDT.

**Verdict: fail.** The smoke scored 11/20 on both runs, under E0's 17. Mellum2.1 is not eligible for calibration on this
build.

## What ran

- **Model:** `~/models/mellum2.1-thinking` (JetBrains/Mellum2.1-12B-A2.5B-Thinking), from goinfer's int4 bundle.
- **Server:** goinfer-serve `b2b5e816` with `-backend cuda -moe-cache-experts -thinking off`.
- **tack:** `9f4e0e0`.
- **Load:**
  - The fit load at 32,768 came up `cuda-resident (int4)` in 20 s: 1.0 GB of KV beside 6.4 GB of weights, 7.4 GB of
    VRAM.
  - goinfer reported `forced_tool_call` grammar.
  - So no 16,384 load was needed.
- **Thinking off,** enforced three ways as registered.
- **Records:** `~/tack-runs/e0/mellum-b2b5e816-2026-10-19/` on nobara.

## Results

| measure | value | registered prediction |
|---|---|---|
| resident at 32,768 | yes | 0.5 |
| smoke | 11/20, 11/20, 0 malformed | eligible at 0.7: **missed** |
| `serve check` | ok, 0 failed rows | — |
| forced-call mode | grammar | grammar |
| decode | 73.2 tok/s median (73.0–73.5) | — |
| VRAM peak | 7,508 MiB | — |
| host RSS peak | 25.3 GB | — |

**Its smoke misses are tool choice, not format.**
- 16 of its 40 calls chose glob where the task wanted grep or read. 2 more were glob patterns missing `_test.go`.
- The same confusion appears in gpt-oss-20b and Granite-4.2-3B, but not in the Qwen models
  (`e1-calibration-preregistration-2026-10-18.md` §4.7).

**Time to first token, ms, three repeats each:**

| prompt | cold | prefix reused |
|---|---|---|
| 4,096 | 43,897–44,067 | 43,501–44,339 |
| 8,192 | 86,659–87,404 | 86,310–87,530 |
| 31,744 | 343,075–344,003 | 342,502–344,287 |

- **The probe's reused cell found no reuse:** its times equal the cold ones at every size. **Correction, 2026-10-19
  (goinfer's reading of its code, P34 reopened at goinfer `6ce495c4`, not yet confirmed by a run): this measures a
  rewrite of the prompt's end, not an append.**
  - E0's reused cell changes the last 512 tokens (`internal/eval/runner.go:ttft`). With the template's end-of-turn
    tokens and the generated token, it diverges about 518 positions back.
  - Mellum2.1's sliding-window layers keep KV for the window plus a rollback. On an expert-cache load goinfer grants no
    extra rollback, so a request can diverge only about 448–512 positions back and still reuse. The probe's 518 falls
    outside that.
  - **A request that only appends, as tack's loop does, is unaffected by this cause.** One that rewrites more than
    about 448 tokens of the end is cold on this load.
  - Qwen3.5-9B's cold-equals-reused in C3 has a different cause (its DeltaNet state, goinfer B35).
- 21 of Mellum2.1's 28 layers are sliding-window.
- **Prefill is about 93 tok/s at every size,** in line with goinfer's 97 tok/s.
- ~~A tack loop on this build would pay a full cold prefill on every request~~: **withdrawn.** It assumed the probe's
  no-reuse applies to appends. A loop's own reuse on this load is unmeasured. A rerun with `-log-requests` (goinfer's
  `reused=` and `div=`), or an E0 reused cell that appends, would measure it. A cold first prompt of 10.6k still costs
  about 115 s.

## What it means

- **Mellum2.1 is out for calibration on `b2b5e816`** by E0's rule.
- **It waits, and its E0 is not re-run for a faster build** (owner, 2026-10-19). Its failure is tool choice, and goinfer's
  grouped MoE prefill (option A, #105) does not touch that. Its E0 is re-run only when something could change tool
  choice:
  - a new Mellum release;
  - a goinfer sampling or grammar change;
  - §4.4's empty-result guidance, once C6 adopts it.
- **Two things would change that, each needing a new E0 on a later build:**
  - prefix reuse for the requests a loop makes, measured as above (and goinfer's batched MoE prefill, for speed);
  - a smoke at or above 17. §4.7's description diagnostic (run today) shows whether the descriptions move the glob
    misses.
- **The amendment ran over its stated time:** 64 minutes against "15–30 (45 if the prefix is never reused)". The
  estimate priced the 31.7k probes from goinfer's 97 tok/s, but the probe's reused cells were cold too, so each of the six took about 343 s.
