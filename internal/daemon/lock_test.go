package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestReleaseClearsTheHolder: a released lock names no holder, so a daemon that starts later and finds the lock held
// by a third cannot name the one that left.
func TestReleaseClearsTheHolder(t *testing.T) {
	dir := shortDir(t)
	l, err := Acquire(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	if h := ReadHolder(dir); h.Version != "old" || h.PID != os.Getpid() {
		t.Fatalf("while held: %+v, want this process at version old", h)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if h := ReadHolder(dir); h != (Holder{}) {
		t.Fatalf("after Release: %+v, want no holder", h)
	}
}

// TestAStaleHolderIsUnknown: the record of a daemon that died without releasing names nobody, whether its pid is
// gone or now another user's process.
func TestAStaleHolderIsUnknown(t *testing.T) {
	dir := shortDir(t)
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	dead := cmd.Process.Pid
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, HolderName), fmt.Appendf(nil, `{"pid":%d,"version":"crashed","start":"2026-11-08T12:00:00Z","proc_start":"1.000000"}`, dead), 0o600)
	if h := ReadHolder(dir); h != (Holder{}) {
		t.Fatalf("a dead daemon's record reads as %+v, want no holder", h)
	}
	// A pid reused by another user's process (pid 1 is root's) is not this user's daemon either.
	if os.Getuid() != 0 {
		os.WriteFile(filepath.Join(dir, HolderName), []byte(`{"pid":1,"version":"crashed","start":"2026-11-08T12:00:00Z","proc_start":"1.000000"}`), 0o600)
		if h := ReadHolder(dir); h != (Holder{}) {
			t.Fatalf("a record naming another user's process reads as %+v, want no holder", h)
		}
	}
}

