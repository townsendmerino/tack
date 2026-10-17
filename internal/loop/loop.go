package loop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

// Budget bounds one turn (DESIGN §6.3).
type Budget struct {
	MaxRequests  int
	MaxToolCalls int
	MaxWall      time.Duration
	MaxTokens    int // output tokens per request
}

// Config is what a session's loop runs with.
type Config struct {
	Source models.Source
	Tools  *tools.Set
	WS     *tools.Workspace
	Log    *store.Log
	Blobs  *store.Blobs
	Budget Budget
	// Emit receives every event as it happens, persisted or live-only, after the log has it.
	Emit func(proto.Event)
	// MaxResultBytes caps a tool result the model sees; beyond it, head and tail are kept and the full output goes to
	// the blob store. Default 16 KiB.
	MaxResultBytes int
	// Now and Sleep are replaced by tests.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
}

// Session is one session's loop. Turns run one at a time; it is not safe for concurrent use.
type Session struct {
	cfg      Config
	conv     *server.Conversation
	requests int // requests sent in the session so far
	turns    int
	pending  string // text of the turn Start opened
	userSeq  int64
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
	return &Session{cfg: cfg, conv: &server.Conversation{System: SystemPrompt, Tools: cfg.Tools.Specs(),
		MaxTokens: cfg.Budget.MaxTokens}}
}

// Conversation is the session's conversation so far, for tests and for checking it against a rebuild from the log.
func (s *Session) Conversation() *server.Conversation { return s.conv }

func (s *Session) record(typ proto.EventType, cause int64, payload any) (proto.Event, error) {
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
	ev, err := s.record(proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text(text)}})
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
	end := func(reason string) (string, error) {
		if _, err := s.conv.CancelPending("the turn ended (" + reason + ")"); err != nil {
			return reason, err
		}
		_ = s.conv.EndTurn()
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

	if err := s.conv.BeginTurn(UserText(first, s.cfg.WS.Root(), text), false); err != nil {
		return fail("internal", err)
	}
	srv, err := s.cfg.Source.Ensure(ctx)
	if err != nil {
		return fail("server", err)
	}
	s.conv.Model = s.cfg.Source.Model()

	requests, calls := 0, 0
	for {
		if requests >= s.cfg.Budget.MaxRequests {
			return trip("requests", float64(s.cfg.Budget.MaxRequests), float64(requests))
		}
		if el := s.cfg.Now().Sub(start); el >= s.cfg.Budget.MaxWall {
			return trip("wall_ms", float64(s.cfg.Budget.MaxWall.Milliseconds()), float64(el.Milliseconds()))
		}
		requests++
		s.requests++
		n := s.requests
		reqEv, err := s.record(proto.EvRequestSent, cause, proto.RequestSent{Request: n, Server: srv.Name(), Model: s.conv.Model})
		if err != nil {
			return proto.StopError, err
		}
		resp, err := s.stream(ctx, srv, reqEv.Seq)
		if err != nil {
			if ctx.Err() != nil {
				return end(proto.StopCancelled)
			}
			return fail("server", err)
		}
		s.cfg.Source.Touch()
		msgEv, err := s.record(proto.EvAssistantMessage, reqEv.Seq, proto.AssistantMessage{Request: n, Content: resp.Content, StopReason: resp.StopReason})
		if err != nil {
			return proto.StopError, err
		}
		// Prompt is the whole prompt. Under goinfer's G3 (docs/briefs/goinfer-g3-messages-cached-tokens.md),
		// resp.Usage.InputTokens is only the tokens this request prefilled; the reused leading tokens are
		// separate, in Reused. A pre-G3 server never sets Reused, so reused is 0 and Prompt is InputTokens alone,
		// unchanged from before G3.
		reused := 0
		if resp.Usage.Reused != nil {
			reused = *resp.Usage.Reused
		}
		u := proto.Usage{Request: n, Server: srv.Name(), Prompt: resp.Usage.InputTokens + reused, Reused: resp.Usage.Reused,
			Output: resp.Usage.OutputTokens, WallMS: int(resp.Usage.Wall.Milliseconds())}
		if _, err := s.record(proto.EvUsage, reqEv.Seq, u); err != nil {
			return proto.StopError, err
		}
		if err := s.conv.AddResponse(resp.Content); err != nil {
			return fail("internal", err)
		}
		if len(s.conv.Pending()) == 0 {
			if resp.StopReason == "max_tokens" {
				return end(proto.StopMaxTokens)
			}
			return end(proto.StopEndTurn)
		}

		var results []server.ToolResult
		overBudget := false
		for _, b := range resp.Content {
			if b.Type != "tool_use" {
				continue
			}
			callEv, err := s.record(proto.EvToolCall, msgEv.Seq, proto.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
			if err != nil {
				return proto.StopError, err
			}
			var fin proto.ToolFinished
			switch {
			case resp.Malformed[b.ID] != "":
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeError, Result: "error: malformed tool call: " + resp.Malformed[b.ID]}
			case ctx.Err() != nil:
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeCancelled, Result: "error: not run: the user cancelled the turn"}
			case calls >= s.cfg.Budget.MaxToolCalls:
				overBudget = true
				fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeCancelled,
					Result: fmt.Sprintf("error: not run: the turn's tool-call budget (%d) was exceeded", s.cfg.Budget.MaxToolCalls)}
			default:
				calls++
				var full []byte
				if fin, full, err = s.runTool(ctx, b, callEv.Seq); err != nil {
					return proto.StopError, err
				}
				// ADR-004's ordering rule: the blob, directory entry included, is durable before the event naming it is
				// recorded, so it is durable before any sync that makes that event durable.
				if full != nil {
					if _, err := s.cfg.Blobs.Put(full); err != nil {
						return proto.StopError, err
					}
				}
			}
			if _, err := s.record(proto.EvToolFinished, callEv.Seq, fin); err != nil {
				return proto.StopError, err
			}
			results = append(results, server.ToolResult{ID: fin.ID, Result: fin.Result, IsError: fin.Outcome != proto.OutcomeOK})
		}
		if err := s.conv.AnswerTools(results); err != nil {
			return fail("internal", err)
		}
		if overBudget {
			return trip("tool_calls", float64(s.cfg.Budget.MaxToolCalls), float64(calls+1))
		}
		if ctx.Err() != nil {
			return end(proto.StopCancelled)
		}
	}
}

