package core

import (
	"cmp"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/townsendmerino/tack/internal/proto"
)

// listed is what session/list has read of one session's log: where it stopped (store.ScanFrom's off and next), and what
// the events up to there say.
type listed struct {
	off, next      int64
	info           proto.SessionInfo // ID and Status are set per call, from turnOpen, held and this Core's running turns
	created        bool              // session.created has been read
	turnOpen, held bool              // a user.message after the last turn.end; a session.held after the last release
}

// listSessions is session/list: every session in the store with a log, newest first, read from the logs. Each log is
// read once and then only from where the last call stopped, so a call costs the events appended since. It changes
// nothing and logs nothing.
func (c *Core) listSessions(p proto.SessionListParams) (proto.SessionListResult, *proto.Error) {
	if c.opt.Store == nil {
		return proto.SessionListResult{}, &proto.Error{Code: proto.CodeInvalidRequest, Message: "session/list: this session core has no store"}
	}
	ids, err := c.opt.Store.Sessions()
	if err != nil {
		return proto.SessionListResult{}, &proto.Error{Code: proto.CodeInternalError, Message: "session/list: " + err.Error()}
	}
	want := ""
	if p.Workspace != "" {
		want = filepath.Clean(p.Workspace)
	}
	c.listMu.Lock()
	defer c.listMu.Unlock()
	live := map[proto.SessionID]*listed{}
	res := proto.SessionListResult{Sessions: []proto.SessionInfo{}}
	for _, id := range ids {
		l := c.listed[id]
		if l == nil {
			l = &listed{next: 1}
		}
		switch err := c.catchUpListed(id, l); {
		case errors.Is(err, fs.ErrNotExist): // deleted since it was listed
			continue
		case err != nil:
			return proto.SessionListResult{}, &proto.Error{Code: proto.CodeInternalError, Message: "session/list: " + string(id) + ": " + err.Error()}
		}
		live[id] = l
		if !l.created || want != "" && filepath.Clean(l.info.Workspace) != want { // no session.created yet: not a session to list
			continue
		}
		info := l.info
		info.ID = id
		info.Status = c.listStatus(id, l)
		res.Sessions = append(res.Sessions, info)
	}
	c.listed = live
	slices.SortFunc(res.Sessions, func(a, b proto.SessionInfo) int {
		return cmp.Or(b.Created.Compare(a.Created), cmp.Compare(b.ID, a.ID))
	})
	return res, nil
}

// catchUpListed reads id's log from where l stopped. A log that no longer matches what was read is read again from the
// start.
func (c *Core) catchUpListed(id proto.SessionID, l *listed) error {
	end, next, err := c.opt.Store.ScanFrom(id, l.off, l.next, l.add)
	if err != nil && l.off > 0 && !errors.Is(err, fs.ErrNotExist) {
		*l = listed{next: 1}
		end, next, err = c.opt.Store.ScanFrom(id, 0, 1, l.add)
	}
	if err != nil {
		return err
	}
	l.off, l.next = end, next
	return nil
}

// add takes one event of the session's log into l. A mode.changed that cannot be read leaves the mode "", as modeFromLog
// does, rather than guessing.
func (l *listed) add(ev proto.Event) error {
	switch ev.Type {
	case proto.EvSessionCreated:
		v, err := ev.Decode()
		if err != nil {
			return err
		}
		sc := v.(proto.SessionCreated)
		l.created = true
		l.info.Workspace, l.info.Created, l.info.Model, l.info.Mode, l.info.Parent = sc.Workspace, ev.Time, sc.Model, sc.Mode, sc.Parent
	case proto.EvModeChanged:
		l.info.Mode = ""
		if v, err := ev.Decode(); err == nil {
			l.info.Mode = v.(proto.ModeChanged).Mode
		}
	case proto.EvUserMessage:
		t := ev.Time
		l.info.LastTurnAt, l.turnOpen = &t, true
	case proto.EvTurnEnd:
		l.turnOpen = false
	case proto.EvSessionHeld:
		l.held = true
	case proto.EvSessionReleased:
		l.held = false
	}
	return nil
}

// listStatus is id's status: running while this Core runs a turn in it; else interrupted when its log has a turn open
// (a crash, or another process's turn); else held or idle.
func (c *Core) listStatus(id proto.SessionID, l *listed) string {
	c.mu.Lock()
	s := c.sessions[id]
	c.mu.Unlock()
	if s != nil {
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if running {
			return proto.StatusRunning
		}
	}
	switch {
	case l.turnOpen:
		return proto.StatusInterrupted
	case l.held:
		return proto.StatusHeld
	}
	return proto.StatusIdle
}
