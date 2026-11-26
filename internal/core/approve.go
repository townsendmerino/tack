package core

import (
	"context"
	"errors"
	"strconv"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
)

// errNoWatcher answers a question no privileged client is watching for: no one can answer it.
var errNoWatcher = errors.New("no client that may answer is attached")

// answer is what reaches a waiting question: the user's approval, or why there will be none.
type answer struct {
	approval loop.Approval
	err      error
}

// question is the one question a session's turn waits on, named by its permission.requested event's seq. The tool
// call's id is not enough: the loop asks again under the same id when the reasons change after a yes (the snapshot
// re-ask), and model-chosen ids repeat across turns, so an answer meant for one question must not settle another.
type question struct {
	seq int64
	id  string
	ch  chan answer
	// shown is the rules the question offers for editing, computed here from the session engine's own parse of the call, the way the loop
	// computes the ones it lists, and not read back from the text of the event. An edited rule is held to these, never to anything the
	// client says it was shown.
	shown []perms.Rule
	// settled is set once an answer is sent on ch, under s.mu. The question stays registered until its approver has read
	// the answer, because the loop emits the event before it calls Approve and a quick client can answer in between.
	settled bool
}

// ask registers the question ev asks, as ev is emitted and before any subscriber can see it, so an answer can never
// arrive for a question not yet registered. A question asked with no privileged client watching is not registered,
// and the approver ends it as no_user. The caller holds s.mu.
func (s *session) ask(ev proto.Event) {
	s.question = nil
	if !s.watched() {
		return
	}
	v, err := ev.Decode()
	if err != nil {
		return
	}
	req := v.(proto.PermissionRequested)
	var shown []perms.Rule
	if req.Tool == "bash" && s.engine != nil { // called from the turn's own goroutine as the event is recorded, so the engine is not in use elsewhere
		shown = perms.SessionRules("bash", s.engine.Analyze(req.Command))
	}
	s.question = &question{seq: ev.Seq, id: req.ID, ch: make(chan answer, 1), shown: shown}
}

// approver is a session's loop.Approver: the question its permission.requested event registered (session.ask) waits for
// permission/respond while a privileged client is subscribed, with no timeout (the owner's call,
// docs/design/proposal-tui-protocol.md). It ends as no_user when no such client watches, or when the last one leaves,
// and with the turn's context when a halt, a shutdown or a cancel ends the turn. The question itself is the
// permission.requested event; a client attaching later finds it in the log, with no permission.resolved after it.
type approver struct{ s *session }

func (a approver) Approve(ctx context.Context, req proto.PermissionRequested) (loop.Approval, error) {
	s := a.s
	s.mu.Lock()
	q := s.question
	s.mu.Unlock()
	if q == nil || q.id != req.ID {
		return loop.Approval{}, errNoWatcher
	}
	defer func() {
		s.mu.Lock()
		if s.question == q {
			s.question = nil
		}
		s.mu.Unlock()
	}()
	select {
	case ans := <-q.ch:
		return ans.approval, ans.err
	case <-ctx.Done():
		return loop.Approval{}, context.Cause(ctx)
	}
}

// watched says whether an attended, privileged client is subscribed to the session: one a person can answer through. The caller holds s.mu.
func (s *session) watched() bool {
	for sub := range s.subs {
		if sub.conn.privileged && sub.conn.attended {
			return true
		}
	}
	return false
}

// endUnwatched ends the waiting question once no privileged client is subscribed. The caller holds s.mu.
func (s *session) endUnwatched() {
	if s.question == nil || s.question.settled || s.watched() {
		return
	}
	s.question.settled = true
	s.question.ch <- answer{err: errNoWatcher}
}

// respond is permission/respond (DESIGN §9.2): the user's answer to the waiting question, named by its
// permission.requested seq and id. The first answer wins; one to a question not waiting, or to another, is
// CodeNotPending.
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
		if !ap.Allow || !ap.Session {
			return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "permission/respond: an edited rule goes with decision allow and scope session"}
		}
		ap.Rule = p.Rule
	}
	if p.Seq <= 0 {
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "permission/respond: seq is the permission.requested event's"}
	}
	ap.Note = p.Note
	s, perr := c.session(p.Session)
	if perr != nil {
		return nil, perr
	}
	s.mu.Lock()
	q := s.question
	if q == nil || q.settled || q.seq != p.Seq || q.id != p.ID {
		s.mu.Unlock()
		return nil, &proto.Error{Code: proto.CodeNotPending, Message: "permission/respond: the question at seq " + strconv.FormatInt(p.Seq, 10) +
			" is not waiting: it was answered, asked again, or never asked"}
	}
	if ap.Rule != "" { // checked before the question is settled, so a refused edit leaves it open for another answer
		if _, err := perms.Narrowing(q.shown, ap.Rule); err != nil {
			s.mu.Unlock()
			// An edited rule that parses and reads back but widens what was shown (or names another program) counts toward the session's
			// hold; one that does not parse is as likely the owner's typo and only fails. The question stays open, and at the threshold the turn ends.
			if errors.Is(err, perms.ErrWidens) {
				if held, herr := s.loop.Hit(q.id, q.seq, "an edited rule: "+ap.Rule); herr == nil && held {
					s.mu.Lock()
					cancel := s.cancel
					s.mu.Unlock()
					if cancel != nil {
						cancel(loop.ErrHeld)
					}
				}
			}
			return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "permission/respond: " + err.Error()}
		}
	}
	q.settled = true
	s.mu.Unlock()
	q.ch <- answer{approval: ap}
	return proto.PermissionRespondResult{}, nil
}
