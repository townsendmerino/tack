package mine

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Verified is a candidate's stage-2 record.
type Verified struct {
	OK         bool                 `json:"ok"`
	Why        string               `json:"why,omitempty"` // the reason when not OK
	Oracle     []string             `json:"oracle"`        // the named tests that fail on the parent and pass on the commit
	OnParent   map[string]string    `json:"on_parent"`     // test → pass, fail, skip, or build-fail
	OnCommit   [2]map[string]string `json:"on_commit"`
	CommitSecs [2]float64           `json:"commit_secs"`
	CheckCmd   string               `json:"check_cmd"`
	// Stub is the stub check (amendment 3), run once the rest of stage 2 passes; nil on a record made before it.
	Stub *Stub `json:"stub,omitempty"`
}

// Stage-2 limits (rules 1 and 3).
const (
	testTimeout   = "120s"
	maxCommitSecs = 60.0
	// candidateCap bounds one candidate's whole verification, builds included, so one slow module cannot stall the
	// night job.
	candidateCap = 10 * time.Minute
)

// Verify runs stage 2 for one candidate in work, a clone of the repository made by PrepareClone and reused across
// candidates: checking out a nearby commit rewrites only the files that differ. The clone can reach later commits;
// that does not matter here, because no agent runs in it.
func Verify(ctx context.Context, r Repo, c Candidate, work string) Verified {
	ctx, cancel := context.WithTimeout(ctx, candidateCap)
	defer cancel()
	v := Verified{CheckCmd: checkCmd(c.Tests, c.Packages)}
	fail := func(format string, args ...any) Verified {
		v.Why = fmt.Sprintf(format, args...)
		return v
	}
	if err := checkout(work, c.Base); err != nil {
		return fail("checkout parent: %v", err)
	}
	// The commit's test files over the parent's tree (rule 1).
	for _, f := range c.TestFiles {
		src, err := git(r.Path, "show", c.Commit+":"+f)
		if err != nil {
			return fail("read %s: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(work, f), []byte(src), 0o644); err != nil {
			return fail("write %s: %v", f, err)
		}
	}
	v.OnParent, _ = RunTests(ctx, work, c.Tests, c.Packages)
	for _, t := range c.Tests { // rule 2
		switch v.OnParent[t] {
		case "skip":
			return fail("%s skips on the parent", t)
		case "fail", "build-fail":
			v.Oracle = append(v.Oracle, t)
		}
	}
	if len(v.Oracle) == 0 {
		return fail("no named test fails on the parent")
	}
	v.CheckCmd = checkCmd(v.Oracle, c.Packages)
	if err := checkout(work, c.Commit); err != nil {
		return fail("checkout commit: %v", err)
	}
	for run := 0; run < 2; run++ { // rule 3
		res, secs := RunTests(ctx, work, v.Oracle, c.Packages)
		v.OnCommit[run], v.CommitSecs[run] = res, secs
		for _, t := range v.Oracle {
			if res[t] != "pass" {
				return fail("%s is %q on commit run %d", t, res[t], run+1)
			}
		}
		if secs > maxCommitSecs {
			return fail("commit run %d took %.0f s", run+1, secs)
		}
	}
	st := StubCheck(ctx, r, c, v.Oracle, work)
	v.Stub = &st
	v.OK = st.Rejected == ""
	if !v.OK {
		v.Why = st.Why()
	}
	return v
}

// PrepareClone makes work a clone of the repository that shares its objects (git clone --shared), once.
func PrepareClone(r Repo, work string) error {
	if _, err := os.Stat(filepath.Join(work, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(work), 0o755); err != nil {
		return err
	}
	out, err := exec.Command("git", "clone", "-q", "--shared", "--no-checkout", r.Path, work).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone --shared: %v: %s", err, out)
	}
	return nil
}

// checkout puts work at commit exactly: tracked files reset, everything untracked removed.
func checkout(work, commit string) error {
	if _, err := git(work, "checkout", "-q", "-f", "--detach", commit); err != nil {
		return err
	}
	_, err := git(work, "clean", "-q", "-f", "-d", "-x")
	return err
}

func checkCmd(tests, pkgs []string) string {
	return fmt.Sprintf("GOWORK=off GOFLAGS=-mod=mod CGO_ENABLED=0 go test -json -count=1 -timeout %s -buildvcs=false -run '^(%s)$' %s", testTimeout, strings.Join(tests, "|"),
		strings.Join(pkgs, " "))
}

// RunTests runs the named tests with go test -json, in the environment the check command names, and returns each one's
// result and the wall time. A test with no result in a package that failed to build is "build-fail". The pilot's oracle
// calls it too, so tasks are graded exactly as they were verified.
func RunTests(ctx context.Context, dir string, tests, pkgs []string) (map[string]string, float64) {
	args := append([]string{"test", "-json", "-count=1", "-timeout", testTimeout, "-buildvcs=false",
		"-run", "^(" + strings.Join(tests, "|") + ")$"}, pkgs...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "CGO_ENABLED=0")
	t0 := time.Now()
	out, _ := cmd.Output()
	secs := time.Since(t0).Seconds()
	res := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev struct{ Action, Test string }
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Test == "" || strings.Contains(ev.Test, "/") {
			continue
		}
		if ev.Action == "pass" || ev.Action == "fail" || ev.Action == "skip" {
			res[ev.Test] = ev.Action
		}
	}
	for _, t := range tests {
		if _, ok := res[t]; !ok {
			res[t] = "build-fail"
		}
	}
	return res, secs
}

// Order sorts candidates into stage 3's fixed order, sha256(repo + "/" + commit) ascending, in a new slice.
func Order(cs []Candidate) []Candidate {
	key := func(c Candidate) string {
		sum := sha256.Sum256([]byte(c.Repo + "/" + c.Commit))
		return hex.EncodeToString(sum[:])
	}
	ordered := append([]Candidate(nil), cs...)
	sort.Slice(ordered, func(i, j int) bool { return key(ordered[i]) < key(ordered[j]) })
	return ordered
}

// Choose is stage 3: order the verified candidates by sha256(repo/commit), take the pilot (pilotN, at most pilotCap per
// repo), then E1 (e1N, at most e1Cap per repo) from what follows, a candidate the pilot skipped for its cap included.
// The sets do not overlap.
func Choose(verified []Candidate, pilotN, pilotCap, e1N, e1Cap int) (pilot, e1 []Candidate) {
	ordered := Order(verified)
	take := func(from []Candidate, n, cap int) (got, rest []Candidate) {
		per := map[string]int{}
		for _, c := range from {
			if len(got) < n && per[c.Repo] < cap {
				got = append(got, c)
				per[c.Repo]++
			} else {
				rest = append(rest, c)
			}
		}
		return got, rest
	}
	pilot, rest := take(ordered, pilotN, pilotCap)
	e1, _ = take(rest, e1N, e1Cap)
	return pilot, e1
}
