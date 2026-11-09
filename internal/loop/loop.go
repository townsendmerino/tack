package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/shell"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

// Budget bounds each turn and the whole session (DESIGN §6.3). A trip ends the turn with budget.exceeded; a session
// budget, once tripped, ends every later turn before its first request. Every limit must be positive.
//
// Tokens are the ones the server computed: the prefilled share of the prompt plus the output, or the whole prompt
// plus the output when the server does not report reuse.
type Budget struct {
	MaxRequests  int
	MaxToolCalls int
	MaxWall      time.Duration
	MaxTokens    int // output tokens per request
	TurnTokens   int
	// MaxErrors caps tool calls in a row that end in an error or a denial; a call that succeeds resets the count.
	MaxErrors int
	// SessionTokens and SessionToolCalls count from when the loop is made.
	SessionTokens    int
	SessionToolCalls int
}

func (b Budget) check() error {
	for name, v := range map[string]int64{"MaxRequests": int64(b.MaxRequests), "MaxToolCalls": int64(b.MaxToolCalls),
		"MaxWall": int64(b.MaxWall), "MaxTokens": int64(b.MaxTokens), "TurnTokens": int64(b.TurnTokens),
		"MaxErrors": int64(b.MaxErrors), "SessionTokens": int64(b.SessionTokens), "SessionToolCalls": int64(b.SessionToolCalls)} {
		if v <= 0 {
			return fmt.Errorf("loop: budget %s is %d; every budget must be positive", name, v)
		}
	}
	return nil
}

// Sections is an instruction file read by section (window.Instructions): ForCall is the text call id brings in, ""
// for none; Masked says a compaction stubbed these calls' results; Drop takes back the note ForCall just gave id, when
// it did not fit the call's result.
type Sections interface {
	ForCall(id, tool string, input json.RawMessage) string
	Masked(ids []string)
	Drop(id string)
}

// Config is what a session's loop runs with.
type Config struct {
	Source models.Source
	Tools  *tools.Set
	WS     *tools.Workspace
	Log    *store.Log
	Blobs  *store.Blobs
	Budget Budget
	// Sampling goes with every request (server.Conversation.Sampling).
	Sampling server.Sampling
	// Emit receives every event as it happens, persisted or live-only, after the log has it.
	Emit func(proto.Event)
	// Record, when set, brackets each event a turn records from Run, its append and its Emit together: it must call
	// record once and return its error. A caller with other writers to the log (core's Halt) holds its write lock across
	// both, so no event of theirs takes a higher seq and goes out first (core's session.recordTurn). The turn's last
	// record is its turn.end. Start's user.message is not bracketed: its caller holds that lock already. Nil records
	// plainly.
	Record func(typ proto.EventType, record func() error) error
	// MaxResultBytes caps a tool result the model sees; beyond it, head and tail are kept and the full output goes to
	// the blob store. Default 16 KiB.
	MaxResultBytes int
	// FitWindow also caps each result at the room the window has left after the last request, less a reserve for the
	// next reply (fitRoom); a long result that meets less than fitMinBytes of room becomes a "window nearly full" note,
	// and every cut says how much it cut and how to narrow it. Off, results are capped by MaxResultBytes alone
	// (calibration pre-registration §4.6).
	FitWindow bool
	// StopRepeats ends a turn with repeat_loop when one call repeats RepeatLimit times with an unchanged result
	// (RepeatGuard; calibration pre-registration §4.5).
	StopRepeats bool
	// MaskObservations compacts the history once the last prompt reaches 75% of the served window: a read of a file
	// that a later read of the same version covers, and command output older than the last 3 requests, each become a
	// one-line stub, and a compaction event records what was replaced. The next compaction waits for the prompt to grow
	// 10% of the window (mask.go; calibration §15.2; DESIGN §7.6). Off, history is only appended to.
	MaskObservations bool
	// Summarize adds compaction's summarizing step (DESIGN §7.6; summarize.go, Session.summarize): when the last prompt
	// has reached 75% of the window and masking cannot bring it under, the oldest half of the turns before the last 3
	// requests become one summary message, written by the model's thinking-off alias. Off, no summary is made.
	Summarize bool
	// SummaryRetrieve, when above 0, re-retrieves after each summary (summary_retrieve.go; DESIGN §7.6, step 3): its open
	// threads are searched through Searcher, and the hits, within SummaryRetrieve percent of the window, become the
	// summary message's last section. It needs Summarize and a Searcher; 0 is off.
	SummaryRetrieve int
	// Searcher is ken's, as the search tool has it; nil without ken.
	Searcher tools.Searcher
	// CompactAtCliff makes the loop react when stuck (mask.go; calibration §16.2):
	//   - at the turn's first FitWindow nearly-full note where something can be stubbed, it compacts before the next
	//     request: besides what MaskObservations stubs, grep and glob output and whole-file reads from before the last
	//     3 requests become one-line stubs;
	//   - when StopRepeats would end the turn on a call answered with that note, it compacts the same way and goes on,
	//     at most stuckMax times a turn.
	CompactAtCliff bool
	// WidenRepeats is progressive widening: a whole read or grep whose base-cap result was shown, and is asked for
	// again unchanged, is shown with 3 times its cap, then all of a file or 10 times a grep's cap (tools.WithWiden; shownChain). A result replaced by the
	// nearly-full note, or cut, was not shown whole and freezes the chain. Repeats are counted on what was shown. It is
	// separate from CompactAtCliff and not yet screened (calibration §16.2, Francis 2026-11-01 18:00).
	WidenRepeats bool
	// ReadLines is how many lines the session's read tool returns for a read with no range (tools.Options.WholeReadLines):
	// what masking takes such a read to cover. 0 means the default, 200.
	ReadLines int
	// ResultNotes appends a line saying what to do to an empty glob or grep result and to an unchanged repeat (notes.go;
	// calibration pre-registration §4.4).
	ResultNotes bool
	// VerifyDone refuses an edit task's done, up to VerifyRefusals times, until a go build, vet or test has run since the
	// latest successful edit or write (verify.go; calibration pre-registration §4.2).
	VerifyDone bool
	// RestateTask makes the refusal of a done with no change quote the turn's task back (DoneNoChangeRestated) instead of
	// DoneNoChange alone. It changes only that refusal's text: still once per turn, still ended by the second done.
	RestateTask bool
	// BuildAfterEdit builds the package of a .go file after each successful edit or write, in Shell, and appends the
	// errors to the result when it does not build (build.go). Shell is the session's shell, the one bash runs in; without
	// it, nothing is built.
	BuildAfterEdit bool
	Shell          tools.Shell
	// Perms decides every tool call that changes something, in Mode (DESIGN §6.6). Without it such a call is refused:
	// a session whose tools can write must have an engine. Approve asks the user when the engine asks; nil means no one
	// is attached, and a call that needs the user is refused.
	Perms   *perms.Engine
	Mode    proto.Mode
	Approve Approver
	// Snapshots, when set, takes a snapshot of the working tree before each bash call (DESIGN §6.8). What the snapshot can undo
	// then needs no question (perms.Engine.Snapshotted), and a checkpoint event records it or why there is none.
	Snapshots Snapshotter
	// EditTask makes every request require a tool call (tool_choice any) and ends a turn only through the done tool,
	// which Tools must hold. Changed reports whether the workspace differs from where the session began; done with
	// no change is refused once (DoneNoChange), R13's loop mitigation (DESIGN §17).
	EditTask bool
	Changed  func() (bool, error)
	// Sections loads the instruction file's sections a tool call brings in (DESIGN §2.3; window.Instructions): after a
	// call runs, what ForCall returns is added to its result, and a compaction that stubs results tells it with Masked.
	// nil loads none.
	Sections Sections
	// Now and Sleep are replaced by tests.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
}

