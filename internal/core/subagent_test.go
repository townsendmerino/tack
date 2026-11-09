package core

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/usage"
)

var childID = regexp.MustCompile(`\[subagent session (s_[0-9A-Z]+)\]`)

// subagentTurn runs one parent turn that calls task, and returns the parent's task result and the child's id.
func subagentTurn(t *testing.T, f *fixture) (proto.SessionID, proto.ToolFinished, proto.SessionID) {
	t.Helper()
	f.core.opt.Subagents, f.core.opt.SubagentBudget = true, f.core.opt.Budget
	id := f.session(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.prompt(t, id, "go")
	var fin proto.ToolFinished
	for _, ev := range f.waitTurnEnd(t) {
		if v, _ := ev.Decode(); ev.Type == proto.EvToolFinished {
			fin = v.(proto.ToolFinished)
		}
	}
	m := childID.FindStringSubmatch(fin.Result)
	if m == nil {
		t.Fatalf("the task result names no child session: %+v", fin)
	}
	return id, fin, proto.SessionID(m[1])
}

// TestATaskRunsAChildSession: a task call runs a child session of its own, linked to the parent, in plan mode with
// the read-only tools and no task; its answer and its tokens come back on the parent's call.
func TestATaskRunsAChildSession(t *testing.T) {
	sc := goinfertest.NewScripted("m", 16384, goinfertest.Calls("", "t1", "task", `{"prompt":"where is A?"}`),
		goinfertest.Says("A is in a.go."), goinfertest.Says("done"))
	f := newFixtureOn(t, sc)
	parent, fin, child := subagentTurn(t, f)
	if fin.Outcome != proto.OutcomeOK || !strings.HasPrefix(fin.Result, "A is in a.go.") || fin.Tokens <= 0 {
		t.Errorf("the task result %+v", fin)
	}
	evs, err := f.core.opt.Store.Events(child, 0)
	if err != nil || len(evs) == 0 {
		t.Fatalf("the child's log: %v", err)
	}
	v, _ := evs[0].Decode()
	if c, ok := v.(proto.SessionCreated); !ok || c.Parent != parent || c.Mode != proto.ModePlan {
		t.Errorf("the child's first event %+v", v)
	}
	if len(sc.Requests) < 3 || !strings.Contains(string(sc.Requests[0]), `"name":"task"`) ||
		strings.Contains(string(sc.Requests[1]), `"name":"task"`) || strings.Contains(string(sc.Requests[1]), `"name":"edit"`) {
		t.Errorf("the parent's tools should have task, and the child's neither task nor edit")
	}
}

// TestAHaltOfTheParentCancelsTheChild: halting while the child's request runs ends the child, whose log records the
// halt, and the parent's turn with it.
func TestAHaltOfTheParentCancelsTheChild(t *testing.T) {
	var mu sync.Mutex
	n := 0
	inChild := make(chan struct{})
	probes := goinfertest.NewScripted("m", 16384)
	f := newFixtureOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			probes.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first { // the parent's: call task
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, goinfertest.Calls("", "t1", "task", `{"prompt":"look"}`).Body)
			return
		}
		io.Copy(io.Discard, r.Body) // the child's: hangs until cancelled, or 10 s, so a missed cancel fails the test
		close(inChild)
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	f.core.opt.Subagents, f.core.opt.SubagentBudget = true, f.core.opt.Budget
	id := f.session(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.prompt(t, id, "go")
	select {
	case <-inChild:
	case <-time.After(10 * time.Second):
		t.Fatal("the child never reached the model")
	}
	f.core.Halt(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: "test"})
	var reason string
	var fin proto.ToolFinished
	for _, ev := range f.waitTurnEnd(t) {
		v, _ := ev.Decode()
		switch p := v.(type) {
		case proto.ToolFinished:
			fin = p
		case proto.TurnEnd:
			reason = p.StopReason
		}
	}
	if reason != proto.StopHalted {
		t.Errorf("the parent's turn ended %q, want halted", reason)
	}
	children, _ := f.core.opt.Store.Sessions()
	halted := false
	for _, c := range children {
		if c == id {
			continue
		}
		evs, _ := f.core.opt.Store.Events(c, 0)
		for _, ev := range evs {
			halted = halted || ev.Type == proto.EvDaemonHalted
		}
	}
	if !halted {
		t.Errorf("no child log records the halt; the parent's task finished %+v", fin)
	}
}

// TestUsageCountsAParentAndChildOnce: the usage table's total over a parent and its child is the sum of their own
// usage events: the child's tokens recorded on the parent's call are not counted again.
func TestUsageCountsAParentAndChildOnce(t *testing.T) {
	f := newFixture(t, goinfertest.Calls("", "t1", "task", `{"prompt":"where is A?"}`), goinfertest.Says("A is in a.go."),
		goinfertest.Says("done"))
	parent, fin, child := subagentTurn(t, f)
	st := f.core.opt.Store
	want := 0
	for _, id := range []proto.SessionID{parent, child} {
		evs, _ := st.Events(id, 0)
		for _, ev := range evs {
			if v, _ := ev.Decode(); ev.Type == proto.EvUsage {
				u := v.(proto.Usage)
				want += u.Prompt + u.Output
			}
		}
	}
	rows, err := usage.FromStore(st, time.Time{}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	tot := usage.Total(rows)
	if got := tot.Prompt + tot.Output; got != want || fin.Tokens == 0 {
		t.Errorf("the table's total %d, the two logs' usage %d (the call recorded %d for the child)", got, want, fin.Tokens)
	}
}
