package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/eval/mine"
)

type repoFlags []mine.Repo

func (r *repoFlags) String() string { return fmt.Sprint(*r) }

// Set parses name=path@commit.
func (r *repoFlags) Set(s string) error {
	name, rest, ok1 := strings.Cut(s, "=")
	p, commit, ok2 := strings.Cut(rest, "@")
	if !ok1 || !ok2 || name == "" || p == "" || commit == "" {
		return fmt.Errorf("want name=path@commit, got %q", s)
	}
	*r = append(*r, mine.Repo{Name: name, Path: p, Frozen: commit})
	return nil
}

// mineCmd is `tack eval mine`: E1's task miner (docs/measurements/e1-task-selection-preregistration-2026-10-16.md).
//
//	tack eval mine -stage static -repo goinfer=~/tmcode/goinfer@5d597aca … -out DIR   writes DIR/candidates.jsonl
//	tack eval mine -stage verify -repo … -out DIR                                     appends DIR/verified.jsonl
//	tack eval mine -stage choose -out DIR [-exclude eval/e1/pilot.jsonl]              writes DIR/pilot.jsonl, DIR/e1.jsonl
func mineCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval mine", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stage := fs.String("stage", "", "static, verify or choose")
	out := fs.String("out", "", "the miner's directory (durable)")
	var repos repoFlags
	fs.Var(&repos, "repo", "name=path@frozen-commit (repeatable)")
	untilPilot := fs.Bool("until-pilot", false, "verify: stop once the pilot set (10, at most 4 per repo) is full; candidates are taken in stage 3's order, so stopping early cannot change it")
	maxN := fs.Int("max", 0, "verify: stop after this many candidates (0: no limit)")
	exclude := fs.String("exclude", "", "choose: the committed pilot set (eval/e1/pilot.jsonl); its tasks, removed ones included, are left out of E1's order, and the pilot chosen here must match it")
	if err := fs.Parse(args); err != nil || *out == "" {
		fmt.Fprintln(stderr, "usage: tack eval mine -stage static|verify|choose -out DIR [-repo name=path@commit ...]")
		return exitUsage
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(stderr, "tack eval mine:", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch *stage {
	case "static":
		err = mineStatic(repos, *out, stderr)
	case "verify":
		err = mineVerify(ctx, repos, *out, *untilPilot, *maxN, stderr)
	case "choose":
		err = mineChoose(*out, *exclude, stderr)
	default:
		fmt.Fprintf(stderr, "tack eval mine: -stage %q, want static, verify or choose\n", *stage)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintln(stderr, "tack eval mine:", err)
		return exitError
	}
	return exitOK
}

func logf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, time.Now().Format("15:04:05")+" "+format+"\n", args...)
}

