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
	EvSessionCreated      EventType = "session.created"
	EvUserMessage         EventType = "user.message"
	EvContextAssembled    EventType = "context.assembled"
	EvRequestSent         EventType = "request.sent"
	EvAssistantDelta      EventType = "assistant.delta"
	EvAssistantMessage    EventType = "assistant.message"
	EvToolCall            EventType = "tool.call"
	EvPermissionRequested EventType = "permission.requested"
	EvPermissionResolved  EventType = "permission.resolved"
	EvToolStarted         EventType = "tool.started"
	EvToolFinished        EventType = "tool.finished"
	EvUsage               EventType = "usage"
	EvBudgetExceeded      EventType = "budget.exceeded"
	EvTurnEnd             EventType = "turn.end"
	EvError               EventType = "error"
	EvDaemonHalted        EventType = "daemon.halted"
	EvDaemonHaltRefused   EventType = "daemon.halt_refused"
	EvCompaction          EventType = "compaction"
	EvCheckpoint          EventType = "checkpoint"
	EvRewind              EventType = "rewind"
	EvModelStatus         EventType = "model.status"
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
	EvModelStatus:         {reflect.TypeFor[ModelStatus](), false, false},  // live only, for the status line
	EvSessionCreated:      {reflect.TypeFor[SessionCreated](), true, true}, // the first event of every log
	EvUserMessage:         {reflect.TypeFor[UserMessage](), true, true},    // opens a turn
	EvContextAssembled:    {reflect.TypeFor[ContextAssembled](), true, false},
	EvRequestSent:         {reflect.TypeFor[RequestSent](), true, false},
	EvAssistantDelta:      {reflect.TypeFor[AssistantDelta](), false, false},
	EvAssistantMessage:    {reflect.TypeFor[AssistantMessage](), true, false},
	EvToolCall:            {reflect.TypeFor[ToolCall](), true, false},
	EvPermissionRequested: {reflect.TypeFor[PermissionRequested](), true, false},
	EvPermissionResolved:  {reflect.TypeFor[PermissionResolved](), true, false},
	EvToolStarted:         {reflect.TypeFor[ToolStarted](), true, true},
	EvToolFinished:        {reflect.TypeFor[ToolFinished](), true, false},
	EvUsage:               {reflect.TypeFor[Usage](), true, false},
	EvBudgetExceeded:      {reflect.TypeFor[BudgetExceeded](), true, false},
	EvTurnEnd:             {reflect.TypeFor[TurnEnd](), true, true},
	EvError:               {reflect.TypeFor[ErrorEvent](), true, false},
	EvDaemonHalted:        {reflect.TypeFor[DaemonHalted](), true, false},
	EvDaemonHaltRefused:   {reflect.TypeFor[DaemonHaltRefused](), true, false},
	EvCompaction:          {reflect.TypeFor[Compaction](), true, false},
	EvCheckpoint:          {reflect.TypeFor[Checkpoint](), true, false},
	EvRewind:              {reflect.TypeFor[Rewind](), true, false},
}

