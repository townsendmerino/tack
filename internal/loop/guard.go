package loop

import (
	"errors"
	"sync"

	"github.com/townsendmerino/tack/internal/proto"
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
