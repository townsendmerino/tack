package sandbox

import (
	"strings"
	"testing"
	"time"
)

// These tests assert on Plan's lists; none sends a signal.

func marks(ms map[int]string) func(Proc, string) bool {
	return func(p Proc, mark string) bool { return ms[p.PID] == mark }
}

func TestPlanNeedsBothConditions(t *testing.T) {
	since := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	procs := []Proc{
		{PID: 1, Name: "old-unmarked", Start: since.Add(-time.Hour)},
		{PID: 2, Name: "new-unmarked", Start: since.Add(time.Second)},
		{PID: 3, Name: "new-marked", Start: since.Add(time.Second)},
		{PID: 4, Name: "new-other-mark", Start: since.Add(time.Second)},
		{PID: 5, Name: "at-since-marked", Start: since},
	}
	r := Plan(Target{Mark: "s-a", Since: since}, procs, marks(map[int]string{3: "s-a", 4: "s-b", 5: "s-a"}))
	if r.Refused != "" || len(r.Older) != 0 || len(r.Kill) != 2 || r.Kill[0].PID != 3 || r.Kill[1].PID != 5 {
		t.Fatalf("plan %+v", r)
	}
}

// TestPlanRefusesAMarkedOlderProcess: a process carrying the mark but older than its session means the mark check is
// wrong, so nothing is killed, not even the processes that look right.
func TestPlanRefusesAMarkedOlderProcess(t *testing.T) {
	since := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	procs := []Proc{{PID: 1, Name: "launchd-agent", Start: since.Add(-24 * time.Hour)}, {PID: 2, Name: "x", Start: since.Add(time.Second)}}
	r := Plan(Target{Mark: "s-a", Since: since}, procs, marks(map[int]string{1: "s-a", 2: "s-a"}))
	if r.Refused == "" || !strings.Contains(r.Refused, "started before") || len(r.Older) != 1 || r.Older[0].PID != 1 {
		t.Fatalf("plan %+v", r)
	}
}

// TestPlanSanityLimit: more than MaxSweep selected is refused.
func TestPlanSanityLimit(t *testing.T) {
	since := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	var procs []Proc
	all := map[int]string{}
	for i := range MaxSweep + 1 {
		procs = append(procs, Proc{PID: 100 + i, Start: since.Add(time.Second)})
		all[100+i] = "s-a"
	}
	if r := Plan(Target{Mark: "s-a", Since: since}, procs[:MaxSweep], marks(all)); r.Refused != "" {
		t.Fatalf("exactly MaxSweep refused: %s", r.Refused)
	}
	r := Plan(Target{Mark: "s-a", Since: since}, procs, marks(all))
	if r.Refused == "" || !strings.Contains(r.Refused, "more than the 32") {
		t.Fatalf("MaxSweep+1 selected and not refused: %+v", r.Refused)
	}
}

func TestNewMark(t *testing.T) {
	a, b := NewMark("s"), NewMark("s")
	if a == b || !strings.HasPrefix(a, "s-") || len(a) != 18 {
		t.Fatalf("marks %q %q", a, b)
	}
}
