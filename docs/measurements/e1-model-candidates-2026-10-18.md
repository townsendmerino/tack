# Model candidates for nobara's 8 GB card, 2026-10-18

<!-- citations-at: goinfer 0de92cd9, tack 5a04d76 -->

The calibration baseline was below the bar (`e1-calibration-baseline-2026-10-18.md`), so the next decision is the
model or the hardware (DESIGN §17, R13). This page lists the models on nobara that goinfer can serve with tools on
CUDA, and what is known about each fitting the RTX 2070 SUPER (8 GB) at 32k. The owner chooses from it.

## What goinfer does with each family (goinfer main `0de92cd9`, read from the code)

- **Tool templates.** goinfer accepts tools only on six chat templates: chatml, mellum2, mistral, llama3, gemma4 and
  harmony (`chat/tools.go:SupportsTools`). A tool request on any other template is refused with a 400.
- **`forced_tool_call` is "grammar"** when the template has a JSON call wrapper; then a reply without a call cannot be
  generated.
- **It is "post-check"** otherwise: the reply runs unconstrained, and one without a call is an error
  (`internal/serveapp/tools.go:forcedCallEnforcement`). tack resends such a request at most twice (`bd6d026`).

| model on nobara | call format | forced_tool_call | CUDA sidecar | fit at 32k on the card |
|---|---|---|---|---|
| Qwen2.5-7B-Instruct `q4_k_m` (the baseline) | JSON (Hermes `<tool_call>`) | grammar | built | known: resident, 1.8 GB KV beside 4.0 GB |
| Qwen2.5-Coder-7B-Instruct `q4_k_m` | JSON (Hermes) | grammar | built | known: resident |
| gemma-4 E4B / E2B `q4_0` | native `<\|tool_call>call:NAME{…}` | post-check | built | known: resident (1.1 / 0.4 GB KV beside 3.9 / 2.4 GB) |
| gpt-oss-20b MXFP4 | native Harmony, JSON arguments | post-check | built | **resident with `--moe-cache-experts`**: below |
| Qwen3.5-9B `Q4_K_M` | JSON by default (native XML with `-tool-format template`) | grammar | tonight | tonight |
| gemma-4 12B `q4_0` | native | post-check | tonight | tonight |
| Qwen3-4B (bf16 safetensors) | JSON (Hermes) | grammar | tonight | tonight; host fit says 4.8 GB KV at 32k |
| Qwen3.6-35B-A3B `q4_k_m` (MoE) | JSON (Hermes) | grammar | tonight | tonight, with `--moe-cache-experts` |
| Granite-4.2-3B (safetensors) | JSON (Hermes) | grammar | tonight | tonight |
| Mellum2.1 (safetensors, a coding model) | JSON (Hermes) | grammar | tonight | tonight; host fit says 1.1 GB KV at 32k |

**Ruled out:**

- **No tool template in goinfer:** gemma-3, ministral3, phi3, command-r7b, aya-expanse, olmo3, nemotron-h, deepseek-v2-lite,
  mistral-7b v0.1, smollm3 as released, and Seed-Coder-8B (its template is not recognised).
- **granite-4.0-h-tiny,** the `granite/` gguf: no tool template, and CPU-only.
- **LFM2.5:** goinfer constrains it to Hermes JSON, which is not its own pythonic call form, and it runs CPU-only.
- **Llama-3.2-1B:** too small.

`docs/tool-call-coverage.md` in goinfer is stale on gpt-oss, which it lists as having no tools, and on Phi-3. goinfer's
Mac session has been told.

## goinfer's fit endpoint cannot answer the card question

- `POST /admin/models/fit` prices the load against **host** memory. On CUDA, whether a model goes resident is decided
  only by building it (goinfer `docs/server.md`, the fit route).
- It also answers `fits: null` for a `.gguf` whose sidecar is not built yet.
- Asked on 0de92cd9 at `-ctx 32768`, int4 (09:38, `~/tack-runs/e0/sidecars-2026-10-18/fit-endpoint/fit.jsonl` on
  nobara):
  - Qwen3-4B fits host memory with 4.8 GB of KV, and Mellum2.1 with 1.1 GB.
  - The other four are unknown until their sidecars are built.
- The card question is answered only by a load.

## gpt-oss-20b at 32k, by day (09:38–09:40 PDT)

**How it was loaded:** through tack's own path, `tack eval pilot-serve -serve-option moe-cache-experts` (tack
`8fd6c93`), on goinfer-serve `0de92cd9`, holding nobara's timing lock. The record is in
`~/tack-runs/e0/gptoss-2026-10-18/` on nobara.

- **Decode path:** `cuda-resident (int4)`, with a context of 32768.
- **Fit:** "32768-token cap needs 1.5 GB KV (weights 11.0 GB, budget 32.0 GB, …)". The experts stream, so 11 GB of
  weights run on an 8 GB card.
- **VRAM:** 7,303 of 8,192 MiB, unchanged after decoding.
- **`forced_tool_call`:** post-check.
- **Up after:** 53 s.
- **Decode:** a 200-token reply at temperature 0 took 7.92 s with a 2.43 s time to first token cold, then 5.53 s with
  81 ms. That is about 36–37 tokens a second in both.

By the owner's rule, a model that loads resident at 32k takes E0's smoke before any calibration run.

## Tonight on nobara's queue

Six jobs, each a 32k load through `pilot-serve` that first builds the model's CUDA sidecar and records residency, fit
and VRAM (`~/tack-runs/e0/sidecars-2026-10-18/sidecar-load.sh`). Estimated 28 minutes in all; the timeouts total
2 h 20 min.

- Qwen3.5-9B;
- gemma-4 12B;
- Qwen3-4B;
- Qwen3.6-35B-A3B, with `moe-cache-experts`;
- Granite-4.2-3B;
- Mellum2.1.