// ModelStatus is how a session's model server stands while a turn waits on it (DESIGN §8.2, §10), live only, for the
// status line: State "loading", with Message such as "loading model on nobara", while it starts and loads, and
// "moved", with Message such as "memory pressure is critical on this machine; using nobara", when a turn leaves this
// machine's model for another server (DESIGN §8.1), and "ready" once it serves.
type ModelStatus struct {
	Server  string `json:"server"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

// SessionCreated is the first event of every session's log: what the session was made with, so a daemon that did not
// make it can reopen it from the log alone (DESIGN §6.2). Workspace is the root as resolved (absolute, symlinks
// followed); Model is what session/new named, a role or a model id.
type SessionCreated struct {
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Mode      Mode   `json:"mode"`
	// Parent is the session whose task call made this one, a subagent (DESIGN §6.2, §6.3); absent for a session the user
	// made.
	Parent SessionID `json:"parent,omitempty"`
	// ParentCall is the tool_use id of the parent's task call that made this session, so crash recovery charges a cut-off
	// call with its own child (core's orphanCost); absent for a session the user made, and in a child logged before it.
	ParentCall string `json:"parent_call,omitempty"`
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

// RequestSent records a request to the model server. Request counts the conversation's requests from 1; it is 0 for a
// request made on the session's behalf outside the conversation (compaction's summarizer), which no request number
// counts but whose usage the budgets do. A summarizer request that fails leaves its request.sent with no usage after
// it.
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

// PermissionRequested records a tool call that waits for the user (DESIGN §6.6). For a shell command, Command is the
// line as the model wrote it and Parsed each simple command as approval saw it, its program resolved to an absolute
// path. Reasons says why it asks, one line per cause.
type PermissionRequested struct {
	ID      string   `json:"id"`
	Tool    string   `json:"tool"`
	Mode    Mode     `json:"mode"`
	Command string   `json:"command,omitempty"`
	Parsed  []string `json:"parsed,omitempty"`
	Reasons []string `json:"reasons"`
}

// Permission decisions, and who made them.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"

	DecidedByMode   = "mode"    // the session's mode
	DecidedByRule   = "rule"    // an allow rule covered every command
	DecidedByUser   = "user"    // the user answered
	DecidedByNoUser = "no_user" // it needed the user, and no one was attached
	DecidedByHalted = "halted"  // a halt or a shutdown ended the question before it was answered
)

// PermissionResolved records the decision on a tool call that changes something: every such call gets one, whether a
// mode, a rule or the user decided (DESIGN §6.6, "Every permission decision is in the event log"). Scope is how far a
// user's allow reaches: once, or the session, which adds RuleAdded for the rest of it. Reasons repeats why, for a deny;
// Note is the user's own words on a deny (permission/respond), which the refused result also carries to the model.
type PermissionResolved struct {
	ID        string   `json:"id"`
	Decision  string   `json:"decision"`
	By        string   `json:"by"`
	Scope     string   `json:"scope,omitempty"`
	RuleAdded string   `json:"rule_added,omitempty"`
	Reasons   []string `json:"reasons,omitempty"`
	Note      string   `json:"note,omitempty"`
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
	// Redacted names the kinds of credential the redaction pass replaced in the output before anyone saw it
	// (perms.Redact): the log of it firing.
	Redacted []string `json:"redacted,omitempty"`
	// Tokens is what the server computed for requests made on the call's behalf, outside this session's own requests: a
	// task's child session (DESIGN §6.3). It counts toward this session's budgets, here and on a resume; the child's own
	// usage events are in its log, so the usage table counts it once.
	Tokens int `json:"tokens,omitempty"`
	// Calls is the tool calls run on the call's behalf (a task's child), counted toward this session's tool-call budgets
	// as Tokens is toward its token budgets.
	Calls int `json:"calls,omitempty"`
}

// Usage is a request's measured cost. A nil field is one the server did not report; it is never shown as zero
// (DESIGN principle 6, "measured or labelled"). Request is its request.sent's, 0 for the summarizer's.
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
	// Per turn: "requests", "wall_ms", "tokens", "tool_calls", "consecutive_errors"; per session: "session_tokens",
	// "session_tool_calls".
	Budget string  `json:"budget"`
	Limit  float64 `json:"limit"`
	Value  float64 `json:"value"`
}

// Stop reasons for turn.end.
const (
	StopEndTurn   = "end_turn"   // the model finished
	StopMaxTokens = "max_tokens" // the model's output hit max_tokens
	StopBudget    = "budget"     // a budget tripped
	StopCancelled = "cancelled"
	StopHalted    = "halted" // tack was halted (daemon.halted precedes it)
	StopDone      = "done"   // an edit task called the done tool (loop.Config.EditTask)
	// StopWindowFull: the prompt filled the context window, so the reply was cut off short of its own token limit.
	StopWindowFull = "window_full"
	// StopRepeatLoop: one call repeated with an unchanged result until the loop's repeat rule ended the turn.
	StopRepeatLoop = "repeat_loop"
	// StopInterrupted: tack stopped inside the turn (a crash or a kill); recorded when the session is next opened (DESIGN §6.1).
	StopInterrupted = "interrupted"
	// StopShutdown: the daemon shut down between the turn's steps (DESIGN §6.1, drain); the session can be continued.
	StopShutdown = "shutdown"
	StopError    = "error"
)

// TurnEnd closes a turn. The store fsyncs after it.
type TurnEnd struct {
	StopReason string `json:"stop_reason"`
}

// ErrorEvent reports an error during a turn. Retrying says whether the loop will try again (e.g. after a 529 from a
// full goinfer queue).
type ErrorEvent struct {
	Class    string `json:"class"` // "server", "protocol", "tool", "internal", "no_tool_call" (a resent request)
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

// Halt triggers.
const (
	HaltByCommand = "command" // tack halt, or the app's Stop All
	HaltByFile    = "file"    // a HALT file a script created
)

// DaemonHalted is recorded in a session's log when tack's halt stops its running turn (DESIGN §6.6). Turn.end with
// stop reason halted follows it.
type DaemonHalted struct {
	Trigger string `json:"trigger"`          // HaltByCommand or HaltByFile
	By      string `json:"by,omitempty"`     // the client that halted, for a command
	Reason  string `json:"reason,omitempty"` // what the owner gave, if anything
}

// DaemonHaltRefused is recorded in a session's log when a halt's sweep of the session's sandbox kills nothing because
// its selection looked wrong (DESIGN §6.6): a process carrying the session's mark started before the session did, or
// the selection was larger than one sweep may kill. Processes lists the suspect processes in the first case and the
// selection in the second.
type DaemonHaltRefused struct {
	Reason    string        `json:"reason"`
	Processes []HaltProcess `json:"processes"`
}

// HaltProcess is one process a sweep considered.
type HaltProcess struct {
	PID   int       `json:"pid"`
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
}

// Checkpoint records the snapshot of the working tree taken before a bash call (DESIGN §6.8), or why there is none, in which
// case a client says the command can't be rewound.
type Checkpoint struct {
	ID          string `json:"id"`        // the tool call
	Workspace   string `json:"workspace"` // the tree it holds
	Snapshot    string `json:"snapshot,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

// Rewind records files restored from a checkpoint: To is that checkpoint's seq, Snapshot its id, and Undo the snapshot of
// the tree the rewind replaced, which a later rewind can go back to, or NoUndo why there is none. Changed is how many paths
// it touched.
type Rewind struct {
	To        int64  `json:"to"`
	Workspace string `json:"workspace"`
	Snapshot  string `json:"snapshot"`
	Undo      string `json:"undo,omitempty"`
	NoUndo    string `json:"no_undo,omitempty"`
	Changed   int    `json:"changed"`
}

// Compaction records a rewrite of the history before request Request (DESIGN §7.6), the one place history changes
// between appends. Trigger says what started it ("window": the last prompt passed a share of the served window).
// Prompt and Window are the last prompt's tokens and the served window when it ran. Masked is each tool result
// replaced, with the text that replaced it, so replaying the log rebuilds the request. Before and After are the replaced
// results' total bytes.
//
// A summarizing compaction (DESIGN §7.6, the summarizing step) also replaces the oldest messages with one user message,
// Summary, word for word as the model is sent it. Summarized is the seq of every event the summary covers, in order;
// the messages kept start at the first assistant.message after the last of them. Both are absent from a compaction
// that only masks.
type Compaction struct {
	Request    int            `json:"request"`
	Trigger    string         `json:"trigger"`
	Prompt     int            `json:"prompt"`
	Window     int            `json:"window"`
	Masked     []MaskedResult `json:"masked"`
	Before     int            `json:"before"`
	After      int            `json:"after"`
	Summary    string         `json:"summary,omitempty"`
	Summarized []int64        `json:"summarized,omitempty"`
	// Body is the summary's body (the summarizer's text under its fixed headings, "" for a fallback with none): the next
	// summary's previous body, recorded rather than parsed back out of Summary, which holds the user's own text first.
	Body string `json:"body,omitempty"`
}

// MaskedResult is one tool result a compaction replaced: the call's ID and the stub sent in its place.
type MaskedResult struct {
	ID   string `json:"id"`
	Stub string `json:"stub"`
}
