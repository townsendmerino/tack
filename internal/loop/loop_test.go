package loop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

type harness struct {
	sess   *Session
	st     *store.Store
	log    *store.Log
	script *goinfertest.Scripted
	events []proto.Event
	slept  []time.Duration
	ws     *tools.Workspace
	// order is one timeline of durability: "put <handle>" when a blob is durable, "append <type> <blob>" when an event is
	// recorded, "sync <type>" when the log syncs on that event's append.
	order []string
}

// testBudget is the harness's budget; a test changes the one limit it trips.
var testBudget = Budget{MaxRequests: 8, MaxToolCalls: 8, MaxWall: time.Minute, MaxTokens: 256, TurnTokens: 1 << 20,
	MaxErrors: 8, SessionTokens: 1 << 20, SessionToolCalls: 100}

// newHarness runs a session against a scripted goinfer over a small workspace.
func newHarness(t *testing.T, b Budget, replies ...goinfertest.Reply) *harness {
	t.Helper()
	root := filepath.Join(t.TempDir(), "ken")
	os.MkdirAll(filepath.Join(root, "mcp"), 0o755)
	os.WriteFile(filepath.Join(root, "mcp", "token_budget.go"), []byte("package mcp\n\nfunc applyTokenBudget() {}\n"), 0o644)
	ws, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{st: st, ws: ws}
	st.OnSync = func(typ proto.EventType, _ time.Duration) { h.order = append(h.order, "sync "+string(typ)) }
	st.Blobs.OnPut = func(handle string) { h.order = append(h.order, "put "+handle) }
	log, err := st.Create("s_test")
	if err != nil {
		t.Fatal(err)
	}
	h.log, h.script = log, goinfertest.NewScripted("m", 16384, replies...)
	ts := httptest.NewServer(h.script)
	t.Cleanup(ts.Close)
	if b == (Budget{}) {
		b = testBudget
	}
	h.sess = New(Config{Source: models.NewRemote("local", ts.URL, "", "", nil), Tools: tools.NewSet(nil), WS: ws,
		Log: log, Blobs: st.Blobs, Budget: b,
		Emit: func(ev proto.Event) {
			h.events = append(h.events, ev)
			if ev.Seq != 0 {
				blob := ""
				if v, err := ev.Decode(); err == nil {
					if f, ok := v.(proto.ToolFinished); ok {
						blob = f.Blob
					}
				}
				h.order = append(h.order, strings.TrimSpace("append "+string(ev.Type)+" "+blob))
			}
		},
		Sleep: func(_ context.Context, d time.Duration) error { h.slept = append(h.slept, d); return nil }})
	return h
}

func (h *harness) types() []string {
	var out []string
	for _, ev := range h.events {
		if ev.Type != proto.EvAssistantDelta {
			out = append(out, string(ev.Type))
		}
	}
	return out
}

func (h *harness) payloads(typ proto.EventType) []any {
	var out []any
	for _, ev := range h.events {
		if ev.Type == typ {
			v, _ := ev.Decode()
			out = append(out, v)
		}
	}
	return out
}

// checkRebuild holds the log to being the truth: the conversation rebuilt from the persisted events must produce the
// same next request as the live one.
func (h *harness) checkRebuild(t *testing.T) {
	t.Helper()
	evs, err := h.st.Events("s_test", 0)
	if err != nil {
		t.Fatal(err)
	}
	live := h.sess.Conversation()
	re, err := Rebuild(evs, h.ws.Root(), *live)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(re.Request(), live.Request()) {
		t.Fatalf("rebuilt conversation differs from the live one:\nlive:    %+v\nrebuilt: %+v", live.Request().Messages, re.Request().Messages)
	}
}

