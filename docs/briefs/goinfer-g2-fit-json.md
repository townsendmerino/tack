# Brief for goinfer: answer "could this model load now?" as JSON, without loading (tack's G2)

<!-- citations-at: goinfer f0d11c70 -->

**From:** tack (DESIGN §13 G2, P1; sent on the owner's word 2026-10-17).

**For:** goinfer's usual process: task doc or brief, then vscode-claude, then goinfer's gates.

**Order:** goinfer's tool-call parsing fix comes first, because the E1 pilot and E1 wait on it
(`~/tack-runs/e1/NOTE-for-goinfer-2026-10-17-tool-calls-as-text.md`). Then this, G2. Then G6
(`docs/briefs/goinfer-g6-night-queue-stops-tack-server.md`).

## What tack needs

Before its daemon loads a model on the Mac, tack must know whether goinfer's own plan admits the load beside the
memory free right now, and on which decode path it would run (DESIGN §8.1, "Admission": a fit check is the server's
plan, not arithmetic).

Today tack can learn both only by loading:

- it sends `POST /admin/models/load`;
- it reads the banner's `decode path:` and `fit:` lines from stderr, or the load's refusal;
- it unloads if the result is not what it asked for.

On a 16 GB Mac that is a multi-gigabyte load to answer a yes-or-no question. E0 also showed the decode path matters as
much as the fit: phi3 asked for Metal and was served on the CPU after a successful load (tack E0 pre-registration,
amendment 4).

## What goinfer already has

- **The pre-load price.** `fitCheckFor` in `decoder/fitguard.go` prices weights, KV at the effective context, the
  source-file peak and the CUDA build peak against available memory: `need`, `budget`, `fits`. It refuses with
  `*FitDeclineError`, carrying a remedy, and can find a smaller fitting context when the caller did not pin one
  (`smallerFittingContext`).
- **The post-load lines.** The banner prints `decode path:`, `fit: N-token cap needs … GB KV (weights …, budget …,
  … left …)` and `KV plan:` (`internal/serveapp/banner.go`, `kvPlanLine`). The `fit:` budget is priced after the
  weights are resident.
- **The admin socket** serves load, unload, generations, cancel, halt, resume and status (`internal/serveapp/admin.go`).
  None of them answers a fit question without loading.

## The ask

A read-only admin route, on the same admin socket and under the same guard as load, that runs the pre-load pricing and
returns it without loading anything. For example, `POST /admin/models/fit`, with the body load takes plus the flags
that change the answer:

```json
{"path": "/Users/x/models/qwen2.5-7b-instruct-q4_k_m.gguf", "ctx": 16384, "backend": "metal", "kv_sessions": 1}
```

It answers with goinfer's own numbers:

```json
{"fits": true, "ctx": 16384, "ctx_pinned": true,
 "need_bytes": 5600000000, "budget_bytes": 7400000000, "weight_bytes": 4400000000, "kv_bytes": 900000000,
 "decode_path": "metal-resident (int4)", "decline": "", "remedy": ""}
```

- **`fits`, `need_bytes`, `budget_bytes`, `weight_bytes`, `kv_bytes`:** what `fitCheck` computes now, priced against
  the memory available at the moment of the request.
- **`ctx` and `ctx_pinned`:** the context it priced. When the caller did not pin one, also the largest that fits
  (`smallerFittingContext`).
- **`decode_path`:** the path the load would take (`metal-resident (int4)`, `cuda-resident (int4)`, `cpu (int8int8)`),
  or the decline reason when a requested GPU backend would fall back. Where it cannot be known without loading,
  `"unknown"` is better than a guess. tack treats `unknown` as "load to find out".
- **`decline` and `remedy`:** the `FitDeclineError` text when it does not fit.

The names are illustrative. Field names and the route are goinfer's call. tack needs the content and that it is
computed by the same code the load uses, so the two cannot disagree.

## Not asked

- Any change to how goinfer prices a load, or to its refusal policy.
- A fit answer on the public API port. The admin socket is enough, since tack supervises the child.
- Loading behaviour changes. `load` keeps refusing what does not fit, exactly as now.

## How tack will check it

- A goinfer test: the route's answer equals what a following `load` does. `fits: false` means the load is refused
  with the same decline; `fits: true` means the load succeeds on the reported `decode_path`. Run once on a model that
  fits and once on one that does not.
- The route does not change resident memory: the process footprint before and after the call is equal within noise.
- On tack's side, the supervisor asks the route before loading, keeps today's load-and-read-the-banner path when the
  route is missing or says `unknown`, and a cassette of both answers covers it.

**Until it lands,** tack keeps P0's admission: a memory-pressure check before loading, goinfer's own refusal of a load
that does not fit (`models.RefusedError`), and the decode-path check after loading, unloading on a mismatch.
