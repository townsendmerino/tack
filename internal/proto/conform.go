package proto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// TranscriptLine is one line of a fixture transcript (proto/fixtures).
type TranscriptLine struct {
	Dir string          `json:"dir"` // "c2d" (client to daemon) or "d2c"
	Msg json.RawMessage `json:"msg"`
}

// Coverage counts what a set of transcripts exercised: methods by name, event types by name.
type Coverage struct {
	Methods map[string]int
	Events  map[EventType]int
}

// CheckTranscript checks one transcript against the protocol types and returns every problem found. Each message must
// decode into its registered type with no unknown fields, and re-encode to the same JSON (so a field the types drop or
// default is caught too); requests flow c2d and notifications d2c; a response answers an earlier request by id; a
// method outside Methods is allowed only when its response is a method-not-found error; each event's payload matches
// its type, live-only events carry seq 0, and persisted events of a session have dense seqs. cov, when non-nil,
// accumulates what the transcript exercised.
func CheckTranscript(data []byte, cov *Coverage) []error {
	var errs []error
	fail := func(line int, format string, args ...any) {
		errs = append(errs, fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...)))
	}
	pending := map[string]string{} // request id → method
	lastSeq := map[SessionID]int64{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	n := 0
	for sc.Scan() {
		n++
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var tl TranscriptLine
		if err := strictDecode(sc.Bytes(), &tl); err != nil {
			fail(n, "transcript line: %v", err)
			continue
		}
		if tl.Dir != "c2d" && tl.Dir != "d2c" {
			fail(n, "dir %q, want c2d or d2c", tl.Dir)
		}
		var probe struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(tl.Msg, &probe); err != nil {
			fail(n, "msg: %v", err)
			continue
		}

		if probe.Method == "" { // a response
			var resp Response
			if err := roundTrip(tl.Msg, &resp); err != nil {
				fail(n, "response: %v", err)
				continue
			}
			if tl.Dir != "d2c" {
				fail(n, "a response travels d2c")
			}
			method, ok := pending[string(resp.ID)]
			if !ok {
				fail(n, "response id %s answers no pending request", resp.ID)
				continue
			}
			delete(pending, string(resp.ID))
			if (resp.Error == nil) == (resp.Result == nil) {
				fail(n, "response must have exactly one of result and error")
				continue
			}
			spec, known := Methods[method]
			switch {
			case !known && (resp.Error == nil || resp.Error.Code != CodeMethodNotFound):
				fail(n, "method %q is not in Methods, so its only valid answer is method-not-found", method)
			case known && resp.Result != nil:
				if err := roundTripAs(resp.Result, spec.Result); err != nil {
					fail(n, "%s result: %v", method, err)
				}
			}
			continue
		}

		var req Request
		if err := roundTrip(tl.Msg, &req); err != nil {
			fail(n, "request: %v", err)
			continue
		}
		if req.JSONRPC != "2.0" {
			fail(n, "jsonrpc %q", req.JSONRPC)
		}
		spec, known := Methods[req.Method]
		if len(req.ID) > 0 {
			if tl.Dir != "c2d" {
				fail(n, "a request travels c2d")
			}
			pending[string(req.ID)] = req.Method
			if !known {
				continue // must be answered with method-not-found, checked at the response
			}
		} else if !known || spec.Result != nil {
			fail(n, "notification %q is not a registered notification", req.Method)
			continue
		}
		if cov != nil {
			cov.Methods[req.Method]++
		}
		if err := roundTripAs(req.Params, spec.Params); err != nil {
			fail(n, "%s params: %v", req.Method, err)
			continue
		}
		if req.Method != MethodSessionEvent {
			continue
		}
		if tl.Dir != "d2c" {
			fail(n, "session/event travels d2c")
		}
		var p SessionEventParams
		_ = json.Unmarshal(req.Params, &p)
		ev := p.Event
		evSpec, ok := EventTypes[ev.Type]
		if !ok {
			fail(n, "unknown event type %q", ev.Type)
			continue
		}
		if cov != nil {
			cov.Events[ev.Type]++
		}
		if err := roundTripAs(ev.Payload, evSpec.Payload); err != nil {
			fail(n, "%s payload: %v", ev.Type, err)
		}
		switch {
		case !evSpec.Persisted && ev.Seq != 0:
			fail(n, "%s is live-only and must carry seq 0, has %d", ev.Type, ev.Seq)
		case evSpec.Persisted:
			if last, seen := lastSeq[p.Session]; seen && ev.Seq != last+1 {
				fail(n, "%s seq %d follows %d: persisted seqs are dense", ev.Type, ev.Seq, last)
			}
			lastSeq[p.Session] = ev.Seq
		}
		if ev.Time.IsZero() {
			fail(n, "%s has no time", ev.Type)
		}
	}
	for id, m := range pending {
		errs = append(errs, fmt.Errorf("request id %s (%s) was never answered", id, m))
	}
	return errs
}

// roundTripAs decodes raw strictly into a new value of type t and checks it re-encodes to the same JSON.
func roundTripAs(raw json.RawMessage, t reflect.Type) error {
	return roundTrip(raw, reflect.New(t).Interface())
}

func roundTrip(raw json.RawMessage, v any) error {
	if err := strictDecode(raw, v); err != nil {
		return err
	}
	back, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if !sameJSON(raw, back) {
		return fmt.Errorf("does not re-encode to itself:\n  fixture: %s\n  go:      %s", raw, back)
	}
	return nil
}

func strictDecode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func sameJSON(a, b []byte) bool {
	var x, y any
	da, db := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	da.UseNumber()
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return false
	}
	return reflect.DeepEqual(normNumbers(x), normNumbers(y))
}

// normNumbers canonicalises json.Number so 20 and 20.0 compare equal (Go encodes a float64 20 as 20).
func normNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case map[string]any:
		for k, e := range t {
			t[k] = normNumbers(e)
		}
	case []any:
		for i, e := range t {
			t[i] = normNumbers(e)
		}
	}
	return v
}
