package core

import (
	"context"
	"errors"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
)

// errNoWatcher answers a question no privileged client is watching for: no one can answer it.
var errNoWatcher = errors.New("no client that may answer is attached")

// answer is what reaches a waiting question: the user's approval, or why there will be none.
type answer struct {
	approval loop.Approval
	err      error
}

// approver is a session's loop.Approver: a question waits for permission/respond while a privileged client is
// subscribed to the session, with no timeout (the owner's call, docs/design/proposal-tui-protocol.md), and ends as
// no_user when none is, or when the last one leaves. The question itself is the permission.requested event the
// subscribers receive; a client attaching later finds it in the log, with no permission.resolved after it.
type approver struct{ s *session }

func (a approver) Approve(ctx context.Context, req proto.PermissionRequested) (loop.Approval, error) {
	s := a.s
	s.mu.Lock()
	if !s.watched() {
		s.mu.Unlock()
		return loop.Approval{}, errNoWatcher
	}
	ch := make(chan answer, 1)
	if s.pending == nil {
		s.pending = map[string]chan answer{}
	}
	s.pending[req.ID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, req.ID)
		s.mu.Unlock()
	}()
	select {
	case ans := <-ch:
		return ans.approval, ans.err
	case <-ctx.Done():
		return loop.Approval{}, context.Cause(ctx)
	}
}

// watched says whether a privileged client is subscribed to the session. The caller holds s.mu.
func (s *session) watched() bool {
	for sub := range s.subs {
		if sub.conn.privileged {
			return true
		}
	}
	return false
}

// endUnwatched ends every waiting question once no privileged client is subscribed. The caller holds s.mu.
func (s *session) endUnwatched() {
	if len(s.pending) == 0 || s.watched() {
		return
	}
	for id, ch := range s.pending {
		ch <- answer{err: errNoWatcher}
		delete(s.pending, id)
	}
}

// respond is permission/respond (DESIGN §9.2): the user's answer to a waiting question. The first answer wins; one to
// a question not waiting is CodeNotPending.
func (c *Core) respond(cn *conn, p proto.PermissionRespondParams) (any, *proto.Error) {
	if !cn.privileged {
		return nil, unprivileged(proto.MethodPermissionRespond)
	}
	var ap loop.Approval
	switch p.Decision {
	case proto.DecisionAllow:
		ap.Allow = true
	case proto.DecisionDeny:
	default:
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: `permission/respond: decision is "allow" or "deny", not "` + p.Decision + `"`}
	}
	switch p.Scope {
	case "", "once":
	case "session":
		ap.Session = ap.Allow
	default:
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: `permission/respond: scope is "once" or "session", not "` + p.Scope + `"`}
	}
	if p.Rule != "" {
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "permission/respond: an edited rule is not built yet; answer with scope once or session"}
	}
	ap.Note = p.Note
	s, perr := c.session(p.Session)
	if perr != nil {
		return nil, perr
	}
	s.mu.Lock()
	ch, ok := s.pending[p.ID]
	delete(s.pending, p.ID)
	s.mu.Unlock()
	if !ok {
		return nil, &proto.Error{Code: proto.CodeNotPending, Message: "permission/respond: question " + p.ID + " is not waiting: it was answered, or never asked"}
	}
	ch <- answer{approval: ap}
	return proto.PermissionRespondResult{}, nil
}
