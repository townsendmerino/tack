package proto_test

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/proto/fixtures"
)

// TestFixturesConform checks every golden transcript against the protocol types, and that together they exercise
// every registered method and event type, so a type added without a fixture fails here.
func TestFixturesConform(t *testing.T) {
	cov := &proto.Coverage{Methods: map[string]int{}, Events: map[proto.EventType]int{}}
	names, err := fs.Glob(fixtures.FS, "*.jsonl")
	if err != nil || len(names) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, name := range names {
		data, _ := fs.ReadFile(fixtures.FS, name)
		for _, e := range proto.CheckTranscript(data, cov) {
			t.Errorf("%s: %v", name, e)
		}
	}
	for m := range proto.Methods {
		if cov.Methods[m] == 0 {
			t.Errorf("method %s appears in no fixture", m)
		}
	}
	for ev := range proto.EventTypes {
		if cov.Events[ev] == 0 {
			t.Errorf("event type %s appears in no fixture", ev)
		}
	}
}

// TestCheckTranscriptCatches perturbs a valid transcript in each way the checker exists to catch and confirms each
// perturbation is reported.
func TestCheckTranscriptCatches(t *testing.T) {
	const req = `{"dir":"c2d","msg":{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"workspace":"/w","model":"main","mode":"ask"}}}`
	const resp = `{"dir":"d2c","msg":{"jsonrpc":"2.0","id":1,"result":{"session":"s_1"}}}`
	ev := func(seq int, typ, payload string) string {
		return `{"dir":"d2c","msg":{"jsonrpc":"2.0","method":"session/event","params":{"session":"s_1","event":{"seq":` +
			strconv.Itoa(seq) + `,"time":"2026-10-16T12:00:00Z","type":"` + typ + `","payload":` + payload + `}}}}`
	}
	good := []string{req, resp, ev(1, "turn.end", `{"stop_reason":"end_turn"}`)}
	if errs := proto.CheckTranscript([]byte(strings.Join(good, "\n")), nil); len(errs) != 0 {
		t.Fatalf("baseline transcript fails: %v", errs)
	}
	cases := map[string][]string{
		"unknown field":         {strings.Replace(req, `"mode":"ask"`, `"mode":"ask","colour":"red"`, 1), resp},
		"missing field":         {strings.Replace(req, `,"mode":"ask"`, ``, 1), resp},
		"wrong type":            {strings.Replace(req, `"model":"main"`, `"model":7`, 1), resp},
		"unanswered request":    {req},
		"orphan response":       {resp},
		"unknown event type":    {ev(1, "tool.exploded", `{}`)},
		"payload unknown field": {ev(1, "turn.end", `{"stop_reason":"end_turn","why":"x"}`)},
		"live event with seq":   {ev(3, "assistant.delta", `{"index":0,"kind":"text","text":"x"}`)},
		"seq gap": {ev(1, "turn.end", `{"stop_reason":"end_turn"}`),
			ev(3, "turn.end", `{"stop_reason":"end_turn"}`)},
		"unregistered method answered with a result": {
			`{"dir":"c2d","msg":{"jsonrpc":"2.0","id":2,"method":"session/fork","params":{}}}`,
			`{"dir":"d2c","msg":{"jsonrpc":"2.0","id":2,"result":{}}}`},
		"request sent d2c": {strings.Replace(req, `"dir":"c2d"`, `"dir":"d2c"`, 1), resp},
	}
	for name, lines := range cases {
		if errs := proto.CheckTranscript([]byte(strings.Join(lines, "\n")), nil); len(errs) == 0 {
			t.Errorf("%s: not caught", name)
		}
	}
}

func TestNewEventRejectsWrongPayloadType(t *testing.T) {
	if _, err := proto.NewEvent(1, time.Now(), proto.EvTurnEnd, 0, proto.ToolStarted{ID: "x"}); err == nil {
		t.Fatal("NewEvent accepted a ToolStarted payload for turn.end")
	}
	e, err := proto.NewEvent(1, time.Now(), proto.EvTurnEnd, 0, proto.TurnEnd{StopReason: proto.StopEndTurn})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Decode()
	if err != nil || v.(proto.TurnEnd).StopReason != proto.StopEndTurn {
		t.Fatalf("Decode = %#v, %v", v, err)
	}
}

func TestNegotiate(t *testing.T) {
	for _, c := range []struct{ client, daemon, want int }{{1, 1, 1}, {2, 1, 1}, {1, 2, 1}, {3, 3, 3}, {3, 1, 0}, {1, 3, 0}, {0, 1, 0}, {0, 0, 0}} {
		got, ok := proto.Negotiate(c.client, c.daemon)
		if ok != (c.want != 0) || ok && got != c.want {
			t.Errorf("Negotiate(%d, %d) = %d, %v; want %d", c.client, c.daemon, got, ok, c.want)
		}
	}
}