func TestTurnWithTools(t *testing.T) {
	h := newHarness(t, Budget{},
		goinfertest.Calls("Searching.", "t1", "grep", `{"pattern":"func applyTokenBudget"}`),
		goinfertest.Calls("", "t2", "read", `{"path":"mcp/token_budget.go","offset":3,"limit":1}`),
		goinfertest.Says("It is defined in mcp/token_budget.go:3."))
	stop, err := h.sess.Turn(context.Background(), "Where is applyTokenBudget defined?")
	if err != nil || stop != proto.StopEndTurn {
		t.Fatalf("stop %q, %v", stop, err)
	}
	want := []string{"user.message", "request.sent", "assistant.message", "usage", "tool.call", "tool.started", "tool.finished",
		"request.sent", "assistant.message", "usage", "tool.call", "tool.started", "tool.finished",
		"request.sent", "assistant.message", "usage", "turn.end"}
	if got := h.types(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n got %v\nwant %v", got, want)
	}
	fins := h.payloads(proto.EvToolFinished)
	if r := fins[0].(proto.ToolFinished).Result; r != "mcp/token_budget.go:3:func applyTokenBudget() {}\n" {
		t.Fatalf("grep result %q", r)
	}
	if r := fins[1].(proto.ToolFinished).Result; r != "     3\tfunc applyTokenBudget() {}\n" {
		t.Fatalf("read result %q", r)
	}
	// The workspace fact rides in the first user message's tail, never in the system prompt.
	if !strings.Contains(string(h.script.Requests[0]), `"text":"Repository: ken\n\nWhere is applyTokenBudget defined?"`) {
		t.Fatalf("first request: %s", h.script.Requests[0])
	}
	if strings.Contains(SystemPrompt, "ken") {
		t.Fatal("per-workspace bytes in the system prompt")
	}
	// Deltas are live-only.
	for _, ev := range h.events {
		if ev.Type == proto.EvAssistantDelta && ev.Seq != 0 {
			t.Fatal("a delta was persisted")
		}
	}
	h.checkRebuild(t)
}