// Session is one session's loop. Turns run one at a time; it is not safe for concurrent use, except Drain.
type Session struct {
	pruned   bool        // Config.Snapshots has pruned its old checkpoints for this session
	draining atomic.Bool // Drain was called: the running turn stops between steps
	cfg      Config
	conv     *server.Conversation
	requests int // requests sent in the session so far
	tokens   int // tokens the server computed in the session so far (Budget)
	calls    int // tool calls run in the session so far
	turns    int
	pending  string // text of the turn Start opened
	userSeq  int64
	// obs is every tool result the conversation holds, and lastPrompt the last response's prompt tokens: what
	// observation masking reads (Config.MaskObservations, mask.go).
	obs        []observation
	vers       fileVersions
	lastPrompt int
	compacted  bool       // a compaction has run
	maskFloor  int        // the prompt of the first response after the last compaction; 0 until it arrives
	cliffNote  bool       // a result got fit_window's nearly-full note since the last request (Config.CompactAtCliff)
	cliffDone  bool       // a cliff compaction masked something this turn
	stuckDone  int        // stuck compactions this turn (stuckMax)
	shown      shownChain // this turn's progressive widening (Config.CompactAtCliff)
	// What summarizing reads (Config.Summarize, summarize_live.go): where the assistant messages are, the last seq a
	// summary covered and that summary's body; freed is what this request's masking freed, in bytes; sumFloor is the
	// prompt at the last summary attempt that changed nothing, 0 if none (summarizeDue backs off from it). It is not
	// rebuilt on resume: a resumed session's next request opens a new turn, which clears it.
	sumFloor     int
	cuts         summaryCuts
	summarizedTo int64
	body         string
	freed        int
	fast         string // the summarizing model (fastModel), once fastKnown
	fastKnown    bool
}

// New returns a session's loop.
func New(cfg Config) *Session {
	if cfg.MaxResultBytes == 0 {
		cfg.MaxResultBytes = 16 << 10
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	if cfg.Emit == nil {
		cfg.Emit = func(proto.Event) {}
	}
	conv := &server.Conversation{System: SystemPrompt, Tools: cfg.Tools.Specs(), MaxTokens: cfg.Budget.MaxTokens,
		Sampling: cfg.Sampling}
	if cfg.EditTask {
		conv.System, conv.ToolChoice = EditSystemPrompt, "any"
	}
	return &Session{cfg: cfg, conv: conv}
}

// Conversation is the session's conversation so far, for tests and for checking it against a rebuild from the log.
func (s *Session) Conversation() *server.Conversation { return s.conv }

// record appends an event the turn makes and emits it, inside Config.Record's bracket when there is one.
func (s *Session) record(typ proto.EventType, cause int64, payload any) (proto.Event, error) {
	var ev proto.Event
	appendEmit := func() error {
		var err error
		ev, err = s.append(typ, cause, payload)
		return err
	}
	if s.cfg.Record == nil {
		return ev, appendEmit()
	}
	return ev, s.cfg.Record(typ, appendEmit)
}

// append appends an event and emits it, with no bracket.
func (s *Session) append(typ proto.EventType, cause int64, payload any) (proto.Event, error) {
	ev, err := s.cfg.Log.Append(typ, cause, payload)
	if err != nil {
		return ev, err
	}
	s.cfg.Emit(ev)
	return ev, nil
}

// Start records the user's message and opens a turn; Run completes it. Splitting them lets the protocol answer
// session/prompt with the message's seq before the turn runs.
func (s *Session) Start(text string) (int64, error) {
	if s.pending != "" {
		return 0, errors.New("loop: a turn is already started")
	}
	ev, err := s.append(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text(text)}}) // unbracketed: Config.Record
	if err != nil {
		return 0, err
	}
	s.pending, s.userSeq = text, ev.Seq
	return ev.Seq, nil
}

