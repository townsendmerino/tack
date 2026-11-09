package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestASlowUnloadIsNotCutOffByTheStatusTimeout: the short bound on Tick's status call is not the unload's. An unload
// that takes longer than it completes, and the supervisor knows the model is gone: an Ensure after it loads again
// rather than hand out a client for an unloaded model.
func TestASlowUnloadIsNotCutOffByTheStatusTimeout(t *testing.T) {
	defer func(d time.Duration) { tickAdminTimeout = d }(tickAdminTimeout)
	tickAdminTimeout = 200 * time.Millisecond
	t.Setenv("TACK_FAKE_GOINFER_UNLOAD_SLEEP", "600ms")
	for _, why := range []string{"critical pressure", "idle"} {
		t.Run(why, func(t *testing.T) {
			s, c, p := newSup(t, "/m/model.gguf")
			ctx := context.Background()
			if _, err := s.Ensure(ctx); err != nil {
				t.Fatal(err)
			}
			if why == "idle" {
				c.add(11 * time.Minute) // past idle_unload, before idle_exit
			} else {
				*p = PressureCritical
			}
			err := s.Tick(ctx)
			if err != nil && !strings.Contains(err.Error(), "memory pressure is critical") {
				t.Fatalf("a slow unload: %v; want it to complete", err)
			}
			if _, loaded := s.Running(); loaded || report(t, s).Unloads != 1 {
				t.Fatalf("after a slow unload: loaded %v, %d unloads; want the model gone and known gone", loaded, report(t, s).Unloads)
			}
		})
	}
}

// TestAStatusErrorStopsAChildOnlyTwiceRunning: a status that answers with an error is not a hung child; past idle_exit
// one such answer keeps the child (a passing fault must not kill a long generation), and a second in a row stops it.
func TestAStatusErrorStopsAChildOnlyTwiceRunning(t *testing.T) {
	t.Setenv("TACK_FAKE_GOINFER_STATUS_FAIL", "1")
	s, c, _ := newSup(t, "/m/model.gguf")
	ctx := context.Background()
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	c.add(31 * time.Minute) // past idle_exit
	s.Tick(ctx)
	if child, _ := s.Running(); !child {
		t.Fatal("one failed status call stopped the child")
	}
	s.Tick(ctx)
	if child, _ := s.Running(); child {
		t.Fatal("two failed status calls in a row, past idle_exit, kept the child")
	}
}

// TestABinaryOnThePathIsCheckedOnce: a goinfer-serve named without a directory is found on PATH once, when the
// supervisor is made, so the check that it is unchanged works and --version runs once, not before every start.
func TestABinaryOnThePathIsCheckedOnce(t *testing.T) {
	bin := t.TempDir()
	copyFile(t, os.Args[0], filepath.Join(bin, "goinfer-serve-test"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(t.TempDir(), "versions")
	t.Setenv("TACK_FAKE_GOINFER_VERSION_LOG", log)
	t.Setenv("TACK_FAKE_GOINFER", "1")
	dir, err := os.MkdirTemp("/tmp", "tksup")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := NewSupervisor(ChildConfig{Binary: "goinfer-serve-test", ModelPath: "/m/model.gguf", Model: "m", Ctx: 8192,
		Args: []string{"-backend", "cpu"}, Dir: dir, IdleUnload: time.Hour, IdleExit: time.Hour, Admission: true,
		Pressure: func() (Pressure, error) { return PressureNormal, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })
	ctx := context.Background()
	for range 2 {
		if _, err := s.Ensure(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "version\n"); n != 1 {
		t.Fatalf("--version ran %d times over two starts of an unchanged binary; want once", n)
	}
}

// TestABinaryThatJoinsThePathLaterIsFoundOnce: a goinfer-serve not yet on PATH when the supervisor is made (the
// daemon started before it was installed) is looked up again at the start that needs it, and then checked once.
func TestABinaryThatJoinsThePathLaterIsFoundOnce(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(t.TempDir(), "versions")
	t.Setenv("TACK_FAKE_GOINFER_VERSION_LOG", log)
	t.Setenv("TACK_FAKE_GOINFER", "1")
	dir, err := os.MkdirTemp("/tmp", "tksup")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := NewSupervisor(ChildConfig{Binary: "goinfer-serve-later", ModelPath: "/m/model.gguf", Model: "m", Ctx: 8192,
		Args: []string{"-backend", "cpu"}, Dir: dir, IdleUnload: time.Hour, IdleExit: time.Hour, Admission: true,
		Pressure: func() (Pressure, error) { return PressureNormal, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })
	copyFile(t, os.Args[0], filepath.Join(bin, "goinfer-serve-later")) // installed after the supervisor was made
	ctx := context.Background()
	for range 2 {
		if _, err := s.Ensure(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "version\n"); n != 1 {
		t.Fatalf("--version ran %d times over two starts of a binary found on PATH late; want once", n)
	}
}
