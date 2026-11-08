package core

import (
	"errors"
	"fmt"
	"io/fs"

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
	evs, err := c.opt.Store.Events(id, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, unknown
	}
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	created, ok := sessionCreated(evs)
	if !ok {
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: fmt.Sprintf(
			"session %s cannot be reopened: its log does not begin with session.created, so it predates tack's daemon", id)}
	}
	log, err := c.opt.Store.OpenLog(id)
	if errors.Is(err, store.ErrLogBusy) {
		return nil, &proto.Error{Code: proto.CodeBusy, Message: fmt.Sprintf("session %s is open in another tack process", id)}
	}
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	fail := func(code int, err error) (*session, *proto.Error) {
		log.Close()
		return nil, &proto.Error{Code: code, Message: fmt.Sprintf("reopening session %s: %v", id, err)}
	}
	// Read again under the writer's lock: the process that held it may have appended before letting go.
	if evs, err = c.opt.Store.Events(id, 0); err != nil {
		return fail(proto.CodeInternalError, err)
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
	if err != nil {
		return fail(proto.CodeInvalidParams, err)
	}
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
func closeInterrupted(log *store.Log, evs []proto.Event) ([]proto.Event, error) {
	if !store.Inspect(evs).InTurn {
		return nil, nil
	}
	var userSeq int64
	type call struct {
		id      string
		seq     int64
		started bool
	}
	var calls []*call
	byID := map[string]*call{}
	for _, ev := range evs {
		v, err := ev.Decode()
		if err != nil {
			return nil, fmt.Errorf("seq %d: %w", ev.Seq, err)
		}
		switch p := v.(type) {
		case proto.UserMessage:
			userSeq, calls, byID = ev.Seq, nil, map[string]*call{}
		case proto.ToolCall:
			cl := &call{id: p.ID, seq: ev.Seq}
			calls, byID[p.ID] = append(calls, cl), cl
		case proto.ToolStarted:
			if cl := byID[p.ID]; cl != nil {
				cl.started = true
			}
		case proto.ToolFinished:
			delete(byID, p.ID)
		}
	}
	var out []proto.Event
	for _, cl := range calls {
		if byID[cl.id] == nil {
			continue // it has its result
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
// interrupted). A daemon calls it at start, before it serves. It returns the sessions it closed; a log it could not
// read is reported in the error and the rest are still recovered.
func (c *Core) Recover() ([]proto.SessionID, error) {
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
		if evs, err := c.opt.Store.Events(id, 0); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		} else if !store.Inspect(evs).InTurn {
			continue
		}
		log, err := c.opt.Store.OpenLog(id)
		if errors.Is(err, store.ErrLogBusy) {
			continue // live in another process: its turn is not interrupted
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		evs, err := c.opt.Store.Events(id, 0)
		var closed []proto.Event
		if err == nil {
			closed, err = closeInterrupted(log, evs)
		}
		if cerr := log.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
		if len(closed) > 0 {
			out = append(out, id)
		}
	}
	return out, errors.Join(errs...)
}