// Turn is Start then Run.
func (s *Session) Turn(ctx context.Context, text string) (string, error) {
	if _, err := s.Start(text); err != nil {
		return proto.StopError, err
	}
	return s.Run(ctx)
}

// Run takes the started turn to its end and returns the stop reason it recorded in turn.end. The returned error is
// for failures to record; a model or tool failure is an event and a stop reason, not an error.
func (s *Session) Run(ctx context.Context) (string, error) {
	text := s.pending
	s.pending = ""
	cause := s.userSeq
	first := s.turns == 0
	s.turns++
	start := s.cfg.Now()
	// turn.end is the turn's last act: once it is emitted the session may start the next turn (core's emitTurn), so
	// nothing of this one may write shared state after it. ended keeps the deferred snapshot reset off that path.
	ended := false
	end := func(reason string) (string, error) {
		if _, err := s.conv.CancelPending("the turn ended (" + reason + ")"); err != nil {
			return reason, err
		}
		_ = s.conv.EndTurn()
		s.endSnapshot()
		ended = true
		_, err := s.record(proto.EvTurnEnd, cause, proto.TurnEnd{StopReason: reason})
		return reason, err
	}
	fail := func(class string, err error) (string, error) {
		if _, rerr := s.record(proto.EvError, cause, proto.ErrorEvent{Class: class, Message: err.Error()}); rerr != nil {
			return proto.StopError, rerr
		}
		return end(proto.StopError)
	}
	trip := func(budget string, limit, value float64) (string, error) {
		if _, err := s.record(proto.EvBudgetExceeded, cause, proto.BudgetExceeded{Budget: budget, Limit: limit, Value: value}); err != nil {
			return proto.StopBudget, err
		}
		return end(proto.StopBudget)
	}

	// stopped ends a turn whose context was cancelled: by the user, or by tack's halt, which the log records first.
	stopped := func() (string, error) {
		if errors.Is(context.Cause(ctx), ErrShutdown) {
			return end(proto.StopShutdown)
		}
		var h *halt.Halted
		if !errors.As(context.Cause(ctx), &h) {
			return end(proto.StopCancelled)
		}
		if _, err := s.record(proto.EvDaemonHalted, cause, h.DaemonHalted); err != nil {
			return proto.StopHalted, err
		}
		return end(proto.StopHalted)
	}

	if err := s.conv.BeginTurn(UserText(first, s.cfg.WS.Root(), text), false); err != nil {
		return fail("internal", err)
	}
	if ctx.Err() != nil { // halted or cancelled before the turn began
		return stopped()
	}
	bud := s.cfg.Budget
	if err := bud.check(); err != nil {
		return fail("internal", err)
	}
	srv, err := s.cfg.Source.Ensure(ctx)
	if err != nil {
		if ctx.Err() != nil { // halted or cancelled while the server was starting: not the server's fault
			return stopped()
		}
		return fail("server", err)
	}
	s.conv.Model = s.cfg.Source.Model()

	requests, calls, tokens, errorsInARow := 0, 0, 0, 0
	doneRefused, finished := false, false                   // an edit task's done (Config.EditTask)
	lastFailed := "\x00"                                    // the last call's key when it failed (callKey); no call has that key
	resent := 0                                             // resends of this request after a refusal for no tool call (NoToolCallNudge)
	var room *int                                           // bytes this response's results may still take (Config.FitWindow); nil: no window cap
	var repeats RepeatGuard                                 // the turn's unchanged repeats (Config.StopRepeats)
	var stopKeys []string                                   // this response's calls that reached RepeatLimit answered with the nearly-full note
	stopAway := false                                       // a call of this response reached RepeatLimit away from the cliff
	s.cliffNote, s.cliffDone, s.stuckDone = false, false, 0 // Config.CompactAtCliff's per-turn arming
	s.sumFloor = 0                                          // a new turn may summarize again (summarizeDue)
	s.shown = shownChain{}                                  // and its widening
	var verify verifyState                                  // the turn's changes and verifications (Config.VerifyDone)
	// The snapshot reset on every other way out, an error's too.
	defer func() {
		if !ended {
			s.endSnapshot()
		}
	}()
	verify.tests = newTestChanges(text, s.cfg.WS)
	repeatLoop := false
	for {
		if s.draining.Load() {
			return end(proto.StopShutdown)
		}
		switch el := s.cfg.Now().Sub(start); {
		case requests >= bud.MaxRequests:
			return trip("requests", float64(bud.MaxRequests), float64(requests))
		case el >= bud.MaxWall:
			return trip("wall_ms", float64(bud.MaxWall.Milliseconds()), float64(el.Milliseconds()))
		case tokens >= bud.TurnTokens:
			return trip("tokens", float64(bud.TurnTokens), float64(tokens))
		case s.tokens >= bud.SessionTokens:
			return trip("session_tokens", float64(bud.SessionTokens), float64(s.tokens))
		case s.calls >= bud.SessionToolCalls:
			return trip("session_tool_calls", float64(bud.SessionToolCalls), float64(s.calls))
		}
		requests++
		s.requests++
		n := s.requests
		summarize := s.cfg.Summarize && s.due(s.cfg.Source.Window()) // read before masking moves the floor
		s.freed = 0
		if s.cfg.MaskObservations || s.cfg.CompactAtCliff {
			if err := s.compact(n, cause); err != nil {
				return proto.StopError, err
			}
		}
		if summarize {
			spent, err := s.summarizeDue(ctx, srv, n, cause)
			tokens += spent
			if err != nil {
				return proto.StopError, err
			}
			if ctx.Err() != nil { // halted or cancelled during the summarizer's call: no request goes out
				return stopped()
			}
		}
		reqEv, err := s.record(proto.EvRequestSent, cause, proto.RequestSent{Request: n, Server: srv.Name(), Model: s.conv.Model})
		if err != nil {
			return proto.StopError, err
		}
		reqBytes := 0 // the request's size, for the window's bytes-per-token rate (fitRoom)
		if s.cfg.FitWindow {
			if b, err := json.Marshal(s.conv.Request()); err == nil {
				reqBytes = len(b)
			}
		}
		resp, err := s.stream(ctx, srv, reqEv.Seq)
		if err != nil {
			if ctx.Err() != nil {
				return stopped()
			}
			if se := (*server.Error)(nil); errors.As(err, &se) && se.NoToolCall() && resent < MaxNoToolCallResends {
				resent++
				if _, err := s.record(proto.EvError, reqEv.Seq, proto.ErrorEvent{Class: NoToolCallClass, Message: se.Error(), Retrying: true}); err != nil {
					return proto.StopError, err
				}
				if err := s.conv.Nudge(NoToolCallNudge); err != nil {
					return fail("internal", err)
				}
				continue
			}
			return fail("server", err)
		}
		resent = 0
		s.cfg.Source.Touch()
		msgEv, err := s.record(proto.EvAssistantMessage, reqEv.Seq, proto.AssistantMessage{Request: n, Content: resp.Content, StopReason: resp.StopReason})
		if err != nil {
			return proto.StopError, err
		}
		u := usageOf(n, srv.Name(), resp)
		s.noteUsage(u.Prompt)
		if _, err := s.record(proto.EvUsage, reqEv.Seq, u); err != nil {
			return proto.StopError, err
		}
		computed := computedTokens(u)
		tokens += computed
		s.tokens += computed
		room = nil
		if s.cfg.FitWindow {
			if r, ok := s.fitRoom(u, reqBytes); ok {
				room = &r
			}
		}
		s.cuts.add(msgEv.Seq, len(s.conv.Messages()))
		if err := s.conv.AddResponse(resp.Content); err != nil {
			return fail("internal", err)
		}
		if len(s.conv.Pending()) == 0 {
			if resp.StopReason == "max_tokens" {
				// A reply stopped short of its own limit was stopped by the window: the prompt left no room.
				if s.conv.MaxTokens > 0 && resp.Usage.OutputTokens < s.conv.MaxTokens {
					return end(proto.StopWindowFull)
				}
				return end(proto.StopMaxTokens)
			}
			return end(proto.StopEndTurn)
		}

		var results []server.ToolResult
		var over *proto.BudgetExceeded // the tool-call budget a call in this response tripped
		for _, b := range resp.Content {
			if b.Type != "tool_use" {
				continue
			}
			callEv, err := s.record(proto.EvToolCall, msgEv.Seq, proto.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
			if err != nil {
				return proto.StopError, err
			}
			var fin proto.ToolFinished
			var ran tools.Result // what the tool itself returned, when it ran: its Empty and State
			ranTool := false     // the tool ran to an outcome, ok or error: it was not refused, cancelled or over a budget
			level := 0           // the call's widening (tools.WithWiden), 0 for none
			key := callKey(b)
			switch {
			case resp.Malformed[b.ID] != "":
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeError, Result: "error: malformed tool call: " + resp.Malformed[b.ID]}
			case key == lastFailed:
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeError, Result: RepeatedCall}
			case ctx.Err() != nil:
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeCancelled, Result: "error: not run: " + notRun(ctx)}
			case s.draining.Load():
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeCancelled, Result: "error: not run: " + ErrShutdown.Error()}
			case calls >= bud.MaxToolCalls:
				over = &proto.BudgetExceeded{Budget: "tool_calls", Limit: float64(bud.MaxToolCalls), Value: float64(calls + 1)}
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeCancelled,
					Result: fmt.Sprintf("error: not run: the turn's tool-call budget (%d) was exceeded", bud.MaxToolCalls)}
			case s.calls >= bud.SessionToolCalls:
				over = &proto.BudgetExceeded{Budget: "session_tool_calls", Limit: float64(bud.SessionToolCalls), Value: float64(s.calls + 1)}
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeCancelled,
					Result: fmt.Sprintf("error: not run: the session's tool-call budget (%d) was exceeded", bud.SessionToolCalls)}
			case b.Name == "done" && s.cfg.EditTask:
				calls++
				s.calls++
				// An unreadable workspace counts as changed: the refusal is for a model that has not tried, and a
				// failure to tell must not keep one that has from finishing.
				changed := true
				if s.cfg.Changed != nil {
					if c, err := s.cfg.Changed(); err == nil {
						changed = c
					}
				}
				if !changed && !doneRefused {
					doneRefused = true
					fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeOK, Result: DoneNoChange}
					if s.cfg.RestateTask {
						fin.Result = DoneNoChangeRestated(text)
					}
				} else if s.cfg.VerifyDone && !verify.verified() && verify.refusals < VerifyRefusals {
					// ok, like DoneNoChange, so an identical done after it runs and counts instead of being answered
					// RepeatedCall.
					verify.refusals++
					fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeOK, Result: verify.refusal()}
				} else {
					finished = true
					fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeOK, Result: "done"}
					if s.cfg.VerifyDone {
						fin.Result = verify.finish()
					}
					fin.Result += verify.tests.report(verify.last, verify.change)
				}
			default:
				calls++
				s.calls++
				checkpoint := s.takeSnapshot(ctx, b)
				var asked []string // what the question is about to hold, read before it: the reasons read the tree, which can change
				if checkpoint != nil {
					asked = s.cfg.Perms.ReasonsIn(bashCommand(b))
				}
				refused, by, err := s.permit(ctx, b, callEv.Seq)
				if checkpoint != nil {
					// Files can change while a question waits, so the snapshot is taken again after each one. If there's a reason now
					// that the question didn't hold, the yes was to something else: ask the whole of it, until an answer holds.
					for err == nil && refused == nil && by == proto.DecidedByUser {
						checkpoint = s.takeSnapshot(ctx, b)
						now := s.cfg.Perms.ReasonsIn(bashCommand(b))
						if subset(now, asked) {
							break
						}
						asked = now
						refused, by, err = s.permit(ctx, b, callEv.Seq)
					}
				}
				if err != nil {
					return proto.StopError, err
				}
				if refused != nil {
					fin = *refused
					s.endSnapshot()
					break
				}
				if checkpoint != nil {
					if _, err := s.record(proto.EvCheckpoint, callEv.Seq, *checkpoint); err != nil {
						return proto.StopError, err
					}
				}
				var full []byte
				// Progressive widening (Config.WidenRepeats; mask.go, shownChain): a whole read or a grep whose result
				// at the base cap was shown, and is asked for again, runs once at a wider cap; the tool also returns its
				// base-cap view (Base). If that differs from what was last shown, the output changed, and the base view
				// is shown instead.
				var pick func(*tools.Result)
				widenable := s.cfg.WidenRepeats && tools.Widenable(b.Name, b.Input)
				if widenable {
					if level = s.shown.level(key); level > 0 {
						pick = func(r *tools.Result) {
							base := r.Base
							if base == "" { // nothing to widen: no matches, an emptied file, an error
								base = r.Text
							}
							if !s.shown.same(key, base) { // the output changed: show it at the base cap
								if r.Base != "" {
									r.Text = r.Base
								}
								level = 0
							}
						}
					}
				}
				if fin, full, ran, err = s.runTool(tools.WithWiden(shell.WithApproval(ctx, by), level), b, callEv.Seq, room, pick); err != nil {
					return proto.StopError, err
				}
				tokens += fin.Tokens // a task's child: it counts toward this turn's and this session's budgets
				s.tokens += fin.Tokens
				ranTool = fin.Outcome == proto.OutcomeOK || fin.Outcome == proto.OutcomeError
				if widenable {
					// The chain steps on the base-cap view. A note or a cut (ToolFinished.Truncated) was not shown whole: it
					// freezes the chain, and the widened lines are not recorded as shown.
					base := ran.Text
					if ran.Base != "" {
						base = ran.Base
					}
					s.shown.record(key, base, level, !fin.Truncated)
					if fin.Truncated {
						level = 0 // for coverage: a widened result not shown whole covers no more than the base
					}
					if level > 0 && ran.ShowWide != nil {
						ran.ShowWide() // the widened lines were shown: the workspace records them
					}
				}
				s.endSnapshot()
				verify.observe(calls, b.Name, b.Input, ran, s.cfg.Perms)
				// ADR-004's ordering rule: the blob, directory entry included, is durable before the event naming it is
				// recorded, so it is durable before any sync that makes that event durable.
				if full != nil {
					if _, err := s.cfg.Blobs.Put(full); err != nil {
						return proto.StopError, err
					}
				}
			}
			// The repeat count is taken on the result before any note, so a note's own count cannot make a repeat
			// look changed; a replay recovers that result with StripNotes.
			if s.cfg.StopRepeats || s.cfg.ResultNotes {
				n, stop := repeats.Observe(key, RepeatResult(fin.Result, ran.State))
				repeatLoop = repeatLoop || (s.cfg.StopRepeats && stop)
				if s.cfg.StopRepeats && stop {
					if strings.HasPrefix(fin.Result, nearlyFullOpen) {
						stopKeys = append(stopKeys, key)
					} else {
						stopAway = true
					}
				}
				if s.cfg.ResultNotes {
					fin.Result += resultNotes(b.Name, ran.Empty, n, s.cfg.StopRepeats)
				}
			}
			if s.cfg.BuildAfterEdit && fin.Outcome == proto.OutcomeOK && !ran.IsError && (b.Name == "edit" || b.Name == "write") {
				fin.Result += s.buildNote(ctx, b.Input)
			}
			// The instruction sections a call that ran brings in (Config.Sections) follow its own result, where the room
			// fit_window leaves allows; where it does not, they are not counted as in.
			if ranTool && s.cfg.Sections != nil {
				if note := s.cfg.Sections.ForCall(b.ID, b.Name, b.Input); note != "" {
					if room == nil || len(fin.Result)+len(note) <= *room {
						fin.Result += note
					} else {
						s.cfg.Sections.Drop(b.ID)
					}
				}
			}
			// edit's current-symbol note (tools.Options.CurrentSymbol) goes last, after the repeat count, and only where it
			// fits in the room fit_window leaves with the result.
			if ran.Note != "" && (room == nil || len(fin.Result)+len(ran.Note) <= *room) {
				fin.Result += ran.Note
			}
			if _, err := s.record(proto.EvToolFinished, callEv.Seq, fin); err != nil {
				return proto.StopError, err
			}
			results = append(results, server.ToolResult{ID: fin.ID, Result: fin.Result, IsError: fin.Outcome != proto.OutcomeOK})
			if s.cfg.MaskObservations || s.cfg.CompactAtCliff {
				o := observe(fin.ID, b.Name, b.Input, n, fin.Result, fin.Outcome == proto.OutcomeOK)
				o.callKey = key // a compaction that stubs it clears its widening chain
				s.track(o, fin.Truncated, level)
			}
			if room != nil {
				*room = max(0, *room-len(fin.Result))
			}
			switch fin.Outcome {
			case proto.OutcomeOK:
				errorsInARow, lastFailed = 0, ""
			case proto.OutcomeError, proto.OutcomeDenied:
				errorsInARow, lastFailed = errorsInARow+1, key
			default:
				lastFailed = ""
			}
		}
		if err := s.conv.AnswerTools(results); err != nil {
			return fail("internal", err)
		}
		if finished {
			return end(proto.StopDone)
		}
		if over != nil {
			return trip(over.Budget, over.Limit, over.Value)
		}
		// React when stuck (Config.CompactAtCliff): a repeat loop answered with the nearly-full note compacts and the turn
		// goes on. Every call of the response that reached the limit must be at the cliff; one away from it ends the
		// turn as before. The turn's first cliff compaction goes first, if it has not run; otherwise a stuck one, at
		// most stuckMax a turn. With neither, or nothing left to stub, the turn ends as before.
		if repeatLoop && s.cfg.CompactAtCliff && !stopAway && len(stopKeys) > 0 {
			m := 0
			if s.cliffNote && !s.cliffDone {
				var err error
				if m, err = s.compactNow(s.requests+1, cause, "cliff"); err != nil {
					return proto.StopError, err
				}
				s.cliffNote, s.cliffDone = false, m > 0
			}
			if m == 0 && s.stuckDone < stuckMax {
				var err error
				if m, err = s.compactNow(s.requests+1, cause, "stuck"); err != nil {
					return proto.StopError, err
				}
				if m > 0 {
					s.stuckDone++
				}
			}
			if m > 0 {
				for _, k := range stopKeys {
					repeats.Reset(k)
				}
				repeatLoop = false
			}
		}
		stopKeys, stopAway = nil, false
		if repeatLoop {
			return end(proto.StopRepeatLoop)
		}
		if errorsInARow >= bud.MaxErrors {
			return trip("consecutive_errors", float64(bud.MaxErrors), float64(errorsInARow))
		}
		if ctx.Err() != nil {
			return stopped()
		}
	}
}