// TestUsagePromptIsPrefilledPlusReused is the ledger-level check for goinfer's G3 split
// (docs/briefs/goinfer-g3-messages-cached-tokens.md): once the goinfer client reports InputTokens as the prefilled
// share alone, the recorded Usage.Prompt must still be the whole prompt (prefilled + reused), since that is what
// the context ledger and the P3 exit rule's reuse share both need it to mean.
func TestUsagePromptIsPrefilledPlusReused(t *testing.T) {
	h := newHarness(t, Budget{},
		goinfertest.Reply{Body: goinfertest.SSEWithReuse("end_turn", 35, 59, 16, proto.ModelBlock{Type: "text", Text: "hi"})})
	stop, err := h.sess.Turn(context.Background(), "hello")
	if err != nil || stop != proto.StopEndTurn {
		t.Fatalf("stop %q, %v", stop, err)
	}
	us := h.payloads(proto.EvUsage)
	if len(us) != 1 {
		t.Fatalf("usage events = %d, want 1", len(us))
	}
	u := us[0].(proto.Usage)
	if u.Prompt != 94 {
		t.Errorf("Prompt = %d, want 94 (35 prefilled + 59 reused)", u.Prompt)
	}
	if u.Reused == nil || *u.Reused != 59 {
		t.Fatalf("Reused = %v, want a pointer to 59", u.Reused)
	}
	if u.Prefilled == nil || *u.Prefilled != 35 {
		t.Fatalf("Prefilled = %v, want a pointer to 35", u.Prefilled)
	}
	// A server that does not report reuse: the prompt is message_start's whole figure, and neither split is invented.
	h = newHarness(t, Budget{}, goinfertest.Reply{Body: goinfertest.SSE("end_turn", proto.ModelBlock{Type: "text", Text: "hi"})})
	if _, err := h.sess.Turn(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if u := h.payloads(proto.EvUsage)[0].(proto.Usage); u.Reused != nil || u.Prefilled != nil || u.Prompt == 0 {
		t.Fatalf("pre-G3 usage %+v: Reused and Prefilled must stay nil, Prompt the whole prompt", u)
	}
}

func TestToolCallBudget(t *testing.T) {
	b := testBudget
	b.MaxToolCalls = 1
	h := newHarness(t, b,
		goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`, "t2", "glob", `{"pattern":"*.md"}`))
	stop, _ := h.sess.Turn(context.Background(), "list")
	if stop != proto.StopBudget {
		t.Fatalf("stop %q", stop)
	}
	fins := h.payloads(proto.EvToolFinished)
	if len(fins) != 2 || fins[0].(proto.ToolFinished).Outcome != proto.OutcomeOK || fins[1].(proto.ToolFinished).Outcome != proto.OutcomeCancelled {
		t.Fatalf("every call needs a result, the one over budget cancelled: %+v", fins)
	}
	if b := h.payloads(proto.EvBudgetExceeded); len(b) != 1 || b[0].(proto.BudgetExceeded).Budget != "tool_calls" {
		t.Fatalf("budget events %+v", b)
	}
	if n := len(h.payloads(proto.EvToolStarted)); n != 1 {
		t.Fatalf("%d tools started, want 1", n)
	}
	h.checkRebuild(t)
}

func TestRequestBudget(t *testing.T) {
	b := testBudget
	b.MaxRequests = 1
	h := newHarness(t, b,
		goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`))
	if stop, _ := h.sess.Turn(context.Background(), "list"); stop != proto.StopBudget {
		t.Fatalf("stop %q", stop)
	}
	h.checkRebuild(t)
	// The next turn's text joins the unanswered tool-result message, so the conversation stays valid.
	if err := h.sess.Conversation().BeginTurn("again", false); err != nil {
		t.Fatal(err)
	}
	if err := serverValidate(h.sess); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedCallIsAnsweredNotRun(t *testing.T) {
	h := newHarness(t, Budget{},
		goinfertest.Calls("", "t1", "rm", `{"path":"/"}`),
		goinfertest.Says("sorry"))
	if stop, _ := h.sess.Turn(context.Background(), "clean up"); stop != proto.StopEndTurn {
		t.Fatalf("stop %q", stop)
	}
	fins := h.payloads(proto.EvToolFinished)
	if len(fins) != 1 || fins[0].(proto.ToolFinished).Outcome != proto.OutcomeError ||
		!strings.Contains(fins[0].(proto.ToolFinished).Result, "malformed tool call: no tool named \"rm\"") {
		t.Fatalf("finished %+v", fins)
	}
	if n := len(h.payloads(proto.EvToolStarted)); n != 0 {
		t.Fatal("a malformed call was started")
	}
	h.checkRebuild(t)
}

func TestRetryOnFullQueue(t *testing.T) {
	h := newHarness(t, Budget{},
		goinfertest.Reply{Status: 529, RetryAfter: "1", Body: `{"type":"error","error":{"type":"overloaded_error","message":"queue full"}}`},
		goinfertest.Says("done"))
	if stop, _ := h.sess.Turn(context.Background(), "hi"); stop != proto.StopEndTurn {
		t.Fatalf("stop %q", stop)
	}
	errs := h.payloads(proto.EvError)
	if len(errs) != 1 || !errs[0].(proto.ErrorEvent).Retrying || !reflect.DeepEqual(h.slept, []time.Duration{time.Second}) {
		t.Fatalf("errors %+v slept %v", errs, h.slept)
	}
}

func TestServerErrorEndsTheTurn(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Reply{Status: 400, Body: `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 5000 tokens > 4096 maximum"}}`})
	if stop, _ := h.sess.Turn(context.Background(), "hi"); stop != proto.StopError {
		t.Fatalf("stop %q", stop)
	}
	if e := h.payloads(proto.EvError); len(e) != 1 || !strings.Contains(e[0].(proto.ErrorEvent).Message, "prompt is too long") {
		t.Fatalf("errors %+v", e)
	}
}

func TestLongResultGoesToTheBlobStore(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "grep", `{"pattern":"."}`), goinfertest.Says("ok"))
	h.sess.cfg.MaxResultBytes = 40
	h.sess.Turn(context.Background(), "everything")
	fin := h.payloads(proto.EvToolFinished)[0].(proto.ToolFinished)
	if !fin.Truncated || fin.Blob == "" || !strings.Contains(fin.Result, "bytes omitted") {
		t.Fatalf("finished %+v", fin)
	}
	full, err := h.st.Blobs.Get(fin.Blob)
	if err != nil || !strings.Contains(string(full), "func applyTokenBudget") {
		t.Fatalf("blob %q, %v", full, err)
	}
}

