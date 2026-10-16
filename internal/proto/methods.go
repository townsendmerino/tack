package proto

import "reflect"

// Method names (DESIGN §9.2). Only the methods P0 implements are declared; the rest arrive with their phase.
const (
	MethodInitialize       = "initialize"
	MethodSessionNew       = "session/new"
	MethodSessionSubscribe = "session/subscribe"
	MethodSessionPrompt    = "session/prompt"
	MethodSessionEvent     = "session/event" // notification, daemon to client
)

// SessionID names a session: "s_" and a ULID.
type SessionID string

// ClientInfo identifies the client in initialize.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeParams opens a connection.
type InitializeParams struct {
	Client   ClientInfo `json:"client"`
	Protocol int        `json:"protocol"`
}

// DaemonInfo identifies the daemon in initialize's result.
type DaemonInfo struct {
	Version string `json:"version"`
}

// Capabilities says what this daemon can do. Ken is how ken is reached ("engine" in-process, "" when unavailable);
// Servers names the configured model servers as name@place, e.g. "goinfer@nobara".
type Capabilities struct {
	Ken     string   `json:"ken"`
	Servers []string `json:"servers"`
}

// InitializeResult answers initialize. Protocol is the version the daemon will speak on this connection.
type InitializeResult struct {
	Daemon       DaemonInfo   `json:"daemon"`
	Protocol     int          `json:"protocol"`
	Capabilities Capabilities `json:"capabilities"`
}

// Mode is a session's permission mode (DESIGN §6.6).
type Mode string

const (
	ModeAsk   Mode = "ask"
	ModeEdits Mode = "edits"
	ModePlan  Mode = "plan"
	ModeAuto  Mode = "auto"
)

// SessionNewParams creates a session. Workspace is a path; Model is a role ("main") or a model id.
type SessionNewParams struct {
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Mode      Mode   `json:"mode"`
}

// SessionNewResult names the new session.
type SessionNewResult struct {
	Session SessionID `json:"session"`
}

// SessionSubscribeParams replays a session's persisted events from FromSeq, then streams new ones as session/event
// notifications.
type SessionSubscribeParams struct {
	Session SessionID `json:"session"`
	FromSeq int64     `json:"from_seq"`
}

// SessionSubscribeResult gives Head, the seq of the session's last event when the subscription began: replay covers
// FromSeq through Head, and live events follow it.
type SessionSubscribeResult struct {
	Head int64 `json:"head"`
}

// SessionPromptParams starts a turn with the user's message.
type SessionPromptParams struct {
	Session SessionID      `json:"session"`
	Content []ContentBlock `json:"content"`
}

// SessionPromptResult acknowledges the prompt with the seq of its user.message event; the turn's progress arrives
// as events.
type SessionPromptResult struct {
	Seq int64 `json:"seq"`
}

// SessionEventParams is the payload of the session/event notification.
type SessionEventParams struct {
	Session SessionID `json:"session"`
	Event   Event     `json:"event"`
}

// MethodSpec gives a method's params and result types; Result is nil for a notification.
type MethodSpec struct {
	Params, Result reflect.Type
}

// Methods is every method this build speaks, keyed by name. Dispatch, the fixtures' conformance test and the Swift
// generator all read it, so a method missing here is missing everywhere.
var Methods = map[string]MethodSpec{
	MethodInitialize:       {reflect.TypeFor[InitializeParams](), reflect.TypeFor[InitializeResult]()},
	MethodSessionNew:       {reflect.TypeFor[SessionNewParams](), reflect.TypeFor[SessionNewResult]()},
	MethodSessionSubscribe: {reflect.TypeFor[SessionSubscribeParams](), reflect.TypeFor[SessionSubscribeResult]()},
	MethodSessionPrompt:    {reflect.TypeFor[SessionPromptParams](), reflect.TypeFor[SessionPromptResult]()},
	MethodSessionEvent:     {reflect.TypeFor[SessionEventParams](), nil},
}