// Approver asks the user about a tool call the permission engine would not allow alone.
type Approver interface {
	Approve(ctx context.Context, req proto.PermissionRequested) (Approval, error)
}

// Approval is the user's answer. Session extends an allow to the rest of the session: for a shell command, each simple
// command it held becomes a rule (its program and first argument); for another tool, the tool itself.
type Approval struct {
	Allow   bool
	Session bool
}

// permit decides a call that changes something, records the decision, and returns the call's refused result when it
// may not run, or nil and who allowed it (perms.ByUser, ByRule or ByMode) when it may. A read-only tool needs no
// decision and records none.
func (s *Session) permit(ctx context.Context, b proto.ModelBlock, callSeq int64) (*proto.ToolFinished, string, error) {
	t, ok := s.cfg.Tools.Get(b.Name)
	if !ok || t.ReadOnly() {
		return nil, "", nil
	}
	resolve := func(r proto.PermissionResolved) (*proto.ToolFinished, string, error) {
		r.ID = b.ID
		if _, err := s.record(proto.EvPermissionResolved, callSeq, r); err != nil {
			return nil, "", err
		}
		if r.Decision == proto.DecisionAllow {
			return nil, r.By, nil
		}
		text := "error: not run: " + strings.Join(r.Reasons, "; ")
		if r.By == proto.DecidedByNoUser {
			text = "error: not run: it needs the user's approval, and no one is attached to answer (" +
				strings.Join(r.Reasons, "; ") + "). Find a way that needs no approval, or say what you would run and why."
		}
		return &proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeDenied, Result: text}, "", nil
	}
	if s.cfg.Perms == nil {
		return resolve(proto.PermissionResolved{Decision: proto.DecisionDeny, By: proto.DecidedByMode,
			Reasons: []string{"this session has no permission engine, so nothing that changes files or runs commands may run"}})
	}
	v := s.cfg.Perms.Decide(s.cfg.Mode, b.Name, false, b.Input)
	switch v.Action {
	case perms.Allow:
		return resolve(proto.PermissionResolved{Decision: proto.DecisionAllow, By: v.By})
	case perms.Deny:
		return resolve(proto.PermissionResolved{Decision: proto.DecisionDeny, By: v.By, Reasons: v.Reasons})
	}
	req := proto.PermissionRequested{ID: b.ID, Tool: b.Name, Mode: s.cfg.Mode, Reasons: v.Reasons}
	if b.Name == "bash" {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(b.Input, &in)
		req.Command = in.Command
		for _, c := range v.Parsed {
			req.Parsed = append(req.Parsed, c.String())
		}
	}
	if _, err := s.record(proto.EvPermissionRequested, callSeq, req); err != nil {
		return nil, "", err
	}
	if s.cfg.Approve == nil {
		return resolve(proto.PermissionResolved{Decision: proto.DecisionDeny, By: proto.DecidedByNoUser, Reasons: v.Reasons})
	}
	a, err := s.cfg.Approve.Approve(ctx, req)
	if err != nil {
		return resolve(proto.PermissionResolved{Decision: proto.DecisionDeny, By: proto.DecidedByNoUser,
			Reasons: append(v.Reasons, "the question was not answered: "+err.Error())})
	}
	if !a.Allow {
		return resolve(proto.PermissionResolved{Decision: proto.DecisionDeny, By: proto.DecidedByUser, Scope: "once",
			Reasons: []string{"the user declined"}})
	}
	r := proto.PermissionResolved{Decision: proto.DecisionAllow, By: proto.DecidedByUser, Scope: "once"}
	if a.Session {
		r.Scope, r.RuleAdded = "session", strings.Join(s.cfg.Perms.AllowForSession(b.Name, v.Parsed), "; ")
	}
	return resolve(r)
}

