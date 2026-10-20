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

- **The prefix is never reused.** The reused times equal the cold ones at every size. Qwen3.5-9B showed the same in C3
  (goinfer P34).
- 21 of Mellum2.1's 28 layers are sliding-window.
- **Prefill is about 93 tok/s at every size,** in line with goinfer's 97 tok/s.
- A tack loop on this build would pay a full cold prefill on every request: about 115 s at a 10.6k first prompt.

## What it means

- **Mellum2.1 is out for calibration on `b2b5e816`** by E0's rule.
- **Two things would change that, each needing a new E0 on a later build:**
  - prefix reuse (and goinfer's batched MoE prefill, for speed);
  - a smoke at or above 17. §4.7's description diagnostic (run today) shows whether the descriptions move the glob
    misses.
- **The amendment ran over its stated time:** 64 minutes against "15–30 (45 if the prefix is never reused)". The
  estimate priced the 31.7k probes from goinfer's 97 tok/s, but with no reuse each of the six took about 343 s.