// TestLosersNameTheWinner: CLIs starting daemons at once each lose to the one that won and name it, never an earlier
// daemon that has left: a loser that reads before the winner has recorded itself waits for the record.
func TestLosersNameTheWinner(t *testing.T) {
	dir := shortDir(t)
	old, err := Acquire(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	old.Release()
	// A crashed daemon's record whose pid now names a live process of this user's (this one), and a winner slow to
	// write its own: losers must see past the first and wait for the second.
	os.WriteFile(filepath.Join(dir, HolderName), fmt.Appendf(nil, `{"pid":%d,"version":"crashed","proc_start":"1.000000"}`, os.Getpid()), 0o600)
	afterLock = func() { time.Sleep(50 * time.Millisecond) }
	defer func() { afterLock = nil }()
	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	winner := ""
	var named []string
	for i := range n {
		wg.Go(func() {
			v := fmt.Sprint("v", i)
			l, err := Acquire(dir, v)
			mu.Lock()
			defer mu.Unlock()
			var he *HeldError
			switch {
			case err == nil:
				winner = v
				t.Cleanup(func() { l.Release() })
			case errors.As(err, &he):
				named = append(named, he.Holder.Version)
			default:
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if winner == "" || len(named) != n-1 {
		t.Fatalf("winner %q, %d losers; want one winner and %d losers", winner, len(named), n-1)
	}
	for _, v := range named {
		if v != winner {
			t.Errorf("a loser named %q; the winner is %q", v, winner)
		}
	}
}

// TestPrivateDirRefusesAnotherUsersDirectory: a state directory that belongs to another user is refused, and left as
// it was.
func TestPrivateDirRefusesAnotherUsersDirectory(t *testing.T) {
	dir := shortDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	me := currentUID
	currentUID = func() int { return me() + 1 } // as if the directory were another user's
	defer func() { currentUID = me }()
	if _, err := Acquire(dir, "1"); err == nil || !strings.Contains(err.Error(), "belongs to uid") {
		t.Fatalf("Acquire in another user's directory: %v, want a refusal naming the uid", err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o755 {
		t.Fatalf("the refused directory became %v; it must be left as it was", st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, LockName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a lock file was made in another user's directory: %v", err)
	}
}

// TestReleaseClosesTheSocketBeforeDroppingTheLock: by the time the lock is dropped, the socket is unlinked, so the
// listener's close can never unlink the socket of the daemon that takes the lock next.
func TestReleaseClosesTheSocketBeforeDroppingTheLock(t *testing.T) {
	dir := shortDir(t)
	l := acquire(t, dir)
	if _, err := l.Listen(); err != nil {
		t.Fatal(err)
	}
	var atUnlock error
	l.beforeUnlock = func() { _, atUnlock = os.Stat(SocketPath(dir)) }
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(atUnlock, fs.ErrNotExist) {
		t.Fatalf("the socket was still there when the lock was dropped (stat: %v)", atUnlock)
	}
	next := acquire(t, dir)
	if _, err := next.Listen(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SocketPath(dir)); err != nil {
		t.Fatalf("the next daemon's socket: %v", err)
	}
}

// TestReleaseIsSafeBesideListenAndNewToken: Release, Listen and NewToken on one lock from different goroutines do not
// race (run under -race), and whatever Listen bound, Release closes.
func TestReleaseIsSafeBesideListenAndNewToken(t *testing.T) {
	for range 20 {
		dir := shortDir(t)
		l, err := Acquire(dir, "1")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var ln *Listener
		wg.Go(func() { ln, _ = l.Listen() })
		wg.Go(func() { l.NewToken() })
		wg.Go(func() { l.Release() })
		wg.Wait()
		l.Release()
		if ln != nil {
			if _, err := os.Stat(SocketPath(dir)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Listen bound a socket that Release left open (stat: %v)", err)
			}
		}
	}
}

// TestPrivateDirRefusesAPathOthersCanRedirect: a state directory reached through a symlink another user owns is
// refused, since they could re-point it after the check at a directory of their own and read the token; so is one
// under a directory others can write without the sticky bit, where they could swap it. This user's own symlink, and a
// sticky shared directory (as /tmp is), are fine. The test box cannot make a file another user owns, so the
// symlink's owner is reported as uid 4242 (ownerOf).
func TestPrivateDirRefusesAPathOthersCanRedirect(t *testing.T) {
	base := shortDir(t)
	os.MkdirAll(base, 0o700)
	base, _ = filepath.EvalSymlinks(base) // the walk names paths as the kernel resolves them
	target, link := filepath.Join(base, "target"), filepath.Join(base, "link")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(link); err != nil {
		t.Fatalf("this user's own symlink: %v", err)
	}
	orig := ownerOf
	ownerOf = func(path string, fi fs.FileInfo) int {
		if path == link {
			return 4242
		}
		return orig(path, fi)
	}
	defer func() { ownerOf = orig }()
	for _, dir := range []string{link, filepath.Join(link, "state")} {
		if err := privateDir(dir); err == nil || !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), "4242") {
			t.Errorf("privateDir(%s) through another user's symlink: %v, want refused", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "state")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused path was made anyway: %v", err)
	}
	ownerOf = orig

	shared := filepath.Join(base, "shared")
	os.Mkdir(shared, 0o700)
	os.Chmod(shared, 0o777)
	if err := privateDir(filepath.Join(shared, "state")); err == nil || !strings.Contains(err.Error(), "writable by other users") {
		t.Errorf("under a directory anyone can write: %v, want refused", err)
	}
	os.Chmod(shared, 0o777|fs.ModeSticky)
	if err := privateDir(filepath.Join(shared, "state")); err != nil {
		t.Errorf("under a sticky shared directory: %v", err)
	}
}

// TestAcquireNarrowsAWideLockFile: a daemon.lock left wider than 0600 is made 0600.
func TestAcquireNarrowsAWideLockFile(t *testing.T) {
	dir := shortDir(t)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, LockName), nil, 0o644)
	acquire(t, dir)
	if st, _ := os.Stat(filepath.Join(dir, LockName)); st.Mode().Perm() != 0o600 {
		t.Fatalf("daemon.lock is %v, want 0600", st.Mode().Perm())
	}
}

// TestCheckPathWalksThePathAsWritten: the kernel resolves a path one component at a time, so the check does too. A
// directory others can write is refused even when this user's symlink inside it leads elsewhere, and a chain of
// symlinks is checked link by link: one owned by another user (reported as uid 4242, as above) anywhere in it is
// refused.
func TestCheckPathWalksThePathAsWritten(t *testing.T) {
	base := shortDir(t)
	os.MkdirAll(base, 0o700)
	base, _ = filepath.EvalSymlinks(base) // the walk names paths as the kernel resolves them
	target := filepath.Join(base, "target")
	shared := filepath.Join(base, "shared")
	for _, d := range []string{target, shared} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.Chmod(shared, 0o777)
	mine := filepath.Join(shared, "mine")
	os.Symlink(target, mine)
	if err := privateDir(filepath.Join(mine, "state")); err == nil || !strings.Contains(err.Error(), "writable by other users") {
		t.Errorf("through this user's link inside a directory anyone can write: %v, want refused", err)
	}
	link1, link2 := filepath.Join(base, "link1"), filepath.Join(base, "link2")
	os.Symlink(target, link2)
	os.Symlink(link2, link1)
	if err := privateDir(link1); err != nil {
		t.Fatalf("a chain of this user's links: %v", err)
	}
	orig := ownerOf
	ownerOf = func(path string, fi fs.FileInfo) int {
		if path == link2 {
			return 4242
		}
		return orig(path, fi)
	}
	defer func() { ownerOf = orig }()
	if err := privateDir(link1); err == nil || !strings.Contains(err.Error(), "4242") {
		t.Errorf("a chain through another user's link: %v, want refused", err)
	}
}

// TestAReusedPidIsNotTheHolder: a record whose pid is alive and this user's, but is not the process that wrote it (a
// crashed daemon's pid reused by another of this user's processes), names nobody: the record carries the process's
// start time, and it must match.
func TestAReusedPidIsNotTheHolder(t *testing.T) {
	dir := shortDir(t)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, HolderName), fmt.Appendf(nil, `{"pid":%d,"version":"crashed","start":"2026-11-08T12:00:00Z","proc_start":"1.000000"}`, os.Getpid()), 0o600)
	if h := ReadHolder(dir); h != (Holder{}) {
		t.Fatalf("a stale record naming a live pid reads as %+v, want no holder", h)
	}
	l := acquire(t, dir)
	if h := ReadHolder(dir); h.Version != "1.2.3" || h.PID != os.Getpid() {
		t.Fatalf("this process's own record reads as %+v", h)
	}
	l.Release()
}
