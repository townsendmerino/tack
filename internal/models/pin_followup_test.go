package models

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// copyFile copies src to dst through a rename, so dst is a new file (a new inode), as an upgrade replaces a binary.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst+".new", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dst+".new", dst); err != nil {
		t.Fatal(err)
	}
}

// TestAReplacedBinaryIsCheckedAgain: the version check holds for the binary it ran, not for the path: a goinfer-serve
// replaced on disk is checked again before the next child starts.
func TestAReplacedBinaryIsCheckedAgain(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSup(t, "/m/model.gguf")
	bin := filepath.Join(t.TempDir(), "goinfer-serve")
	copyFile(t, os.Args[0], bin)
	s.cfg.Binary = bin
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatalf("the pinned release: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACK_FAKE_GOINFER_VERSION", "v0.23.0") // the replacement is another release
	copyFile(t, os.Args[0], bin)
	if _, err := s.Ensure(ctx); err == nil || !strings.Contains(err.Error(), "v0.23.0") {
		t.Fatalf("a child started from a replaced binary: %v; want it checked again and refused", err)
	}
}

// TestATickDoesNotWaitOnAStart: while Ensure is starting the child (here, a --version slow to answer), a tick skips:
// a start in progress is use, not idleness, and the daemon's ticker is not held up behind it.
func TestATickDoesNotWaitOnAStart(t *testing.T) {
	t.Setenv("TACK_FAKE_GOINFER_VERSION_SLEEP", "2s")
	s, _, _ := newSup(t, "/m/model.gguf")
	checking := make(chan struct{})
	versionHook = func() { close(checking) }
	t.Cleanup(func() { versionHook = nil })
	started := make(chan error, 1)
	go func() { _, err := s.Ensure(context.Background()); started <- err }()
	<-checking // Ensure holds the lock, inside the version check, which takes 2s
	began := time.Now()
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(began); d > 500*time.Millisecond {
		t.Fatalf("a tick during a start took %s; want it to skip at once", d)
	}
	if err := <-started; err != nil {
		t.Fatal(err)
	}
}

// TestAHungAdminSocketDoesNotKeepAnIdleChild: a child whose admin socket accepts and never answers cannot say whether
// a generation is in flight; past idle_exit it is stopped anyway, and the tick does not hang on it.
func TestAHungAdminSocketDoesNotKeepAnIdleChild(t *testing.T) {
	defer func(d time.Duration) { tickAdminTimeout = d }(tickAdminTimeout)
	tickAdminTimeout = 200 * time.Millisecond
	t.Setenv("TACK_FAKE_GOINFER_STATUS_HANG", "1")
	s, c, _ := newSup(t, "/m/model.gguf")
	ctx := context.Background()
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	c.add(31 * time.Minute) // past idle_exit
	done := make(chan error, 1)
	go func() { done <- s.Tick(ctx) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the tick hung on the child's admin socket")
	}
	if child, _ := s.Running(); child {
		t.Fatal("an idle child whose admin socket does not answer was kept past idle_exit")
	}
}
