package core

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sync/atomic"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

// Result texts for the calls a crash left without a result (DESIGN §6.1). A call that started is never re-run: a
// shell command may have had side effects. The first is the text Rebuild gives such a call in memory.
const (
	unknownResult = "error: outcome unknown: tack stopped while this call was running; it was not re-run"
	notRunResult  = "error: not run: tack stopped before it ran"
)

// reopen makes a session that is in the store but not in this Core, as a daemon does for a session an earlier daemon
// (or a `tack run`) made: from its log alone. A turn the log ends inside is closed first (closeInterrupted). The
// caller holds c.reopenMu, so one session is not reopened twice at once.
func (c *Core) reopen(id proto.SessionID) (*session, *proto.Error) {
	unknown := &proto.Error{Code: proto.CodeUnknownSession, Message: "unknown session " + string(id)}
	if !store.ValidSessionID(id) {
		return nil, unknown
	}
	busy := &proto.Error{Code: proto.CodeBusy, Message: fmt.Sprintf("session %s is open in another tack process", id)}
	// The hold first: a session another core has in memory is its, between turns too, when its log is free.
	hold, err := c.opt.Store.Hold(id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, unknown
	case errors.Is(err, store.ErrSessionHeld):
		return nil, busy
	case err != nil:
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	log, evs, err := c.opt.Store.Reopen(id) // read once, under the writer's lock
	if err != nil {
		hold.Release()
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, unknown
	case errors.Is(err, store.ErrLogBusy): // tack rewind, writing it
		return nil, busy
	case err != nil:
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	fail := func(code int, err error) (*session, *proto.Error) {
		log.Close()
		hold.Release()
		return nil, &proto.Error{Code: code, Message: fmt.Sprintf("reopening session %s: %v", id, err)}
	}
	created, ok := sessionCreated(evs)
	if !ok {
		return fail(proto.CodeInvalidParams, errors.New("its log does not begin with session.created, so it predates tack's daemon"))
	}
	closed, err := closeInterrupted(log, evs)
	if err != nil {
		return fail(proto.CodeInternalError, err)
	}
	evs = append(evs, closed...)
	ws, err := tools.NewWorkspace(created.Workspace)
	if err != nil {
		return fail(proto.CodeInvalidParams, fmt.Errorf("its workspace: %w", err))
	}
	s, err := c.open(id, log, ws, created, evs)
	if err != nil { // ken, the sandbox, halt's registration or the rebuild: not the client's params
		return fail(proto.CodeInternalError, err)
	}
	s.hold = hold
	_ = log.Idle() // until its next prompt
	return s, nil
}

// sessionCreated is the log's first event, which must be session.created.
func sessionCreated(evs []proto.Event) (proto.SessionCreated, bool) {
	if len(evs) == 0 || evs[0].Type != proto.EvSessionCreated {
		return proto.SessionCreated{}, false
	}
	v, err := evs[0].Decode()
	if err != nil {
		return proto.SessionCreated{}, false
	}
	return v.(proto.SessionCreated), true
}

// closeInterrupted closes the turn a crash left open at the end of evs, the log's events, and returns the events it
// appended (none when the log is not inside a turn). Every call of the turn without a result gets one: outcome
// unknown if it had started, never re-run; not run if it had not. Then turn.end records the turn as interrupted
// (DESIGN §6.1). The model sees those results at the next prompt, and the user resumes, rewinds or forks.
//
// A call is its tool.call event: tool.started and tool.finished name it by seq, as their cause, so a server that
// reuses a tool-call id across a turn's steps is told apart. An event without a cause (a log from before causes) is
// matched to the latest open call with its id. A tool_use of the turn's last response with no tool.call (the crash
// came between the two appends) gets its tool.call recorded first, from the block, and is answered not run.
func closeInterrupted(log *store.Log, evs []proto.Event) ([]proto.Event, error) {
	if !store.Inspect(evs).InTurn {
		return nil, nil
	}
	type call struct {
		id            string
		seq           int64
		started, done bool
	}
	var userSeq int64
	var calls []*call
	// The turn's last response's tool_use blocks that have no tool.call yet: the response and its calls are separate
	// appends, and a crash can land between them.
	var uses []proto.ModelBlock
	var msgSeq int64
	find := func(cause int64, id string) *call {
		for i := len(calls) - 1; i >= 0; i-- {
			if cl := calls[i]; cause != 0 && cl.seq == cause || cause == 0 && cl.id == id && !cl.done {
				return cl
			}
		}
		return nil
	}
	for _, ev := range evs {
		v, err := ev.Decode()
		if err != nil {
			return nil, fmt.Errorf("seq %d: %w", ev.Seq, err)
		}
		switch p := v.(type) {
		case proto.UserMessage:
			userSeq, calls, uses = ev.Seq, nil, nil
		case proto.AssistantMessage:
			msgSeq, uses = ev.Seq, nil
			for _, b := range p.Content {
				if b.Type == "tool_use" {
					uses = append(uses, b)
				}
			}
		case proto.ToolCall:
			calls = append(calls, &call{id: p.ID, seq: ev.Seq})
			if i := slices.IndexFunc(uses, func(b proto.ModelBlock) bool { return b.ID == p.ID }); i >= 0 {
				uses = slices.Delete(uses, i, i+1) // this block only: a response may repeat an id
			}
		case proto.ToolStarted:
			if cl := find(ev.Cause, p.ID); cl != nil {
				cl.started = true
			}
		case proto.ToolFinished:
			if cl := find(ev.Cause, p.ID); cl != nil {
				cl.done = true
			}
		}
	}
	var out []proto.Event
	for _, b := range uses { // record the calls the crash cut off before their tool.call
		ev, err := log.Append(proto.EvToolCall, msgSeq, proto.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
		if err != nil {
			return out, err
		}
		out = append(out, ev)
		calls = append(calls, &call{id: b.ID, seq: ev.Seq})
	}
	for _, cl := range calls {
		if cl.done {
			continue
		}
		fin := proto.ToolFinished{ID: cl.id, Outcome: proto.OutcomeCancelled, Result: notRunResult}
		if cl.started {
			fin = proto.ToolFinished{ID: cl.id, Outcome: proto.OutcomeUnknown, Result: unknownResult}
		}
		ev, err := log.Append(proto.EvToolFinished, cl.seq, fin)
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
	ev, err := log.Append(proto.EvTurnEnd, userSeq, proto.TurnEnd{StopReason: proto.StopInterrupted})
	if err != nil {
		return out, err
	}
	return append(out, ev), nil
}

// Recover closes the turns a crash left open, in every session in the store that this Core does not hold and no
// other process has open (DESIGN §6.1: on start, a session whose last event falls inside a turn is marked
// interrupted). A daemon calls it at start, before it serves; it holds the reopen lock, so no session is reopened
// meanwhile. It returns the sessions it closed; each session it could not read or close is in the error, and the rest
// are still recovered.
func (c *Core) Recover() ([]proto.SessionID, error) {
	c.reopenMu.Lock()
	defer c.reopenMu.Unlock()
	ids, err := c.opt.Store.Sessions()
	if err != nil {
		return nil, err
	}
	var out []proto.SessionID
	var errs []error
	for _, id := range ids {
		c.mu.Lock()
		_, held := c.sessions[id]
		c.mu.Unlock()
		if held {
			continue
		}
		switch closed, err := c.recoverOne(id); {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		case closed:
			out = append(out, id)
		}
	}
	return out, errors.Join(errs...)
}

// recoverOne closes the turn a crash left open in one session, and reports whether there was one. It reads the log
// first without a lock, and takes the session's hold and its log's lock only when the last turn is open, so tack
// rewind and a core reopening a finished session are not refused meanwhile. A session another core holds, or another
// process is writing, is live and left alone, as is one from before the daemon.
func (c *Core) recoverOne(id proto.SessionID) (bool, error) {
	evs, err := c.opt.Store.Events(id, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil // discarded since it was listed
	}
	if err != nil {
		return false, err
	}
	if _, ok := sessionCreated(evs); !ok || !store.Inspect(evs).InTurn { // from before the daemon, or finished
		return false, nil
	}
	hold, err := c.opt.Store.Hold(id)
	if errors.Is(err, store.ErrSessionHeld) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer hold.Release()
	log, evs, err := c.opt.Store.Reopen(id) // read again under the lock: what it holds now is what is closed
	if errors.Is(err, store.ErrLogBusy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if recoverHook != nil {
		recoverHook(id)
	}
	closed, err := closeInterrupted(log, evs)
	if cerr := log.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, fmt.Errorf("closing its interrupted turn: %w", err)
	}
	return len(closed) > 0, nil
}

// errClosed answers a request for a session once the Core is closed.
var errClosed = &proto.Error{Code: proto.CodeShuttingDown, Message: "the daemon is closing"}

// madeHook, when set (tests), runs in newSession after its closed check.
var madeHook func()

// recoverHook, when set (tests), runs in Recover for each session while it holds the session and its log's lock.
var recoverHook func(proto.SessionID)

// haltHook, when set (tests), runs in Halt's refused path between taking the log back and appending to it.
var haltHook func()

// turnEndHook, when set (tests), runs as a turn's goroutine returns from the loop, before it gives the log up. It is
// atomic because any test's turn goroutine reads it.
var turnEndHook atomic.Pointer[func()]
