package loop

import (
	"errors"
	"slices"
	"sync"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// The guard counts shell calls that have a shape with no honest use (perms.Engine.Tripwire; DESIGN §6.6, "The tripwire"). Each such call
// is refused with a plain message, recorded as tripwire.hit, and counted. At the owner's threshold the session is held: session.held is
// recorded, the turn ends, a prompt is refused until the owner releases the session. The count and the hold live in the log, so a
// reopened or forked session has them.

// ErrHeld is why a prompt is refused, and the cause a turn is cancelled with when the guard holds the session while it waits.
var ErrHeld = errors.New("the session is held for the owner's review")

// refusalText is all a model is told: it names nothing the guard caught.
const refusalText = "error: not run: it needs the owner's review"

// guard is the session's count and hold, under its own lock, since a question's refusal in core can count from another goroutine.
type guard struct {
	mu   sync.Mutex
	hits []string // the commands counted since the last release
	held bool
}

// Held reports whether the session is held.
func (s *Session) Held() bool {
	s.guard.mu.Lock()
	defer s.guard.mu.Unlock()
	return s.guard.held
}

// Hits is the commands counted since the last release.
func (s *Session) Hits() []string {
	s.guard.mu.Lock()
	defer s.guard.mu.Unlock()
	return append([]string(nil), s.guard.hits...)
}

// Hit counts one call: id is its tool call, command its line. It records tripwire.hit, and when the count reaches the limit it records
// session.held, and held is true: the caller ends the turn. With no limit set it counts nothing.
func (s *Session) Hit(id string, cause int64, command string) (held bool, err error) {
	if s.cfg.TripwireLimit <= 0 {
		return false, nil
	}
	s.guard.mu.Lock()
	defer s.guard.mu.Unlock()
	if s.guard.held {
		return true, nil // already held: more hits add nothing
	}
	n := len(s.guard.hits) + 1
	if _, err := s.record(proto.EvTripwireHit, cause, proto.TripwireHit{ID: id, Command: command, Count: n}); err != nil {
		return false, err
	}
	s.guard.hits = append(s.guard.hits, command)
	if n < s.cfg.TripwireLimit {
		return false, nil
	}
	if _, err := s.record(proto.EvSessionHeld, cause, proto.SessionHeld{Commands: append([]string(nil), s.guard.hits...)}); err != nil {
		return false, err
	}
	s.guard.held = true
	return true, nil
}

// Release lifts a hold and starts the count again. It records session.released, by who lifted it. A session that is not held records
// nothing.
func (s *Session) Release(by string) error {
	s.guard.mu.Lock()
	defer s.guard.mu.Unlock()
	if !s.guard.held {
		return nil
	}
	if _, err := s.append(proto.EvSessionReleased, 0, proto.SessionReleased{By: by}); err != nil {
		return err
	}
	s.guard.hits, s.guard.held = nil, false
	return nil
}

// restoreGuard rebuilds the count and the hold from a log's events: tripwire.hit adds, session.held holds, session.released clears.
func (g *guard) restore(p any) {
	switch e := p.(type) {
	case proto.TripwireHit:
		g.hits = append(g.hits, e.Command)
	case proto.SessionHeld:
		g.held = true
	case proto.SessionReleased:
		g.hits, g.held = nil, false
	}
}

// tripwire refuses a counted call: it records the hit, may hold the session, and returns the refused result, which says only that the call
// needs the owner's review.
func (s *Session) tripwire(b proto.ModelBlock, callSeq int64, command string) (*proto.ToolFinished, error) {
	if _, err := s.Hit(b.ID, callSeq, command); err != nil {
		return nil, err
	}
	if _, err := s.record(proto.EvPermissionResolved, callSeq, proto.PermissionResolved{ID: b.ID, Decision: proto.DecisionDeny, By: proto.DecidedByGuard,
		Reasons: []string{"it needs the owner's review"}}); err != nil {
		return nil, err
	}
	return &proto.ToolFinished{ID: b.ID, Outcome: proto.OutcomeDenied, Result: refusalText}, nil
}

// GuardState is the count and the hold a log's events leave.
func GuardState(evs []proto.Event) (hits []string, held bool) {
	var g guard
	for _, ev := range evs {
		if v, err := ev.Decode(); err == nil {
			g.restore(v)
		}
	}
	return g.hits, g.held
}

// GuardEvent is one event a fork records in its child to carry the parent's tripwire state.
type GuardEvent struct {
	Type    proto.EventType
	Payload any
}

// CarryGuard returns the events that make a fork's tripwire state the parent's whole current state, whatever seq the fork was cut at: a
// fork cut before the parent's first hit would otherwise start with no count, and an unheld child of a held session is a way out of the
// hold. The child's prefix may hold fewer hits (the missing ones are added) or more (a release in the parent after the cut: the child's
// are released and the parent's added). Nothing is returned when the two already agree.
func CarryGuard(parent, child []proto.Event) []GuardEvent {
	ph, pheld := GuardState(parent)
	ch, cheld := GuardState(child)
	var out []GuardEvent
	have := ch
	if len(ch) > len(ph) || !slices.Equal(ch, ph[:min(len(ch), len(ph))]) {
		out = append(out, GuardEvent{proto.EvSessionReleased, proto.SessionReleased{By: "fork"}})
		have, cheld = nil, false
	}
	for i := len(have); i < len(ph); i++ {
		out = append(out, GuardEvent{proto.EvTripwireHit, proto.TripwireHit{ID: "fork", Command: ph[i], Count: i + 1}})
	}
	if pheld && !cheld {
		out = append(out, GuardEvent{proto.EvSessionHeld, proto.SessionHeld{Commands: ph}})
	}
	return out
}

// CarryGuardTo records, in a fork just made, the events CarryGuard says: every way a session is forked (session/fork, tack rewind's
// fork) calls it, so none sheds the parent's count or hold.
func CarryGuardTo(st *store.Store, parent, child proto.SessionID) error {
	pe, err := st.Events(parent, 0)
	if err != nil {
		return err
	}
	ce, err := st.Events(child, 0)
	if err != nil {
		return err
	}
	add := CarryGuard(pe, ce)
	if len(add) == 0 {
		return nil
	}
	l, err := st.OpenLog(child)
	if err != nil {
		return err
	}
	defer l.Close()
	for _, e := range add {
		if _, err := l.Append(e.Type, 0, e.Payload); err != nil {
			return err
		}
	}
	return nil
}
