package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/eval/pilot"
)

// readCapLines is where read stops a whole-file read (tools.readMaxLines; pilot's contextReadCapLines).
const readCapLines = 200

// outlineHit is C7 §13.2's gate 0 for one run (e1-calibration-preregistration-2026-10-18.md §13.2): whether it made a
// whole-file read, with no offset or limit, that read stopped at readCapLines, of a gold file whose gold span starts past
// that line. An outline at the cut would have named that span's line range.
func outlineHit(events string, base map[string]string, gold []pilot.LineSpan) (bool, error) {
	late := map[string]bool{} // gold files with a span starting past the cut
	for _, s := range gold {
		if s.Start > readCapLines {
			late[s.File] = true
		}
	}
	if len(late) == 0 {
		return false, nil
	}
	f, err := os.Open(events)
	if err != nil {
		return false, err
	}
	defer f.Close()
	type ev struct {
		Type    string `json:"type"`
		Payload struct {
			ID      string          `json:"id"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			Outcome string          `json:"outcome"`
		} `json:"payload"`
	}
	capped := map[string]string{} // call id → gold file, for a whole-file read past the cut
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var e ev
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "tool.call":
			if e.Payload.Name != "read" {
				continue
			}
			var in struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			_ = json.Unmarshal(e.Payload.Input, &in)
			p := path.Clean(strings.TrimPrefix(in.Path, "./"))
			if in.Offset > 0 || in.Limit > 0 || !late[p] {
				continue
			}
			if strings.Count(base[p], "\n") > readCapLines {
				capped[e.Payload.ID] = p
			}
		case "tool.finished":
			if _, ok := capped[e.Payload.ID]; ok && e.Payload.Outcome == "ok" {
				return true, nil
			}
		}
	}
	return false, sc.Err()
}

// outlineTally is §13.2's gate 0 over the runs scored: runs with an outlineHit, per task.
type outlineTally struct {
	runs map[string]int // "suite task N" → runs
	seen map[string]int // "suite task N" → runs scored
}

func (o *outlineTally) add(suite string, task int, hit bool) {
	k := fmt.Sprintf("%s task %d", suite, task)
	o.seen[k]++
	if hit {
		o.runs[k]++
	}
}

// report prints the tally and §13.2's rule: a screen if at least 15 runs over at least 3 tasks.
func (o *outlineTally) report(w io.Writer) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## C7 §13.2 gate 0: whole-file reads cut at 200 lines on a gold file whose gold span starts past the cut")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| task | runs scored | runs with such a read |")
	fmt.Fprintln(w, "|---|---|---|")
	var keys []string
	for k := range o.seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total, tasks := 0, 0
	for _, k := range keys {
		if o.runs[k] == 0 {
			continue
		}
		fmt.Fprintf(w, "| %s | %d | %d |\n", k, o.seen[k], o.runs[k])
		total += o.runs[k]
		tasks++
	}
	verdict := "not screened: built only as DESIGN §7.3's feature"
	if total >= 15 && tasks >= 3 {
		verdict = "goes to a screen"
	}
	fmt.Fprintf(w, "\n**§13.2's rule (at least 15 runs over at least 3 tasks): %d runs over %d tasks: %s.**\n", total, tasks, verdict)
}
