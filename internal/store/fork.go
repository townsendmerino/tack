package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

var (
	// ErrForkSeq is a fork cut at a seq the parent's log does not have.
	ErrForkSeq = errors.New("store: no such seq in the session's log")
	// ErrForkInTurn is a fork cut inside a turn, where the history would end on a user message or a tool call with no result.
	ErrForkInTurn = errors.New("store: the cut falls inside a turn")
	// ErrForkNotDurable is a fork that was made whole and is findable, but whose directory entry could not be fsynced after retries: the
	// child exists and is complete, and may be gone after a power loss. A caller treats it as made, with a warning.
	ErrForkNotDurable = errors.New("store: the fork was made but its directory entry is not known to be durable")
)

// forkLockName is the file a fork holds a lock on in its temporary directory while it runs, so a sweep can tell a fork that is alive,
// even a stalled one, from one that died (a lock dies with its process).
const forkLockName = ".lock"

// Fork makes the session child from the first atSeq events of parent (DESIGN §6.2). The child's log holds those events
// byte for byte, with the same seqs and times, except that its session.created also names the fork (ForkOf, ForkAt), so a
// reader that counts usage knows the copied events are not the child's own. The blobs they name are shared by hash. The
// child then opens and appends like any session. The cut must leave the history whole: not inside a turn (Inspect). The
// parent's log is only read, and a failed fork leaves no child.
func (s *Store) Fork(parent, child proto.SessionID, atSeq int64) error {
	return s.ForkCarrying(parent, child, atSeq, nil)
}

// ForkEvent is an event a fork writes after the copied prefix, from the parent's whole log and not its cut: state that must not
// be shed by cutting early, such as the tripwire's count and hold (loop.CarryGuard).
type ForkEvent struct {
	Type    proto.EventType
	Payload any
}

// forkTempPrefix starts the name of the directory a fork is written in. It is not a session id (those start "s_"), so Sessions skips
// it, and logPath refuses it, so no reader can open, read or reopen it by name.
const forkTempPrefix = ".fork-"

// staleForkTemp is how old a fork's temporary directory must be before the next fork may remove it, which it does only if no fork holds
// the directory's lock (forkLockName): the age keeps a sweep off a directory made a moment ago and not yet locked, and the lock keeps it
// off one whose fork is alive, however long it stalls.
const staleForkTemp = time.Hour

