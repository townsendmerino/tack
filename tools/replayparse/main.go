// Command replayparse replays captured runs to count edits that leave a Go file unparseable, for the question of
// refusing such an edit before it reaches disk (DESIGN §7.5, 2026-11-08). For each run it rebuilds each file from the
// task's base commit in its mirror plus the run's own successful edits and writes, as tools/replaytolerant does, and
// follows every .go file outside testdata/ from parsing to not parsing and back.
//
//	go run ./tools/replayparse -mirrors DIR [-dump FILE] JOB/results.jsonl...
//
// A break is a successful edit or write after which a file that parsed no longer parses. It is restored when a later
// edit or write makes the file parse again; otherwise it lasts to the run's end. A write that creates a file that does
// not parse is not a break, since the file never parsed, and is counted apart (the "new unparseable" column). A file whose content the replay cannot
// follow (an edit it cannot place) is dropped from that point.
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tools"
)

type editIn struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	Content    string `json:"content"`
}

var trees = map[string]map[string]string{}

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

func clean(p string) string { return strings.TrimPrefix(path.Clean(strings.TrimPrefix(p, "./")), "/") }

func parseErr(src string) string {
	_, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.SkipObjectResolution)
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

// brk is one break: where, by which call, and what became of it.
type brk struct {
	Job, Run, File, Tool, Err string
	Old, New                  string
	Restored                  bool
	CallsToRestore            int // later tool calls of any kind until the restoring one
	Pass                      bool
}

type runOut struct {
	breaks    []*brk
	endBroken bool
	pass      bool
	goEdits   int
	untracked int
	// newUnparseable counts writes that created a .go file that does not parse; endNew is set when such a file still
	// does not parse at the run's end. Neither is a break (the file never parsed), so the break columns leave them out.
	newUnparseable int
	endNew         bool
}

func replay(events string, base map[string]string, job, name string, pass bool) runOut {
	var ro runOut
	ro.pass = pass
	f, err := os.Open(events)
	if err != nil {
		return ro
	}
	defer f.Close()
	files := map[string]string{}
	lost := map[string]bool{}
	get := func(p string) (string, bool) {
		if s, ok := files[p]; ok {
			return s, true
		}
		s, ok := base[p]
		return s, ok
	}
	open := map[string]*brk{}    // file -> its unrestored break
	newOpen := map[string]bool{} // files a write created unparseable, not yet made to parse
	calls := map[string]proto.ToolCall{}
	callNo := 0
	openAt := map[string]int{}
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
			callNo++
			c := calls[p.ID]
			if p.Outcome != proto.OutcomeOK || (c.Name != "edit" && c.Name != "write") {
				continue
			}
			var in editIn
			json.Unmarshal(c.Input, &in)
			pth := clean(in.Path)
			if !strings.HasSuffix(pth, ".go") || strings.Contains(pth, "testdata/") || lost[pth] {
				continue
			}
			cur, had := get(pth)
			var next string
			switch c.Name {
			case "write":
				next = in.Content
			case "edit":
				ro.goEdits++
				if !had || in.OldString == "" {
					lost[pth] = true
					ro.untracked++
					continue
				}
				switch {
				case strings.Contains(cur, in.OldString) && in.ReplaceAll:
					next = strings.ReplaceAll(cur, in.OldString, in.NewString)
				case strings.Contains(cur, in.OldString):
					next = strings.Replace(cur, in.OldString, in.NewString, 1)
				default:
					m := tools.MatchTolerances(cur, in.OldString, tools.TolerancePunct)
					if m.Name == "" {
						lost[pth] = true
						ro.untracked++
						continue
					}
					next = cur[:m.Start] + in.NewString + cur[m.End:]
				}
			}
			files[pth] = next
			before := had && parseErr(cur) == ""
			after := parseErr(next)
			if !had && after != "" {
				ro.newUnparseable++
				newOpen[pth] = true
			}
			if had && after == "" {
				delete(newOpen, pth)
			}
			if b := open[pth]; b != nil {
				if after == "" {
					b.Restored = true
					b.CallsToRestore = callNo - openAt[pth]
					delete(open, pth)
				}
				continue
			}
			if before && after != "" {
				b := &brk{Job: job, Run: name, File: pth, Tool: c.Name, Err: after, Old: in.OldString, New: in.NewString, Pass: pass}
				if c.Name == "write" {
					b.Old, b.New = "", ""
				}
				ro.breaks = append(ro.breaks, b)
				open[pth] = b
				openAt[pth] = callNo
			}
		}
	}
	ro.endBroken = len(open) > 0
	ro.endNew = len(newOpen) > 0
	return ro
}

