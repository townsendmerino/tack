package pilot

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tools"
)

// RepeatReplay is what loop.RepeatGuard would have done to a captured A1 run (e1-calibration-preregistration-2026-10-18.md
// §12.7). A stop takes effect, as in the loop, once the results of the response holding the stopping call are in.
type RepeatReplay struct {
	Requests int  `json:"requests"` // model requests the run sent
	Stopped  bool `json:"stopped"`
	// StopRequest is the request whose response held the stopping call, StopTool its tool and Repeats its unchanged
	// repeats; SavedRequests and SavedSecs are the requests sent and the wall time taken after that response's results.
	StopRequest   int     `json:"stop_request,omitempty"`
	StopTool      string  `json:"stop_tool,omitempty"`
	Repeats       int     `json:"repeats,omitempty"`
	SavedRequests int     `json:"saved_requests,omitempty"`
	SavedSecs     float64 `json:"saved_secs,omitempty"`
	// Lost counts the run's successful edits whose old_string the rebuilt file did not hold, so the rebuild lost track
	// of that file (a bash call changed it): with a base tree, its later states may differ from the live ones.
	Lost int `json:"lost,omitempty"`
}

// ReplayRepeats feeds every tool call of the events log at path, with the result the model saw less its notes
// (loop.StripNotes), through a RepeatGuard, one guard per turn, as the loop does with Config.StopRepeats.
//
// With base nil it replays the rule as first registered (§12.7), on results alone. With base, the run's repository at
// its base commit (BaseTree), it replays the rule as amended (§12.13): it rebuilds each file from base and the run's own
// successful edits and writes, and adds the file's state after each edit or write the tool ran (loop.RepeatResult).
func ReplayRepeats(path string, base map[string]string) (RepeatReplay, error) {
	var r RepeatReplay
	f, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer f.Close()
	var g loop.RepeatGuard
	calls := map[string]proto.ToolCall{}
	started := map[string]bool{} // calls the tool ran, so the loop read its State
	files := map[string]string{} // the run's own writes, over base
	get := func(p string) (string, bool) {
		if s, ok := files[p]; ok {
			return s, true
		}
		s, ok := base[p]
		return s, ok
	}
	request, stopAt := 0, time.Time{}
	var last time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev proto.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		last = ev.Time
		switch p := v.(type) {
		case proto.UserMessage:
			g = loop.RepeatGuard{}
		case proto.RequestSent:
			request++
			r.Requests++
			if r.Stopped {
				r.SavedRequests++
			}
		case proto.ToolCall:
			calls[p.ID] = p
		case proto.ToolStarted:
			started[p.ID] = true
		case proto.ToolFinished:
			c := calls[p.ID]
			state := ""
			if base != nil && started[p.ID] && (c.Name == "edit" || c.Name == "write") {
				var in struct {
					Path       string `json:"path"`
					OldString  string `json:"old_string"`
					NewString  string `json:"new_string"`
					ReplaceAll bool   `json:"replace_all"`
					Content    string `json:"content"`
				}
				_ = json.Unmarshal(c.Input, &in)
				pth := cleanPath(in.Path)
				cur, ok := get(pth)
				switch {
				case p.Outcome != proto.OutcomeOK:
				case c.Name == "write":
					cur, ok = in.Content, true
				case !ok || !strings.Contains(cur, in.OldString):
					r.Lost++
				case in.ReplaceAll:
					cur = strings.ReplaceAll(cur, in.OldString, in.NewString)
				default:
					cur = strings.Replace(cur, in.OldString, in.NewString, 1)
				}
				if ok {
					files[pth], state = cur, tools.ContentState(cur)
				}
			}
			if r.Stopped && request == r.StopRequest {
				stopAt = ev.Time // the stopping response's later results still come in
			}
			if r.Stopped {
				continue
			}
			if n, stop := g.Observe(loop.CallKey(c.Name, c.Input), loop.RepeatResult(loop.StripNotes(p.Result), state)); stop {
				r.Stopped, r.StopRequest, r.StopTool, r.Repeats, stopAt = true, request, c.Name, n, ev.Time
			}
		}
	}
	if err := sc.Err(); err != nil {
		return r, err
	}
	if r.Stopped {
		r.SavedSecs = last.Sub(stopAt).Seconds()
	}
	return r, nil
}

// DuplicateEdits is what the duplicate-edit guard (tools.Options.RefuseDuplicateEdits; §12.14) would have refused in a
// captured run: edit calls the tool ran whose path, old_string and new_string equal those of an edit that succeeded
// earlier in the session. Applied counts those the run applied again; the rest had failed for another reason.
type DuplicateEdits struct {
	Calls   int `json:"calls"`
	Applied int `json:"applied"`
	// First is the request whose response held the first such call.
	First int `json:"first,omitempty"`
}

// ReplayDuplicateEdits counts the duplicate edits in the events log at path.
func ReplayDuplicateEdits(path string) (DuplicateEdits, error) {
	var d DuplicateEdits
	f, err := os.Open(path)
	if err != nil {
		return d, err
	}
	defer f.Close()
	calls := map[string]proto.ToolCall{}
	started := map[string]bool{}
	applied := map[string]bool{} // path, old_string and new_string of the session's successful edits
	request := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev proto.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		switch p := v.(type) {
		case proto.RequestSent:
			request++
		case proto.ToolCall:
			calls[p.ID] = p
		case proto.ToolStarted:
			started[p.ID] = true
		case proto.ToolFinished:
			c := calls[p.ID]
			if c.Name != "edit" || !started[p.ID] {
				continue
			}
			var in struct {
				Path      string `json:"path"`
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			}
			if json.Unmarshal(c.Input, &in) != nil {
				continue
			}
			k := cleanPath(in.Path) + "\x00" + in.OldString + "\x00" + in.NewString
			if applied[k] {
				d.Calls++
				if d.First == 0 {
					d.First = request
				}
				if p.Outcome == proto.OutcomeOK {
					d.Applied++
				}
			}
			if p.Outcome == proto.OutcomeOK {
				applied[k] = true
			}
		}
	}
	return d, sc.Err()
}
