# Decisions

Architecture decisions, one entry each: the decision, the alternatives considered, and the consequences. The owner's
decisions from the design review are in `DESIGN.md` §1 (D1–D13); new ones are recorded here as `ADR-NNN` once work
starts.

## ADR-001 — Swift types come from tack's own small generator, not goduct

**Status:** decided 2026-10-16 (P0; DESIGN §9.3 left it open).

**Question.** Can goduct's intermediate representation take a Swift emitter, or does tack write its own generator?

**What goduct is** (read at goduct `4c5c458`, v0.6.1): a Go-handlers-to-TypeScript generator. Its IR
(`internal/ir/ir.go:API`) is built around HTTP routes: `Route` with method, path, path/query/header params, body and
response types; `TypeDef` for structs, string enums and aliases. The analyzer (`internal/analyzer/analyze.go:Analyze`)
loads packages with `golang.org/x/tools/go/packages` and discovers types only by walking out from functions annotated
`goduct:route`.

**Why not goduct.**

- The IR is in an `internal/` package, so tack cannot import it. Using it means changing goduct to export the IR and
  analyzer, in goduct's repo, on its schedule.
- tack's protocol has no HTTP routes. It is JSON-RPC methods plus an event stream. goduct would need a second way to
  seed type discovery (a method table), which it does not have.
- tack's events are a tagged union: `Event.Payload`'s shape is fixed by `Event.Type`. goduct's IR has no discriminated
  union (`KindUnion` is a generic-constraint union), and the Swift side needs one (`enum EventPayload`).
- It would bring `golang.org/x/tools` into tack's module for a job reflection does in about 300 lines.

**Decision.** `internal/proto/swiftgen` reflects over `proto.Methods` and `proto.EventTypes`, which are the same tables
dispatch and the fixture conformance test use. Reflection sees exactly what `encoding/json` sees: tag names, `omitempty`
and pointers. `go run ./tools/protogen` writes `apps/macos/Sources/TackProto/Proto.generated.swift`. A Go test fails
while that file is stale, and on macOS CI a Swift check decodes and re-encodes every fixture event.

**Consequences.** Doc comments do not carry into Swift; `go/doc` can add them when the app wants them. Timestamps are
strings on the Swift side for now. JSON Schema, and a C++ emitter for the Qt app (DESIGN §18), would be further
emitters over the same walk. If goduct later exports its IR and gains unions, revisit.

## ADR-002 — `assistant.message` keeps the model's raw content beside the rendered blocks

**Status:** proposed 2026-10-16, for the owner's review (P0).

DESIGN §9.3 and Appendix B have `assistant.message` carry rendered blocks (paragraph, heading, code with highlight
spans). Rendered blocks cannot rebuild the request history byte for byte. The next request must resend exactly what
the model produced, or goinfer's cached prefix is lost (§7.2) and "the log is the truth" (principle 4) fails for the
model's view.

**Decision.** `assistant.message` carries `content`: the model's blocks (text, thinking with signature, tool_use) exactly
as the server returned them, which is what the loop sends back. Rendered `blocks` for clients are added beside it when
`internal/render` lands, and never replace it. `tool.finished` likewise carries `result`, the exact text the model
received, with the full output in the blob store.
