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
	"sync"
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
		if err := os.MkdirAll(d, 0o700); err != nil {
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
	sync func(*os.File) error
	onSync func(proto.EventType, time.Duration)
}

func (s *Store) logPath(id proto.SessionID) string {
	return filepath.Join(s.root, "sessions", string(id), "events.jsonl")
}

// Create starts a new session's log. It fails if the session exists.
func (s *Store) Create(id proto.SessionID) (*Log, error) {
	p := s.logPath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Log{id: id, f: f, now: s.now, sync: (*os.File).Sync, onSync: s.OnSync}, nil
}

// OpenLog reopens an existing session's log for appending. A torn last line (a crash mid-append, so no trailing
// newline) is cut off; a malformed line anywhere else is an error, since the log is the truth and must not be guessed
// at.
func (s *Store) OpenLog(id proto.SessionID) (*Log, error) {
	p := s.logPath(id)
	f, err := os.OpenFile(p, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	evs, good, err := readEvents(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("store: %s: %w", p, err)
	}
	if st, _ := f.Stat(); st != nil && st.Size() != good {
		if err := f.Truncate(good); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
	}
	l := &Log{id: id, f: f, now: s.now, sync: (*os.File).Sync, onSync: s.OnSync}
	if n := len(evs); n > 0 {
		l.lastSeq = evs[n-1].Seq
	}
	return l, nil
}

// Events reads a session's persisted events with seq ≥ fromSeq. A torn last line is ignored.
func (s *Store) Events(id proto.SessionID, fromSeq int64) ([]proto.Event, error) {
	f, err := os.Open(s.logPath(id))
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