// ForkCarrying is Fork that also writes the events carry returns, given the parent's whole log and the copied prefix, after the
// prefix with the next seqs.
//
// The child is made whole before it exists. Every event is built, and so checked, first; then the whole log is written to a
// temporary directory in the sessions directory, fsynced, and the directory renamed to the child's name and the sessions directory
// fsynced. Until the rename no reader can find the child (Sessions, Events, Reopen all go by the session id), and after it the child
// is complete, so there is no moment, and no point a crash can stop at, at which a fork is findable without what it must carry. A
// failure removes the temporary directory; one that cannot be removed is left under a name nothing reads, and the next fork removes
// it once it is an hour old.
//
// The parent is read once, without its lock (a running parent holds it for appending): the fork carries the parent's state as of
// that read, and an event the parent appends after it is not in the child. A nil carry carries nothing.
func (s *Store) ForkCarrying(parent, child proto.SessionID, atSeq int64, carry func(parent, prefix []proto.Event) []ForkEvent) error {
	final, err := s.logPath(child)
	if err != nil {
		return err
	}
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
	all := append([]proto.Event(nil), prefix...)
	all[0] = first
	if carry != nil {
		for _, x := range carry(evs, prefix) {
			ev, err := proto.NewEvent(int64(len(all))+1, s.now(), x.Type, 0, x.Payload)
			if err != nil {
				return fmt.Errorf("store: fork of %s: carried %s: %w", parent, x.Type, err)
			}
			all = append(all, ev)
		}
	}
	lines := make([][]byte, len(all))
	for i, ev := range all {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		lines[i] = append(b, '\n')
	}

	sessions := filepath.Dir(filepath.Dir(final))
	if err := mkdirAllSynced(sessions, 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Dir(final)); err == nil {
		return fmt.Errorf("store: session %s exists", child)
	}
	sweepForkTemps(sessions, time.Now()) // the file system's mtimes are real time, whatever clock the store stamps events with
	tmp, err := os.MkdirTemp(sessions, forkTempPrefix+string(child)+"-")
	if err != nil {
		return err
	}
	// The fork holds a lock in its temp for as long as it runs, so a sweep never takes a live fork's directory, however long it stalls.
	lockPath := filepath.Join(tmp, forkLockName)
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err == nil {
		err = lockLog(lock)
	}
	if err != nil {
		if lock != nil {
			lock.Close()
		}
		return errors.Join(err, removeAll(tmp))
	}
	if err := writeForkLog(tmp, lines); err != nil {
		lock.Close()
		return errors.Join(err, removeAll(tmp))
	}
	// The lock file goes before the rename, so the child's directory holds only its log.
	if err := os.Remove(lockPath); err != nil {
		lock.Close()
		return errors.Join(err, removeAll(tmp))
	}
	lock.Close()
	if err := os.Rename(tmp, filepath.Dir(final)); err != nil {
		return errors.Join(err, removeAll(tmp))
	}
	// The child's directory entry (ADR-004); its log is already durable. The child is complete and findable from the rename on, so a
	// failure here is retried, and one that lasts is reported as ErrForkNotDurable, not as a fork that was not made.
	var serr error
	for i := 0; i < 3; i++ {
		if serr = syncDir(sessions); serr == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 20 * time.Millisecond)
	}
	return fmt.Errorf("%w: %v", ErrForkNotDurable, serr)
}

// writeForkLog writes lines as events.jsonl in dir, fsyncs the file and then the directory.
func writeForkLog(dir string, lines [][]byte) error {
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	for i, b := range lines {
		if _, err := f.Write(b); err != nil {
			f.Close()
			return err
		}
		if forkWriteHook != nil {
			if err := forkWriteHook(i + 1); err != nil {
				f.Close()
				return err
			}
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(dir)
}

// sweepForkTemps removes the temporary directories of forks that died, those older than staleForkTemp. It is best effort: what it
// cannot remove stays, invisible.
func sweepForkTemps(sessions string, now time.Time) {
	ents, err := os.ReadDir(sessions)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), forkTempPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= staleForkTemp {
			continue
		}
		dir := filepath.Join(sessions, e.Name())
		if afterSweepStat != nil {
			afterSweepStat(dir)
		}
		// A fork that is still running holds its lock; one that died does not, or never made the file.
		if lock, err := os.OpenFile(filepath.Join(dir, forkLockName), os.O_RDWR, 0); err == nil {
			busy := lockLog(lock) != nil
			lock.Close()
			if busy {
				continue
			}
		} else if again, err := os.Stat(dir); err != nil || now.Sub(again.ModTime()) <= staleForkTemp {
			// No lock file: the fork died, or it is finishing, since it removes the file just before it renames the directory, and that
			// changes the directory's mtime. Look at the mtime again, so a fork stalled for hours that finishes now is not swept.
			continue
		}
		_ = removeAll(dir)
	}
}

// removeAll removes a directory tree; a variable so a test can make it fail, as a crash or a full disk would, and see what is left.
var removeAll = os.RemoveAll

// forkWriteHook, when set by a test, is called after each of a fork's lines is written, with how many have been, and may fail the
// fork there: the same as a crash at that point, except that the process goes on.
var forkWriteHook func(written int) error

// afterSweepStat, when set by a test, is called with a temp directory between the sweep's look at its age and its attempt to open the
// lock, where a fork that is finishing removes its lock file.
var afterSweepStat func(dir string)
