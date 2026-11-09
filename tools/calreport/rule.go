package main

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// rule is §12.3's A/B decision rule as a registration fixes it: the sd of d under no effect, the counted runs each arm
// must have, and the two arms it compares, the do-nothing arm first (A and B, or B and C for C12's second contrast,
// calibration §18.1).
type rule struct {
	sd   float64
	n    int
	base string // the do-nothing arm
	try  string // the arm with the setting
}

// parseRule reads "sd=S,n=N[,pair=X:Y]": S a finite positive number, N a positive integer, X and Y two different arm
// names, the do-nothing arm first (A:B when left out), each given once.
func parseRule(s string) (rule, error) {
	r := rule{base: "A", try: "B"}
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
		case "pair":
			x, y, ok := strings.Cut(v, ":")
			if !ok || x == "" || y == "" || x == y || strings.Contains(y, ":") {
				return r, fmt.Errorf("pair %q: want two different arms, X:Y, the do-nothing arm first", v)
			}
			r.base, r.try = x, y
		default:
			return r, fmt.Errorf("unknown key %q (sd, n, pair)", k)
		}
	}
	if r.sd == 0 || r.n == 0 {
		return r, fmt.Errorf("%q: both sd and n are needed", s)
	}
	return r, nil
}

// band is d's verdict against sd, d being arm try's passes less arm base's: shown at d ≥ 2 sd, parked at sd ≤ d < 2 sd,
// else not shown (the do-nothing arm, base, wins ties). d and sd are compared unrounded.
func band(d, sd float64, base, try string) string {
	switch {
	case d >= 2*sd:
		return "shown: " + try + " beats " + base
	case d >= sd:
		return "parked: reported, no more runs unless Francis says so"
	}
	return "not shown: the do-nothing arm " + base + " wins"
}

// d is the tried arm's passes less the do-nothing arm's (B − A by default); when either arm has other than n counted
// runs, it is rate-scaled to n, and scaled says so.
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
	pA, nA := count(r.base)
	pB, nB := count(r.try)
	for _, a := range []struct {
		name    string
		pass, n int
	}{{r.base, pA, nA}, {r.try, pB, nB}} {
		short := ""
		if a.n != r.n {
			short = fmt.Sprintf(" (registered %d: %+d)", r.n, a.n-r.n)
		}
		fmt.Fprintf(w, "- %s: %d passes of %d counted runs%s\n", a.name, a.pass, a.n, short)
	}
	fmt.Fprintf(w, "- void runs, in neither count: %d\n", len(j.voids))
	if nA == 0 || nB == 0 {
		fmt.Fprintf(w, "- **no band:** the job needs both an %s and a %s arm\n", r.base, r.try)
		fmt.Fprintln(w)
		return
	}
	d, scaled := r.d(pA, nA, pB, nB)
	sd := r.sdFor(nA, nB)
	how, edges := r.try+" − "+r.base, "the registered sd"
	if scaled {
		how = fmt.Sprintf("rate-scaled to %d: %d · (%d/%d − %d/%d)", r.n, r.n, pB, nB, pA, nA)
		edges = fmt.Sprintf("the sd scaled for the short arm: %s · √(%d/2 · (1/%d + 1/%d))", num(r.sd), r.n, nA, nB)
	}
	sign := ""
	if d >= 0 {
		sign = "+"
	}
	fmt.Fprintf(w, "- d = %s = %s%s; edges %s (1 sd) and %s (2 sd), %s\n", how, sign, num(d), num(sd), num(2*sd), edges)
	fmt.Fprintf(w, "- **verdict: %s**\n\n", band(d, sd, r.base, r.try))
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
