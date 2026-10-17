package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/sandbox"
)

// TestHaltAndResume: tack halt writes the HALT file and sweeps each registered session; tack run refuses while it
// exists; tack resume lifts it. The sweep is a stand-in: no test here signals a process.
func TestHaltAndResume(t *testing.T) {
	cfg := writeConfig(t, "http://cassette.invalid")
	state := filepath.Join(filepath.Dir(cfg), "state")
	target := sandbox.Target{Mark: "s-test", Since: time.Now()}
	if err := halt.Register(state, "s_1", target); err != nil {
		t.Fatal(err)
	}
	var calls []bool
	sweep = func(tg sandbox.Target, dryRun bool) (sandbox.Report, error) {
		calls = append(calls, dryRun)
		if tg.Mark != target.Mark || !tg.Since.Equal(target.Since) {
			t.Errorf("swept %+v, want the registered %+v", tg, target)
		}
		return sandbox.Report{Kill: []sandbox.Proc{{PID: 4242, Name: "sleep", Start: target.Since.Add(time.Second)}}, Killed: 1}, nil
	}
	t.Cleanup(func() { sweep = sweepTarget })

	var out, errb bytes.Buffer
	if code := dispatch([]string{"halt", "-config", cfg, "-dry-run"}, &out, &errb); code != exitOK {
		t.Fatalf("tack halt -dry-run: %d %s", code, errb.String())
	}
	if len(calls) != 1 || !calls[0] || !strings.Contains(out.String(), "would kill 1 process(es)") || !strings.Contains(out.String(), "pid 4242  sleep") {
		t.Fatalf("dry run said %q, sweeps %v", out.String(), calls)
	}
	if _, err := os.Stat(filepath.Join(state, "HALT")); !os.IsNotExist(err) {
		t.Fatal("a dry run halted tack")
	}

	out.Reset()
	if code := dispatch([]string{"halt", "-config", cfg, "-reason", "drill"}, &out, &errb); code != exitOK {
		t.Fatalf("tack halt: %d %s", code, errb.String())
	}
	if len(calls) != 2 || calls[1] || !strings.Contains(out.String(), "tack is halted") || !strings.Contains(out.String(), "session s_1: 1 process(es) killed") {
		t.Fatalf("tack halt said %q, sweeps %v", out.String(), calls)
	}
	if b, err := os.ReadFile(filepath.Join(state, "HALT")); err != nil || !strings.Contains(string(b), `"reason":"drill"`) {
		t.Fatalf("HALT: %q %v", b, err)
	}
	errb.Reset()
	if code := dispatch([]string{"run", "--config", cfg, "--workspace", t.TempDir(), "hello"}, &out, &errb); code != exitHalted {
		t.Fatalf("tack run while halted: exit %d, %s", code, errb.String())
	}
	out.Reset()
	if code := dispatch([]string{"resume", "-config", cfg}, &out, &errb); code != exitOK || out.String() != "tack resumed\n" {
		t.Fatalf("tack resume: %d %q", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(state, "HALT")); !os.IsNotExist(err) {
		t.Fatalf("HALT still there: %v", err)
	}
}

// TestHaltSaysWhenASweepIsRefused: a refused sweep is reported with its list, and tack halt exits non-zero.
func TestHaltSaysWhenASweepIsRefused(t *testing.T) {
	cfg := writeConfig(t, "http://cassette.invalid")
	state := filepath.Join(filepath.Dir(cfg), "state")
	halt.Register(state, "s_1", sandbox.Target{Mark: "s-test", Since: time.Now()})
	sweep = func(sandbox.Target, bool) (sandbox.Report, error) {
		return sandbox.Report{Older: []sandbox.Proc{{PID: 77, Name: "icdd", Start: time.Now().Add(-time.Hour)}},
			Refused: "1 process(es) carry mark \"s-test\" but started before …"}, nil
	}
	t.Cleanup(func() { sweep = sweepTarget })
	var out, errb bytes.Buffer
	if code := dispatch([]string{"halt", "-config", cfg}, &out, &errb); code != exitError {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "REFUSED, nothing killed") || !strings.Contains(out.String(), "pid 77  icdd") {
		t.Fatalf("tack halt said %q", out.String())
	}
}
