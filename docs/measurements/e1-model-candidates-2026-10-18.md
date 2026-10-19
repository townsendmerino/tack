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

## Next, if gemma-4 E4B and gpt-oss-20b both fall short (owner, 2026-10-18)

**Candidate:** a Qwen3 8B-class chatml model, for example Qwen3-8B at `Q4_K_M` (about 5 GB).

- **Why it:** chatml has a JSON call wrapper, so its calls are grammar-constrained (`forced_tool_call` "grammar"), and
  tack's post-check resend never applies.
- **What it needs:** nobara has no Qwen3-8B; the nearest file is Qwen3.8-27B, far too big. So it needs a download, on
  the owner's word.
- **Then:** the same steps as C1. A 32k fit load (it builds its sidecar), E0's smoke at the bar, then its own
  calibration amendment.

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

## The night jobs, and the by-day fit checks after them

**The queue ran 12:45–12:58 PDT.** It was started by day; the records are in
`~/goinfer-logs/night/runs/2026-10-18/<job>.log` and `~/tack-runs/e0/sidecars-2026-10-18/<name>/` on nobara.

- **Every job built its CUDA sidecar.** The Qwen3-8B download landed: `Qwen3-8B-Q4_K_M.gguf`, sha256 `d98cdcbd…`.
- **Two came up `cuda-resident` at 32k:**
  - Qwen3.5-9B: 2.0 GB of KV beside 4.7 GB of weights, 7.2 GB of VRAM.
  - Qwen3.6-35B-A3B with its experts streamed: 1.2 GB of KV beside 18.2 GB of weights, 7.3 GB of VRAM. It took 176 s to
    come up.
- **Five ended rc=1.** Each did so correctly: pilot-serve voids a load that is not resident. goinfer declined the CUDA
  resident at 32k because the KV, plus its 384 MB reserve, did not fit beside the weights:

| model | KV at 32k | free beside the weights | KV per position |
|---|---|---|---|
| gemma-4 12B | 2.08 GB | 0.37 GB | 62.0 KB |
| Qwen3-4B | 4.83 GB | 5.13 GB | 144.0 KB |
| Granite-4.2-3B | 5.37 GB | 5.53 GB | 160.0 KB |
| Mellum2.1 | 1.07 GB | 0.68 GB | 31.9 KB |
| Qwen3-8B | 4.83 GB | 3.17 GB | 144.0 KB |

**The fix: context sized to the card, with tack's 16k design window as the floor.** Each context was then checked by a
load by day, through pilot-serve with goinfer `0de92cd9`, holding nobara's lock, 13:57–13:58 PDT:

| model | context | result |
|---|---|---|
| Qwen3-4B | 30,720 | `cuda-resident (int4)`: 4.2 GB of KV beside 2.1 GB of weights, 7.3 GB of VRAM |
| Granite-4.2-3B | 30,720 | `cuda-resident (int4)`: 4.7 GB of KV beside 1.9 GB of weights, 7.4 GB of VRAM |
| Qwen3-8B | 16,384 | `cuda-resident (int4)`: 2.2 GB of KV beside 4.3 GB of weights, 7.2 GB of VRAM |
| Mellum2.1 | not loaded | ~~out: about 8,500 positions fit, under the 16k floor~~ withdrawn, see the correction below |
| gemma-4 12B | not loaded | out: nothing fits; the free space is under goinfer's reserve |

**Correction, 2026-10-19 (goinfer, relayed by the owner): Mellum2.1 is not out.**
- Mellum2.1 is a mixture-of-experts model (12B total, 2.5B active). Its night job loaded it in 17 s, but without
  `moe-cache-experts`, so every expert's weights had to sit on the card.
- goinfer declined the resident build at 32k on that basis, and the load fell back to CPU.
- "About 8,500 positions fit" was computed from that all-resident load, so it does not describe Mellum2.1 with its
  experts streamed, the way Qwen3.6-35B-A3B came up resident at 32k.

**Mellum2.1 is a candidate after C4 and C5** (owner):
- **Fit load first,** with `-serve-option moe-cache-experts` on goinfer `b2b5e816` or later, at 32k.
- **Its prefill is slow today.** goinfer measures its cold prefill at about 97 tok/s and may batch its MoE prefill
  (2–4× expected). A 10k-token first prompt at today's rate is about 100 s.
- **It thinks by default, so its amendment states the thinking setting explicitly** (§3 of the calibration
  pre-registration). A1 forces every call (`tool_choice: any`), and goinfer renders such requests with thinking off.
  So the amendment must say off, and say how that is enforced, not leave it to the template
  (`e1-calibration-thinking-arm-draft-2026-10-19.md` §1).
- **E0's smoke comes before any calibration amendment,** as for every model in this record.

**Nothing was re-queued.** The sidecars are built, and the by-day loads answered the one remaining question.

**Each model that is resident at its context needs E0's smoke before any calibration amendment.** That is Qwen3.5-9B
and Qwen3.6-35B-A3B at 32k, Qwen3-4B and Granite-4.2-3B at 30,720, and Qwen3-8B at 16,384.

A context under 32k would also change the calibration's registered window (32,768), so such a model needs that in its
own amendment.
