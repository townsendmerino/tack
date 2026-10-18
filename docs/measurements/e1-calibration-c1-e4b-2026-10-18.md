# E1 calibration, amendment C1: gemma-4 E4B, 2026-10-18

<!-- citations-at: a7855bc -->

Amendment C1 (`e1-calibration-preregistration-2026-10-18.md` §7) measures gemma-4 E4B on the calibration set.

**Status: stopped after stage A, before the 36 runs, and brought to the owner.** Both gates passed. The one-task
smoke showed that the runs would mostly measure goinfer's gemma-4 path on the RTX 2070 SUPER, not E4B with tack's
loop.

## Gate 1: the 32k fit load, passed

- **Run:** 09:54 PDT, through `tack eval pilot-serve` on goinfer-serve `0de92cd9`.
- **Result:** `cuda-resident (int4)`, with "32768-token cap needs 1.1 GB KV (weights 3.9 GB, …)", and 4,709 of
  8,192 MiB of VRAM.
- **A harness error came first, and is kept beside it.** An attempt at 09:53 was recorded as failed because the load
  wrapper read the decode path from the first line of pilot-serve's log, while the endpoint JSON is on the second.
  - That attempt's own log shows `cuda-resident (int4)`.
  - The wrapper was fixed, the gate was rerun, and the attempt is kept as `…-wrapper-bug`.

## Gate 2: E0 on goinfer 0de92cd9, passed by C1's rule; E0's own verdict void

**Run:** 09:54–10:21 PDT, `eval/e0/nobara-0de92cd9.toml`, tack `dd39263`. Output is in
`~/tack-runs/e0/c1-e4b-0de92cd9/` on nobara.

- **Smoke:** 17/20 on both runs, 0 malformed. That meets C1's gate.
- **E0's own verdict: void.** Its time-to-first-token phase at 15,360 tokens failed 4 of 8 requests with a goinfer
  error: "cuda: windowed KV: position 7552 needs history from position 7041 but this slot holds only from 10625 (a
  reuse or rollback past the window slack …)".
  - This is a goinfer bug in windowed KV on CUDA, which is on by default in `0de92cd9`, when a request reuses a
    cached prefix.
  - E0 grades the arm itself "eligible": smoke 17/17, and the check is ok.

## The one-task smoke: what stopped C1

**Run:** task 1, 10:21 PDT, tack `dd39263`, goinfer `0de92cd9`. Output is in `~/tack-runs/e1/cal-e4b-2026-10-18/smoke-t1/`.

- **Harness:**
  - the forced-call mode arrived as "post-check";
  - the first request carried the registered sampling;
  - all 5 completed requests' timings paired;
  - no resend was needed (`resends` 0).
- **Prefill is slow on this card for gemma-4.** goinfer fell back to sequential prefill on every request after the
  first, because "layer 5 attention at 12447 attended keys needs 50300 B of shared memory, past this device's
  49152 B limit — the exact batched attention has no split-KV path and the fused attention does not serve this pass
  (it needs head dim 64 or 128 …)".
  - Time to first token: 48 s, 86 s, 10 s, 76 s, 5 s and 128 s, on prompts of 10.9k–25.6k tokens.
  - At that rate, a run of 15 requests reaches the 10-minute cap on prefill alone.
- **The run ended `error`: "goinfer: reading stream: unexpected EOF".**
  - The 6th request is in goinfer's log with status 200 and 131.6 s total, 127.7 s of it before the first token.
    tack's client saw the stream end early.
  - Earlier requests up to 96.9 s total got through.
  - goinfer's HTTP server sets `IdleTimeout: 120 s` (`internal/serveapp/main.go`), which fits a cut near 120 s of
    silence. This is not proven.
  - Six requests, 4.7 minutes, no diff. The oracle failed.

## What it means

- **Each of the three is goinfer's or the card's, not E4B's or tack's loop:**
  - sequential prefill past the 2070 SUPER's shared-memory limit, for gemma-4's head size;
  - the stream lost near 120 s;
  - windowed KV failing on prefix reuse.
- **Running the 36 runs now would measure them,** mostly as cap hits and errors. C1 waits for the owner's decision.
