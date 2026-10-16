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
	"github.com/townsendmerino/tack/internal/tools"
)

// FixedPromptBudget is the hard budget for tack's fixed prompt, system instructions plus tool schemas, in tokens of
// the target model's own tokenizer (DESIGN §2.3, proposed at 3,000 for a 16k window).
const FixedPromptBudget = 3000

var countURL = flag.String("count-url", "", "re-record testdata/fixed-prompt.json with this goinfer-serve's count_tokens "+
	"(a Qwen2.5 model: the tokenizer and chat template of the default model)")

// anySearcher stands in for ken so the search tool's schema is counted: the budget holds for the largest tool set.
type anySearcher struct{}

func (anySearcher) Search(context.Context, string, int) ([]tools.Hit, error) { return nil, nil }

// fixedPromptRequest is the fixed prompt as goinfer renders it: the system prompt, every tool's schema, and a
// one-word user message, the least a request can carry.
func fixedPromptRequest(model string) server.Request {
	return server.Request{Model: model, System: SystemPrompt, Tools: tools.NewSet(anySearcher{}).Specs(),
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("hi")}}}}
}

// TestFixedPromptBudget fails when tack's system prompt plus tool schemas pass FixedPromptBudget, counted by goinfer's
// count_tokens with the model's own tokenizer, from a cassette. Changing the prompt or a tool's schema changes the
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
	t.Logf("fixed prompt: %d tokens of %d", n, FixedPromptBudget)
	if err := checkBudget(n); err != nil {
		t.Fatal(err)
	}
}

func checkBudget(n int) error {
	if n > FixedPromptBudget {
		return fmt.Errorf("the fixed prompt is %d tokens, over its budget of %d (DESIGN §2.3): shorten the system prompt "+
			"or a tool description, or drop a tool", n, FixedPromptBudget)
	}
	return nil
}

func TestCheckBudgetFailsOverTheLine(t *testing.T) {
	if checkBudget(FixedPromptBudget) != nil || checkBudget(FixedPromptBudget+1) == nil {
		t.Fatal("checkBudget does not hold the line at FixedPromptBudget")
	}
}
