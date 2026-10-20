package loop

import "encoding/json"

// RepeatLimit is how many unchanged repeats of one call end a turn (Config.StopRepeats).
const RepeatLimit = 8

// RepeatGuard is the stop rule for repeated identical calls (e1-calibration-preregistration-2026-10-18.md §4.5). Within
// a turn it remembers, for each call (CallKey), the result the model last saw from it and how many unchanged repeats it
// has made: a call whose key comes back with that same result is an unchanged repeat, and one with a different result
// starts its count again. Calls in between do not reset a count. The loop and the calibration's replay of captured runs
// both use it, so the replay measures exactly the rule that runs. The zero value is ready; one guard serves one turn.
type RepeatGuard struct {
	last  map[string]string
	count map[string]int
}

// Observe records one call's result and reports its unchanged repeats so far, and whether they reached RepeatLimit.
func (g *RepeatGuard) Observe(key, result string) (repeats int, stop bool) {
	if g.last == nil {
		g.last, g.count = map[string]string{}, map[string]int{}
	}
	if prev, seen := g.last[key]; seen && prev == result {
		g.count[key]++
	} else {
		g.count[key] = 0
	}
	g.last[key] = result
	return g.count[key], g.count[key] >= RepeatLimit
}

// CallKey is a call's identity for repeats: its tool and its input, with the input's key order and spacing ignored.
func CallKey(name string, input json.RawMessage) string {
	var v any
	if json.Unmarshal(input, &v) == nil {
		if n, err := json.Marshal(v); err == nil {
			return name + "\x00" + string(n)
		}
	}
	return name + "\x00" + string(input)
}