// TestTruncatedResultNamesItsHandle: with read_output in the session's tools, a truncated result tells the model the
// handle, and read_output with that handle pages the stored output.
func TestTruncatedResultNamesItsHandle(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "grep", `{"pattern":"."}`), goinfertest.Says("ok"))
	h.sess.cfg.MaxResultBytes = 40
	h.sess.cfg.Tools = tools.New(tools.Options{Blobs: h.st.Blobs})
	h.sess.Turn(context.Background(), "everything")
	fin := h.payloads(proto.EvToolFinished)[0].(proto.ToolFinished)
	if !fin.Truncated || !strings.Contains(fin.Result, `read_output with handle "`+fin.Blob+`"`) {
		t.Fatalf("the truncated result does not name its handle %q: %q", fin.Blob, fin.Result)
	}
	ro, _ := h.sess.cfg.Tools.Get("read_output")
	r, err := ro.Run(context.Background(), h.ws, json.RawMessage(`{"handle":"`+fin.Blob+`"}`))
	if err != nil || r.IsError || !strings.Contains(r.Text, "func applyTokenBudget") {
		t.Fatalf("read_output: %q, %v", r.Text, err)
	}
}

func TestRebuildAfterACrashMarksTheCallUnknown(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`), goinfertest.Says("ok"))
	h.sess.Turn(context.Background(), "list")
	evs, _ := h.st.Events("s_test", 0)
	var cut []proto.Event
	for _, ev := range evs { // the log as a crash during the tool would leave it
		cut = append(cut, ev)
		if ev.Type == proto.EvToolStarted {
			break
		}
	}
	if st := store.Inspect(cut); !st.InTurn || len(st.UnknownTools) != 1 {
		t.Fatalf("Inspect %+v", st)
	}
	live := h.sess.Conversation()
	re, err := Rebuild(cut, h.ws.Root(), *live)
	if err != nil {
		t.Fatal(err)
	}
	last := re.Request().Messages[len(re.Request().Messages)-1]
	if b := last.Content[0]; b.Type != "tool_result" || !b.IsError || !strings.Contains(b.Result, "outcome unknown") {
		t.Fatalf("last block %+v", b)
	}
}

// TestBlobDurableBeforeTheEventNamingItIsSynced holds ADR-004's ordering rule through the loop, the store's caller: a
// tool result too long for the model goes to a blob, and that blob, directory entry included, is durable before the
// tool.finished event naming it is recorded, and so before the log sync that makes the event durable.
func TestBlobDurableBeforeTheEventNamingItIsSynced(t *testing.T) {
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "grep", `{"pattern":"."}`), goinfertest.Says("ok"))
	h.sess.cfg.MaxResultBytes = 40
	if stop, err := h.sess.Turn(context.Background(), "everything"); err != nil || stop != proto.StopEndTurn {
		t.Fatalf("stop %q, %v", stop, err)
	}
	var handle string
	for _, ev := range h.events {
		if ev.Type == proto.EvToolFinished {
			v, _ := ev.Decode()
			handle = v.(proto.ToolFinished).Blob
		}
	}
	if handle == "" {
		t.Fatal("the result did not go to a blob")
	}
	index := func(s string) int { return slices.Index(h.order, s) }
	put, appended := index("put "+handle), index("append tool.finished "+handle)
	synced := -1 // the first sync after the event is appended makes it durable
	for i := appended + 1; i < len(h.order) && appended >= 0; i++ {
		if strings.HasPrefix(h.order[i], "sync ") {
			synced = i
			break
		}
	}
	if put < 0 || appended < 0 || synced < 0 || !(put < appended && appended < synced) {
		t.Fatalf("want the blob durable, then the event naming it appended, then synced; order:\n  %s", strings.Join(h.order, "\n  "))
	}
}

func (h *harness) budgets() []string {
	var out []string
	for _, b := range h.payloads(proto.EvBudgetExceeded) {
		out = append(out, b.(proto.BudgetExceeded).Budget)
	}
	return out
}

func TestTurnTokenBudget(t *testing.T) {
	b := testBudget
	b.TurnTokens = 1
	h := newHarness(t, b, goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`), goinfertest.Says("done"))
	if stop, _ := h.sess.Turn(context.Background(), "list"); stop != proto.StopBudget {
		t.Fatalf("stop %q", stop)
	}
	if got := h.budgets(); !slices.Equal(got, []string{"tokens"}) {
		t.Fatalf("budgets %v", got)
	}
	if n := len(h.payloads(proto.EvRequestSent)); n != 1 {
		t.Fatalf("%d requests; the budget trips before the second", n)
	}
	h.checkRebuild(t)
}

