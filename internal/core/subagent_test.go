package core

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
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
	if c, ok := v.(proto.SessionCreated); !ok || c.Parent != parent || c.ParentCall != "t1" || c.Mode != proto.ModePlan {
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
	if tot.Sessions != 1 {
		t.Errorf("the table counts %d sessions for a parent and its child, want 1: a child is not a session the user had", tot.Sessions)
	}
	if got := tot.Prompt + tot.Output; got != want || fin.Tokens == 0 {
		t.Errorf("the table's total %d, the two logs' usage %d (the call recorded %d for the child)", got, want, fin.Tokens)
	}
}

// TestAChildCannotSpendPastTheParentsBudget: a child's limits are what the parent has left, so a task call cannot carry
// the parent past its turn's tokens by more than one request, however large the child's own budget.
func TestAChildCannotSpendPastTheParentsBudget(t *testing.T) {
	var replies []goinfertest.Reply
	replies = append(replies, goinfertest.Calls("", "t1", "task", `{"prompt":"read a.go six times"}`))
	for i := range 6 {
		replies = append(replies, goinfertest.Calls("", "r"+string(rune('0'+i)), "read", `{"path":"a.go"}`))
	}
	replies = append(replies, goinfertest.Says("read it"), goinfertest.Says("done"))
	sc := goinfertest.NewScripted("m", 16384, replies...)
	f := newFixtureOn(t, sc)
	f.core.opt.Budget.TurnTokens = 300 // about two and a half of the scripted 120-token requests
	f.core.opt.Subagents = true
	f.core.opt.SubagentBudget = f.core.opt.Budget
	f.core.opt.SubagentBudget.TurnTokens, f.core.opt.SubagentBudget.SessionTokens = 100_000, 100_000
	f.core.opt.SubagentBudget.MaxRequests, f.core.opt.SubagentBudget.MaxToolCalls = 20, 50
	id := f.session(t)
	if err := f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: id}, nil); err != nil {
		t.Fatal(err)
	}
	f.prompt(t, id, "go")
	f.waitTurnEnd(t)
	evs, _ := f.core.opt.Store.Events(id, 0)
	spent := 0
	for _, ev := range evs {
		v, _ := ev.Decode()
		switch p := v.(type) {
		case proto.Usage:
			spent += p.Prompt + p.Output
		case proto.ToolFinished:
			spent += p.Tokens
		}
	}
	if spent > 300+120 {
		t.Errorf("the parent's turn spent %d tokens with its child, past its 300 by more than one 120-token request", spent)
	}
}