func mineStatic(repos repoFlags, out string, log io.Writer) error {
	f, err := os.Create(filepath.Join(out, "candidates.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	total := 0
	for _, r := range repos {
		t0 := time.Now()
		cs, st, err := mine.Static(r)
		if err != nil {
			return err
		}
		for _, c := range cs {
			if err := enc.Encode(c); err != nil {
				return err
			}
		}
		total += len(cs)
		logf(log, "static %s @%s: %d commits read, %d candidates, %d removed by rule 8, %s", r.Name, r.Frozen, st.Read, len(cs), st.Rule8, time.Since(t0).Round(time.Millisecond))
	}
	logf(log, "static: %d candidates in %s", total, filepath.Join(out, "candidates.jsonl"))
	return nil
}

func readCandidates(p string) ([]mine.Candidate, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cs []mine.Candidate
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var c mine.Candidate
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		cs = append(cs, c)
	}
	return cs, sc.Err()
}

// mineVerify runs stage 2 over every candidate not yet in verified.jsonl, appending each result as it finishes, so an
// interrupted run resumes where it stopped. It logs each candidate and a heartbeat every 30 s.
func mineVerify(ctx context.Context, repos repoFlags, out string, untilPilot bool, maxN int, log io.Writer) error {
	cs, err := readCandidates(filepath.Join(out, "candidates.jsonl"))
	if err != nil {
		return err
	}
	cs = mine.Order(cs) // stage 3's order, so a run stopped early has verified a prefix of it
	var okSoFar []mine.Candidate
	vp := filepath.Join(out, "verified.jsonl")
	done := map[string]mine.Candidate{} // each candidate's latest record
	if prev, err := readCandidates(vp); err == nil {
		for _, c := range latest(prev) {
			done[c.Repo+"/"+c.Commit] = c
			if c.Verified != nil && c.Verified.OK && c.Verified.Stub != nil {
				okSoFar = append(okSoFar, c)
			}
		}
	}
	byName := map[string]mine.Repo{}
	for _, r := range repos {
		byName[r.Name] = r
	}
	f, err := os.OpenFile(vp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	start := time.Now()
	todo := 0
	for _, c := range cs {
		if needsWork(done, c) {
			todo++
		}
	}
	logf(log, "verify: start %s, %d candidates, %d already verified, %d to do", start.Format("15:04 MST"), len(cs), len(cs)-todo, todo)
	var cur string
	beat := time.NewTicker(30 * time.Second)
	defer beat.Stop()
	stopBeat := make(chan struct{})
	defer close(stopBeat)
	go func() {
		for {
			select {
			case <-beat.C:
				logf(log, "  heartbeat: on %s, elapsed %s (started %s)", cur, time.Since(start).Round(time.Second), start.Format("15:04"))
			case <-stopBeat:
				return
			}
		}
	}()
	i, ok := 0, 0
	for _, c := range cs {
		if !needsWork(done, c) {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r, found := byName[c.Repo]
		if !found {
			return fmt.Errorf("no -repo for %s", c.Repo)
		}
		if maxN > 0 && i >= maxN { // i counts the candidates verified in this run
			logf(log, "verify: stopping after %d candidates (-max)", maxN)
			break
		}
		if untilPilot {
			if pilot, _ := mine.Choose(okSoFar, 10, 4, nil); len(pilot) == 10 {
				logf(log, "verify: the pilot set is full; stopping (-until-pilot)")
				break
			}
		}
		i++
		cur = fmt.Sprintf("%s %s (%d/%d)", c.Repo, c.Commit[:8], i, todo)
		work := filepath.Join(out, "clones", r.Name)
		if err := mine.PrepareClone(r, work); err != nil {
			return err
		}
		t0 := time.Now()
		if prev, ok := done[c.Repo+"/"+c.Commit]; ok { // verified before the stub check existed: add it (amendment 3)
			c = prev
			v := *c.Verified
			st := mine.StubCheck(ctx, r, c, v.Oracle, work)
			v.Stub, v.OK = &st, st.Rejected == ""
			if !v.OK {
				v.Why = st.Why()
			}
			c.Verified = &v
		} else {
			v := mine.Verify(ctx, r, c, work)
			c.Verified = &v
		}
		v := *c.Verified
		if err := enc.Encode(c); err != nil {
			return err
		}
		if v.OK {
			ok++
			okSoFar = append(okSoFar, c)
		}
		logf(log, "  %d/%d %s %s: ok=%v %s (%s)", i, todo, c.Repo, c.Commit[:8], v.OK, v.Why, time.Since(t0).Round(time.Second))
	}
	logf(log, "verify: done, %d of %d verified ok, in %s", ok, i, time.Since(start).Round(time.Second))
	return nil
}

// mineChoose is stage 3. It writes the pilot set and E1's full fixed order, which is not cut to a size (selection
// pre-registration, amendment 4). With exclude, the committed pilot file, every task in it is left out of E1's order,
// and the pilot chosen here must be its tasks that are not marked removed: a mismatch means the order or the
// verification changed under an approved pilot, and nothing is written.
func mineChoose(out, exclude string, log io.Writer) error {
	all, err := readCandidates(filepath.Join(out, "verified.jsonl"))
	if err != nil {
		return err
	}
	excluded, committed := map[string]bool{}, map[string]bool{} // every committed pilot task; those not removed
	if exclude != "" {
		cs, err := readCandidates(exclude)
		if err != nil {
			return err
		}
		for _, c := range cs {
			excluded[c.Commit] = true
			if c.Removed == "" {
				committed[c.Commit] = true
			}
		}
	}
	var okc []mine.Candidate
	stubbed := map[string]int{} // candidates the stub check rejected, by reason
	for _, c := range latest(all) {
		v := c.Verified
		switch {
		case v == nil:
		case v.OK && v.Stub == nil:
			return fmt.Errorf("%s %s passed stage 2 without the stub check (amendment 3): run -stage verify first", c.Repo, c.Commit[:8])
		case v.OK:
			okc = append(okc, c)
		case v.Stub != nil && v.Stub.Rejected != "":
			stubbed[v.Stub.Rejected]++
		}
	}
	pilot, e1 := mine.Choose(okc, 10, 4, excluded)
	if exclude != "" {
		same := len(pilot) == len(committed)
		for _, c := range pilot {
			same = same && committed[c.Commit]
		}
		if !same {
			return fmt.Errorf("the pilot chosen from verified.jsonl is not the committed pilot in %s (its tasks not marked removed)", exclude)
		}
	}
	for name, set := range map[string][]mine.Candidate{"pilot.jsonl": pilot, "e1.jsonl": e1} {
		f, err := os.Create(filepath.Join(out, name))
		if err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		for _, c := range set {
			if err := enc.Encode(c); err != nil {
				f.Close()
				return err
			}
		}
		f.Close()
	}
	perRepo := map[string]int{}
	for _, c := range e1 {
		perRepo[c.Repo]++
	}
	logf(log, "choose: %d verified; the stub check removed %d (%s %d, %s %d, %s %d); pilot %d; E1's order %d, uncut %v",
		len(okc), stubbed[mine.RejectPassed]+stubbed[mine.RejectNoRun]+stubbed[mine.RejectNoBuild], mine.RejectPassed,
		stubbed[mine.RejectPassed], mine.RejectNoRun, stubbed[mine.RejectNoRun], mine.RejectNoBuild, stubbed[mine.RejectNoBuild],
		len(pilot), len(e1), perRepo)
	return nil
}

// latest keeps each candidate's last record: verified.jsonl is appended to, and a stub check appends a candidate's
// record again with its result.
func latest(cs []mine.Candidate) []mine.Candidate {
	idx := map[string]int{}
	var out []mine.Candidate
	for _, c := range cs {
		k := c.Repo + "/" + c.Commit
		if i, ok := idx[k]; ok {
			out[i] = c
			continue
		}
		idx[k] = len(out)
		out = append(out, c)
	}
	return out
}

// needsWork reports whether stage 2 still has something to do for c: no record yet, or one that passed without the
// stub check.
func needsWork(done map[string]mine.Candidate, c mine.Candidate) bool {
	prev, ok := done[c.Repo+"/"+c.Commit]
	return !ok || (prev.Verified != nil && prev.Verified.OK && prev.Verified.Stub == nil)
}