// TestSessionBudgets: a session budget spans turns; once tripped, the next turn ends before its first request.
func TestSessionBudgets(t *testing.T) {
	b := testBudget
	b.SessionTokens = 1
	h := newHarness(t, b, goinfertest.Says("one"), goinfertest.Says("two"))
	if stop, _ := h.sess.Turn(context.Background(), "first"); stop != proto.StopEndTurn {
		t.Fatalf("first turn: %q", stop)
	}
	if stop, _ := h.sess.Turn(context.Background(), "second"); stop != proto.StopBudget {
		t.Fatalf("second turn: %q", stop)
	}
	if got, n := h.budgets(), len(h.payloads(proto.EvRequestSent)); !slices.Equal(got, []string{"session_tokens"}) || n != 1 {
		t.Fatalf("budgets %v after %d requests", got, n)
	}

	b = testBudget
	b.SessionToolCalls = 2
	h = newHarness(t, b, goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`), goinfertest.Says("one"),
		goinfertest.Calls("", "t2", "glob", `{"pattern":"*.go"}`, "t3", "glob", `{"pattern":"*.md"}`))
	if stop, _ := h.sess.Turn(context.Background(), "first"); stop != proto.StopEndTurn {
		t.Fatalf("first turn: %q", stop)
	}
	if stop, _ := h.sess.Turn(context.Background(), "second"); stop != proto.StopBudget {
		t.Fatalf("second turn: %q", stop)
	}
	fins := h.payloads(proto.EvToolFinished)
	if len(fins) != 3 || fins[2].(proto.ToolFinished).Outcome != proto.OutcomeCancelled || !strings.Contains(fins[2].(proto.ToolFinished).Result, "session's tool-call budget") {
		t.Fatalf("the call over the session's budget must be answered, not run: %+v", fins)
	}
	if got := h.budgets(); !slices.Equal(got, []string{"session_tool_calls"}) {
		t.Fatalf("budgets %v", got)
	}
	h.checkRebuild(t)
}

// TestConsecutiveErrorBudget: failing calls in a row trip it; one success between them resets the count.
func TestConsecutiveErrorBudget(t *testing.T) {
	fail := func(id string) goinfertest.Reply {
		return goinfertest.Calls("", id, "read", `{"path":"no/such/file.go"}`)
	}
	ok := func(id string) goinfertest.Reply { return goinfertest.Calls("", id, "glob", `{"pattern":"*.go"}`) }
	b := testBudget
	b.MaxErrors = 2
	h := newHarness(t, b, fail("t1"), ok("t2"), fail("t3"), goinfertest.Says("done"))
	if stop, _ := h.sess.Turn(context.Background(), "go"); stop != proto.StopEndTurn {
		t.Fatalf("errors with a success between them tripped the budget: %q, %v", stop, h.budgets())
	}
	h = newHarness(t, b, fail("t1"), fail("t2"), goinfertest.Says("never sent"))
	if stop, _ := h.sess.Turn(context.Background(), "go"); stop != proto.StopBudget {
		t.Fatalf("stop %q", stop)
	}
	if got := h.budgets(); !slices.Equal(got, []string{"consecutive_errors"}) {
		t.Fatalf("budgets %v", got)
	}
	h.checkRebuild(t)
}

// TestBudgetsMustBeSet: a budget left at zero is a configuration mistake, not "no limit".
func TestBudgetsMustBeSet(t *testing.T) {
	b := testBudget
	b.MaxErrors = 0
	h := newHarness(t, b, goinfertest.Says("hi"))
	if stop, _ := h.sess.Turn(context.Background(), "hi"); stop != proto.StopError {
		t.Fatalf("stop %q", stop)
	}
	if n := len(h.payloads(proto.EvRequestSent)); n != 0 {
		t.Fatalf("%d requests sent with an unset budget", n)
	}
}

// TestSamplingReachesTheWire: Config.Sampling goes out with every request; unset, the fields are left out so the
// server's (or the model's) defaults apply.
func TestSamplingReachesTheWire(t *testing.T) {
	temp, topP, topK := 0.7, 0.8, 20
	h := newHarness(t, Budget{}, goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`), goinfertest.Says("done"))
	cfg := h.sess.cfg
	cfg.Sampling = server.Sampling{Temperature: &temp, TopP: &topP, TopK: &topK}
	h.sess = New(cfg)
	if stop, err := h.sess.Turn(context.Background(), "list"); err != nil || stop != proto.StopEndTurn {
		t.Fatalf("turn: %q %v", stop, err)
	}
	if len(h.script.Requests) != 2 {
		t.Fatalf("%d requests", len(h.script.Requests))
	}
	for i, body := range h.script.Requests {
		s := string(body)
		if !strings.Contains(s, `"temperature":0.7`) || !strings.Contains(s, `"top_p":0.8`) || !strings.Contains(s, `"top_k":20`) {
			t.Errorf("request %d lacks the sampling: %s", i, s[:min(len(s), 300)])
		}
	}
	h = newHarness(t, Budget{}, goinfertest.Says("hi"))
	h.sess.Turn(context.Background(), "hi")
	if s := string(h.script.Requests[0]); strings.Contains(s, "top_p") || strings.Contains(s, "top_k") || strings.Contains(s, "temperature") {
		t.Errorf("unset sampling was sent: %s", s[:min(len(s), 300)])
	}
}

