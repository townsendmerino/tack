package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// The daemon's files in tack's state directory (DESIGN §5.3).
const (
	SocketName = "daemon.sock"
	LockName   = "daemon.lock"
	HolderName = "daemon.holder" // who holds the lock: written whole once it is taken, removed before it is dropped
	TokenName  = "client.token"
)

// Lock is the single-daemon lock: an exclusive flock on daemon.lock, held for the daemon's lifetime. The kernel drops
// it when the process exits, however it exits, so it cannot go stale. The socket and the token are made through it,
// so neither is touched by a process that does not hold it. Its methods are safe for concurrent use.
type Lock struct {
	dir string

	mu sync.Mutex // held by every method throughout, Release included
	f  *os.File   // nil once released
	ln *Listener  // the socket Listen bound, closed by Release before the lock is dropped
	// beforeUnlock, when set (tests), runs in Release just before the flock is dropped.
	beforeUnlock func()
}

// Holder is what daemon.holder records about the daemon holding the lock.
type Holder struct {
	PID     int       `json:"pid"`
	Version string    `json:"version"`
	Start   time.Time `json:"start"`
}

// HeldError is Acquire finding another daemon holding the lock. Holder is that daemon's record, zero when it is not
// known: the winner of a start has not written it yet, or what is on disk is stale.
type HeldError struct{ Holder Holder }

func (e *HeldError) Error() string {
	h := e.Holder
	if h.PID == 0 {
		return "another tack daemon holds the lock (it has not said which)"
	}
	return fmt.Sprintf("tack daemon %s (pid %d, since %s) holds the lock", h.Version, h.PID, h.Start.Local().Format(time.DateTime))
}

// currentUID is this process's uid, for privateDir; tests replace it.
var currentUID = os.Getuid

// Acquire takes the lock in stateDir for a daemon of version, never waiting: another holder is a *HeldError naming it.
// It makes stateDir if needed, and makes it 0700 if it was wider; a directory another user owns is refused.
func Acquire(stateDir, version string) (*Lock, error) {
	if err := privateDir(stateDir); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, LockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &HeldError{Holder: awaitHolder(stateDir)}
		}
		return nil, fmt.Errorf("daemon: locking %s: %w", path, err)
	}
	h := Holder{PID: os.Getpid(), Version: version, Start: time.Now().UTC().Truncate(time.Second)}
	if err := writeHolder(stateDir, h); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("daemon: recording the holder: %w", err)
	}
	return &Lock{dir: stateDir, f: f}, nil
}

// writeHolder writes the record whole, through a rename, so a reader sees all of it or none. Only the lock's holder
// writes it, so the temporary name is never shared.
func writeHolder(stateDir string, h Holder) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, HolderName)
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadHolder reads the record of the daemon holding the lock in stateDir. It is zero when there is none, and when the
// record's process is gone: a daemon that died without releasing leaves its record behind.
func ReadHolder(stateDir string) Holder {
	var h Holder
	b, err := os.ReadFile(filepath.Join(stateDir, HolderName))
	if err != nil || json.Unmarshal(b, &h) != nil || h.PID <= 0 || !alive(h.PID) {
		return Holder{}
	}
	return h
}

// awaitHolder is ReadHolder for a start that just lost: the winner writes its record right after it takes the lock,
// so an empty read is retried for a moment before the holder is reported unknown.
func awaitHolder(stateDir string) Holder {
	for range 10 {
		if h := ReadHolder(stateDir); h.PID != 0 {
			return h
		}
		time.Sleep(10 * time.Millisecond)
	}
	return Holder{}
}

// alive says whether pid names a running process (one this user may not signal counts).
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Dir is the state directory the lock is in.
func (l *Lock) Dir() string { return l.dir }

// Release closes the socket Listen bound, removes the holder record, then drops the lock. The order matters: closing
// the listener unlinks the socket path, and once the lock is dropped that path may already be the next daemon's, as
// may the record. Releasing twice is a no-op.
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	var errs []error
	if l.ln != nil {
		if err := l.ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
		l.ln = nil
	}
	if err := os.Remove(filepath.Join(l.dir, HolderName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	if l.beforeUnlock != nil {
		l.beforeUnlock()
	}
	errs = append(errs, syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN), l.f.Close())
	l.f = nil
	return errors.Join(errs...)
}

// privateDir makes dir (0700) if it is missing, and makes it 0700 if it is wider. It refuses a directory another user
// owns, changing nothing: tack's state is its user's alone.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("daemon: %s is not a directory", dir)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("daemon: cannot read the owner of %s", dir)
	}
	if int(sys.Uid) != currentUID() {
		return fmt.Errorf("daemon: %s belongs to uid %d, not to this user (uid %d)", dir, sys.Uid, currentUID())
	}
	if st.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
