package proto

import (
	"reflect"
	"time"
)

// Method names (DESIGN §9.2). Only the methods P0 implements are declared; the rest arrive with their phase.
const (
	MethodInitialize       = "initialize"
	MethodSessionNew       = "session/new"
	MethodSessionSubscribe = "session/subscribe"
	MethodSessionPrompt    = "session/prompt"
	MethodSessionEvent     = "session/event" // notification, daemon to client
	MethodDaemonStatus     = "daemon/status"
	MethodDaemonShutdown   = "daemon/shutdown"
	MethodUsageSummary     = "usage/summary"
)

// SessionID names a session: "s_" and a ULID.
type SessionID string

// ClientInfo identifies the client in initialize.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeParams opens a connection. Token is the daemon's client token, read from client.token in its state directory
// by a client that can read it (an unsandboxed one, DESIGN §6.6). It is checked here, once per connection: one that
// presents it is privileged, one that omits it is not, and a wrong one is refused. No other method carries it.
type InitializeParams struct {
	Client   ClientInfo `json:"client"`
	Protocol int        `json:"protocol"`
	Token    string     `json:"token,omitempty"`
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

// InitializeResult answers initialize. Protocol is the version the daemon will speak on this connection. Privileged
// says the connection presented the client token, so the methods that change permissions, mode, trust or halt state
// are open to it (DESIGN §9.2).
type InitializeResult struct {
	Daemon       DaemonInfo   `json:"daemon"`
	Protocol     int          `json:"protocol"`
	Capabilities Capabilities `json:"capabilities"`
	Privileged   bool         `json:"privileged,omitempty"`
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

// DaemonStatusParams asks for daemon/status; it has no fields.
type DaemonStatusParams struct{}

// DaemonStatusResult is what daemon/status reports (DESIGN §6.1; tack daemon status, tack doctor). Protocol is the
// newest version the daemon speaks (it serves that one and the one before). Sessions counts the sessions it holds,
// Running those with a turn running. Draining is true once a shutdown has begun: no new turns start.
type DaemonStatusResult struct {
	Version  string    `json:"version"`
	Protocol int       `json:"protocol"`
	PID      int       `json:"pid"`
	Started  time.Time `json:"started"`
	Sessions int       `json:"sessions"`
	Running  int       `json:"running"`
	Halted   bool      `json:"halted"`
	Draining bool      `json:"draining"`
}

// DaemonShutdownParams asks the daemon to drain and exit (DESIGN §6.1). It needs a privileged connection.
type DaemonShutdownParams struct{}

// DaemonShutdownResult acknowledges the shutdown; the daemon drains and exits after answering.
type DaemonShutdownResult struct{}

// UsageSummaryParams asks for the usage table (DESIGN §7.7, §9.2): what each day's requests cost, by model server,
// summed from the usage events of every session in the daemon's state directory. Since is the first instant counted;
// nil counts everything. Days are UTC days with UTC set, the days of TZ when it names a zone, and the daemon's local
// days otherwise. Rows are grouped by day and server, the only grouping so far.
type UsageSummaryParams struct {
	Since *time.Time `json:"since,omitempty"`
	UTC   bool       `json:"utc,omitempty"`
	// TZ is the client's time zone, an IANA name ("America/Los_Angeles"), whose days the rows count when UTC is not
	// set; empty means the daemon's own. A name the daemon does not know is refused (invalid params).
	TZ string `json:"tz,omitempty"`
}

// UsageRow is one day's requests to one server (internal/usage.Row). Reused and Prefilled sum only the Known requests,
// the ones whose server reported reuse, and KnownPrompt their prompt tokens; PrefillMS sums the PrefillKnown requests'.
// An unreported figure is never counted as zero (DESIGN principle 6).
type UsageRow struct {
	Day          string `json:"day"` // YYYY-MM-DD
	Server       string `json:"server"`
	Sessions     int    `json:"sessions"`
	Requests     int    `json:"requests"`
	Prompt       int    `json:"prompt"`
	Output       int    `json:"output"`
	Known        int    `json:"known"`
	KnownPrompt  int    `json:"known_prompt"`
	Reused       int    `json:"reused"`
	Prefilled    int    `json:"prefilled"`
	PrefillKnown int    `json:"prefill_known"`
	PrefillMS    int    `json:"prefill_ms"`
	WallMS       int    `json:"wall_ms"`
}

// UsageSummaryResult is the table, sorted by day then server.
type UsageSummaryResult struct {
	Rows []UsageRow `json:"rows"`
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
	MethodDaemonStatus:     {reflect.TypeFor[DaemonStatusParams](), reflect.TypeFor[DaemonStatusResult]()},
	MethodDaemonShutdown:   {reflect.TypeFor[DaemonShutdownParams](), reflect.TypeFor[DaemonShutdownResult]()},
	MethodUsageSummary:     {reflect.TypeFor[UsageSummaryParams](), reflect.TypeFor[UsageSummaryResult]()},
}

// ReuseShare is the share of the known requests' prompt tokens served from the cached prefix, and false when no
// request said.
func (r UsageRow) ReuseShare() (float64, bool) {
	if r.Known == 0 || r.KnownPrompt == 0 {
		return 0, false
	}
	return float64(r.Reused) / float64(r.KnownPrompt), true
}