// editHarness is newHarness as an edit task: the done tool, tool_choice any, and changed as the workspace's answer.
func editHarness(t *testing.T, changed func() (bool, error), replies ...goinfertest.Reply) *harness {
	h := newHarness(t, Budget{}, replies...)
	cfg := h.sess.cfg
	cfg.Tools, cfg.EditTask, cfg.Changed = tools.New(tools.Options{Done: true}), true, changed
	h.sess = New(cfg)
	return h
}

func doneResults(h *harness) []string {
	var out []string
	for _, f := range h.payloads(proto.EvToolFinished) {
		out = append(out, f.(proto.ToolFinished).Result)
	}
	return out
}

// TestEditTaskForcesToolCalls: every request of an edit task carries tool_choice any and the edit system prompt, and
// done ends the turn with stop reason done once files have changed.
func TestEditTaskForcesToolCalls(t *testing.T) {
	h := editHarness(t, func() (bool, error) { return true, nil },
		goinfertest.Calls("", "t1", "glob", `{"pattern":"*.go"}`), goinfertest.Calls("", "d1", "done", `{"reason":"fixed it"}`))
	if stop, err := h.sess.Turn(context.Background(), "fix it"); err != nil || stop != proto.StopDone {
		t.Fatalf("stop %q %v", stop, err)
	}
	for i, body := range h.script.Requests {
		var req struct {
			System     string `json:"system"`
			ToolChoice *struct {
				Type string `json:"type"`
			} `json:"tool_choice"`
		}
		json.Unmarshal(body, &req)
		if req.ToolChoice == nil || req.ToolChoice.Type != "any" || req.System != EditSystemPrompt {
			t.Errorf("request %d: tool_choice %+v, system edit prompt %v", i, req.ToolChoice, req.System == EditSystemPrompt)
		}
	}
	if r := doneResults(h); len(r) != 2 || r[1] != "done" {
		t.Fatalf("results %q", r)
	}
	h.checkRebuild(t)
}

