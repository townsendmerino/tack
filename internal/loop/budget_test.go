package loop

import (
	"context"
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

// anySearcher, anyBlobs and anyShell stand in for ken, the blob store and the shell so every optional tool's schema is counted: the
// budget holds for the largest tool set.
type anySearcher struct{}

func (anySearcher) Search(context.Context, string, int) ([]tools.Hit, error) { return nil, nil }

type anyBlobs struct{}

func (anyBlobs) Get(string) ([]byte, error) { return nil, nil }

type anyShell struct{}

func (anyShell) Run(context.Context, string, time.Duration) (shell.Result, error) {
	return shell.Result{}, nil
}

// fixedPromptRequest is the fixed prompt as goinfer renders it: the system prompt, every tool's schema, and a
// one-word user message, the least a request can carry.
func fixedPromptRequest(model string) server.Request {
	return server.Request{Model: model, System: SystemPrompt, Tools: tools.New(tools.Options{Searcher: anySearcher{}, Write: true, Shell: anyShell{}, Blobs: anyBlobs{}, Plan: true}).Specs(),
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
