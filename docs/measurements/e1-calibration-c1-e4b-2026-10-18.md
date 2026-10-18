# E1 calibration, amendment C1: gemma-4 E4B, 2026-10-18

<!-- citations-at: a7855bc -->

Amendment C1 (`e1-calibration-preregistration-2026-10-18.md` §7) measures gemma-4 E4B on the calibration set.

**Status: paused (owner, 2026-10-18), not void and not failed.** Stopped after stage A, before the 36 runs. Both gates passed. The one-task
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

## Status: paused (owner, 2026-10-18)

**C1 is paused, not void and not failed.** No run of the 36 happens on goinfer `0de92cd9`. goinfer gets a brief on the
three findings above.

When goinfer ships fixes, C1 resumes under a small amendment naming the new build:

1. it starts again at gate 2: E0, which must not be void this time;
2. then the one-task smoke;
3. then the 36 runs, only after the owner has read the smoke.

### The dropped stream: tack's side ruled out

Every timeout on the request's path, Mac to nobara, as set on 2026-10-18:

| hop | setting |
|---|---|
| tack's goinfer client (`internal/server/goinfer/client.go:New`, `hc` nil) | `&http.Client{}`: no overall `Timeout`; `http.DefaultTransport`, `ResponseHeaderTimeout` 0 |
| the capture proxy's server (`internal/eval/pilot/capture.go`) | `http.Server` with no `ReadTimeout`, `WriteTimeout` or `IdleTimeout` |
| the capture proxy's upstream (`httputil.NewSingleHostReverseProxy`) | `http.DefaultTransport`: `ResponseHeaderTimeout` 0. `IdleConnTimeout` is 90 s, but it applies only to idle pooled connections. |
| the ssh port forward (`cmd/tack/pilot_a1.go:forwardCmd`) | its own connection (`ControlPath=none`); from `~/.ssh/config`, `ServerAliveInterval 30`, `ServerAliveCountMax 6` (gives up only after 180 s without a reply), `TCPKeepAlive yes` |
| nobara's sshd | no `ClientAlive*` or `TCPKeepAlive` line in `/etc/ssh/sshd_config`, so the defaults: `ClientAliveInterval` 0, `TCPKeepAlive yes` |
| goinfer-serve `0de92cd9` (`internal/serveapp/main.go`) | `ReadHeaderTimeout` 10 s, `ReadTimeout` 60 s, `IdleTimeout` 120 s, **`WriteTimeout` 0** |

**Where the bytes stopped:**

- The capture recorded the cut request's response as 297 bytes: `message_start` and one `ping`, which goinfer sends
  before prefill. Then nothing.
- The capture had not finished the request (no `.meta` file) when tack saw "unexpected EOF".
- goinfer logged the same request as complete: status 200, 136 tokens, 131.6 s.

So the bytes goinfer believed it wrote after its 127.7 s of prefill never reached the Mac's capture. The cut is
between goinfer's socket and the capture: the ssh forward, or goinfer's own connection handling. Nothing on tack's
side is set near 120 s. goinfer's `IdleTimeout` does not cut an in-flight response, and its `WriteTimeout` is 0.
