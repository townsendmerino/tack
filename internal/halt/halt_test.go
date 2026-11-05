package halt

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
)

func TestHaltCheckResume(t *testing.T) {
	dir := t.TempDir()
	if h, err := Check(dir); h != nil || err != nil {
		t.Fatalf("no file: %+v %v", h, err)
	}
	if was, err := Halt(dir, "tack halt", "drill"); was || err != nil {
		t.Fatalf("Halt: %v %v", was, err)
	}
	h, err := Check(dir)
	if err != nil || h == nil || *h != (proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "tack halt", Reason: "drill"}) {
		t.Fatalf("Check after Halt: %+v %v", h, err)
	}
	if st, _ := os.Stat(Path(dir)); st.Mode().Perm() != 0o600 {
		t.Fatalf("HALT is mode %v", st.Mode().Perm())
	}
	if was, _ := Halt(dir, "tack halt", ""); !was {
		t.Fatal("a second Halt did not report tack already halted")
	}
	if was, err := Resume(dir); !was || err != nil {
		t.Fatalf("Resume: %v %v", was, err)
	}
	if was, err := Resume(dir); was || err != nil {
		t.Fatalf("Resume again: %v %v", was, err)
	}
	// A script's empty file is a halt by file; so is a file claiming a command it does not carry.
	for _, body := range []string{"", "not json", `{"trigger":"bogus","by":"x"}`} {
		os.WriteFile(Path(dir), []byte(body), 0o644)
		if h, err := Check(dir); err != nil || h == nil || *h != (proto.DaemonHalted{Trigger: proto.HaltByFile}) {
			t.Fatalf("file %q: %+v %v", body, h, err)
		}
	}
}

func TestWatch(t *testing.T) {
	dir := t.TempDir()
	Halt(dir, "tack halt", "") // already halted when the watch starts
	events := make(chan string, 8)
	ctx := t.Context()
	go Watch(ctx, dir, 5*time.Millisecond, func(h proto.DaemonHalted) { events <- "halt " + h.Trigger }, func() { events <- "resume" })
	next := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %q", want)
		}
	}
	next("halt command")
	Resume(dir)
	next("resume")
	os.WriteFile(Path(dir), nil, 0o644)
	next("halt file")
	time.Sleep(50 * time.Millisecond) // a standing halt is reported once
	select {
	case got := <-events:
		t.Fatalf("a standing halt was reported again: %q", got)
	default:
	}
}

func TestSweepRegistry(t *testing.T) {
	dir := t.TempDir()
	if ts, err := Targets(dir); err != nil || len(ts) != 0 {
		t.Fatalf("empty: %v %v", ts, err)
	}
	a := sandbox.Target{Mark: "s-aaaa", Since: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}
	if err := Register(dir, "s_1", a); err != nil {
		t.Fatal(err)
	}
	Register(dir, "s_2", sandbox.Target{Mark: "s-bbbb", Since: a.Since})
	ts, err := Targets(dir)
	if err != nil || len(ts) != 2 || !ts["s_1"].Since.Equal(a.Since) || ts["s_1"].Mark != a.Mark {
		t.Fatalf("targets %v %v", ts, err)
	}
	if err := Unregister(dir, "s_2"); err != nil {
		t.Fatal(err)
	}
	if ts, _ := Targets(dir); len(ts) != 1 {
		t.Fatalf("after unregister: %v", ts)
	}
	// A target without a start time would sweep without the start-time condition: refused.
	os.WriteFile(filepath.Join(dir, sweepDir, "s_3.json"), []byte(`{"mark":"s-cccc"}`), 0o600)
	if _, err := Targets(dir); err == nil {
		t.Fatal("a target with no start time was accepted")
	}
}
