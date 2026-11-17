package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/townsendmerino/tack/internal/proto"
)

// asked is an App whose bash call c1 has an open question, the permission.requested event at seq 7.
func asked(t *testing.T) (*App, *fakeBackend) {
	t.Helper()
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	a.Update(EventMsg(ev(t, 6, proto.EvToolCall, proto.ToolCall{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"rm x"}`)})))
	a.Update(EventMsg(ev(t, 7, proto.EvPermissionRequested, proto.PermissionRequested{ID: "c1", Tool: "bash", Mode: proto.ModeAsk,
		Command: "rm x", Reasons: []string{"rm deletes files"}})))
	return a, fb
}

// press sends a key, and runs the command it returns, as the program would.
func press(a *App, k tea.KeyMsg) {
	_, cmd := a.Update(k)
	run(a, cmd)
}

func rune1(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// TestTheKeysAnswerTheOpenQuestion: a, s and d answer the question the screen shows, naming its event's seq and the call's id,
// allow once, allow for the session, and deny; capitals do the same; the answer goes through the backend and not the composer.
func TestTheKeysAnswerTheOpenQuestion(t *testing.T) {
	for _, c := range []struct {
		key             rune
		decision, scope string
	}{
		{'a', proto.DecisionAllow, "once"}, {'s', proto.DecisionAllow, "session"}, {'d', proto.DecisionDeny, "once"},
		{'A', proto.DecisionAllow, "once"}, {'S', proto.DecisionAllow, "session"}, {'D', proto.DecisionDeny, "once"},
	} {
		a, fb := asked(t)
		press(a, rune1(c.key))
		want := answer{id: "c1", seq: 7, decision: c.decision, scope: c.scope}
		if len(fb.answers) != 1 || fb.answers[0] != want {
			t.Errorf("key %q: answers %+v, want %+v", c.key, fb.answers, want)
		}
		if len(a.input) != 0 || len(fb.sent) != 0 {
			t.Errorf("key %q reached the composer: %q, sent %q", c.key, string(a.input), fb.sent)
		}
	}
}

// TestKeysTypeWhenNothingIsAsked: with no open question, and after one is resolved, a, s and d are ordinary letters and answer
// nothing; while one is open, other letters are not typed into the composer.
func TestKeysTypeWhenNothingIsAsked(t *testing.T) {
	fb := &fakeBackend{}
	a := &App{Backend: fb}
	typed(a, "sad")
	if string(a.input) != "sad" || len(fb.answers) != 0 {
		t.Fatalf("composer %q, answers %+v", string(a.input), fb.answers)
	}
	a, fb = asked(t)
	typed(a, "xyz")
	if len(a.input) != 0 {
		t.Errorf("typed into the composer while a question is open: %q", string(a.input))
	}
	a.Update(EventMsg(ev(t, 8, proto.EvPermissionResolved, proto.PermissionResolved{ID: "c1", Decision: proto.DecisionAllow, By: proto.DecidedByUser, Scope: "once"})))
	typed(a, "ok")
	press(a, rune1('d'))
	if string(a.input) != "okd" || len(fb.answers) != 0 {
		t.Errorf("after the answer: composer %q, answers %+v", string(a.input), fb.answers)
	}
}

// TestAQuestionAnsweredElsewhereIsNotAskedAgain: a replayed question that already has its permission.resolved is not open, so
// a re-attached screen offers no keys for it; and a question the turn ending closes stops offering them too.
func TestAQuestionAnsweredElsewhereIsNotAskedAgain(t *testing.T) {
	a, fb := asked(t)
	a.Update(EventMsg(ev(t, 8, proto.EvPermissionResolved, proto.PermissionResolved{ID: "c1", Decision: proto.DecisionDeny, By: proto.DecidedByUser, Scope: "once"})))
	press(a, rune1('a'))
	if len(fb.answers) != 0 {
		t.Errorf("answered a resolved question: %+v", fb.answers)
	}
	a, fb = asked(t)
	a.Update(EventMsg(ev(t, 8, proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopCancelled})))
	press(a, rune1('a'))
	if len(fb.answers) != 0 {
		t.Errorf("answered a question the turn's end closed: %+v", fb.answers)
	}
}

// TestARefusedAnswerShowsAsANote: a late answer (not_pending), an older daemon (method not found) and any other error say so in
// the transcript, and the question stays open until the daemon says it is resolved.
func TestARefusedAnswerShowsAsANote(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&proto.Error{Code: proto.CodeNotPending, Message: "x"}, "already answered"},
		{&proto.Error{Code: proto.CodeMethodNotFound, Message: "x"}, "older than this screen"},
		{errors.New("pipe closed"), "pipe closed"},
	} {
		a, fb := asked(t)
		fb.respondErr = c.err
		press(a, rune1('a'))
		if f := a.Frame(); !strings.Contains(f, c.want) || !strings.Contains(f, "[a]llow") {
			t.Errorf("error %v: want %q and the keys still offered:\n%s", c.err, c.want, f)
		}
	}
}
