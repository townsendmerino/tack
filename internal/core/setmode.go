package core

import (
	"errors"
	"fmt"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tools"
)

// modeFromLog is the mode a session has from its events: the last mode.changed's, else created, the mode in session.created. A
// mode.changed that cannot be read gives "", which is not a mode, so open refuses the log rather than guessing.
func modeFromLog(evs []proto.Event, created proto.Mode) proto.Mode {
	mode := created
	for _, ev := range evs {
		if ev.Type != proto.EvModeChanged {
			continue
		}
		v, err := ev.Decode()
		if err != nil {
			mode = ""
			continue
		}
		mode = v.(proto.ModeChanged).Mode
	}
	return mode
}

// setMode is session/setMode (DESIGN §6.6): it moves an idle session to p.Mode, lowered to a trusted repository's ceiling, and
// says the mode it now has. A turn running is busy. The change is logged as mode.changed before it is applied, and if it cannot be
// applied a second mode.changed, by "system", puts the log back, so the log always says the mode the session has.
//
// Among ask, edits and auto the session's tools and prompt are the same and only perms.Engine.Decide's mode changes, so the loop
// is told the new mode. To or from plan the tool set differs, so the loop session is rebuilt from the log (rebuild).
func (c *Core) setMode(p proto.SessionSetModeParams) (any, *proto.Error) {
	if !p.Mode.Valid() {
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: fmt.Sprintf("%q is not a mode (plan, ask, edits or auto)", p.Mode)}
	}
	if p.Mode == proto.ModeAuto && !p.Confirmed { // asked for, whatever a ceiling would make of it
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "moving to auto mode needs the owner's confirmation (confirmed: true)"}
	}
	s, perr := c.session(p.Session)
	if perr != nil {
		return nil, perr
	}
	// wmu first: a turn's start takes it too, so no turn starts between the check and the change, and one that began first is
	// seen as running.
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.Lock()
	running, cur := s.running, s.mode
	s.mu.Unlock()
	if running {
		return nil, &proto.Error{Code: proto.CodeBusy, Message: "a turn is running in " + string(s.id) + "; change the mode between turns"}
	}
	to := c.clampMode(s.root, p.Mode)
	if to == cur {
		return proto.SessionSetModeResult{Mode: cur}, nil
	}
	foreign, err := s.log.Wake()
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeBusy, Message: fmt.Sprintf("session %s's log is open in another tack process; try again when it is done", s.id)}
	}
	for _, ev := range foreign {
		s.emit(ev)
	}
	idle := func() {
		s.mu.Lock()
		if !s.running {
			_ = s.log.Idle() // the append took the log back
		}
		s.mu.Unlock()
	}
	defer idle()
	record := func(m proto.Mode, by string) error {
		ev, err := s.log.Append(proto.EvModeChanged, 0, proto.ModeChanged{Mode: m, By: by})
		if err == nil {
			s.emit(ev)
		}
		return err
	}
	if err := record(to, "user"); err != nil {
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	if err := c.applyMode(s, cur, to); err != nil {
		// The log is the truth: undo what it now says. If even that cannot be written, a reopen will take the new mode, which is
		// the log's to decide.
		_ = record(cur, "system")
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: fmt.Sprintf("the mode was not changed: %v", err)}
	}
	return proto.SessionSetModeResult{Mode: to}, nil
}

// applyMode makes the session run in to, which it was in from. The caller holds wmu and has seen no turn running.
func (c *Core) applyMode(s *session, from, to proto.Mode) error {
	if (from == proto.ModePlan) == (to == proto.ModePlan) {
		s.mu.Lock()
		s.loop.SetMode(to)
		s.mode = to
		s.mu.Unlock()
		return nil
	}
	return c.rebuild(s)
}

// rebuild replaces the session's loop session with one built from its log, which already holds the new mode.changed: the same
// path a reopen takes, on the session as it is, so its subscribers, hold, log and sweep target stay. The new loop is built whole
// before anything is replaced, so a failure leaves the old one. What the user allowed "for the session" lived in the old edit kit's
// engine and is forgotten; the shell's working directory is too.
func (c *Core) rebuild(s *session) error {
	evs, err := s.log.Events()
	if err != nil {
		return err
	}
	created, ok := sessionCreated(evs)
	if !ok {
		return errors.New("the log does not begin with session.created")
	}
	created.Mode = modeFromLog(evs, created.Mode)
	ws, err := tools.NewWorkspace(s.root)
	if err != nil {
		return err
	}
	fresh, err := c.build(s.id, s.log, ws, created, evs, s)
	if err != nil {
		return err
	}
	s.mu.Lock()
	oldKit := s.kit
	s.loop, s.kit, s.mode = fresh.loop, fresh.kit, fresh.mode
	s.instructions, s.notesFirst, s.know, s.sectionSearch, s.sections = fresh.instructions, fresh.notesFirst, fresh.know, fresh.sectionSearch, fresh.sections
	s.injectRoot, s.mapRoot = fresh.injectRoot, fresh.mapRoot
	s.mu.Unlock()
	if oldKit != nil {
		_ = oldKit()
	}
	return nil
}
