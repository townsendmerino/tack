package loop

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/cassette"
	"github.com/townsendmerino/tack/internal/server/goinfer"
	"github.com/townsendmerino/tack/internal/shell"
	"github.com/townsendmerino/tack/internal/tools"
)

var countURL = flag.String("count-url", "", "re-record testdata/fixed-prompt.json with this goinfer-serve's count_tokens "+
	"(a Qwen2.5 model: the tokenizer and chat template of the default model)")

// anySearcher, budgetSymbols, anyBlobs and anyShell stand in for ken, its symbol index, the blob store and the shell so
// every optional tool's schema is counted, edit's symbol and line forms included: the budget holds for the largest tool
// set.
type anySearcher struct{}

func (anySearcher) Search(context.Context, string, int) ([]tools.Hit, error) { return nil, nil }

// anySearcher relates too, so find_related's schema is counted.
func (anySearcher) Relates() bool { return true }

func (anySearcher) Sync(context.Context, []string) (bool, error) { return false, nil }

func (anySearcher) Related(context.Context, string, int, int) ([]tools.Hit, error) { return nil, nil }

type anyBlobs struct{}

func (anyBlobs) Get(string) ([]byte, error) { return nil, nil }

type anyShell struct{}

func (anyShell) Run(context.Context, string, time.Duration) (shell.Result, error) {
	return shell.Result{}, nil
}

// fixedPromptRequest is the largest fixed prompt as goinfer renders it: the edit task's system prompt, the longer of
// the two, every tool's schema including done, and a one-word user message, the least a request can carry.
func fixedPromptRequest(model string) server.Request {
	return server.Request{Model: model, System: EditSystemPrompt, Tools: tools.New(tools.Options{Searcher: anySearcher{}, Symbols: budgetSymbols{}, LineEdits: true, Write: true, Shell: anyShell{}, Blobs: anyBlobs{}, Plan: true, Done: true, RecentlyChanged: true, FindRelated: true}).Specs(),
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("hi")}}}}
}

// TestFixedPromptBudget fails when tack's system prompt plus tool schemas pass FixedPromptBudget(DesignWindow), counted
// by goinfer's count_tokens with the model's own tokenizer, from a cassette. Changing the prompt or a tool's schema changes the
// request, so the replay fails until the count is re-recorded with -count-url against a live goinfer.
func TestFixedPromptBudget(t *testing.T) {
	path := filepath.Join("testdata", "fixed-prompt.json")
	const model = "qwen2.5-coder-0.5b-instruct-q4_k_m"
	var c *goinfer.Client
	var rec *cassette.Recorder
	var rep *cassette.Replayer
	if *countURL != "" {
		rec = cassette.NewRecorder(fmt.Sprintf("goinfer-serve count_tokens, %s on CPU (Qwen2.5 tokenizer and chat template, as "+
			"the default qwen2.5-7b-instruct); recorded %s", model, time.Now().Format("2006-01-02")), nil)
		c = goinfer.New("goinfer@count", *countURL, "", rec.Client())
	} else {
		cs, err := cassette.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		rep = cassette.NewReplayer(cs)
		c = goinfer.New("goinfer@count", "http://cassette.invalid", "", rep.Client())
	}
	n, err := c.CountTokens(context.Background(), fixedPromptRequest(model))
	if err != nil {
		t.Fatalf("%v\n(the fixed prompt changed: re-record with go test ./internal/loop -run TestFixedPromptBudget -args -count-url=<goinfer with a Qwen2.5 model>)", err)
	}
	if rec != nil {
		if err := rec.C.Save(path); err != nil {
			t.Fatal(err)
		}
	} else if err := rep.Done(); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixed prompt: %d tokens of %d (a fifth of the %d design window)", n, FixedPromptBudget(DesignWindow), DesignWindow)
	if err := checkBudget(n); err != nil {
		t.Fatal(err)
	}
}

func checkBudget(n int) error {
	if b := FixedPromptBudget(DesignWindow); n > b {
		return fmt.Errorf("the fixed prompt is %d tokens, over its budget of %d, a fifth of the %d design window (DESIGN §2.3, "+
			"ADR-003): shorten the system prompt or a tool description, or drop a tool", n, b, DesignWindow)
	}
	return nil
}

func TestCheckBudgetFailsOverTheLine(t *testing.T) {
	b := FixedPromptBudget(DesignWindow)
	if b != 3276 || checkBudget(b) != nil || checkBudget(b+1) == nil {
		t.Fatal("checkBudget does not hold the line at a fifth of the design window")
	}
}

// TestTheTaskToolFitsTheBudget: with subagents on, the fixed prompt gains task's schema. The recorded count is for the
// default tools (TestFixedPromptBudget), so task's schema is added at 3 bytes a token, more than Qwen's tokenizer takes,
// and the sum must stay within the budget. A real count of that prompt is a re-record, a run.
func TestTheTaskToolFitsTheBudget(t *testing.T) {
	cs, err := cassette.Load(filepath.Join("testdata", "fixed-prompt.json"))
	if err != nil {
		t.Fatal(err)
	}
	rep := cassette.NewReplayer(cs)
	c := goinfer.New("goinfer@count", "http://cassette.invalid", "", rep.Client())
	n, err := c.CountTokens(context.Background(), fixedPromptRequest("qwen2.5-coder-0.5b-instruct-q4_k_m"))
	if err != nil {
		t.Fatal(err)
	}
	set := tools.New(tools.Options{Subagent: func(context.Context, string) (tools.SubagentAnswer, error) { return tools.SubagentAnswer{}, nil }})
	var spec []byte
	for _, s := range set.Specs() {
		if s.Name == "task" {
			spec, _ = json.Marshal(s)
		}
	}
	if len(spec) == 0 {
		t.Fatal("no task tool in a set with a Subagent")
	}
	if err := checkBudget(n + len(spec)/3); err != nil {
		t.Errorf("with task (%d bytes of schema): %v", len(spec), err)
	}
	t.Logf("fixed prompt with task: about %d tokens of %d", n+len(spec)/3, FixedPromptBudget(DesignWindow))
}