// runTool records tool.started (fsynced by the store, so a crash leaves the call marked started), runs the tool and
// returns its tool.finished payload. When the output is too long for the model, the payload names a blob by handle
// and full holds the bytes; the caller stores them before recording the payload (ADR-004).
func (s *Session) runTool(ctx context.Context, b proto.ModelBlock, callSeq int64) (fin proto.ToolFinished, full []byte, err error) {
	t, ok := s.cfg.Tools.Get(b.Name)
	if !ok { // checked by the client already; kept so a server that skipped the check cannot reach a nil tool
		return proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeError, Result: "error: no tool named " + b.Name}, nil, nil
	}
	if _, err := s.record(proto.EvToolStarted, callSeq, proto.ToolStarted{ID: b.ID}); err != nil {
		return proto.ToolFinished{}, nil, err
	}
	r, rerr := t.Run(ctx, s.cfg.WS, b.Input)
	if rerr != nil {
		r = tools.Result{Text: "error: " + rerr.Error(), IsError: true}
	}
	fin = proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeOK, Result: r.Text}
	if r.IsError {
		fin.Outcome = proto.OutcomeError
	}
	if len(r.Text) > s.cfg.MaxResultBytes {
		full = []byte(r.Text)
		head, tail := s.cfg.MaxResultBytes*3/4, s.cfg.MaxResultBytes/4
		fin.Result = r.Text[:head] + fmt.Sprintf("\n[… %d bytes omitted …]\n", len(r.Text)-head-tail) + r.Text[len(r.Text)-tail:]
		fin.Truncated, fin.Blob = true, store.HandleOf(full)
	}
	return fin, full, nil
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
