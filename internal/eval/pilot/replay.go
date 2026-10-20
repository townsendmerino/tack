package pilot

import (
	"bufio"
	"encoding/json"
	"os"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
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
}

// ReplayRepeats feeds every tool call of the events log at path, with the result the model saw less its notes
// (loop.StripNotes), through a RepeatGuard,
// one guard per turn, as the loop does with Config.StopRepeats.
func ReplayRepeats(path string) (RepeatReplay, error) {
	var r RepeatReplay
	f, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer f.Close()
	var g loop.RepeatGuard
	keys, names := map[string]string{}, map[string]string{}
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
			keys[p.ID], names[p.ID] = loop.CallKey(p.Name, p.Input), p.Name
		case proto.ToolFinished:
			if r.Stopped && request == r.StopRequest {
				stopAt = ev.Time // the stopping response's later results still come in
			}
			if r.Stopped {
				continue
			}
			if n, stop := g.Observe(keys[p.ID], loop.StripNotes(p.Result)); stop {
				r.Stopped, r.StopRequest, r.StopTool, r.Repeats, stopAt = true, request, names[p.ID], n, ev.Time
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
