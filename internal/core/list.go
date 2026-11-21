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
	root           string            // info.Workspace resolved as a filter is (resolveDir), once, when session.created is read
	turnOpen, held bool              // a user.message after the last turn.end; a session.held after the last release
}

// listSessions is session/list: every session in the store with a log, newest first, read from the logs. Each log is
// read once and then only from where the last call stopped, so a call costs the events appended since; a filtered call
// does not read on in a log already known to be another workspace's. A log that cannot be read is left out of the
// answer and what was read of it before is kept, so one damaged log does not fail the list. It changes nothing and logs
// nothing.
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
		if !filepath.IsAbs(p.Workspace) {
			return proto.SessionListResult{}, &proto.Error{Code: proto.CodeInvalidParams, Message: "session/list: workspace " + p.Workspace + " is not an absolute path"}
		}
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
		if l.created && want != "" && l.root != want { // another workspace's: nothing to read for this answer
			live[id] = l
			continue
		}
		before := c.turnMark(id) // read before the log, so a turn that ends while it is read is not taken for a crash
		switch err := c.catchUpListed(id, l); {
		case errors.Is(err, fs.ErrNotExist): // deleted since it was listed
			continue
		case err != nil: // unreadable: left out, what was read before kept
			live[id] = l
			continue
		}
		live[id] = l
		if !l.created || want != "" && l.root != want { // no session.created yet: not a session to list
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

// add takes one event of the session's log into l. A session.created or mode.changed that cannot be read is an error:
// the session cannot be opened either (modeFromLog), and the list leaves it out rather than guess its mode.
func (l *listed) add(ev proto.Event) error {
	switch ev.Type {
	case proto.EvSessionCreated:
		v, err := ev.Decode()
		if err != nil {
			return err
		}
		sc := v.(proto.SessionCreated)
		l.created = true
		l.root = resolveDir(sc.Workspace)
		l.info.Workspace, l.info.Created, l.info.Model, l.info.Mode, l.info.Parent = sc.Workspace, ev.Time, sc.Model, sc.Mode, sc.Parent
	case proto.EvModeChanged:
		v, err := ev.Decode()
		if err != nil {
			return err
		}
		l.info.Mode = v.(proto.ModeChanged).Mode
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
// It is running when a turn runs here after the read. A turn the log holds open is interrupted (a crash, or another
// process's turn) unless this Core's turns account for it: one ran before the read, or one began between the two
// marks, the session's first in this Core if it was opened during the read. Such a turn shows running, at most a moment
// stale, never interrupted. Else the session is held or idle, as the log says.
func listStatus(l *listed, before, after turnState) string {
	began := after.here && (before.here && after.turn != before.turn || !before.here && after.turn > 0)
	switch {
	case after.running:
		return proto.StatusRunning
	case l.turnOpen && (before.running || began):
		return proto.StatusRunning
	case l.turnOpen:
		return proto.StatusInterrupted
	case l.held:
		return proto.StatusHeld
	}
	return proto.StatusIdle
}

// resolveDir is p as session/new resolves a workspace (tools.NewWorkspace): absolute, with symlinks resolved, so a
// filter by the path a client knows matches the path the log holds (macOS's /var is /private/var). Both sides of the
// comparison go through it. For a path that no longer exists, its nearest existing parent is resolved and the rest kept,
// so a removed workspace under /var still matches the /private/var/... its log holds.
func resolveDir(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	rest := ""
	for dir := abs; ; dir = filepath.Dir(dir) {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(dir) == dir {
			return abs
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}
