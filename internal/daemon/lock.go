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

// afterLock, when set (tests), runs in Acquire between taking the lock and writing the holder record.
var afterLock func()

// Acquire takes the lock in stateDir for a daemon of version, or returns a *HeldError naming the daemon that holds it:
// at once when that daemon is running, after waiting for it when it is starting or shutting down (takeLock). stateDir
// is made if needed and checked first (privateDir).
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

// takeLock takes the exclusive flock on f, the lock file in stateDir, without blocking, and tries again while the
// holder may let go soon:
//   - held by a daemon whose record says it is running, it is a *HeldError naming that daemon at once;
//   - held by one whose record says it is stopping (its listener has closed, Listener.Close), it is tried again for up
//     to stopWait, the drain's grace window and then some, and taken once released;
//   - held with no valid record, the holder is starting (it records itself right after taking the lock) or nearly
//     done stopping (Release removed the record), so it is tried again for up to a second.
//
// Past its wait it is a *HeldError naming the last holder seen, or nobody.
func takeLock(f *os.File, stateDir string) error {
	start := time.Now()
	deadline := start.Add(time.Second)
	var last Holder
	busy := false
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("daemon: locking %s: %w", f.Name(), err)
		}
		if !busy && lockBusy != nil {
			lockBusy()
		}
		busy = true
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

// lockBusy, when set (tests), is told the first time a takeLock finds the lock held.
var lockBusy func()

// stopWait is how long a start waits for a stopping daemon to release the lock: its drain's grace window and then
// some.
const stopWait = time.Minute

// Dir is the state directory the lock is in.
func (l *Lock) Dir() string { return l.dir }

// Release removes the holder record, closes the socket Listen bound, then drops the lock. The order matters:
//   - the record goes first, so a client that finds the socket gone and starts a daemon finds no holder named, and
//     that start waits for the lock (takeLock) instead of naming this daemon and giving up. (When the socket closed
//     earlier, as Serve closes it, the record already says stopping: Listener.Close.)
//   - the socket goes before the lock, since closing it unlinks its path, and once the lock is dropped that path may
//     already be the next daemon's.
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
		// The raw socket, not the Listener: the record is gone already, so nothing is to be marked stopping.
		if err := l.ln.ul.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
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
//
// What is missing is made where the path resolves, through a symlink whose target does not exist yet too, one
// directory at a time (makeDirs), so a directory planted meanwhile in a shared parent is refused before anything is
// made in it.
func privateDir(dir string) error {
	if slices.Contains(strings.Split(dir, string(filepath.Separator)), "..") {
		return fmt.Errorf("daemon: the state directory %s holds \"..\"; name it without: after a symlink the kernel and a path cleaned as text resolve \"..\" differently", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	resolved, err := checkPath(abs)
	if err != nil {
		return err
	}
	if afterCheck != nil {
		afterCheck()
	}
	if err := makeDirs(resolved); err != nil {
		return err
	}
	if _, err := checkPath(abs); err != nil { // now that every directory exists, the state directory's own rules apply
		return err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if st.Mode().Perm() != 0o700 {
		return os.Chmod(abs, 0o700)
	}
	return nil
}

// afterCheck, when set (tests), runs in privateDir between the first check and the making of what is missing.
var afterCheck func()

// makeDirs makes the directories of p, a path checkPath resolved, that do not exist yet, one at a time: each with
// Mkdir, which never follows a symlink already there, and each Lstat'ed as soon as it is made or found, so that a
// symlink or another user's directory planted after checkPath is refused before anything is made in it.
func makeDirs(p string) error {
	cur := "/"
	for _, name := range components(p) {
		next := filepath.Join(cur, name)
		if err := os.Mkdir(next, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		fi, err := os.Lstat(next)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0: // checkPath resolved every link on p, so this one came after it
			return fmt.Errorf("daemon: %s became a symlink, owned by uid %d, while tack made its state directory", next, ownerOf(next, fi))
		case !fi.IsDir():
			return fmt.Errorf("daemon: %s is not a directory", next)
		}
		if err := checkDir(next, fi, false); err != nil {
			return err
		}
		cur = next
	}
	return nil
}

// checkDir refuses a directory another user could use to replace tack's state. One on the way to the state directory
// must be this user's or root's, and not writable by others unless it is sticky; the state directory itself (leaf)
// must be this user's, and not writable by others even if sticky: the sticky bit keeps others from replacing what a
// shared directory holds, so it may hold the state directory, but not from adding files, so it may not be one.
func checkDir(p string, fi fs.FileInfo, leaf bool) error {
	if uid := ownerOf(p, fi); uid != os.Getuid() && (leaf || uid != 0) {
		return fmt.Errorf("daemon: %s belongs to uid %d; tack's state must be this user's, under this user's or root's directories", p, uid)
	}
	if fi.Mode().Perm()&0o022 != 0 && (leaf || fi.Mode()&fs.ModeSticky == 0) {
		return fmt.Errorf("daemon: %s is writable by other users, who could replace tack's state under it; run chmod go-w %s, or set state_dir elsewhere", p, p)
	}
	return nil
}

// checkPath refuses a path another user could redirect to a directory of theirs, and returns it resolved: the part
// that exists with its symlinks followed, and the names that do not exist yet after it. It walks the path as the
// kernel resolves it, one name at a time from /, following each symlink where it is met, one whose target does not
// exist yet too: every directory passed through is checked (checkDir), and every symlink must be this user's or
// root's. Once every name exists, the directory the path resolves to is checked as the state directory itself,
// however it was reached (a link to "." or ".." ends the walk on a directory first passed through).
func checkPath(abs string) (string, error) {
	root, err := os.Lstat("/")
	if err != nil {
		return "", err
	}
	if err := checkDir("/", root, false); err != nil {
		return "", err
	}
	cur, rest, links := "/", components(abs), 0
	var missing []string // names past the first that does not exist: none of them can be a link yet
	for len(rest) > 0 {
		name := rest[0]
		rest = rest[1:]
		switch {
		case name == ".." && len(missing) > 0:
			missing = missing[:len(missing)-1]
			continue
		case name == "..":
			cur = filepath.Dir(cur) // cur is resolved, so its parent is the physical one
			continue
		case len(missing) > 0:
			missing = append(missing, name)
			continue
		}
		next := filepath.Join(cur, name)
		fi, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, name)
			continue
		}
		if err != nil {
			return "", err
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			if uid := ownerOf(next, fi); uid != os.Getuid() && uid != 0 {
				return "", fmt.Errorf("daemon: %s is a symlink owned by uid %d; tack's state may be reached only through this user's or root's", next, uid)
			}
			if links++; links > 40 {
				return "", fmt.Errorf("daemon: %s: too many levels of symlinks", abs)
			}
			target, err := os.Readlink(next)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				cur = "/"
			}
			rest = append(components(target), rest...)
		case fi.IsDir():
			if err := checkDir(next, fi, false); err != nil {
				return "", err
			}
			cur = next
		default:
			return "", fmt.Errorf("daemon: %s is not a directory", next)
		}
	}
	if len(missing) > 0 {
		return filepath.Join(append([]string{cur}, missing...)...), nil
	}
	fi, err := os.Lstat(cur)
	if err != nil {
		return "", err
	}
	return cur, checkDir(cur, fi, true)
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
