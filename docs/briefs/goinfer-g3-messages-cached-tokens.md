# Brief for goinfer: report reused-prefix tokens on `/v1/messages` (tack's G3)

<!-- citations-at: goinfer 0e598d3f -->

**From:** tack (DESIGN §13 G3, decided by the owner 2026-10-16).

**For:** goinfer's usual process: task doc or brief, then vscode-claude, then goinfer's gates.

## What tack needs

Per request, how many prompt tokens goinfer served from the reused prefix, and how many it prefilled.

tack records both in each turn's `usage` event. The context ledger shows the reuse share, E1 reports it per arm, and
the P3 exit rule grades it: at least 80% of prompt tokens reused after turn 3 (DESIGN §7.2). Today tack records
"reused: unknown" on every request, because tack stays on `/v1/messages` (owner, 2026-10-16) and that route does not
report reuse.

## What goinfer already has

- **The number exists.** `loadedModel.drive` (`internal/serveapp/openai.go`) returns `prefillReused`, the leading prompt
  tokens whose prefill a prior turn's KV made unnecessary (`decoder.Generation.PrefillReused`).
- **`/v1/responses` already reports it,** as `usage.input_tokens_details.cached_tokens`, through `withCachedTokens`
  (`internal/serveapp/responses.go`).
- **`/v1/messages` carries only `input_tokens` and `output_tokens`:**
  - the non-streaming response's `usage` (`internal/serveapp/anthropic.go`);
  - `message_start` and `message_delta` (`internal/serveapp/anthropic_stream.go`).
- **`-log-requests` prints `reused=` per request** (`internal/serveapp/reqlog.go`), but only on stderr.

## The ask

Report the reused count on `/v1/messages`, in the Messages API's own field, so Anthropic SDK clients read it unchanged.

1. **`usage.cache_read_input_tokens`**: the prompt tokens served from the reused prefix, the same count `/v1/responses`
   reports as `cached_tokens`. Report it on:
   - the non-streaming response's `usage`;
   - the streaming `message_start` event's `message.usage`;
   - the streaming `message_delta` event's `usage`.
2. **A decision for goinfer on `input_tokens`:**
   - **Keep the Messages API's split:** `input_tokens` counts only the tokens prefilled, and the prompt is
     `input_tokens + cache_read_input_tokens`. That is what Anthropic's clients sum (Claude Code computes context from
     the sum), and tack would read it that way.
   - **Or keep `input_tokens` as the whole prompt,** as now, and document that goinfer's `cache_read_input_tokens` is a
     subset of it rather than an addition.
   - tack needs only to know which one, so it is goinfer's call. The first matches the API's documented semantics; the
     second changes nothing for existing clients.
3. **Optionally, prefill time:** `goinfer_prefill_ms` in `usage`, beside the existing `goinfer_*` extensions. tack's
   ledger shows prefill time per request, and today it can only time the whole stream.

## Not asked

- Any change to how goinfer reuses or caches.
- `cache_creation_input_tokens`. goinfer keeps every prefix it prefills, so there is no separate "write to cache" cost
  to report. Leaving the field out, or setting it to 0, is fine.
- Any change to `count_tokens`.

## How tack will check it

- A goinfer test: two requests sharing a long prefix. The second reports `cache_read_input_tokens` > 0, equal to what
  `/v1/responses` reports for the same pair. The streaming and non-streaming reports agree.
- goinfer's `sdktest` reads the field through the official Anthropic Go SDK's `Usage.CacheReadInputTokens`.
- On tack's side, the goinfer client fills `server.Usage.Reused` (now always nil), and a cassette re-recorded against
  the new goinfer covers it.
