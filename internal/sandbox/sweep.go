package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Target is what one sweep may kill: processes inside a sandbox carrying Mark that started at or after Since. Mark is a
// session's Config.Group or a command's Config.Mark; Since is when that session or command began. The two conditions
// are independent, so a bug in either still leaves every process older than Since out, boot-time services included.
type Target struct {
	Mark  string    `json:"mark"`
	Since time.Time `json:"since"`
}

// MaxSweep is the most processes one sweep kills. A sweep that selects more is refused: a session's commands rarely
// leave many processes behind, and a selection that large more likely means the selection is wrong.
const MaxSweep = 32

// Proc is one process as a sweep sees it.
type Proc struct {
	PID   int       `json:"pid"`
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
}

// Report is a sweep's plan and outcome. Kill is what both conditions select. Older is every process carrying the mark
// that started before Since: none should exist, so any is a sign the mark check is wrong, and the sweep is refused.
// Refused, when set, says why nothing was killed; Killed counts the processes signalled.
type Report struct {
	Kill    []Proc
	Older   []Proc
	Refused string
	Killed  int
}

// Plan selects, from procs, what a sweep of t would kill. carries reports whether p's sandbox carries mark. Plan never
// signals anything; Sweep acts on its result.
func Plan(t Target, procs []Proc, carries func(p Proc, mark string) bool) Report {
	var r Report
	for _, p := range procs {
		if !carries(p, t.Mark) {
			continue
		}
		if p.Start.Before(t.Since) {
			r.Older = append(r.Older, p)
			continue
		}
		r.Kill = append(r.Kill, p)
	}
	switch {
	case len(r.Older) > 0:
		r.Refused = fmt.Sprintf("%d process(es) carry mark %q but started before %s, so the mark check cannot be trusted",
			len(r.Older), t.Mark, t.Since.Format(time.RFC3339Nano))
	case len(r.Kill) > MaxSweep:
		r.Refused = fmt.Sprintf("%d processes selected, more than the %d one sweep may kill", len(r.Kill), MaxSweep)
	}
	return r
}

// NewMark returns a random mark with prefix: a session's group ("s") or a command's ("c").
func NewMark(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24)
	return prefix + "-" + hex.EncodeToString(b)
}
