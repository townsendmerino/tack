package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	if _, err := l.Listen(); err != nil { // closing it marks the record stopping: Release must still leave none
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

// TestAStaleHolderIsUnknown: a record names its process by pid and start time, both real here. A running process's
// own record names it; once the process has exited, as a zombie not yet reaped and then reaped, the same record names
// nobody: a daemon that died without releasing left it.
func TestAStaleHolderIsUnknown(t *testing.T) {
	dir := shortDir(t)
	os.MkdirAll(dir, 0o700)
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() }) // the pid this test started
	start, live, err := procInfo(pid)
	if err != nil || !live || start == "" {
		t.Fatalf("procInfo(%d) = %q, %v, %v; want a running process's start", pid, start, live, err)
	}
	os.WriteFile(filepath.Join(dir, HolderName), fmt.Appendf(nil, `{"pid":%d,"version":"crashed","proc_start":%q}`, pid, start), 0o600)
	if h := ReadHolder(dir); h.PID != pid || h.Version != "crashed" {
		t.Fatalf("a running process's own record reads as %+v, want it named", h)
	}
	cmd.Process.Kill() // it exits, and stays a zombie until Wait reaps it
	deadline := time.Now().Add(5 * time.Second)
	for {
		if out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output(); strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			break // ps, not procInfo, says when: the test is of procInfo
		}
		if time.Now().After(deadline) {
			t.Fatal("the killed process never showed as a zombie")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// procInfo itself must say so: on some kernels a zombie's start time changes too, which would hide a missing
	// state check behind the start-time comparison.
	if _, live, err := procInfo(pid); err == nil && live {
		t.Fatal("procInfo reports the zombie as running")
	}
	if h := ReadHolder(dir); h != (Holder{}) {
		t.Fatalf("a zombie's record reads as %+v, want no holder", h)
	}
	cmd.Wait()
	if h := ReadHolder(dir); h != (Holder{}) {
		t.Fatalf("a reaped process's record reads as %+v, want no holder", h)
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

// TestPrivateDirRefusesAnotherUsersDirectory: a state directory that belongs to another user, or to root, is refused
// and left as it was.
func TestPrivateDirRefusesAnotherUsersDirectory(t *testing.T) {
	base := shortDir(t)
	os.MkdirAll(base, 0o700)
	base, _ = filepath.EvalSymlinks(base) // the walk names paths as the kernel resolves them
	dir := filepath.Join(base, "state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := ownerOf
	defer func() { ownerOf = orig }()
	// The test cannot make a directory another user owns, so the state directory alone is reported as one: its parent
	// stays this user's, and the refusal must be of the directory under test.
	for _, owner := range []int{4242, 0} { // another user's; root's, which the walk trusts and the leaf check does not
		ownerOf = func(path string, fi fs.FileInfo) int {
			if path == dir {
				return owner
			}
			return orig(path, fi)
		}
		_, err := Acquire(dir, "1")
		if err == nil || !strings.Contains(err.Error(), dir+" belongs to uid "+strconv.Itoa(owner)) {
			t.Errorf("a state directory owned by uid %d: %v, want it refused, naming it", owner, err)
		}
		if st, _ := os.Stat(dir); st.Mode().Perm() != 0o755 {
			t.Errorf("uid %d: the refused directory became %v; it must be left as it was", owner, st.Mode().Perm())
		}
		if _, err := os.Stat(filepath.Join(dir, LockName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("uid %d: a lock file was made in the refused directory: %v", owner, err)
		}
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
	l.step = func(s string) {
		if s == "unlocking" {
			_, atUnlock = os.Stat(SocketPath(dir))
		}
	}
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

// TestAStartRacingAReleaseWins: a daemon starting while the last one releases finds the lock held and no holder
// record (Release removes it before it unlocks). It keeps trying the lock, and takes it once it is dropped, instead
// of reporting a holder that has gone.
func TestAStartRacingAReleaseWins(t *testing.T) {
	dir := shortDir(t)
	old, err := Acquire(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	old.step = func(s string) {
		if s != "unlocking" { // the record is gone and the lock still held: start the next daemon now
			return
		}
		go func() {
			l, err := Acquire(dir, "next")
			if err == nil {
				defer l.Release()
			}
			got <- err
		}()
		time.Sleep(100 * time.Millisecond) // it finds the lock held, and must not give up
	}
	start := time.Now()
	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-got; err != nil {
		t.Fatalf("a start racing the release: %v, want it to take the lock", err)
	}
	if took := time.Since(start); took > 900*time.Millisecond {
		t.Fatalf("the racing start took %s; it should take the lock as soon as it is dropped", took)
	}
}

// TestAStartOnceTheSocketIsGoneWins: a client that finds the socket gone starts a daemon; if that happens while the
// last one is still releasing, the new one must not find the old one's record and give up: Release removes the
// record before it closes the socket, so the start waits for the lock and takes it.
func TestAStartOnceTheSocketIsGoneWins(t *testing.T) {
	dir := shortDir(t)
	old, err := Acquire(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Listen(); err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	old.step = func(s string) {
		if s != "socket closed" {
			return
		}
		go func() { // as a client that just failed to dial would start one
			l, err := Acquire(dir, "next")
			if err == nil {
				defer l.Release()
			}
			got <- err
		}()
		time.Sleep(100 * time.Millisecond)
	}
	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-got; err != nil {
		t.Fatalf("a start once the socket was gone: %v, want it to take the lock", err)
	}
}

// TestAStateDirWithDotDotIsRefused: ".." in the state directory is refused. After a symlink, the kernel takes it as
// the parent of the link's target, and a path cleaned as text (filepath.Join) as the parent of the link, so the daemon
// and the sandbox's profile would name different directories.
func TestAStateDirWithDotDotIsRefused(t *testing.T) {
	base := shortDir(t)
	if _, err := Acquire(base+"/x/../state", "1"); err == nil || !strings.Contains(err.Error(), `".."`) {
		t.Fatalf("a state directory with ..: %v, want it refused", err)
	}
}
