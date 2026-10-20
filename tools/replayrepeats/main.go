// Command replayrepeats applies change 4.5's stop rule (loop.RepeatGuard) to captured A1 runs, exactly as the loop would
// have with [run] stop_repeats on (e1-calibration-preregistration-2026-10-18.md §12.7). Each argument is a job's
// results.jsonl; each run's events.jsonl sits beside it under task-NN/<arm> or task-NN/<arm>-<rep>. It prints one line
// per stopped run, a summary, and the registered verdict: PASS (adopt) when no passing run would have been stopped,
// FAIL when one would, INCONCLUSIVE when a run's events or base tree are missing.
//
// With -mirrors, a directory of bare mirrors <repo>.git, it replays the rule as amended (§12.13), which compares the
// file's state after each edit and write, and judges that; it also prints each stop that moves between the two rules,
// and what the duplicate-edit guard (§12.14) would have refused.
//
//	go run ./tools/replayrepeats [-mirrors DIR] JOB/results.jsonl...
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/tools/gate"
)

func main() {
	mirrors := flag.String("mirrors", "", "directory of bare mirrors, <repo>.git: replay the amended rule (§12.13)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Println(gate.Verdict(gate.Inconclusive, "replayrepeats: no results.jsonl given"))
		os.Exit(2)
	}
	var runs, stopped, passStopped, missing, savedReq, moved, lostStopped int
	var dupRuns, dupPass, dupCalls, dupApplied int
	var savedSecs float64
	for _, res := range flag.Args() {
		f, err := os.Open(res)
		if err != nil {
			fmt.Println(gate.Verdict(gate.Inconclusive, "replayrepeats: "+err.Error()))
			os.Exit(2)
		}
		job := filepath.Join(filepath.Base(filepath.Dir(filepath.Dir(res))), filepath.Base(filepath.Dir(res)))
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
			events := filepath.Join(filepath.Dir(res), fmt.Sprintf("task-%02d", rec.Task), name, "events.jsonl")
			runs++
			var base map[string]string
			if *mirrors != "" {
				if base, err = pilot.BaseTree(*mirrors, rec.Repo, rec.Base); err != nil {
					missing++
					fmt.Printf("MISSING base tree for %s: %v\n", events, err)
					continue
				}
			}
			r, err := pilot.ReplayRepeats(events, base)
			if err != nil {
				missing++
				fmt.Printf("MISSING %s: %v\n", events, err)
				continue
			}
			mark := ""
			if rec.Pass {
				mark = "  <- PASSING RUN"
			}
			if base != nil {
				was, err := pilot.ReplayRepeats(events, nil)
				if err != nil {
					missing++
					fmt.Printf("MISSING %s: %v\n", events, err)
					continue
				}
				if was.Stopped != r.Stopped || was.StopRequest != r.StopRequest {
					moved++
					live := ""
					if slices.Contains(rec.Settings, "stop_repeats") {
						live = " [stop_repeats was on live: the run ended at the registered rule's stop, so what the amended rule does after it is not seen]"
					}
					fmt.Printf("MOVED %s task %2d %-4s: registered rule %s, amended rule %s (%d requests)%s%s\n", job, rec.Task,
						name, stopAt(was), stopAt(r), r.Requests, live, mark)
				}
				d, err := pilot.ReplayDuplicateEdits(events)
				if err != nil {
					missing++
					fmt.Printf("MISSING %s: %v\n", events, err)
					continue
				}
				if d.Calls > 0 {
					dupRuns++
					dupCalls += d.Calls
					dupApplied += d.Applied
					if rec.Pass {
						dupPass++
					}
					fmt.Printf("DUPLICATE %s task %2d %-4s: %d identical edits (%d applied again), the first in request %d of %d%s\n",
						job, rec.Task, name, d.Calls, d.Applied, d.First, r.Requests, mark)
				}
			}
			if !r.Stopped {
				continue
			}
			stopped++
			savedReq += r.SavedRequests
			savedSecs += r.SavedSecs
			lost := ""
			if r.Lost > 0 {
				lostStopped++
				lost = fmt.Sprintf(" [rebuild lost %d edits]", r.Lost)
			}
			if rec.Pass {
				passStopped++
			}
			fmt.Printf("%s task %2d %-4s stopped after request %d of %d (%s, %d unchanged repeats), saves %d requests, %.0f s%s%s\n",
				job, rec.Task, name, r.StopRequest, r.Requests, r.StopTool, r.Repeats, r.SavedRequests, r.SavedSecs, lost, mark)
		}
		f.Close()
	}
	rule := "registered rule"
	if *mirrors != "" {
		rule = "amended rule"
		fmt.Printf("duplicate-edit guard: would have touched %d runs (%d passing), refusing %d calls, %d of which the run applied again\n",
			dupRuns, dupPass, dupCalls, dupApplied)
	}
	sum := fmt.Sprintf("replayrepeats (%s): %d runs, %d stopped, %d of them passing; saves %d requests and %.0f min of wall time",
		rule, runs, stopped, passStopped, savedReq, savedSecs/60)
	if *mirrors != "" {
		sum += fmt.Sprintf("; %d stops moved; %d stopped runs with a lost rebuild", moved, lostStopped)
	}
	switch {
	case missing > 0:
		fmt.Println(gate.Verdict(gate.Inconclusive, fmt.Sprintf("%s; %d runs' events or base trees missing", sum, missing)))
		os.Exit(2)
	case passStopped > 0:
		fmt.Println(gate.Verdict(gate.Fail, sum+": it would have stopped a passing run (not adopted)"))
		os.Exit(1)
	}
	fmt.Println(gate.Verdict(gate.OK, sum+": it stops no passing run (adopted)"))
}

// stopAt says where a replay stopped.
func stopAt(r pilot.RepeatReplay) string {
	if !r.Stopped {
		return "does not stop"
	}
	return fmt.Sprintf("stops after request %d (%s)", r.StopRequest, r.StopTool)
}
