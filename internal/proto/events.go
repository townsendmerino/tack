package proto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// Event is one entry in a session's log (DESIGN §6.2, Appendix B). Seq is dense over the persisted events of a session,
// from 1; a live-only event (EventSpec.Persisted false) is not in the log and carries seq 0. Cause is the seq this
// event answers, when it answers one. Payload's shape is fixed by Type (EventTypes).
type Event struct {
	Seq     int64           `json:"seq"`
	Time    time.Time       `json:"time"`
	Type    EventType       `json:"type"`
	Cause   int64           `json:"cause,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// EventType names an event's kind.
type EventType string

// The event types P0's loop emits. The rest of Appendix B arrives with the features that emit them.
const (
	EvUserMessage      EventType = "user.message"
	EvContextAssembled EventType = "context.assembled"
	EvRequestSent      EventType = "request.sent"
	EvAssistantDelta   EventType = "assistant.delta"
	EvAssistantMessage EventType = "assistant.message"
	EvToolCall         EventType = "tool.call"
	EvToolStarted      EventType = "tool.started"
	EvToolFinished     EventType = "tool.finished"
	EvUsage            EventType = "usage"
	EvBudgetExceeded   EventType = "budget.exceeded"
	EvTurnEnd          EventType = "turn.end"
	EvError            EventType = "error"
)

// EventSpec describes an event type: its payload, whether the log persists it (live-only events are streamed and
// dropped), and whether the store fsyncs after appending it (DESIGN §6.2: before every tool starts, and at turn
// boundaries).
type EventSpec struct {
	Payload   reflect.Type
	Persisted bool
	Fsync     bool
}

// EventTypes is every event type this build emits. The store, the fixtures' conformance test and the Swift generator
// read it.
var EventTypes = map[EventType]EventSpec{
	EvUserMessage:      {reflect.TypeFor[UserMessage](), true, true}, // opens a turn
	EvContextAssembled: {reflect.TypeFor[ContextAssembled](), true, false},
	EvRequestSent:      {reflect.TypeFor[RequestSent](), true, false},
	EvAssistantDelta:   {reflect.TypeFor[AssistantDelta](), false, false},
	EvAssistantMessage: {reflect.TypeFor[AssistantMessage](), true, false},
	EvToolCall:         {reflect.TypeFor[ToolCall](), true, false},
	EvToolStarted:      {reflect.TypeFor[ToolStarted](), true, true},
	EvToolFinished:     {reflect.TypeFor[ToolFinished](), true, false},
	EvUsage:            {reflect.TypeFor[Usage](), true, false},
	EvBudgetExceeded:   {reflect.TypeFor[BudgetExceeded](), true, false},
	EvTurnEnd:          {reflect.TypeFor[TurnEnd](), true, true},
	EvError:            {reflect.TypeFor[ErrorEvent](), true, false},
}

// UserMessage is the user's message that starts a turn.
type UserMessage struct {
	Content []ContentBlock `json:"content"`
}

// ContextAssembled records a request's composition before it is sent: tokens by ledger kind (DESIGN §7.1) and their
// total. Exact is true when the counts came from the server's own tokenizer (goinfer's count_tokens), false for
// estimates.
type ContextAssembled struct {
	Tokens map[string]int `json:"tokens"`
	Total  int            `json:"total"`
	Exact  bool           `json:"exact"`
}

// RequestSent records a request to the model server. Request counts the session's requests from 1.
type RequestSent struct {
	Request int    `json:"request"`
	Server  string `json:"server"`
	Model   string `json:"model"`
}

// AssistantDelta is raw streamed text for the open block (live only). Index is the block's index in the response;
// Kind is "text" or "thinking".
type AssistantDelta struct {
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
}

// AssistantMessage is the model's finished response: its raw content blocks, exactly as received, which is what goes
// back to the model. Rendered blocks for clients (DESIGN §9.3) are derived from it, never stored here (Appendix B).
type AssistantMessage struct {
	Request    int          `json:"request"`
	Content    []ModelBlock `json:"content"`
	StopReason string       `json:"stop_reason"`
}

// ToolCall is one tool call the model made. ID is the server's tool_use id.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolStarted records that a tool call began executing. The store fsyncs after it, so a crash leaves a call either
// never started or started with outcome unknown (DESIGN §6.1).
type ToolStarted struct {
	ID string `json:"id"`
}

// Tool outcomes. Every call gets a result, and every result other than ok is an error result to the model
// (DESIGN §6.3).
const (
	OutcomeOK        = "ok"
	OutcomeError     = "error"     // the tool ran and failed, or its input was invalid
	OutcomeDenied    = "denied"    // the permission engine refused it
	OutcomeCancelled = "cancelled" // the user cancelled, or a halt or budget stopped it
	OutcomeUnknown   = "unknown"   // started before a crash; never re-run automatically
)

// ToolFinished is a tool call's result. Result is the exact text the model receives (truncated for the window); when
// Truncated, the full output is in the blob store under Blob.
type ToolFinished struct {
	ID        string `json:"id"`
	Outcome   string `json:"outcome"`
	Result    string `json:"result"`
	Truncated bool   `json:"truncated"`
	Blob      string `json:"blob,omitempty"`
}

// Usage is a request's measured cost. A nil field is one the server did not report; it is never shown as zero
// (DESIGN principle 6, "measured or labelled").
type Usage struct {
	Request   int      `json:"request"`
	Server    string   `json:"server"`
	Prompt    int      `json:"prompt"`
	Reused    *int     `json:"reused,omitempty"`
	Prefilled *int     `json:"prefilled,omitempty"`
	Output    int      `json:"output"`
	PrefillMS *int     `json:"prefill_ms,omitempty"`
	DecodeTPS *float64 `json:"decode_tps,omitempty"`
	WallMS    int      `json:"wall_ms"`
}

// BudgetExceeded ends a turn because a budget tripped (DESIGN §6.3).
type BudgetExceeded struct {
	Budget string  `json:"budget"` // "tokens", "wall_ms", "tool_calls", "consecutive_errors", "requests"
	Limit  float64 `json:"limit"`
	Value  float64 `json:"value"`
}

// Stop reasons for turn.end.
const (
	StopEndTurn   = "end_turn"   // the model finished
	StopMaxTokens = "max_tokens" // the model's output hit max_tokens
	StopBudget    = "budget"     // a budget tripped
	StopCancelled = "cancelled"
	StopError     = "error"
)

// TurnEnd closes a turn. The store fsyncs after it.
type TurnEnd struct {
	StopReason string `json:"stop_reason"`
}

// ErrorEvent reports an error during a turn. Retrying says whether the loop will try again (e.g. after a 529 from a
// full goinfer queue).
type ErrorEvent struct {
	Class    string `json:"class"` // "server", "protocol", "tool", "internal"
	Message  string `json:"message"`
	Retrying bool   `json:"retrying"`
}

// NewEvent builds an event, encoding payload, which must be the registered payload type for typ.
func NewEvent(seq int64, at time.Time, typ EventType, cause int64, payload any) (Event, error) {
	spec, ok := EventTypes[typ]
	if !ok {
		return Event{}, fmt.Errorf("proto: unknown event type %q", typ)
	}
	if got := reflect.TypeOf(payload); got != spec.Payload {
		return Event{}, fmt.Errorf("proto: %s payload is %v, want %v", typ, got, spec.Payload)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{Seq: seq, Time: at.UTC(), Type: typ, Cause: cause, Payload: b}, nil
}

// Decode returns the event's payload as its registered type (a value, not a pointer). Unknown fields are an error:
// both sides of the protocol are generated from the same types, so an unknown field is a version skew to surface.
func (e Event) Decode() (any, error) {
	spec, ok := EventTypes[e.Type]
	if !ok {
		return nil, fmt.Errorf("proto: unknown event type %q", e.Type)
	}
	v := reflect.New(spec.Payload)
	dec := json.NewDecoder(bytes.NewReader(e.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v.Interface()); err != nil {
		return nil, fmt.Errorf("proto: %s payload: %w", e.Type, err)
	}
	return v.Elem().Interface(), nil
}
