package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	// holderMu guards the record: rec is what was written, gone that Release removed it.
	holderMu sync.Mutex
	rec      Holder
	gone     bool
	// step, when set (tests), is told each of Release's steps as it completes: "holder removed", "socket closed",
	// then "unlocking", just before the flock is dropped.
	step func(string)
}

// Holder is what daemon.holder records about the daemon holding the lock.
type Holder struct {
	PID     int       `json:"pid"`
	Version string    `json:"version"`
	Start   time.Time `json:"start"`
	// ProcStart is when the process with PID started (procInfo), so a record whose pid a later process reused names
	// nobody.
	ProcStart string `json:"proc_start,omitempty"`
	// Stopping says the daemon is shutting down: its socket is gone or going, and the lock will follow. A start that
	// finds it waits for the lock instead of giving up (takeLock).
	Stopping bool `json:"stopping,omitempty"`
}

// HeldError is Acquire finding another daemon holding the lock. Holder is that daemon's record, zero when it is not
// known: the winner of a start has not written it yet, or what is on disk is stale.
type HeldError struct{ Holder Holder }

func (e *HeldError) Error() string {
	h := e.Holder
	if h.PID == 0 {
		return "another tack daemon holds the lock (it has not said which)"
	}
	if h.Stopping {
		return fmt.Sprintf("tack daemon %s (pid %d) is shutting down and still holds the lock after %s", h.Version, h.PID, stopWait)
	}
	return fmt.Sprintf("tack daemon %s (pid %d, since %s) holds the lock", h.Version, h.PID, h.Start.Local().Format(time.DateTime))
}

// currentUID is this process's uid, for privateDir; tests replace it.
var currentUID = os.Getuid

// afterLock, when set (tests), runs in Acquire between taking the lock and writing the holder record.
var afterLock func()

// Acquire takes the lock in stateDir for a daemon of version. A holder that has recorded itself is a *HeldError naming
// it at once; while the lock is held with no valid record, a daemon starting or stopping, it is tried again for up to
// a second (takeLock). stateDir is made if needed and checked first (privateDir).
func Acquire(stateDir, version string) (*Lock, error) {
	if err := privateDir(stateDir); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, LockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil { // a lock file left wider by hand
		f.Close()
		return nil, err
	}
	if err := takeLock(f, stateDir); err != nil {
		f.Close()
		return nil, err
	}
	if afterLock != nil {
		afterLock()
	}
	ps, _, _ := procInfo(os.Getpid()) // empty where unsupported: then ReadHolder trusts no record
	h := Holder{PID: os.Getpid(), Version: version, Start: time.Now().UTC().Truncate(time.Second), ProcStart: ps}
	if err := writeHolder(stateDir, h); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("daemon: recording the holder: %w", err)
	}
	return &Lock{dir: stateDir, f: f, rec: h}, nil
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
// process that wrote it is gone or a zombie: a daemon that died without releasing leaves its record behind, and its
// pid may since name another process, so the record's process start time must match.
//
// It does not probe daemon.lock: taking the lock even for a moment would make a daemon starting then find it held and
// exit, leaving none.
func ReadHolder(stateDir string) Holder {
	var h Holder
	b, err := os.ReadFile(filepath.Join(stateDir, HolderName))
	if err != nil || json.Unmarshal(b, &h) != nil || h.PID <= 0 || h.ProcStart == "" {
		return Holder{}
	}
	if ps, live, err := procInfo(h.PID); err != nil || !live || ps != h.ProcStart {
		return Holder{}
	}
	return h
}

