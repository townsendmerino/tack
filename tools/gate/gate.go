// Package gate tallies a gate's checks into one outcome and one VERDICT line, in the shape of aikit's tools/gate:
// a cell is ok, n/a, FAIL or INCONCLUSIVE; n/a is excluded from the tally; INCONCLUSIVE is never a pass.
//
// tack keeps its own copy rather than importing aikit's tools module, which is not tagged. The output format is the
// contract (CLAUDE.md: every gate command ends with one VERDICT: line); keep it identical to aikit's.
package gate

import (
	"fmt"
	"io"
	"strings"
)

// Outcome classifies a cell and, after Reconcile, a whole run.
type Outcome string

const (
	OK           Outcome = "ok"
	NA           Outcome = "n/a"
	Fail         Outcome = "FAIL"
	Inconclusive Outcome = "INCONCLUSIVE"
)

// Field is one labelled sub-result printed on a cell's line, e.g. "files=3" or "skipped=2".
type Field struct {
	Key, Value string
}

// Cell is the result of one check.
type Cell struct {
	Name    string
	Outcome Outcome
	Fields  []Field
	// Detail is the failing command's output, printed indented under the cell line; empty for a pass.
	Detail string
}

// Report is the reconciled tally of a run. Exit is 0 for OK, 1 for FAIL, 2 for INCONCLUSIVE.
type Report struct {
	Total, Pass, Fail, Incon, NA int
	Outcome                      Outcome
	Exit                         int
}

// Reconcile tallies cells. A FAIL dominates an INCONCLUSIVE: a broken build is worse news than a check that could not
// run, and must not be masked by it (aikit's FailWins). A run with no applicable cells is INCONCLUSIVE: it judged
// nothing.
func Reconcile(cells []Cell) Report {
	r := Report{Total: len(cells)}
	for _, c := range cells {
		switch c.Outcome {
		case OK:
			r.Pass++
		case NA:
			r.NA++
		case Fail:
			r.Fail++
		default:
			r.Incon++
		}
	}
	switch {
	case r.Fail > 0:
		r.Outcome, r.Exit = Fail, 1
	case r.Incon > 0 || r.Pass == 0:
		r.Outcome, r.Exit = Inconclusive, 2
	default:
		r.Outcome, r.Exit = OK, 0
	}
	return r
}

// Verdict formats the one line every gate ends with.
func Verdict(o Outcome, msg string) string {
	word := string(o)
	if o == OK {
		word = "PASS"
	}
	return "VERDICT: " + word + " — " + msg
}

// Print writes one line per cell, the detail of each cell that did not pass, and the VERDICT line, and returns the
// report. name is the gate's name, used in the verdict message.
func Print(w io.Writer, name string, cells []Cell) Report {
	for _, c := range cells {
		var b strings.Builder
		fmt.Fprintf(&b, "%-13s %s", c.Outcome, c.Name)
		for _, f := range c.Fields {
			fmt.Fprintf(&b, " %s=%s", f.Key, f.Value)
		}
		fmt.Fprintln(w, b.String())
		if c.Outcome != OK && c.Outcome != NA && strings.TrimSpace(c.Detail) != "" {
			for _, line := range strings.Split(strings.TrimRight(c.Detail, "\n"), "\n") {
				fmt.Fprintln(w, "    "+line)
			}
		}
	}
	r := Reconcile(cells)
	fmt.Fprintln(w, Verdict(r.Outcome, fmt.Sprintf("%s: %d/%d applicable checks passed (%d failed, %d inconclusive, %d n/a)",
		name, r.Pass, r.Total-r.NA, r.Fail, r.Incon, r.NA)))
	return r
}

// F makes a Field, formatting value with fmt.Sprint.
func F(key string, value any) Field { return Field{Key: key, Value: fmt.Sprint(value)} }
