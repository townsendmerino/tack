package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// guardWindow is how many requests after a firing count as "followed" (e1-calibration-preregistration-2026-10-18.md
// §13.4): progress within this many requests is credited to the firing.
const guardWindow = 3

// guardMarks says how each setting's firing shows in a run's events: a tool result the setting wrote, or, for
// stop_repeats, the turn's end. A setting that changes only the prompt (instructions_head) has no firing to count.
var guardMarks = map[string]func(tool, result string) bool{
	"fit_window": func(_, r string) bool {
		return strings.HasPrefix(r, "[window nearly full") || strings.Contains(r, " to fit the window; ")
	},
	"result_notes": func(_, r string) bool {
		return strings.Contains(r, "\n\n[tack: glob matches file names only") || strings.Contains(r, "\n\n[tack: nothing matched") ||
			strings.Contains(r, "\n\n[tack: unchanged: ")
	},
	"refuse_duplicate_edits": func(t, r string) bool {
		return (t == "edit" || t == "write") && strings.Contains(r, "this exact edit was already applied")
	},
	"verify_done": func(t, r string) bool {
		return t == "done" && strings.HasPrefix(r, "done refused: no build or test has run since")
	},
	"edit_tolerances": func(t, r string) bool { return t == "edit" && strings.Contains(r, " (old_string matched with ") },
}

var buildCmd = regexp.MustCompile(`\bgo (build|test|vet)\b`)

// runEvent is the part of an events.jsonl line the guard table reads.
type runEvent struct {
	Type    string `json:"type"`
	Payload struct {
		ID         string          `json:"id"`
		Name       string          `json:"name"`
		Input      json.RawMessage `json:"input"`
		Outcome    string          `json:"outcome"`
		Result     string          `json:"result"`
		StopReason string          `json:"stop_reason"`
	} `json:"payload"`
}

// step is one tool result, at the request (1-based) whose response made the call.
type step struct {
	req              int
	tool, result     string
	edited, verified bool // an edit or write applied; a go build, test or vet that exited 0
}

// readSteps is a run's tool results in order, and its request count and stop reason.
func readSteps(path string) (steps []step, requests int, stop string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, "", err
	}
	defer f.Close()
	calls := map[string]runEvent{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var e runEvent
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "request.sent":
			requests++
		case "tool.call":
			calls[e.Payload.ID] = e
		case "tool.finished":
			c := calls[e.Payload.ID]
			s := step{req: requests, tool: c.Payload.Name, result: e.Payload.Result}
			ok := e.Payload.Outcome == "ok"
			s.edited = ok && (s.tool == "edit" || s.tool == "write")
			if ok && s.tool == "bash" {
				var in struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(c.Payload.Input, &in)
				s.verified = buildCmd.MatchString(in.Command)
			}
			steps = append(steps, s)
		case "turn.end":
			stop = e.Payload.StopReason
		}
	}
	return steps, requests, stop, sc.Err()
}

// guardTally is one setting's firings over an arm's runs.
type guardTally struct {
	runsOn, runsFired, firings, edited, verified, either int
}

// guards is the per-setting table (§13.4): for each setting an arm has on, the runs it fired in, its firings, and how
// many firings an applied edit or a passing build followed within guardWindow requests. stop_repeats fires once, at the
// turn's end, so nothing can follow it. New gold lines seen is not counted here: it needs the task's FixGold
// (tools/replaycontext).
func guards(w io.Writer, j *jobData) {
	fmt.Fprintln(w, "## Guard firings (§13.4)")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "A firing is followed when an edit is applied, or a `go build`, `go test` or `go vet` passes, within %d requests after it.\n\n", guardWindow)
	fmt.Fprintln(w, "| arm | setting | runs on | runs it fired in | firings | per run | followed by an edit | by a passing build | by either |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|")
	unread := 0
	for _, a := range j.armOrder {
		names := map[string]bool{}
		for _, r := range j.arms[a] {
			for _, s := range r.Settings {
				names[s] = true
			}
		}
		var order []string
		for n := range names {
			order = append(order, n)
		}
		slices.Sort(order)
		tallies := map[string]*guardTally{}
		for _, n := range order {
			tallies[n] = &guardTally{}
		}
		for i, r := range j.arms[a] {
			steps, _, stop, err := readSteps(filepath.Join(j.dirs[a][i], "events.jsonl"))
			if err != nil {
				unread++
				continue
			}
			for _, n := range order {
				if !slices.Contains(r.Settings, n) {
					continue
				}
				t := tallies[n]
				t.runsOn++
				fired := 0
				if n == "stop_repeats" {
					if stop == "repeat_loop" {
						fired = 1
					}
				} else if mark := guardMarks[n]; mark != nil {
					for k, s := range steps {
						if !mark(s.tool, s.result) {
							continue
						}
						fired++
						e, v := false, false
						for _, later := range steps[k+1:] {
							if later.req > s.req+guardWindow {
								break
							}
							if later.req > s.req {
								e, v = e || later.edited, v || later.verified
							}
						}
						if e {
							t.edited++
						}
						if v {
							t.verified++
						}
						if e || v {
							t.either++
						}
					}
				}
				if fired > 0 {
					t.runsFired++
				}
				t.firings += fired
			}
		}
		for _, n := range order {
			t := tallies[n]
			if _, counted := guardMarks[n]; !counted && n != "stop_repeats" {
				fmt.Fprintf(w, "| %s | %s | %d | — | — | — | prompt only: no firing to count | | |\n", a, n, t.runsOn)
				continue
			}
			per := 0.0
			if t.runsOn > 0 {
				per = float64(t.firings) / float64(t.runsOn)
			}
			if n == "stop_repeats" {
				fmt.Fprintf(w, "| %s | %s | %d | %d | %d | %.2f | ends the turn | | |\n", a, n, t.runsOn, t.runsFired, t.firings, per)
				continue
			}
			fmt.Fprintf(w, "| %s | %s | %d | %d | %d | %.2f | %s | %s | %s |\n", a, n, t.runsOn, t.runsFired, t.firings, per,
				frac(t.edited, t.firings), frac(t.verified, t.firings), frac(t.either, t.firings))
		}
	}
	fmt.Fprintln(w)
	if unread > 0 {
		fmt.Fprintf(w, "- %d runs' events could not be read and are left out of the table\n\n", unread)
	}
}

func frac(n, d int) string {
	if d == 0 {
		return "—"
	}
	return fmt.Sprintf("%d (%.0f%%)", n, 100*float64(n)/float64(d))
}

// runDirOf is where pilot wrote a record's run (internal/eval/pilot/run.go:runDir), beside its results.jsonl.
func runDirOf(results string, r pilot.Record) string {
	name := r.Run.Arm
	if r.Rep > 0 {
		name = fmt.Sprintf("%s-%d", r.Run.Arm, r.Rep)
	}
	return filepath.Join(filepath.Dir(results), fmt.Sprintf("task-%02d", r.Task), name)
}
