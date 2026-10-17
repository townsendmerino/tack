//go:build darwin

package sandbox

import (
	"testing"
	"time"
)

// TestStartTimeAloneKeepsPreexistingProcessesOut: with the mark check removed (every process "carries" the mark), the
// start-time condition alone keeps every one of this user's running processes out of the selection, and the plan is
// refused besides. It asserts on Plan's lists over the real process table and sends no signal.
func TestStartTimeAloneKeepsPreexistingProcessesOut(t *testing.T) {
	procs, err := Processes()
	if err != nil {
		t.Fatal(err)
	}
	since := time.Now() // the session begins now: everything listed is older
	if len(procs) < 10 {
		t.Fatalf("only %d processes listed; the test would prove nothing", len(procs))
	}
	r := Plan(Target{Mark: "s-test", Since: since}, procs, func(Proc, string) bool { return true })
	for _, p := range r.Kill {
		t.Errorf("selected a process that was running before the session began: %+v", p)
	}
	if len(r.Older) != len(procs) || r.Refused == "" {
		t.Errorf("with every process carrying the mark, all %d should be listed as older and the sweep refused: older %d, refused %q",
			len(procs), len(r.Older), r.Refused)
	}
}
