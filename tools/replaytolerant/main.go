// Command replaytolerant replays captured failed edits against edit's tolerances (tools.MatchTolerances;
// e1-calibration-preregistration-2026-10-18.md §12.22), one punctuation kind at a time. For each run it rebuilds each file
// as it was at that request, from the task's base commit in its mirror plus the run's own successful edits and writes, as
// tools/replayedits did for 4.1. Each "text isn't in" failure on a .go file is put through the tolerances with each kind
// alone; an edit a tolerance would apply is checked for parsing as Go and for landing on the lines the task's fix
// changes, and written to -dump for reading case by case.
//
//	go run ./tools/replaytolerant -mirrors DIR [-dump FILE] JOB/results.jsonl...
//
// Each run's events.jsonl sits beside its job's results.jsonl under task-NN/<arm> or task-NN/<arm>-<rep>.
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tools"
	"github.com/townsendmerino/tack/tools/gate"
)

// kinds are the punctuation kinds §12.22 replays, each alone.
var kinds = []byte{';', ',', ')', ']', '}', '.', ':'}

type editIn struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	Content    string `json:"content"`
}

// tally is one tolerance's count over the runs.
type tally struct{ calls, distinct, parses, onFix, ambiguous int }

var trees = map[string]map[string]string{} // repo@base -> path -> content