// runTool records tool.started (fsynced by the store, so a crash leaves the call marked started), runs the tool and
// returns its tool.finished payload. When the output is too long for the model, the payload names a blob by handle
// and full holds the bytes; the caller stores them before recording the payload (ADR-004). room, when not nil, is the
// bytes the window has left for this response's results (Config.FitWindow). r is what the tool returned, redacted and escaped, for its
// Empty and State.
func (s *Session) runTool(ctx context.Context, b proto.ModelBlock, callSeq int64, room *int, pick func(*tools.Result)) (fin proto.ToolFinished, full []byte, r tools.Result, err error) {
	t, ok := s.cfg.Tools.Get(b.Name)
	if !ok { // checked by the client already; kept so a server that skipped the check cannot reach a nil tool
		return proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeError, Result: "error: no tool named " + b.Name}, nil, tools.Result{}, nil
	}
	if _, err := s.record(proto.EvToolStarted, callSeq, proto.ToolStarted{ID: b.ID}); err != nil {
		return proto.ToolFinished{}, nil, tools.Result{}, err
	}
	r, rerr := t.Run(ctx, s.cfg.WS, b.Input)
	if rerr != nil {
		r = tools.Result{Text: "error: " + rerr.Error(), IsError: true, Tokens: r.Tokens} // a cancelled task still spent them
	}
	// Redaction comes first, so neither the model, the log nor the blob store ever holds a credential it caught.
	var kinds []string
	r.Text, kinds = perms.Redact(r.Text)
	if r.Base != "" {
		var bk []string
		r.Base, bk = perms.Redact(r.Base)
		for _, k := range bk {
			if !slices.Contains(kinds, k) {
				kinds = append(kinds, k)
			}
		}
	}
	// Then the control characters, before anything measures, compares, cuts or stores the text.
	r.Text, r.Base, r.Note = escapeControl(r.Text), escapeControl(r.Base), escapeControl(r.Note)
	if pick != nil { // chooses, on redacted and escaped text, what is shown (Config.CompactAtCliff's widening)
		pick(&r)
	}
	fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeOK, Result: r.Text, Redacted: kinds, Tokens: r.Tokens}
	if r.IsError {
		fin.Outcome = proto.OutcomeError
	}
	if room != nil {
		fin, full, err := s.fit(fin, r.Text, *room)
		return fin, full, r, err
	}
	if len(r.Text) > s.cfg.MaxResultBytes {
		full = []byte(r.Text)
		head, tail := wholeCut(r.Text, s.cfg.MaxResultBytes*3/4, s.cfg.MaxResultBytes/4)
		handle := store.HandleOf(full)
		omitted := fmt.Sprintf("\n[… %d bytes omitted …]\n", len(r.Text)-len(head)-len(tail))
		if _, ok := s.cfg.Tools.Get("read_output"); ok {
			omitted = fmt.Sprintf("\n[… %d bytes omitted; read_output with handle %q pages them …]\n", len(r.Text)-len(head)-len(tail), handle)
		}
		fin.Result = head + omitted + tail
		fin.Truncated, fin.Blob = true, handle
	}
	return fin, full, r, nil
}

