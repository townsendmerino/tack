package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// Store is tack's state directory: sessions/<id>/events.jsonl and the blob store (DESIGN §5.3).
type Store struct {
	root  string
	Blobs *Blobs
	// OnSync, when set, is told how long each fsync Append makes took, and for which event type. Set it before creating
	// or opening logs.
	OnSync func(typ proto.EventType, took time.Duration)
	// now stamps events; tests replace it.
	now func() time.Time
}

// Open opens (creating if needed) the store rooted at dir. dir is created 0700: nothing in it is for other users.
func Open(dir string) (*Store, error) {
	for _, d := range []string{dir, filepath.Join(dir, "sessions"), filepath.Join(dir, "blobs")} {
		if err := mkdirAllSynced(d, 0o700); err != nil {
			return nil, err
		}
	}
	return &Store{root: dir, Blobs: &Blobs{dir: filepath.Join(dir, "blobs")}, now: time.Now}, nil
}

// Root is the store's directory.
func (s *Store) Root() string { return s.root }

// Log is one session's append-only event log. One Log per session per process: the daemon is its only writer. Its
// methods are safe for concurrent use.
type Log struct {
	mu      sync.Mutex
	id      proto.SessionID
	f       *os.File
	lastSeq int64
	now     func() time.Time
	// sync makes the file durable; tests count calls through it.
	sync   func(*os.File) error
	onSync func(proto.EventType, time.Duration)
}

func (s *Store) logPath(id proto.SessionID) (string, error) {
	if !ValidSessionID(id) {
		return "", fmt.Errorf("store: %q is not a session id", id)
	}
	return filepath.Join(s.root, "sessions", string(id), "events.jsonl"), nil
}

// lockLog takes the writer's lock on an open log: an exclusive flock, dropped when the file is closed.
func lockLog(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLogBusy
		}
		return fmt.Errorf("store: locking %s: %w", f.Name(), err)
	}
	return nil
}

// Create starts a new session's log and holds its writer's lock until Close. It fails if the session exists.
func (s *Store) Create(id proto.SessionID) (*Log, error) {
	p, err := s.logPath(id)
	if err != nil {
		return nil, err
	}
	if err := mkdirAllSynced(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockLog(f); err != nil {
		f.Close()
		return nil, err
	}
	if err := syncDir(filepath.Dir(p)); err != nil { // the new log's directory entry (ADR-004)
		f.Close()
		return nil, err
	}
	return &Log{id: id, f: f, now: s.now, sync: (*os.File).Sync, onSync: s.OnSync}, nil
}

// OpenLog is Reopen without the events.
func (s *Store) OpenLog(id proto.SessionID) (*Log, error) {
	l, _, err := s.Reopen(id)
	return l, err
}

// Reopen reopens an existing session's log for appending, holding its writer's lock until Close, and returns its
// events, read once under that lock: a log another Log has open, in this process or another, is ErrLogBusy, and
// nothing is read or cut. A torn last line (a crash mid-append, so no trailing
// newline) is cut off; a malformed line anywhere else is an error, since the log is the truth and must not be guessed
// at.
func (s *Store) Reopen(id proto.SessionID) (*Log, []proto.Event, error) {
	p, err := s.logPath(id)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if err := lockLog(f); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%w: %s", err, id)
	}
	evs, good, err := readEvents(f)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("store: %s: %w", p, err)
	}
	if st, _ := f.Stat(); st != nil && st.Size() != good {
		if err := f.Truncate(good); err != nil {
			f.Close()
			return nil, nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, nil, err
		}
	}
	l := &Log{id: id, f: f, now: s.now, sync: (*os.File).Sync, onSync: s.OnSync}
	if n := len(evs); n > 0 {
		l.lastSeq = evs[n-1].Seq
	}
	return l, evs, nil
}

// Discard closes a log Create made and removes its session, for a session that could not be made after all. The
// writer's lock is held until the files are gone.
func (s *Store) Discard(l *Log) error {
	p, err := s.logPath(l.id)
	if err != nil {
		return err
	}
	err = os.RemoveAll(filepath.Dir(p))
	return errors.Join(err, l.Close())
}