// takeLock takes the exclusive flock on f, the lock file in stateDir, without blocking. Held by a daemon that has
// recorded itself, it is a *HeldError naming that daemon at once. Held with no valid record, the holder is starting
// (it writes its record right after taking the lock) or stopping (Release removes the record, then unlocks), so the
// lock is tried again for up to a second: a stopping holder's lock is taken once dropped, and a starting one's record
// read once written. Past that it is a *HeldError naming nobody.
func takeLock(f *os.File, stateDir string) error {
	start := time.Now()
	deadline := start.Add(time.Second)
	var last Holder
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("daemon: locking %s: %w", f.Name(), err)
		}
		h := ReadHolder(stateDir)
		switch {
		case h.PID != 0 && !h.Stopping:
			return &HeldError{Holder: h}
		case h.Stopping && !last.Stopping: // a shutdown: wait for it, through its drain
			deadline = start.Add(stopWait)
		}
		if h.PID != 0 {
			last = h
		}
		if time.Now().After(deadline) {
			return &HeldError{Holder: last}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stopWait is how long a start waits for a stopping daemon to release the lock: its drain's grace window and then
// some.
const stopWait = time.Minute

// Dir is the state directory the lock is in.
func (l *Lock) Dir() string { return l.dir }

// Release removes the holder record, closes the socket Listen bound, then drops the lock. The order matters:
//   - the record goes first, so a client that finds the socket gone and starts a daemon finds no holder named, and
//     that start waits for the lock (takeLock) instead of naming this daemon and giving up;
//   - the socket goes before the lock, since closing the listener unlinks its path, and once the lock is dropped that
//     path may already be the next daemon's.
//
// Releasing twice is a no-op.
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	var errs []error
	l.holderMu.Lock()
	if err := os.Remove(filepath.Join(l.dir, HolderName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	l.gone = true
	l.holderMu.Unlock()
	l.tell("holder removed")
	if l.ln != nil {
		if err := l.ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
		l.ln = nil
	}
	l.tell("socket closed")
	l.tell("unlocking")
	errs = append(errs, syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN), l.f.Close())
	l.f = nil
	return errors.Join(errs...)
}

// markStopping rewrites the record to say the daemon is stopping, unless Release has removed it. Closing the listener
// calls it, before the socket is unlinked, so a start that finds the socket gone waits for the lock.
func (l *Lock) markStopping() {
	l.holderMu.Lock()
	defer l.holderMu.Unlock()
	if l.gone || l.rec.Stopping {
		return
	}
	l.rec.Stopping = true
	_ = writeHolder(l.dir, l.rec) // at worst a start sees the record as running and names it
}

// tell reports a Release step to the test hook, if any.
func (l *Lock) tell(step string) {
	if l.step != nil {
		l.step(step)
	}
}

// privateDir makes dir (0700) if it is missing, and narrows it to 0700 if others may read or search it. It refuses,
// changing nothing, a directory others own or may write, or one under a path they could redirect (checkPath): what
// they may already have put there is not tack's to trust, so it is not narrowed. tack's state is its user's alone.
func privateDir(dir string) error {
	if slices.Contains(strings.Split(dir, string(filepath.Separator)), "..") {
		return fmt.Errorf("daemon: the state directory %s holds \"..\"; name it without: after a symlink the kernel and a path cleaned as text resolve \"..\" differently", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := checkPath(abs); err != nil { // before MkdirAll, which follows symlinks
		return err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return err
	}
	if err := checkPath(abs); err != nil { // and after, now that every component exists
		return err
	}
	st, err := os.Stat(abs) // a directory: MkdirAll fails on anything else
	if err != nil {
		return err
	}
	if uid := ownerOf(abs, st); uid != currentUID() {
		return fmt.Errorf("daemon: %s belongs to uid %d, not to this user (uid %d)", dir, uid, currentUID())
	}
	if st.Mode().Perm() != 0o700 {
		return os.Chmod(abs, 0o700)
	}
	return nil
}

// checkPath refuses a path another user could redirect to a directory of theirs. It walks the path as the kernel
// resolves it, one component at a time from /, following each symlink where it is met: every directory passed through
// must be this user's or root's and not writable by others unless it is sticky, as /tmp is (sshd's StrictModes is the
// model), and every symlink must be this user's or root's. A component that does not exist yet ends the walk:
// privateDir makes the rest, as this user's.
func checkPath(abs string) error {
	trusted := func(uid int) bool { return uid == currentUID() || uid == 0 }
	checkDir := func(p string, fi fs.FileInfo) error {
		if uid := ownerOf(p, fi); !trusted(uid) {
			return fmt.Errorf("daemon: %s belongs to uid %d; tack's state may lie only under this user's or root's directories", p, uid)
		}
		if fi.Mode().Perm()&0o022 != 0 && fi.Mode()&fs.ModeSticky == 0 {
			return fmt.Errorf("daemon: %s is writable by other users, who could replace tack's state under it; run chmod go-w %s, or set state_dir elsewhere", p, p)
		}
		return nil
	}
	root, err := os.Lstat("/")
	if err != nil {
		return err
	}
	if err := checkDir("/", root); err != nil {
		return err
	}
	cur, rest, links := "/", components(abs), 0
	for len(rest) > 0 {
		name := rest[0]
		rest = rest[1:]
		if name == ".." {
			cur = filepath.Dir(cur) // cur is resolved, so its parent is the physical one
			continue
		}
		next := filepath.Join(cur, name)
		fi, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			if uid := ownerOf(next, fi); !trusted(uid) {
				return fmt.Errorf("daemon: %s is a symlink owned by uid %d; tack's state may be reached only through this user's or root's", next, uid)
			}
			if links++; links > 40 {
				return fmt.Errorf("daemon: %s: too many levels of symlinks", abs)
			}
			target, err := os.Readlink(next)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				cur = "/"
			}
			rest = append(components(target), rest...)
		case fi.IsDir():
			if err := checkDir(next, fi); err != nil {
				return err
			}
			cur = next
		case len(rest) > 0:
			return fmt.Errorf("daemon: %s is not a directory", next)
		default:
			return nil // the last component is a file; privateDir says so
		}
	}
	return nil
}

// components splits a path into its names, dropping empty ones and ".", keeping "..".
func components(p string) []string {
	var out []string
	for _, c := range strings.Split(p, string(filepath.Separator)) {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}

// ownerOf is the uid owning the file fi describes, -1 if the platform does not say; tests replace it, since a test
// cannot make a file another user owns.
var ownerOf = func(_ string, fi fs.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}