// TestADrainReachesARunningChild: a drain that begins while a child's request runs ends the child before its next
// request, as it ends any turn, so the parent's turn can end with it.
func TestADrainReachesARunningChild(t *testing.T) {
	var mu sync.Mutex
	n := 0
	inChild, release := make(chan struct{}), make(chan struct{})
	probes := goinfertest.NewScripted("m", 16384)
	f := newFixtureOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			probes.ServeHTTP(w, r)
			return
		}
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		n++
		i := n
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch i {
		case 1: // the parent's: call task
			io.WriteString(w, goinfertest.Calls("", "t1", "task", `{"prompt":"look twice"}`).Body)
		case 2: // the child's first: held until the drain has begun, then a read
			close(inChild)
			<-release
			io.WriteString(w, goinfertest.Calls("", "c1", "read", `{"path":"a.go"}`).Body)
		default: // a child that ignored the drain asks again
			io.WriteString(w, goinfertest.Says("still going").Body)
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
	drained := make(chan []proto.SessionID, 1)
	go func() { drained <- f.core.Drain(5 * time.Second) }()
	time.Sleep(50 * time.Millisecond) // the drain has begun
	close(release)
	select {
	case cancelled := <-drained:
		if len(cancelled) != 0 {
			t.Errorf("Drain had to cancel %v: the child did not stop at its next request", cancelled)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Drain did not return")
	}
	mu.Lock()
	defer mu.Unlock()
	if n != 2 {
		t.Errorf("%d model requests, want 2: the drained child asked again", n)
	}
}

// TestRecoveryChargesACutOffTaskWithItsChild: a crash during a child leaves the parent's task call open and the
// child's turn open. Recovery closes both, and the parent's closed call carries the child's spend, so a resume counts
// it toward the parent's budgets.
func TestRecoveryChargesACutOffTaskWithItsChild(t *testing.T) {
	f := newFixture(t)
	st := f.core.opt.Store
	parent, child := proto.SessionID("s_01JAPARENT0000000000000001"), proto.SessionID("s_01JACHILD00000000000000002")
	write := func(id proto.SessionID, evs ...any) {
		log, err := st.Create(id)
		if err != nil {
			t.Fatal(err)
		}
		cause := int64(0)
		for i := 0; i < len(evs); i += 2 {
			ev, err := log.Append(evs[i].(proto.EventType), cause, evs[i+1])
			if err != nil {
				t.Fatal(err)
			}
			if evs[i].(proto.EventType) == proto.EvToolCall {
				cause = ev.Seq // the tool.started that follows names its call
			}
		}
		log.Close()
	}
	write(parent,
		proto.EvSessionCreated, proto.SessionCreated{Workspace: f.workspace(t), Model: "main", Mode: proto.ModePlan},
		proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("go")}},
		proto.EvAssistantMessage, proto.AssistantMessage{Request: 1, Content: []proto.ModelBlock{{Type: "tool_use", ID: "t1", Name: "task", Input: []byte(`{"prompt":"look"}`)}}},
		proto.EvToolCall, proto.ToolCall{ID: "t1", Name: "task", Input: []byte(`{"prompt":"look"}`)},
		proto.EvToolStarted, proto.ToolStarted{ID: "t1"})
	write(child,
		proto.EvSessionCreated, proto.SessionCreated{Workspace: f.workspace(t), Model: "main", Mode: proto.ModePlan, Parent: parent},
		proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("look")}},
		proto.EvRequestSent, proto.RequestSent{Request: 1, Server: "goinfer@local", Model: "m"},
		proto.EvUsage, proto.Usage{Request: 1, Server: "goinfer@local", Prompt: 1000, Output: 50})
	if _, err := f.core.Recover(); err != nil {
		t.Fatal(err)
	}
	pevs, _ := st.Events(parent, 0)
	var fin proto.ToolFinished
	for _, ev := range pevs {
		if v, _ := ev.Decode(); ev.Type == proto.EvToolFinished {
			fin = v.(proto.ToolFinished)
		}
	}
	if fin.Outcome != proto.OutcomeUnknown || fin.Tokens != 1050 {
		t.Errorf("the parent's cut-off task finished %+v; want unknown, charged the child's 1,050 tokens", fin)
	}
	cevs, _ := st.Events(child, 0)
	if last := cevs[len(cevs)-1]; last.Type != proto.EvTurnEnd {
		t.Errorf("the child's log ends %s, want its turn closed", last.Type)
	}
}

