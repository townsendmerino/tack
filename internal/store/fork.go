package store

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/townsendmerino/tack/internal/proto"
)

var (
	// ErrForkSeq is a fork cut at a seq the parent's log does not have.
	ErrForkSeq = errors.New("store: no such seq in the session's log")
	// ErrForkInTurn is a fork cut inside a turn, where the history would end on a user message or a tool call with no result.
	ErrForkInTurn = errors.New("store: the cut falls inside a turn")
)

// Fork makes the session child from the first atSeq events of parent (DESIGN §6.2). The child's log holds those events
// byte for byte, with the same seqs and times, except that its session.created also names the fork (ForkOf, ForkAt), so a
// reader that counts usage knows the copied events are not the child's own. The blobs they name are shared by hash. The
// child then opens and appends like any session. The cut must leave the history whole: not inside a turn (Inspect). The
// parent's log is only read, and a failed fork leaves no child.
func (s *Store) Fork(parent, child proto.SessionID, atSeq int64) error {
	evs, err := s.Events(parent, 0)
	if err != nil {
		return err
	}
	if atSeq < 1 || atSeq > int64(len(evs)) || evs[atSeq-1].Seq != atSeq {
		return fmt.Errorf("%w: %d", ErrForkSeq, atSeq)
	}
	prefix := evs[:atSeq]
	if st := Inspect(prefix); st.InTurn || len(st.UnknownTools) > 0 {
		return fmt.Errorf("%w: %d", ErrForkInTurn, atSeq)
	}
	v, err := prefix[0].Decode()
	if err != nil {
		return err
	}
	created, ok := v.(proto.SessionCreated)
	if !ok {
		return fmt.Errorf("store: session %s does not start with session.created", parent)
	}
	created.ForkOf, created.ForkAt = parent, atSeq
	first, err := proto.NewEvent(prefix[0].Seq, prefix[0].Time, proto.EvSessionCreated, prefix[0].Cause, created)
	if err != nil {
		return err
	}
	l, err := s.Create(child)
	if err != nil {
		return err
	}
	for i, ev := range prefix {
		if i == 0 {
			ev = first
		}
		if err := l.appendVerbatim(ev); err != nil {
			return errors.Join(err, s.Discard(l))
		}
	}
	// Close fsyncs what the appends left unsynced (ADR-004); a log that could not be made durable is not left behind.
	if err := l.Close(); err != nil {
		return errors.Join(err, s.Discard(l))
	}
	return nil
}

// appendVerbatim writes ev as it is, seq and time included, which must be the next seq. It does not sync; Close does.
func (l *Log) appendVerbatim(ev proto.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return errors.New("store: log is closed")
	}
	if ev.Seq != l.lastSeq+1 {
		return fmt.Errorf("store: event %d is not next after %d", ev.Seq, l.lastSeq)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := l.f.Write(append(b, '\n')); err != nil {
		return err
	}
	l.lastSeq, l.unsynced = ev.Seq, true
	return nil
}