// stream sends the conversation's next request, emitting deltas live, and retries a full server queue up to three
// times, waiting as the server asks (DESIGN §6.3, "Errors").
func (s *Session) stream(ctx context.Context, srv server.Server, reqSeq int64) (*server.Response, error) {
	onDelta := func(d server.Delta) {
		_, _ = s.record(proto.EvAssistantDelta, reqSeq, proto.AssistantDelta{Index: d.Index, Kind: d.Kind, Text: d.Text})
	}
	for attempt := 0; ; attempt++ {
		resp, err := srv.Stream(ctx, s.conv.Request(), onDelta)
		var se *server.Error
		if err == nil || !errors.As(err, &se) || !se.Retryable() || attempt == 3 {
			return resp, err
		}
		if _, rerr := s.record(proto.EvError, reqSeq, proto.ErrorEvent{Class: "server", Message: se.Error(), Retrying: true}); rerr != nil {
			return nil, rerr
		}
		wait := se.RetryAfter
		if wait == 0 {
			wait = time.Second << attempt
		}
		if err := s.cfg.Sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ErrShutdown is the cause a turn's context is cancelled with when the daemon shuts down before the turn drained
// (core's Drain): its commands are killed, and the turn ends with stop reason shutdown.
var ErrShutdown = errors.New("the daemon is shutting down")

// Drain asks the session's turn to stop between steps, for a shutdown (DESIGN §6.1): the tool call running finishes, no
// other starts (each is answered not run), and the turn ends before its next request with stop reason shutdown. It
// lasts for the session's life, and may be called from any goroutine.
func (s *Session) Drain() { s.draining.Store(true) }

// notRun says why a call was not run once the turn's context is cancelled.
func notRun(ctx context.Context) string {
	if errors.Is(context.Cause(ctx), ErrShutdown) {
		return ErrShutdown.Error()
	}
	if h, ok := errors.AsType[*halt.Halted](context.Cause(ctx)); ok {
		return h.Error()
	}
	return "the user cancelled the turn"
}

// callKey identifies a tool call by its tool and its input, with the input's JSON normalised so a repeat with its keys
// in another order is the same call. A malformed input keys by its raw bytes.
func callKey(b proto.ModelBlock) string { return CallKey(b.Name, b.Input) }

// Snapshotter takes snapshots of the working tree (internal/snapshot.Shadow).
type Snapshotter interface {
	// Take returns the checkpoint's id, or "" and why there is none.
	Take(ctx context.Context) (id, why string)
	Prune(ctx context.Context) error
	// Staged reports whether a command that overwrites the index would lose content a snapshot doesn't hold.
	Staged(ctx context.Context) bool
}

// takeSnapshot takes the snapshot a bash call's permission decision reads, and returns what its checkpoint event would say:
// nil for any other call, or when the session keeps no snapshots. Pruning fails quietly, since a checkpoint kept too long
// costs only disk. Plan mode refuses bash before it could matter.
func (s *Session) takeSnapshot(ctx context.Context, b proto.ModelBlock) *proto.Checkpoint {
	if s.cfg.Snapshots == nil || s.cfg.Perms == nil || b.Name != "bash" || s.cfg.Mode == proto.ModePlan {
		return nil
	}
	if !s.pruned {
		s.pruned = true
		_ = s.cfg.Snapshots.Prune(ctx)
	}
	id, why := s.cfg.Snapshots.Take(ctx)
	s.cfg.Perms.Snapshotted = id != ""
	s.cfg.Perms.Staged = id != "" && s.cfg.Snapshots.Staged(ctx)
	return &proto.Checkpoint{ID: b.ID, Workspace: s.cfg.WS.Root(), Snapshot: id, Unavailable: why}
}

// bashCommand is a bash call's command line.
func bashCommand(b proto.ModelBlock) string {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(b.Input, &in)
	return in.Command
}

// subset reports whether every string in a is in b.
func subset(a, b []string) bool {
	return !slices.ContainsFunc(a, func(s string) bool { return !slices.Contains(b, s) })
}

// endSnapshot ends what a snapshot answers: it was of the tree before that one command.
func (s *Session) endSnapshot() {
	if s.cfg.Perms != nil {
		s.cfg.Perms.Snapshotted, s.cfg.Perms.Staged = false, false
	}
}

// usageOf is the usage event of request n's response from server. Prompt is the whole prompt: InputTokens plus Reused
// when the server reports reuse (server.Usage), and then InputTokens is the prefilled share. A server that does not
// report reuse leaves Reused and Prefilled nil.
func usageOf(n int, srvName string, resp *server.Response) proto.Usage {
	reused, prefilled := 0, (*int)(nil)
	if resp.Usage.Reused != nil {
		reused = *resp.Usage.Reused
		p := resp.Usage.InputTokens
		prefilled = &p
	}
	return proto.Usage{Request: n, Server: srvName, Prompt: resp.Usage.InputTokens + reused, Reused: resp.Usage.Reused,
		Prefilled: prefilled, Output: resp.Usage.OutputTokens, WallMS: int(resp.Usage.Wall.Milliseconds())}
}

// computedTokens is what the server computed for u: its prefilled share and output when it reports reuse, else the
// whole prompt and output. Budgets count this.
func computedTokens(u proto.Usage) int {
	if u.Prefilled != nil {
		return *u.Prefilled + u.Output
	}
	return u.Prompt + u.Output
}

// Tokens is what the server computed for the session's requests so far, as its budget counts them: a task's caller
// counts a child session's toward its own (tools.SubagentAnswer).
func (s *Session) Tokens() int { return s.tokens }