// TestRecoveryChargesOnlyTheCutOffTasksChild: a task whose child failed (its result names no child) and then a task the
// crash cut off: recovery charges the cut-off call with its own child's spend, not the failed one's again, which the
// failed call's tool.finished already carries.
func TestRecoveryChargesOnlyTheCutOffTasksChild(t *testing.T) {
	f := newFixture(t)
	st := f.core.opt.Store
	parent := proto.SessionID("s_01JAPARENT0000000000000003")
	failed, cut := proto.SessionID("s_01JACHILD00000000000000004"), proto.SessionID("s_01JACHILD00000000000000005")
	logs := map[proto.SessionID]*store.Log{}
	for _, id := range []proto.SessionID{parent, failed, cut} {
		l, err := st.Create(id)
		if err != nil {
			t.Fatal(err)
		}
		logs[id] = l
	}
	var cause int64
	app := func(id proto.SessionID, typ proto.EventType, p any) {
		// A tick apart, as real appends are (each tool.started is fsynced): recovery tells children apart by when
		// their logs began.
		time.Sleep(time.Millisecond)
		ev, err := logs[id].Append(typ, cause, p)
		if err != nil {
			t.Fatal(err)
		}
		if typ == proto.EvToolCall {
			cause = ev.Seq
		}
	}
	child := func(id proto.SessionID, prompt int) {
		app(id, proto.EvSessionCreated, proto.SessionCreated{Workspace: f.workspace(t), Model: "main", Mode: proto.ModePlan, Parent: parent})
		app(id, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("look")}})
		app(id, proto.EvRequestSent, proto.RequestSent{Request: 1, Server: "goinfer@local", Model: "m"})
		app(id, proto.EvUsage, proto.Usage{Request: 1, Server: "goinfer@local", Prompt: prompt, Output: 50})
	}
	use := func(id string) proto.ModelBlock {
		return proto.ModelBlock{Type: "tool_use", ID: id, Name: "task", Input: []byte(`{"prompt":"look"}`)}
	}
	app(parent, proto.EvSessionCreated, proto.SessionCreated{Workspace: f.workspace(t), Model: "main", Mode: proto.ModePlan})
	app(parent, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("go")}})
	app(parent, proto.EvAssistantMessage, proto.AssistantMessage{Request: 1, Content: []proto.ModelBlock{use("t1"), use("t2")}})
	app(parent, proto.EvToolCall, proto.ToolCall{ID: "t1", Name: "task", Input: []byte(`{"prompt":"look"}`)})
	app(parent, proto.EvToolStarted, proto.ToolStarted{ID: "t1"})
	child(failed, 2000)
	app(failed, proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopBudget})
	app(parent, proto.EvToolFinished, proto.ToolFinished{ID: "t1", Outcome: proto.OutcomeError, Result: "error: its turn ended with budget", Tokens: 2050, Calls: 0})
	app(parent, proto.EvToolCall, proto.ToolCall{ID: "t2", Name: "task", Input: []byte(`{"prompt":"look"}`)})
	app(parent, proto.EvToolStarted, proto.ToolStarted{ID: "t2"})
	child(cut, 1000)
	for _, l := range logs {
		l.Close()
	}
	if _, err := f.core.Recover(); err != nil {
		t.Fatal(err)
	}
	pevs, _ := st.Events(parent, 0)
	var fin proto.ToolFinished
	for _, ev := range pevs {
		if v, _ := ev.Decode(); ev.Type == proto.EvToolFinished {
			fin = v.(proto.ToolFinished)
		}
	}
	if fin.ID != "t2" || fin.Outcome != proto.OutcomeUnknown || fin.Tokens != 1050 {
		t.Errorf("the cut-off task finished %+v; want t2 unknown, charged its own child's 1,050 tokens and not the failed child's 2,050 again", fin)
	}
}

// TestADrainingCoreMakesNoChildLog: a task call while the core drains is refused before a child log exists, so no
// empty child session is left behind.
func TestADrainingCoreMakesNoChildLog(t *testing.T) {
	f := newFixture(t)
	ws, err := tools.NewWorkspace(f.workspace(t))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := f.core.opt.Store.Sessions()
	f.core.mu.Lock()
	f.core.draining = true
	f.core.mu.Unlock()
	_, err = f.core.subagent("s_01JAPARENT0000000000000006", ws, proto.SessionCreated{Model: "main"})(context.Background(), "look")
	after, _ := f.core.opt.Store.Sessions()
	if err == nil || len(after) != len(before) {
		t.Errorf("while draining: err %v, %d sessions then %d; want refused with no new log", err, len(before), len(after))
	}
}

