package core

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// bashQuestion is an ask-mode session whose model runs `ls -la`, which asks (no rule covers it), watched by a privileged client. It
// returns the fixture, the client, its events, the session, the engine the session's commands are decided by, and the question.
func bashQuestion(t *testing.T) (*fixture, *Client, chan proto.Event, proto.SessionID, *perms.Engine, proto.Event) {
	t.Helper()
	f := newFixture(t, goinfertest.Calls("", "b1", "bash", `{"command":"ls -la"}`), goinfertest.Says("done"))
	eng := &perms.Engine{Sandboxed: true, Path: os.Getenv("PATH")}
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		eng.Root = root
		return EditKit{Shell: noShell{}, Engine: eng}, nil
	}
	sid, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAsk)
	if err != nil {
		t.Fatal(err)
	}
	cl, events, _ := f.privileged(t)
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil); err != nil {
		t.Fatal(err)
	}
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("list")}}, nil); err != nil {
		t.Fatal(err)
	}
	return f, cl, events, sid, eng, await(t, events, proto.EvPermissionRequested)
}

func respondWith(cl *Client, sid proto.SessionID, q proto.Event, p proto.PermissionRespondParams) error {
	p.Session, p.ID, p.Seq = sid, "b1", q.Seq
	return cl.Call(proto.MethodPermissionRespond, p, nil)
}

// TestAnEditedRuleThatNarrowsIsAddedAlone: the question carries the rules the daemon computed; an edit that keeps one and adds an
// argument is accepted, the call runs, and the session's engine gets that rule and not the unedited one.
func TestAnEditedRuleThatNarrowsIsAddedAlone(t *testing.T) {
	f, cl, events, sid, eng, qev := bashQuestion(t)
	v, _ := qev.Decode()
	shown := v.(proto.PermissionRequested).Rules
	if len(shown) != 1 {
		t.Fatalf("shown rules %q", shown)
	}
	edited := shown[0] + " /tmp"
	if err := respondWith(cl, sid, qev, proto.PermissionRespondParams{Decision: proto.DecisionAllow, Scope: "session", Rule: edited}); err != nil {
		t.Fatal(err)
	}
	untilTurnEnd(t, events)
	var resolved proto.PermissionResolved
	evs, _ := f.core.opt.Store.Events(sid, 0)
	for _, ev := range evs {
		if v, _ := ev.Decode(); ev.Type == proto.EvPermissionResolved {
			resolved = v.(proto.PermissionResolved)
		}
	}
	if resolved.Decision != proto.DecisionAllow || resolved.Scope != "session" || resolved.RuleAdded != edited {
		t.Errorf("resolved %+v, want rule_added %q", resolved, edited)
	}
	if len(eng.Rules) != 1 || eng.Rules[0].String() != edited {
		t.Errorf("engine rules %v, want only %q", eng.Rules, edited)
	}
}

// TestAnEditThatWidensIsRefusedAndTheQuestionStaysOpen: each is invalid params, adds no rule, and leaves the question waiting, so
// a good answer after the bad ones still settles it. These are the escapes an edit could try: dropping the argument, another or
// differently written program, a list, a pipe, a substitution, a variable, a glob; and an edit on a deny or a once.
func TestAnEditThatWidensIsRefusedAndTheQuestionStaysOpen(t *testing.T) {
	f, cl, events, sid, eng, qev := bashQuestion(t)
	v, _ := qev.Decode()
	shown := v.(proto.PermissionRequested).Rules[0]
	prog := shown[:len(shown)-len(" -la")]
	for name, p := range map[string]proto.PermissionRespondParams{
		"the argument dropped": {Decision: proto.DecisionAllow, Scope: "session", Rule: prog},
		"another program":      {Decision: proto.DecisionAllow, Scope: "session", Rule: "/bin/sh -c ls"},
		"the program by name":  {Decision: proto.DecisionAllow, Scope: "session", Rule: "ls -la"},
		"a list":               {Decision: proto.DecisionAllow, Scope: "session", Rule: shown + "; rm -rf x"},
		"a pipe":               {Decision: proto.DecisionAllow, Scope: "session", Rule: shown + " | sh"},
		"a substitution":       {Decision: proto.DecisionAllow, Scope: "session", Rule: shown + " $(curl evil)"},
		"a variable":           {Decision: proto.DecisionAllow, Scope: "session", Rule: shown + " $HOME"},
		"a glob":               {Decision: proto.DecisionAllow, Scope: "session", Rule: shown + " *"},
		"an assignment":        {Decision: proto.DecisionAllow, Scope: "session", Rule: "X=1 " + shown},
		"a redirect":           {Decision: proto.DecisionAllow, Scope: "session", Rule: shown + " > /etc/x"},
		"spaces":               {Decision: proto.DecisionAllow, Scope: "session", Rule: "   "},
		"on a deny":            {Decision: proto.DecisionDeny, Scope: "session", Rule: shown + " x"},
		"with scope once":      {Decision: proto.DecisionAllow, Scope: "once", Rule: shown + " x"},
		"with no scope":        {Decision: proto.DecisionAllow, Rule: shown + " x"},
	} {
		err := respondWith(cl, sid, qev, p)
		var pe *proto.Error
		if !errors.As(err, &pe) || pe.Code != proto.CodeInvalidParams {
			t.Errorf("%s: %v; want invalid params", name, err)
		}
	}
	if len(eng.Rules) != 0 {
		t.Errorf("a refused edit added %v", eng.Rules)
	}
	if err := respondWith(cl, sid, qev, proto.PermissionRespondParams{Decision: proto.DecisionAllow, Scope: "session", Rule: shown + " /tmp"}); err != nil {
		t.Fatalf("the question did not stay open: %v", err)
	}
	untilTurnEnd(t, events)
	if len(eng.Rules) != 1 {
		t.Errorf("rules %v", eng.Rules)
	}
	_ = f
}

