package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
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
	TokenName  = "client.token"
)

// Lock is the single-daemon lock: an exclusive flock on daemon.lock, held for the daemon's lifetime. The kernel drops
// it when the process exits, however it exits, so it cannot go stale. The socket and the token are made through it,
// so neither is touched by a process that does not hold it.
type Lock struct {
	dir string
	f   *os.File

	mu sync.Mutex
	ln *Listener // the socket Listen bound, closed by Release before the lock is dropped
}

// Holder is what daemon.lock records about the daemon holding it.
type Holder struct {
	PID     int       `json:"pid"`
	Version string    `json:"version"`
	Start   time.Time `json:"start"`
}

// HeldError is Acquire finding another daemon holding the lock. Holder is that daemon's record, zero when it has not
// written one yet.
type HeldError struct{ Holder Holder }

func (e *HeldError) Error() string {
	h := e.Holder
	if h.PID == 0 {
		return "another tack daemon holds the lock (it has not recorded its version yet)"
	}
	return fmt.Sprintf("tack daemon %s (pid %d, since %s) holds the lock", h.Version, h.PID, h.Start.Local().Format(time.DateTime))
}

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
			return nil, &HeldError{Holder: ReadHolder(stateDir)}
		}
		return nil, fmt.Errorf("daemon: locking %s: %w", path, err)
	}
	b, _ := json.Marshal(Holder{PID: os.Getpid(), Version: version, Start: time.Now().UTC().Truncate(time.Second)})
	err = f.Truncate(0)
	if err == nil {
		_, err = f.WriteAt(append(b, '\n'), 0)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("daemon: recording the holder in %s: %w", path, err)
	}
	return &Lock{dir: stateDir, f: f}, nil
}

// ReadHolder reads the record of the daemon holding (or that last held) the lock in stateDir; zero when there is none.
func ReadHolder(stateDir string) Holder {
	var h Holder
	b, _ := os.ReadFile(filepath.Join(stateDir, LockName))
	_ = json.Unmarshal(b, &h)
	return h
}

// Dir is the state directory the lock is in.
func (l *Lock) Dir() string { return l.dir }

// Release closes the socket Listen bound, then drops the lock. The order matters: closing the listener unlinks the
// socket path, and once the lock is dropped that path may already be the next daemon's.
func (l *Lock) Release() error {
	l.mu.Lock()
	ln := l.ln
	l.ln = nil
	l.mu.Unlock()
	var errs []error
	if ln != nil {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if l.f != nil {
		errs = append(errs, syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN), l.f.Close())
		l.f = nil
	}
	return errors.Join(errs...)
}

// privateDir makes dir (0700) if it is missing, and makes it 0700 if it is wider. It refuses a directory another user
// owns: tack's state is its user's alone.
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
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("daemon: %s belongs to uid %d, not to this user (uid %d)", dir, sys.Uid, os.Getuid())
	}
	if st.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
