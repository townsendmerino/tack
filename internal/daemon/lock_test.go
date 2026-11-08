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

// TestAStaleHolderIsUnknown: the record of a daemon that died without releasing (its pid is gone) names nobody.
func TestAStaleHolderIsUnknown(t *testing.T) {
	dir := shortDir(t)
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	dead := cmd.Process.Pid
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, HolderName), fmt.Appendf(nil, `{"pid":%d,"version":"crashed","start":"2026-11-08T12:00:00Z"}`, dead), 0o600)
	if h := ReadHolder(dir); h != (Holder{}) {
		t.Fatalf("a dead daemon's record reads as %+v, want no holder", h)
	}
}

// TestLosersNameTheWinner: CLIs starting daemons at once each lose to the one that won, and name it or nobody, never
// an earlier daemon that has left.
func TestLosersNameTheWinner(t *testing.T) {
	dir := shortDir(t)
	old, err := Acquire(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	old.Release()
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
	for _, v := range named {
		if v != "" && v != winner {
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