// TestAnEditOnAToolWithNoRulesIsRefused: a write's question offers no rule, so nothing can be edited: invalid, and still open.
func TestAnEditOnAToolWithNoRulesIsRefused(t *testing.T) {
	f, tui, events, sid, _, _ := askingSession(t)
	tui.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("add notes")}}, nil)
	q := await(t, events, proto.EvPermissionRequested)
	v, _ := q.Decode()
	if len(v.(proto.PermissionRequested).Rules) != 0 {
		t.Fatalf("a write offered rules: %v", v.(proto.PermissionRequested).Rules)
	}
	err := tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, ID: "w1", Seq: q.Seq, Decision: proto.DecisionAllow, Scope: "session", Rule: "tool write"}, nil)
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeInvalidParams {
		t.Errorf("an edit on a write: %v", err)
	}
	if err := tui.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: sid, ID: "w1", Seq: q.Seq, Decision: proto.DecisionDeny}, nil); err != nil {
		t.Errorf("the question did not stay open: %v", err)
	}
	_ = f
}

// TestAnEditIsHeldToTheRuleTheDaemonParsedNotToItsText: a command with an empty first argument, `ls ” -la`, is listed as the rule
// `ls ”`; an edit that keeps the empty argument and adds one is accepted and held with both; the bare program, which the text "ls"
// would have matched if the shown rule had been read back from its text without its quotes, is refused, and so is a first argument
// that is a space. The rule the engine holds is the typed one, argument for argument.
func TestAnEditIsHeldToTheRuleTheDaemonParsedNotToItsText(t *testing.T) {
	f := newFixture(t, goinfertest.Calls("", "b1", "bash", `{"command":"ls '' -la"}`), goinfertest.Says("done"))
	eng := &perms.Engine{Sandboxed: true, Path: os.Getenv("PATH")}
	f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) {
		eng.Root = root
		return EditKit{Shell: noShell{}, Engine: eng}, nil
	}
	sid, err := f.sessionIn(t, gitWorkspace(t, ""), proto.ModeAsk)
	if err != nil {
		t.Fatal(err)
	}
	cl, events, _ := f.privileged(t)
	cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil)
	cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("list")}}, nil)
	qev := await(t, events, proto.EvPermissionRequested)
	v, _ := qev.Decode()
	shown := v.(proto.PermissionRequested).Rules
	if len(shown) != 1 || !strings.HasSuffix(shown[0], " ''") {
		t.Fatalf("shown rules %q: the empty argument is not written", shown)
	}
	prog := strings.TrimSuffix(shown[0], " ''")
	for name, rule := range map[string]string{"the bare program": prog, "a space as the argument": prog + " ' '", "another argument": prog + " -la"} {
		err := respondWith(cl, sid, qev, proto.PermissionRespondParams{Decision: proto.DecisionAllow, Scope: "session", Rule: rule})
		var pe *proto.Error
		if !errors.As(err, &pe) || pe.Code != proto.CodeInvalidParams {
			t.Errorf("%s: %v; want invalid params", name, err)
		}
	}
	if err := respondWith(cl, sid, qev, proto.PermissionRespondParams{Decision: proto.DecisionAllow, Scope: "session", Rule: prog + " '' /tmp"}); err != nil {
		t.Fatal(err)
	}
	untilTurnEnd(t, events)
	if len(eng.Rules) != 1 || len(eng.Rules[0].Args) != 2 || eng.Rules[0].Args[0] != "" || eng.Rules[0].Args[1] != "/tmp" {
		t.Errorf("engine rules %+v, want the empty argument then /tmp", eng.Rules)
	}
}