type agg struct {
	runs, pass, goEdits, untracked                 int
	breaks, restored, runsWithBreak, runsEndBroken int
	endBrokenPass, breakRestoredPass               int
	failRunsEndBroken                              int
	newUnparseable, runsEndNew                     int
}

func main() {
	mirrors := flag.String("mirrors", "", "directory of bare mirrors, <repo>.git")
	dump := flag.String("dump", "", "write each break here, one JSON object per line")
	flag.Parse()
	var dumpW io.Writer = io.Discard
	if *dump != "" {
		f, err := os.Create(*dump)
		if err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
		defer f.Close()
		dumpW = f
	}
	byJob := map[string]*agg{}
	all := &agg{}
	restoreSteps := []int{}
	for _, res := range flag.Args() {
		job := filepath.Dir(res)
		if i := strings.Index(job, "/e1/"); i >= 0 {
			job = job[i+4:]
		}
		if parts := strings.Split(job, "/"); len(parts) > 0 {
			job = parts[0] // the job's top directory: one screen, check or night
		}
		f, err := os.Open(res)
		if err != nil {
			continue
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
				continue
			}
			ev := filepath.Join(filepath.Dir(res), fmt.Sprintf("task-%02d", rec.Task), name, "events.jsonl")
			if _, err := os.Stat(ev); err != nil {
				continue
			}
			ro := replay(ev, base, job, fmt.Sprintf("task-%02d/%s", rec.Task, name), rec.Pass)
			a := byJob[job]
			if a == nil {
				a = &agg{}
				byJob[job] = a
			}
			for _, x := range []*agg{a, all} {
				x.runs++
				x.goEdits += ro.goEdits
				x.untracked += ro.untracked
				x.newUnparseable += ro.newUnparseable
				if ro.endNew {
					x.runsEndNew++
				}
				if rec.Pass {
					x.pass++
				}
				x.breaks += len(ro.breaks)
				if len(ro.breaks) > 0 {
					x.runsWithBreak++
				}
				if ro.endBroken {
					x.runsEndBroken++
					if rec.Pass {
						x.endBrokenPass++
					} else {
						x.failRunsEndBroken++
					}
				}
				anyRestored := false
				for _, b := range ro.breaks {
					if b.Restored {
						x.restored++
						anyRestored = true
					}
				}
				if anyRestored && rec.Pass {
					x.breakRestoredPass++
				}
			}
			for _, b := range ro.breaks {
				if b.Restored {
					restoreSteps = append(restoreSteps, b.CallsToRestore)
				}
				j, _ := json.Marshal(b)
				fmt.Fprintln(dumpW, string(j))
			}
		}
		f.Close()
	}
	jobs := make([]string, 0, len(byJob))
	for j := range byJob {
		jobs = append(jobs, j)
	}
	sort.Strings(jobs)
	fmt.Println("job | runs | pass | go edits | untracked | breaks | restored | runs with a break | runs ending unparseable (pass/fail) | passing runs with a restored break | new files written unparseable | runs ending with one")
	pr := func(n string, a *agg) {
		fmt.Printf("%s | %d | %d | %d | %d | %d | %d | %d | %d (%d/%d) | %d | %d | %d\n", n, a.runs, a.pass, a.goEdits, a.untracked,
			a.breaks, a.restored, a.runsWithBreak, a.runsEndBroken, a.endBrokenPass, a.failRunsEndBroken, a.breakRestoredPass,
			a.newUnparseable, a.runsEndNew)
	}
	for _, j := range jobs {
		pr(j, byJob[j])
	}
	pr("ALL", all)
	sort.Ints(restoreSteps)
	fmt.Println("calls until restored, sorted:", restoreSteps)
}
