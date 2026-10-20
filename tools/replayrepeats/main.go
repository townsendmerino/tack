// Command replayrepeats applies change 4.5's stop rule (loop.RepeatGuard) to captured A1 runs, exactly as the loop would
// have with [run] stop_repeats on (e1-calibration-preregistration-2026-10-18.md §12.7). Each argument is a job's
// results.jsonl; each run's events.jsonl sits beside it under task-NN/<arm> or task-NN/<arm>-<rep>. It prints one line
// per stopped run, a summary, and the registered verdict: PASS (adopt) when no passing run would have been stopped,
// FAIL when one would, INCONCLUSIVE when a run's events are missing.
//
//	go run ./tools/replayrepeats JOB/results.jsonl...
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/tools/gate"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println(gate.Verdict(gate.Inconclusive, "replayrepeats: no results.jsonl given"))
		os.Exit(2)
	}
	var runs, stopped, passStopped, missing, savedReq int
	var savedSecs float64
	for _, res := range os.Args[1:] {
		f, err := os.Open(res)
		if err != nil {
			fmt.Println(gate.Verdict(gate.Inconclusive, "replayrepeats: "+err.Error()))
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
			events := filepath.Join(filepath.Dir(res), fmt.Sprintf("task-%02d", rec.Task), name, "events.jsonl")
			runs++
			r, err := pilot.ReplayRepeats(events)
			if err != nil {
				missing++
				fmt.Printf("MISSING %s: %v\n", events, err)
				continue
			}
			if !r.Stopped {
				continue
			}
			stopped++
			savedReq += r.SavedRequests
			savedSecs += r.SavedSecs
			mark := ""
			if rec.Pass {
				passStopped++
				mark = "  <- PASSING RUN"
			}
			fmt.Printf("%s task %2d %-4s stopped after request %d of %d (%s, %d unchanged repeats), saves %d requests, %.0f s%s\n",
				filepath.Base(filepath.Dir(res)), rec.Task, name, r.StopRequest, r.Requests, r.StopTool, r.Repeats,
				r.SavedRequests, r.SavedSecs, mark)
		}
		f.Close()
	}
	sum := fmt.Sprintf("replayrepeats: %d runs, %d stopped, %d of them passing; saves %d requests and %.0f min of wall time",
		runs, stopped, passStopped, savedReq, savedSecs/60)
	switch {
	case missing > 0:
		fmt.Println(gate.Verdict(gate.Inconclusive, fmt.Sprintf("%s; %d runs' events missing", sum, missing)))
		os.Exit(2)
	case passStopped > 0:
		fmt.Println(gate.Verdict(gate.Fail, sum+": it would have stopped a passing run (not adopted)"))
		os.Exit(1)
	}
	fmt.Println(gate.Verdict(gate.OK, sum+": it stops no passing run (adopted)"))
}