// TestDoneWithNoChangeIsRefusedOnce: the first done with no file changed is answered DoneNoChange and the turn goes on;
// the second ends it, whatever the files say.
func TestDoneWithNoChangeIsRefusedOnce(t *testing.T) {
	h := editHarness(t, func() (bool, error) { return false, nil },
		goinfertest.Calls("", "d1", "done", `{"reason":"looks fine"}`), goinfertest.Calls("", "d2", "done", `{"reason":"no change is needed: X"}`))
	if stop, err := h.sess.Turn(context.Background(), "fix it"); err != nil || stop != proto.StopDone {
		t.Fatalf("stop %q %v", stop, err)
	}
	if r := doneResults(h); len(r) != 2 || r[0] != DoneNoChange || r[1] != "done" || len(h.script.Requests) != 2 {
		t.Fatalf("results %q after %d requests", r, len(h.script.Requests))
	}
	// An unreadable workspace counts as changed: done ends at once.
	h = editHarness(t, func() (bool, error) { return false, errors.New("git: not a repository") },
		goinfertest.Calls("", "d1", "done", `{"reason":"x"}`))
	if stop, _ := h.sess.Turn(context.Background(), "fix it"); stop != proto.StopDone || len(doneResults(h)) != 1 {
		t.Fatalf("unreadable workspace: stop %q, results %q", stop, doneResults(h))
	}
}

// TestWindowFullIsItsOwnStop: a reply cut off at max_tokens with fewer output tokens than the request allowed was stopped
// by the window (the prompt left no room), and the turn ends window_full; one that used its whole limit ends max_tokens.
func TestWindowFullIsItsOwnStop(t *testing.T) {
	cut := proto.ModelBlock{Type: "text", Text: "<tool_call>\n{\"name\": \"edit\", \""}
	h := newHarness(t, Budget{}, goinfertest.Reply{Body: goinfertest.SSEWithReuse("max_tokens", 101, 32658, 9, cut)})
	if stop, err := h.sess.Turn(context.Background(), "go"); err != nil || stop != proto.StopWindowFull {
		t.Fatalf("a reply cut at 9 of 256 tokens: stop %q %v", stop, err)
	}
	h = newHarness(t, Budget{}, goinfertest.Reply{Body: goinfertest.SSEWithReuse("max_tokens", 100, 0, 256, cut)})
	if stop, _ := h.sess.Turn(context.Background(), "go"); stop != proto.StopMaxTokens {
		t.Fatalf("a reply that used its whole limit: stop %q", stop)
	}
}

// TestRepeatOfAFailedCallIsNotRun: the exact repeat of the call that just failed, its input's keys in another order, is
// answered RepeatedCall without running and counts as a failure; the same call after a success in between runs again.
func TestRepeatOfAFailedCallIsNotRun(t *testing.T) {
	failing := `{"path":"mcp/token_budget.go","offset":9999}`
	h := newHarness(t, Budget{},
		goinfertest.Calls("", "r1", "read", failing),
		goinfertest.Calls("", "r2", "read", `{"offset":9999,"path":"mcp/token_budget.go"}`),
		goinfertest.Calls("", "g1", "glob", `{"pattern":"**/*.go"}`),
		goinfertest.Calls("", "r3", "read", failing),
		goinfertest.Says("stop"))
	if stop, err := h.sess.Turn(context.Background(), "go"); err != nil || stop != proto.StopEndTurn {
		t.Fatalf("stop %q %v", stop, err)
	}
	var got []proto.ToolFinished
	for _, f := range h.payloads(proto.EvToolFinished) {
		got = append(got, f.(proto.ToolFinished))
	}
	if len(got) != 4 || got[0].Outcome != proto.OutcomeError || got[0].Result == RepeatedCall ||
		got[1].Result != RepeatedCall || got[1].Outcome != proto.OutcomeError ||
		got[2].Outcome != proto.OutcomeOK || got[3].Result == RepeatedCall {
		t.Fatalf("results %+v", got)
	}
	if n := len(h.payloads(proto.EvToolStarted)); n != 3 {
		t.Fatalf("%d tools started; the repeat must not run", n)
	}
	// Repeats count toward the consecutive-error budget.
	b := testBudget
	b.MaxErrors = 3
	h = newHarness(t, b, goinfertest.Calls("", "r1", "read", failing), goinfertest.Calls("", "r2", "read", failing),
		goinfertest.Calls("", "r3", "read", failing), goinfertest.Says("never"))
	if stop, _ := h.sess.Turn(context.Background(), "go"); stop != proto.StopBudget {
		t.Fatalf("three failures, two of them repeats: stop %q", stop)
	}
}