// tree is every regular file of repo at base, from its mirror.
func tree(mirrors, repo, base string) (map[string]string, error) {
	k := repo + "@" + base
	if t, ok := trees[k]; ok {
		return t, nil
	}
	out, err := exec.Command("git", "--git-dir", filepath.Join(mirrors, repo+".git"), "archive", base).Output()
	if err != nil {
		return nil, fmt.Errorf("git archive %s: %w", k, err)
	}
	t := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(out))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && h.Size < 4<<20 {
			b, _ := io.ReadAll(tr)
			t[h.Name] = string(b)
		}
	}
	trees[k] = t
	return t, nil
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? `)

// fixHunks is, per file, the base's line ranges (1-based, inclusive) that the task's fix commit changes.
func fixHunks(mirrors, repo, base, commit string) map[string][][2]int {
	out, _ := exec.Command("git", "--git-dir", filepath.Join(mirrors, repo+".git"), "diff", "-U0", base, commit).Output()
	h := map[string][][2]int{}
	file := ""
	for l := range strings.SplitSeq(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, "+++ b/"):
			file = strings.TrimPrefix(l, "+++ b/")
		case strings.HasPrefix(l, "@@"):
			if m := hunkRe.FindStringSubmatch(l); m != nil {
				s, n := 0, 1
				fmt.Sscan(m[1], &s)
				if m[2] != "" {
					fmt.Sscan(m[2], &n)
				}
				h[file] = append(h[file], [2]int{s, s + max(n, 1) - 1})
			}
		}
	}
	return h
}

func clean(p string) string { return strings.TrimPrefix(path.Clean(strings.TrimPrefix(p, "./")), "/") }

func main() {
	mirrors := flag.String("mirrors", "", "directory of bare mirrors, <repo>.git")
	dump := flag.String("dump", "", "write each edit a tolerance would apply here, one JSON object per line")
	flag.Parse()
	if *mirrors == "" || flag.NArg() == 0 {
		fmt.Println(gate.Verdict(gate.Inconclusive, "replaytolerant: usage: -mirrors DIR [-dump FILE] JOB/results.jsonl..."))
		os.Exit(2)
	}
	var dumpW io.Writer = io.Discard
	if *dump != "" {
		f, err := os.Create(*dump)
		if err != nil {
			fmt.Println(gate.Verdict(gate.Inconclusive, "replaytolerant: "+err.Error()))
			os.Exit(2)
		}
		defer f.Close()
		dumpW = f
	}
	tallies := map[string]*tally{}
	var runs, failures int
	for _, res := range flag.Args() {
		f, err := os.Open(res)
		if err != nil {
			fmt.Println(gate.Verdict(gate.Inconclusive, "replaytolerant: "+err.Error()))
			os.Exit(2)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var rec pilot.Record
			if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Run.Void != "" {
				continue
			}
			name := rec.Run.Arm
			if rec.Rep > 0 {
				name = fmt.Sprintf("%s-%d", rec.Run.Arm, rec.Rep)
			}
			base, err := tree(*mirrors, rec.Repo, rec.Base)
			if err != nil {
				fmt.Println(gate.Verdict(gate.Inconclusive, "replaytolerant: "+err.Error()))
				os.Exit(2)
			}
			runs++
			ev := filepath.Join(filepath.Dir(res), fmt.Sprintf("task-%02d", rec.Task), name, "events.jsonl")
			failures += replayRun(ev, base, fixHunks(*mirrors, rec.Repo, rec.Base, rec.Commit), tallies, dumpW)
		}
		f.Close()
	}
	names := []string{tools.TolNoSpace}
	for _, k := range kinds {
		names = append(names, tools.TolPunctName(k))
	}
	fmt.Printf("runs %d; .go edits failing \"this text isn't in\": %d\n", runs, failures)
	fmt.Println("tolerance | calls | distinct | parse after | on the fix's lines | refused as ambiguous")
	for _, n := range names {
		t := tallies[n]
		if t == nil {
			t = &tally{}
		}
		fmt.Printf("%s | %d | %d | %d | %d | %d\n", n, t.calls, t.distinct, t.parses, t.onFix, t.ambiguous)
	}
	fmt.Println(gate.Verdict(gate.OK, fmt.Sprintf("replaytolerant: %d failed .go edits replayed over %d runs", failures, runs)))
}

// replayRun replays one run's events and adds each tolerance's rescues to tallies, returning the failures it tried.
func replayRun(events string, base map[string]string, hunks map[string][][2]int, tallies map[string]*tally, dump io.Writer) int {
	f, err := os.Open(events)
	if err != nil {
		return 0
	}
	defer f.Close()
	files := map[string]string{} // the run's own view, overriding base
	get := func(p string) (string, bool) {
		if s, ok := files[p]; ok {
			return s, true
		}
		s, ok := base[p]
		return s, ok
	}
	calls := map[string]proto.ToolCall{}
	seen := map[string]bool{}
	tried := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var e proto.Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		v, err := e.Decode()
		if err != nil {
			continue
		}
		switch p := v.(type) {
		case proto.ToolCall:
			calls[p.ID] = p
		case proto.ToolFinished:
			c := calls[p.ID]
			var in editIn
			json.Unmarshal(c.Input, &in)
			pth := clean(in.Path)
			switch {
			case c.Name == "write" && p.Outcome == proto.OutcomeOK:
				files[pth] = in.Content
			case c.Name == "edit" && p.Outcome == proto.OutcomeOK:
				if cur, ok := get(pth); ok && strings.Contains(cur, in.OldString) {
					if in.ReplaceAll {
						files[pth] = strings.ReplaceAll(cur, in.OldString, in.NewString)
					} else {
						files[pth] = strings.Replace(cur, in.OldString, in.NewString, 1)
					}
				}
			case c.Name == "edit" && strings.HasPrefix(loop.StripNotes(p.Result), "error: this text isn't in") &&
				strings.HasSuffix(pth, ".go") && !in.ReplaceAll:
				cur, ok := get(pth)
				if !ok {
					continue
				}
				tried++
				key := pth + "\x00" + in.OldString
				first := !seen[key]
				seen[key] = true
				for _, k := range kinds {
					m := tools.MatchTolerances(cur, in.OldString, []byte{k})
					if m.Name == tools.TolNoSpace && k != kinds[0] {
						continue // counted once, with the first kind
					}
					if m.AmbiguousIn != "" {
						if m.AmbiguousIn != tools.TolNoSpace || k == kinds[0] {
							tl(tallies, m.AmbiguousIn).ambiguous++
						}
						continue
					}
					if m.Name == "" {
						continue
					}
					t := tl(tallies, m.Name)
					t.calls++
					out := cur[:m.Start] + in.NewString + cur[m.End:]
					parses := tools.ParsesAsGo(out)
					on := onFix(cur, m.Start, m.End, hunks[pth])
					if parses {
						t.parses++
					}
					if on {
						t.onFix++
					}
					if first {
						t.distinct++
						b, _ := json.Marshal(map[string]any{"run": events, "path": pth, "tolerance": m.Name, "old": in.OldString,
							"region": cur[m.Start:m.End], "new": in.NewString, "parses": parses, "on_fix": on})
						fmt.Fprintln(dump, string(b))
					}
				}
			}
		}
	}
	return tried
}

func tl(m map[string]*tally, k string) *tally {
	if m[k] == nil {
		m[k] = &tally{}
	}
	return m[k]
}

// onFix reports whether [start, end) of cur lies within 3 lines of a line the fix changes.
func onFix(cur string, start, end int, hunks [][2]int) bool {
	a := strings.Count(cur[:start], "\n") + 1
	b := strings.Count(cur[:end], "\n") + 1
	for _, h := range hunks {
		if a <= h[1]+3 && b >= h[0]-3 {
			return true
		}
	}
	return false
}
