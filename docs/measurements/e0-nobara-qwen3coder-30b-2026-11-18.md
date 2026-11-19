# E0 on nobara: Qwen3-Coder-30B-A3B, experts cached from host memory, goinfer 9de08125c, 2026-11-18

<!-- citations-at: 2aca669 -->

This is step 1 of calibration C13 (`e1-calibration-preregistration-2026-10-18.md` §19), registered and pinned before the run.
It is E0 for Qwen3-Coder-30B-A3B `Q4_K_M` at 16,384 with `-moe-cache-experts`, on goinfer `9de08125c` and tack `08c7d30`. It
ran by day, as one job of nobara's `night.py`, which held nobara's timing lock: 15:25:16–15:31:41 PST.

**Verdict, as registered: void.** goinfer's swap guard halted the server during the timed cells, so 11 requests got a 503,
and the arm was not measured. **Setting the void aside, it is parked, not eligible:** smoke was 16 of 20 on both runs (E0's
14–16 band), with 0 malformed and `check` ok. Under §19, E0 void or not eligible means no calibration run, and the 35B stays
E1's model. §19's step 2 did not run.

## What ran

- **Model:** `~/models/qwen3-coder-30b-a3b-gguf/Qwen3-Coder-30B-A3B-Instruct-Q4_K_M.gguf` (unsloth's GGUF at `b17cb02`,
  sha256 `fadc3e5f8d42bf7e…`), served from its v17 int4 `.giw` sidecar.
- **Server:** goinfer-serve `9de08125c` (main), `-backend cuda -moe-cache-experts -thinking off`, at `-ctx 16384`. A copy is
  at `~/tack-runs/bin/goinfer-serve-cuda-9de08125c` on nobara, sha256 `4af7bda99cb76e3a…`.
- **Tool calls:** goinfer renders the tools with its own Hermes prompt (`<tool_call>{json}</tool_call>`), not the template's
  XML form; `forced_tool_call` is `grammar`. All 40 smoke responses were well-formed.
- **tack:** `08c7d30`, built clean, at `~/tack-runs/bin/tack-08c7d30` on nobara.
- **The job:** `tack-c13-qwen3coder-e0` on nobara's `night.py`, queued 15:23:52 and started 15:25:16, with load 0.08, 59 GB
  available, 1,523 MB of system swap in use and 483 MiB of VRAM in use. The fit-only load ran first, then E0 from 15:25:38.
  It exited with rc 2 at 15:31:41: `VERDICT: INCONCLUSIVE — eval models: 0 of 1 arms measured`.
- **Files on nobara:**
  - script `~/tack-runs/e0/c13-qwen3coder-e0-9de08125c.sh`;
  - manifest `~/tack-runs/e0/manifests/nobara-c13-qwen3coder-9de08125c.toml`;
  - records `~/tack-runs/e0/qwen3coder-30b-9de08125c-2026-11-18/` (`fit.log`, `e0.log`, `fit/results.jsonl` and
    `qwen3-coder-30b-a3b-q4km-cuda-moecache/results.jsonl`).

## Results

| measure | value | the 35B's E0 (C8, 2026-10-30) |
|---|---|---|
| admitted | yes, 16,384, `cuda-resident (int4)`, loaded in 18.4 s | yes, 16,384, `cuda-resident (int4)` |
| fit line | 16384-token cap needs 1.5 GB KV (weights 16.0 GB, budget 30.4 GB, 28.9 GB left for a request's own prefill) | 0.6 GB KV (weights 18.2 GB) |
| `serve check` | ok, 0 failed rows | ok |
| smoke | 16/20, 16/20, 0 malformed, no unstable task | 20/20, 20/20 |
| decode | not measured (every decode request got a 503); nobara's setup read 30.0 tok/s on a short prompt | 51.5 tok/s |
| host RSS peak | 35.9 GB | 40.5 GB |
| VRAM peak | 7,478 MiB | 7,733 MiB |

**The four smoke misses, the same on both runs** (greedy, so the second run checks stability):
- task 1: globbed `internal/**/*.go`, which lacks `_test.go`. This is the glob every Qwen model missed (C3, §12.23); the
  35B passed it;
- task 12: no tool call;
- task 18: called `glob`, where `grep` was wanted;
- task 19: called `read` on `DECISIONS.md`, where `glob` was wanted.

**Time to first token, ms, three repeats each.** These are reported, not gated. The cells after the trip got 503s:

| prompt | cold | append | rewrite |
|---|---|---|---|
| 4,096 | 15,446 / 16,341 / 17,451, median 16,341 | 3,084 / 3,201 / 3,264, median 3,201 | 3,020 / 3,049 / 3,026, median 3,026 |
| 8,192 | 31,978 / 34,478 / 36,169, median 34,478 | 503 ×3 | 503 ×3 |
| 15,360 | 503 | — | — |

- Cold prefill ran at about 240–250 tokens/s, against the 35B's 110–150.
- The 4k append median was 3.2 s, against the 35B's 11.6 s.

## The swap-guard halt

- **When:** right after the third cold 8,192 cell (15:31:37). Every request after it got `503 halted: swap guard tripped:
  swap grew past the guard threshold`. That was 8,192 append and rewrite (6 requests), 15,360 (1) and decode (3).
- **Which source tripped is not in the record.** E0 keeps the server's results, not its log, so the guard's own line is not
  here.
  - At `9de08125c` on Linux the guard watches two sources: the server's own swap (VmSwap of the process and its
    descendants) at +512 MB over its baseline, and system swap at a +4,096 MB backstop.
  - System swap read 1,523 MB at the job's start and 1,477 MB after the server exited, so the backstop is unlikely to have
    tripped.
  - That points to the server's own swap growing past 512 MB during the 8k cold prefills, freed when the process exited.
    This is inferred, not measured: nobody sampled VmSwap during the cells.
- **Not seen before on this model:** nobara's setup read VmSwap 0 at 16,384 and 32,768. Those were 27-token prompts, so no
  long prefill ran.
- **The guard was not changed or bypassed.** A re-read with VmSwap sampled through an 8k cold prefill, and the server's log
  kept, would settle the source. That read is for goinfer's session on nobara.

## What follows

- **§19's step 2 does not run** (§19: "E0 not eligible, or void: no calibration run, and the 35B stays E1's model"). No
  calibration run reached the Mac, and neither machine's lock was held for one.
- **A clean re-run of E0 would still grade parked** on this smoke (16 of 20, stable across runs). Under E0's rule a parked
  model is used only by explicit choice, and §19 registered step 2 for an eligible E0 only. Running the first set on a parked
  E0 would be Francis's choice and a new line in §19 before any run.
- **Against the predictions:** §19 predicted E0 eligible. It was void, and parked on smoke.
- **Decided (Francis, 2026-11-18, after this record): C13 stops here.** No E0 re-run and no first set, and the 35B stays
  E1's model. The swap-guard trip is under investigation by goinfer's session on nobara: an 8k cold prefill on this model,
  with the serve log kept. No guard is changed.
