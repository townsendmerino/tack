package core

import (
	"context"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/tools"
)

// relatingSearcher is ken in a mode with vectors, as far as find_related is concerned.
type relatingSearcher struct{}

func (relatingSearcher) Search(context.Context, string, int) ([]tools.Hit, error) { return nil, nil }
func (relatingSearcher) Relates() bool                                            { return true }
func (relatingSearcher) Sync(context.Context, []string) (bool, error)             { return false, nil }
func (relatingSearcher) Related(context.Context, string, int, int) ([]tools.Hit, error) {
	return nil, nil
}

// TestASessionOffersFindRelatedWhenAsked: with Options.FindRelated, a session over a relating searcher has the
// find_related tool in its requests, in plan and in auto mode; without it, it does not.
func TestASessionOffersFindRelatedWhenAsked(t *testing.T) {
	for _, on := range []bool{false, true} {
		for _, mode := range []proto.Mode{proto.ModePlan, proto.ModeAuto} {
			f := newFixture(t, goinfertest.Says("done"))
			f.core.opt.FindRelated = on
			f.core.opt.OpenSearcher = func(context.Context, string) (tools.Searcher, error) { return relatingSearcher{}, nil }
			f.core.opt.Edit = func(root string, _ sandbox.Target) (EditKit, error) { // auto mode's kit, as the edit tests make it
				return EditKit{Shell: noShell{}, Engine: &perms.Engine{Root: root, Sandboxed: true}}, nil
			}
			sid, err := f.sessionIn(t, gitWorkspace(t, ""), mode)
			if err != nil {
				t.Fatal(err)
			}
			f.client.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sid}, nil)
			f.client.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sid, Content: []proto.ContentBlock{proto.Text("hi")}}, nil)
			f.waitTurnEnd(t)
			if len(f.script.Requests) == 0 {
				t.Fatalf("%s, find_related %v: no request reached the model", mode, on)
			}
			if got := strings.Contains(string(f.script.Requests[0]), `"name":"find_related"`); got != on {
				t.Errorf("%s, find_related %v: the tool is in the request: %v", mode, on, got)
			}
		}
	}
}