// TestRecoveryMatchesAChildByItsCallID: a child that names its task call (SessionCreated.ParentCall) is charged to that
// call whatever the clock said. Here the logs' times point the wrong way, as after a clock step: the cut-off call's
// own child began before the call started, and an earlier call's child after it. Matching by time would charge the
// wrong child; matching by id charges the cut-off call its own child's 1,050 tokens.
func TestRecoveryMatchesAChildByItsCallID(t *testing.T) {
	f := newFixture(t)
	st := f.core.opt.Store
	parent := proto.SessionID("s_01JAPARENT0000000000000007")
	earlier, own := proto.SessionID("s_01JACHILD00000000000000008"), proto.SessionID("s_01JACHILD00000000000000009")
	logs := map[proto.SessionID]*store.Log{}
	for _, id := range []proto.SessionID{parent, earlier, own} {
		l, err := st.Create(id)
		if err != nil {
			t.Fatal(err)
		}
		logs[id] = l
	}
	var cause int64
	app := func(id proto.SessionID, typ proto.EventType, p any) {
		time.Sleep(time.Millisecond) // a tick apart, so the times order the appends as written
		ev, err := logs[id].Append(typ, cause, p)
		if err != nil {
			t.Fatal(err)
		}
		if typ == proto.EvToolCall {
			cause = ev.Seq
		}
	}
	child := func(id proto.SessionID, call string, prompt int) {
		app(id, proto.EvSessionCreated, proto.SessionCreated{Workspace: f.workspace(t), Model: "main", Mode: proto.ModePlan, Parent: parent, ParentCall: call})
		app(id, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("look")}})
		app(id, proto.EvRequestSent, proto.RequestSent{Request: 1, Server: "goinfer@local", Model: "m"})
		app(id, proto.EvUsage, proto.Usage{Request: 1, Server: "goinfer@local", Prompt: prompt, Output: 50})
	}
	use := func(id string) proto.ModelBlock {
		return proto.ModelBlock{Type: "tool_use", ID: id, Name: "task", Input: []byte(`{"prompt":"look"}`)}
	}
	app(parent, proto.EvSessionCreated, proto.SessionCreated{Workspace: f.workspace(t), Model: "main", Mode: proto.ModePlan})
	app(parent, proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("go")}})
	app(parent, proto.EvAssistantMessage, proto.AssistantMessage{Request: 1, Content: []proto.ModelBlock{use("t1"), use("t2")}})
	app(parent, proto.EvToolCall, proto.ToolCall{ID: "t1", Name: "task", Input: []byte(`{"prompt":"look"}`)})
	app(parent, proto.EvToolStarted, proto.ToolStarted{ID: "t1"})
	child(own, "t2", 1000) // t2's child, its log begun "before" t2 started
	app(parent, proto.EvToolFinished, proto.ToolFinished{ID: "t1", Outcome: proto.OutcomeError, Result: "error: its turn ended with budget", Tokens: 2050})
	app(parent, proto.EvToolCall, proto.ToolCall{ID: "t2", Name: "task", Input: []byte(`{"prompt":"look"}`)})
	app(parent, proto.EvToolStarted, proto.ToolStarted{ID: "t2"})
	child(earlier, "t1", 2000) // t1's child, begun "after" t2 started
	app(earlier, proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopBudget})
	for _, l := range logs {
		l.Close()
	}
	if _, err := f.core.Recover(); err != nil {
		t.Fatal(err)
	}
	pevs, _ := st.Events(parent, 0)
	var fin proto.ToolFinished
	for _, ev := range pevs {
		if v, _ := ev.Decode(); ev.Type == proto.EvToolFinished {
			fin = v.(proto.ToolFinished)
		}
	}
	if fin.ID != "t2" || fin.Outcome != proto.OutcomeUnknown || fin.Tokens != 1050 {
		t.Errorf("the cut-off task finished %+v; want t2 unknown, charged its own child's 1,050 tokens by id", fin)
	}
}
