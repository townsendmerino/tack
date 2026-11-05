package main

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// rule is §12.3's A/B decision rule as a registration fixes it: the sd of d under no effect, and the counted runs
// each arm must have.
type rule struct {
	sd float64
	n  int
}

// parseRule reads "sd=S,n=N": S a finite positive number, N a positive integer, each given once.
func parseRule(s string) (rule, error) {
	var r rule
	seen := map[string]bool{}
	for kv := range strings.SplitSeq(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			return r, fmt.Errorf("%q is not key=value", kv)
		}
		if seen[k] {
			return r, fmt.Errorf("%q is given twice", k)
		}
		seen[k] = true
		switch k {
		case "sd":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || !(f > 0) || math.IsInf(f, 0) {
				return r, fmt.Errorf("sd %q: want a finite positive number", v)
			}
			r.sd = f
		case "n":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return r, fmt.Errorf("n %q: want a positive integer", v)
			}
			r.n = n
		default:
			return r, fmt.Errorf("unknown key %q (sd, n)", k)
		}
	}
	if r.sd == 0 || r.n == 0 {
		return r, fmt.Errorf("%q: both sd and n are needed", s)
	}
	return r, nil
}

// band is d's verdict against sd: shown at d ≥ 2 sd, parked at sd ≤ d < 2 sd, else not shown (the do-nothing arm
// wins ties). d and sd are compared unrounded.
func band(d, sd float64) string {
	switch {
	case d >= 2*sd:
		return "shown: B beats A"
	case d >= sd:
		return "parked: reported, no more runs unless Francis says so"
	}
	return "not shown: the do-nothing arm A wins"
}

// d is B − A's passes; when either arm has other than n counted runs, it is rate-scaled to n, and scaled says so.
func (r rule) d(passA, nA, passB, nB int) (d float64, scaled bool) {
	if nA == r.n && nB == r.n {
		return float64(passB - passA), false
	}
	if nA == 0 || nB == 0 {
		return 0, true
	}
	return float64(r.n) * (float64(passB)/float64(nB) - float64(passA)/float64(nA)), true
}

// sdFor is the sd of a rate-scaled d: the registered sd, for n runs per arm, grown by sqrt(n/2 · (1/nA + 1/nB)), so a
// short arm does not loosen the edges. With n runs in each arm it is the registered sd.
func (r rule) sdFor(nA, nB int) float64 {
	if nA == r.n && nB == r.n {
		return r.sd
	}
	return r.sd * math.Sqrt(float64(r.n)/2*(1/float64(nA)+1/float64(nB)))
}

// num prints x as it is compared: in full, with no rounding.
func num(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

// read prints the rule's section: each arm's passes and n against the registered n, d, and its band. A job without
// both an A and a B arm gets no band.
func (r rule) read(w io.Writer, j *jobData) {
	fmt.Fprintf(w, "## The registered rule (§12.3's form): sd %.2f, %d counted runs per arm\n\n", r.sd, r.n)
	count := func(arm string) (pass, n int) {
		for _, rec := range j.arms[arm] {
			n++
			if rec.Pass {
				pass++
			}
		}
		return pass, n
	}
	pA, nA := count("A")
	pB, nB := count("B")
	for _, a := range []struct {
		name    string
		pass, n int
	}{{"A", pA, nA}, {"B", pB, nB}} {
		short := ""
		if a.n != r.n {
			short = fmt.Sprintf(" (registered %d: %+d)", r.n, a.n-r.n)
		}
		fmt.Fprintf(w, "- %s: %d passes of %d counted runs%s\n", a.name, a.pass, a.n, short)
	}
	fmt.Fprintf(w, "- void runs, in neither count: %d\n", len(j.voids))
	if nA == 0 || nB == 0 {
		fmt.Fprintln(w, "- **no band:** the job needs both an A and a B arm")
		fmt.Fprintln(w)
		return
	}
	d, scaled := r.d(pA, nA, pB, nB)
	sd := r.sdFor(nA, nB)
	how, edges := "B − A", "the registered sd"
	if scaled {
		how = fmt.Sprintf("rate-scaled to %d: %d · (%d/%d − %d/%d)", r.n, r.n, pB, nB, pA, nA)
		edges = fmt.Sprintf("the sd scaled for the short arm: %s · √(%d/2 · (1/%d + 1/%d))", num(r.sd), r.n, nA, nB)
	}
	sign := ""
	if d >= 0 {
		sign = "+"
	}
	fmt.Fprintf(w, "- d = %s = %s%s; edges %s (1 sd) and %s (2 sd), %s\n", how, sign, num(d), num(sd), num(2*sd), edges)
	fmt.Fprintf(w, "- **verdict: %s**\n\n", band(d, sd))
}

// voidWhy is why a run counts as void, or "": pilot voided it, or its oracle failed to run.
func voidWhy(r pilot.Record) string {
	switch {
	case r.Run.Void != "":
		return r.Run.Void
	case r.OracleErr != "":
		return "oracle error (a harness error, not a model failure): " + r.OracleErr
	}
	return ""
}
