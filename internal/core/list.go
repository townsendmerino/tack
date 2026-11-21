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
		want = resolveDir(p.Workspace)
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
		before := c.turnMark(id) // read before the log, so a turn that ends while it is read is not taken for a crash
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
		info.Status = listStatus(l, before, c.turnMark(id))
		res.Sessions = append(res.Sessions, info)
	}
	c.listed = live
	slices.SortFunc(res.Sessions, func(a, b proto.SessionInfo) int {
		return cmp.Or(b.Created.Compare(a.Created), cmp.Compare(b.ID, a.ID))
	})
	return res, nil
}

// catchUpListed reads id's log from where l stopped. A log that no longer matches what was read is read again from the
// start. It reads into a copy and keeps it only when the read succeeds, so a failed read leaves l as it was.
func (c *Core) catchUpListed(id proto.SessionID, l *listed) error {
	read := *l
	end, next, err := c.opt.Store.ScanFrom(id, read.off, read.next, read.add)
	if err != nil && l.off > 0 && !errors.Is(err, fs.ErrNotExist) {
		read = listed{next: 1}
		end, next, err = c.opt.Store.ScanFrom(id, 0, 1, read.add)
	}
	if err != nil {
		return err
	}
	read.off, read.next = end, next
	*l = read
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

// turnState is what this Core says of a session's turns at one moment: whether one is running, and how many have begun
// (session.turn). here is false for a session this Core does not have in memory.
type turnState struct {
	here, running bool
	turn          uint64
}

// turnMark is id's turnState now.
func (c *Core) turnMark(id proto.SessionID) turnState {
	c.mu.Lock()
	s := c.sessions[id]
	c.mu.Unlock()
	if s == nil {
		return turnState{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return turnState{here: true, running: s.running, turn: s.turn}
}

// listStatus is a session's status from its log as read (l) and this Core's turns just before and just after the read.
// It is running when a turn ran here at either moment, or one began between them; else interrupted when the log has a
// turn open (a crash, or another process's turn); else held or idle. So a turn that ends while the log is read shows
// running, and one that begins while it is read shows running with the last_turn_at the read saw: at most a moment
// stale, never interrupted.
func listStatus(l *listed, before, after turnState) string {
	switch {
	case before.running || after.running || before.here && after.here && before.turn != after.turn:
		return proto.StatusRunning
	case l.turnOpen:
		return proto.StatusInterrupted
	case l.held:
		return proto.StatusHeld
	}
	return proto.StatusIdle
}

// resolveDir is p as session/new resolves a workspace (tools.NewWorkspace): absolute, with symlinks resolved, so a
// filter by the path a client knows matches the path the log holds (macOS's /var is /private/var). A path that cannot
// be resolved, one since removed, is taken cleaned.
func resolveDir(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}
