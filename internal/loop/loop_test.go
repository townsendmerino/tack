package loop

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
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
}

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
	log, err := st.Create("s_test")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{st: st, log: log, ws: ws, script: goinfertest.NewScripted("m", 16384, replies...)}
	ts := httptest.NewServer(h.script)
	t.Cleanup(ts.Close)
	if b.MaxRequests == 0 {
		b = Budget{MaxRequests: 8, MaxToolCalls: 8, MaxWall: time.Minute, MaxTokens: 256}
	}
	h.sess = New(Config{Source: models.NewRemote("local", ts.URL, "", "", nil), Tools: tools.NewSet(nil), WS: ws,
		Log: log, Blobs: st.Blobs, Budget: b,
		Emit:  func(ev proto.Event) { h.events = append(h.events, ev) },
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
	re, err := Rebuild(evs, h.ws.Root(), live.Model, live.Tools, live.MaxTokens)
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

func TestToolCallBudget(t *testing.T) {
	h := newHarness(t, Budget{MaxRequests: 8, MaxToolCalls: 1, MaxWall: time.Minute, MaxTokens: 256},
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
	h := newHarness(t, Budget{MaxRequests: 1, MaxToolCalls: 8, MaxWall: time.Minute, MaxTokens: 256},
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
	re, err := Rebuild(cut, h.ws.Root(), live.Model, live.Tools, live.MaxTokens)
	if err != nil {
		t.Fatal(err)
	}
	last := re.Request().Messages[len(re.Request().Messages)-1]
	if b := last.Content[0]; b.Type != "tool_result" || !b.IsError || !strings.Contains(b.Result, "outcome unknown") {
		t.Fatalf("last block %+v", b)
	}
}
