package proto

import (
	"reflect"
	"time"
)

// Method names (DESIGN §9.2). Only the methods P0 implements are declared; the rest arrive with their phase.
const (
	MethodInitialize        = "initialize"
	MethodSessionNew        = "session/new"
	MethodSessionSubscribe  = "session/subscribe"
	MethodSessionPrompt     = "session/prompt"
	MethodSessionCancel     = "session/cancel"
	MethodSessionFork       = "session/fork"
	MethodSessionEvent      = "session/event" // notification, daemon to client
	MethodDaemonStatus      = "daemon/status"
	MethodDaemonShutdown    = "daemon/shutdown"
	MethodUsageSummary      = "usage/summary"
	MethodPermissionRespond = "permission/respond"
	MethodWorkspaceComplete = "workspace/complete"
	MethodWorkspaceTrust    = "workspace/trust"
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

// Strictness orders the modes by how much each lets a session do without asking: plan 0, ask 1, edits 2, auto 3. A mode that is
// not one has -1.
func (m Mode) Strictness() int {
	switch m {
	case ModePlan:
		return 0
	case ModeAsk:
		return 1
	case ModeEdits:
		return 2
	case ModeAuto:
		return 3
	}
	return -1
}

// Valid reports whether m is one of the four modes.
func (m Mode) Valid() bool { return m.Strictness() >= 0 }

// Stricter is the stricter of two modes: the one that lets a session do less without asking. A mode that is not valid loses
// to a valid one, so a bad value never raises anything.
func Stricter(a, b Mode) Mode {
	switch {
	case !a.Valid():
		return b
	case !b.Valid():
		return a
	case b.Strictness() < a.Strictness():
		return b
	}
	return a
}

// SessionNewParams creates a session. Workspace is a path; Model is a role ("main") or a model id. A session in auto mode is
// refused unless Confirmed says the owner chose auto knowingly: by the -mode flag, his own config.toml, or an edit task he
// started. A client sets it only on those paths, never because a repository, a file or a model said so. It is a marker for the
// daemon to hold the client to, not a defence against one that holds the client token: that token is the boundary (DESIGN §6.6).
type SessionNewParams struct {
	Workspace string `json:"workspace"`
	Model     string `json:"model"`
	Mode      Mode   `json:"mode"`
	Confirmed bool   `json:"confirmed,omitempty"`
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

// SessionCancelParams ends the session's running turn (docs/design/proposal-tui-protocol.md §2). It needs the client
// token. A command the turn is running is killed, a pending permission question is denied by the user with the reason
// "cancelled", and the turn ends with turn.end cancelled. With no turn running it does nothing and still succeeds.
type SessionCancelParams struct {
	Session SessionID `json:"session"`
}

// SessionCancelResult acknowledges a cancel; the events it causes are its record.
type SessionCancelResult struct{}

// SessionForkParams makes a new session from the first AtSeq events of Session (DESIGN §6.2). It needs the client token. AtSeq
// must leave the history whole: a seq the log has, and not inside a turn (after a turn.end, or at 1 for the start). The parent
// is unchanged and may be running.
type SessionForkParams struct {
	Session SessionID `json:"session"`
	AtSeq   int64     `json:"at_seq"`
}

// SessionForkResult names the new session. It has the parent's workspace, model and mode and no events of its own yet; a client
// subscribes to it and prompts it like any session.
type SessionForkResult struct {
	Session SessionID `json:"session"`
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

// PermissionRespondParams answers a permission.requested question (DESIGN §9.2; docs/design/proposal-tui-protocol.md).
// Seq is the question's permission.requested event's seq and ID its PermissionRequested.ID (the tool call's id): the
// seq names the question, since a call can be asked again under the same id. Decision is allow or deny; Scope is once (or
// empty) or session, which extends an allow to the rest of the session as a rule. Note, on a deny, reaches the model
// in the refused result; it is content and grants nothing. Rule is reserved for an edited rule and refused until it
// is built. It needs a privileged connection; the first answer wins, and another gets CodeNotPending.
type PermissionRespondParams struct {
	Session  SessionID `json:"session"`
	ID       string    `json:"id"`
	Seq      int64     `json:"seq"`
	Decision string    `json:"decision"`
	Scope    string    `json:"scope,omitempty"`
	Note     string    `json:"note,omitempty"`
	Rule     string    `json:"rule,omitempty"`
}

// PermissionRespondResult acknowledges an answer; the permission.resolved event that follows is its record.
type PermissionRespondResult struct{}

// WorkspaceCompleteParams asks for the paths of the session's workspace that Query matches, for `@` completion
// (docs/design/proposal-tui-protocol.md §3). Query is what follows the @, as typed. Limit defaults to 20 and is at most
// 50. It needs the client token.
type WorkspaceCompleteParams struct {
	Session SessionID `json:"session"`
	Query   string    `json:"query"`
	Limit   int       `json:"limit,omitempty"`
}

// CompletionItem is one path offered: workspace-relative with forward slashes; Kind is "file" or "dir".
type CompletionItem struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// WorkspaceCompleteResult lists the matches, best first. Paths the workspace's .gitignore leaves out are not offered; a
// read still reaches them by name.
type WorkspaceCompleteResult struct {
	Items []CompletionItem `json:"items"`
}

// WorkspaceTrustParams trusts the repository at Root: its .tack/config.toml, as it is now, takes effect in sessions started
// afterwards (DESIGN §6.6). It needs the client token. The file is checked first; one that does not parse is refused.
type WorkspaceTrustParams struct {
	Root string `json:"root"`
}

// WorkspaceTrustResult says what was trusted: Root as the grant is keyed (absolute, links resolved), Trusted what the
// repository has that the grant covers ("config", or none), and Config the digest of the configuration file's content.
type WorkspaceTrustResult struct {
	Root    string   `json:"root"`
	Trusted []string `json:"trusted"`
	Config  string   `json:"config,omitempty"`
}

// MethodSpec gives a method's params and result types; Result is nil for a notification.
type MethodSpec struct {
	Params, Result reflect.Type
}

// Methods is every method this build speaks, keyed by name. Dispatch, the fixtures' conformance test and the Swift
// generator all read it, so a method missing here is missing everywhere.
var Methods = map[string]MethodSpec{
	MethodInitialize:        {reflect.TypeFor[InitializeParams](), reflect.TypeFor[InitializeResult]()},
	MethodSessionNew:        {reflect.TypeFor[SessionNewParams](), reflect.TypeFor[SessionNewResult]()},
	MethodSessionSubscribe:  {reflect.TypeFor[SessionSubscribeParams](), reflect.TypeFor[SessionSubscribeResult]()},
	MethodSessionPrompt:     {reflect.TypeFor[SessionPromptParams](), reflect.TypeFor[SessionPromptResult]()},
	MethodSessionCancel:     {reflect.TypeFor[SessionCancelParams](), reflect.TypeFor[SessionCancelResult]()},
	MethodSessionFork:       {reflect.TypeFor[SessionForkParams](), reflect.TypeFor[SessionForkResult]()},
	MethodSessionEvent:      {reflect.TypeFor[SessionEventParams](), nil},
	MethodDaemonStatus:      {reflect.TypeFor[DaemonStatusParams](), reflect.TypeFor[DaemonStatusResult]()},
	MethodDaemonShutdown:    {reflect.TypeFor[DaemonShutdownParams](), reflect.TypeFor[DaemonShutdownResult]()},
	MethodUsageSummary:      {reflect.TypeFor[UsageSummaryParams](), reflect.TypeFor[UsageSummaryResult]()},
	MethodPermissionRespond: {reflect.TypeFor[PermissionRespondParams](), reflect.TypeFor[PermissionRespondResult]()},
	MethodWorkspaceComplete: {reflect.TypeFor[WorkspaceCompleteParams](), reflect.TypeFor[WorkspaceCompleteResult]()},
	MethodWorkspaceTrust:    {reflect.TypeFor[WorkspaceTrustParams](), reflect.TypeFor[WorkspaceTrustResult]()},
}

// ReuseShare is the share of the known requests' prompt tokens served from the cached prefix, and false when no
// request said.
func (r UsageRow) ReuseShare() (float64, bool) {
	if r.Known == 0 || r.KnownPrompt == 0 {
		return 0, false
	}
	return float64(r.Reused) / float64(r.KnownPrompt), true
}