// Events reads a session's persisted events with seq ≥ fromSeq. A torn last line is ignored.
func (s *Store) Events(id proto.SessionID, fromSeq int64) ([]proto.Event, error) {
	p, err := s.logPath(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	evs, _, err := readEvents(f)
	if err != nil {
		return nil, err
	}
	i := 0
	for i < len(evs) && evs[i].Seq < fromSeq {
		i++
	}
	return evs[i:], nil
}

// readEvents parses a log from the start. good is the byte length of its complete lines; anything after it is a torn
// tail.
func readEvents(r io.Reader) (evs []proto.Event, good int64, err error) {
	br := bufio.NewReaderSize(r, 1<<16)
	var want int64 = 1
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			var ev proto.Event
			dec := json.NewDecoder(bytes.NewReader(line))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&ev); err != nil {
				return nil, 0, fmt.Errorf("line %d: %w", want, err)
			}
			if ev.Seq != want {
				return nil, 0, fmt.Errorf("line %d has seq %d: the log's seqs must be dense from 1", want, ev.Seq)
			}
			evs = append(evs, ev)
			good += int64(len(line))
			want++
		}
		if rerr == io.EOF {
			return evs, good, nil
		}
		if rerr != nil {
			return nil, 0, rerr
		}
	}
}

// Append records an event of type typ answering cause (0 for none), with payload of typ's registered type, and returns
// it. A persisted event gets the next seq and is written; when its type requires it (proto.EventSpec.Fsync: a turn
// boundary, or tool.started, so it is durable before the tool runs) the file is fsynced before Append returns. A
// live-only event gets seq 0 and is not written.
func (l *Log) Append(typ proto.EventType, cause int64, payload any) (proto.Event, error) {
	spec, ok := proto.EventTypes[typ]
	if !ok {
		return proto.Event{}, fmt.Errorf("store: unknown event type %q", typ)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !spec.Persisted {
		return proto.NewEvent(0, l.now(), typ, cause, payload)
	}
	if l.f == nil {
		return proto.Event{}, errors.New("store: log is closed")
	}
	ev, err := proto.NewEvent(l.lastSeq+1, l.now(), typ, cause, payload)
	if err != nil {
		return proto.Event{}, err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return proto.Event{}, err
	}
	if _, err := l.f.Write(append(b, '\n')); err != nil {
		return proto.Event{}, err
	}
	l.lastSeq = ev.Seq
	if spec.Fsync {
		t0 := time.Now()
		if err := l.sync(l.f); err != nil {
			return proto.Event{}, err
		}
		if l.onSync != nil {
			l.onSync(typ, time.Since(t0))
		}
	}
	return ev, nil
}

// Head is the seq of the last persisted event, 0 for an empty log.
func (l *Log) Head() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastSeq
}

// ID is the session the log belongs to.
func (l *Log) ID() proto.SessionID { return l.id }

// Close fsyncs and closes the log.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.sync(l.f)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

// Interrupted is what a log says about a turn a crash cut short (DESIGN §6.1).
type Interrupted struct {
	InTurn       bool     // the last turn has a user.message and no turn.end
	UnknownTools []string // tool calls with tool.started and no tool.finished: outcome unknown, never re-run
}

// Inspect reads crash state from a session's events.
func Inspect(evs []proto.Event) Interrupted {
	var st Interrupted
	started := map[string]bool{}
	var order []string
	for _, ev := range evs {
		switch ev.Type {
		case proto.EvUserMessage:
			st.InTurn = true
			started, order = map[string]bool{}, nil
		case proto.EvTurnEnd:
			st.InTurn = false
			started, order = map[string]bool{}, nil
		case proto.EvToolStarted, proto.EvToolFinished:
			var p struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(ev.Payload, &p)
			if ev.Type == proto.EvToolStarted {
				started[p.ID] = true
				order = append(order, p.ID)
			} else {
				delete(started, p.ID)
			}
		}
	}
	for _, id := range order {
		if started[id] {
			st.UnknownTools = append(st.UnknownTools, id)
		}
	}
	return st
}

// ErrLogBusy is OpenLog finding the session's log open for appending in another Log, in this process or another.
var ErrLogBusy = errors.New("store: the session's log is open in another tack process")

// Sessions names every session in the store that has a log, in id order: creation order, since ids are ULIDs.
func (s *Store) Sessions() ([]proto.SessionID, error) {
	ents, err := os.ReadDir(filepath.Join(s.root, "sessions"))
	if err != nil {
		return nil, err
	}
	var ids []proto.SessionID
	for _, e := range ents {
		id := proto.SessionID(e.Name())
		if !e.IsDir() || !ValidSessionID(id) {
			continue
		}
		if p, _ := s.logPath(id); fileExists(p) {
			ids = append(ids, id)
		}
	}
	return ids, nil // ReadDir sorts by name
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ValidSessionID says whether id can name a session: "s_" then 1 to 62 ASCII letters and digits. Ids come from
// clients and become paths, so an id is one path element and cannot leave the store.
func ValidSessionID(id proto.SessionID) bool {
	rest, ok := strings.CutPrefix(string(id), "s_")
	if !ok || rest == "" || len(rest) > 62 {
		return false
	}
	for _, r := range rest {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}
	return true
}
