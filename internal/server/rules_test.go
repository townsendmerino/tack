package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
)

func TestValidate(t *testing.T) {
	use := func(id string) Block {
		return Block{ModelBlock: proto.ModelBlock{Type: "tool_use", ID: id, Name: "read", Input: json.RawMessage(`{}`)}}
	}
	u := func(bs ...Block) Message { return Message{Role: User, Content: bs} }
	a := func(bs ...Block) Message { return Message{Role: Assistant, Content: bs} }
	ok := []Message{u(TextBlock("q")), a(use("t1"), use("t2")), u(ToolResultBlock("t1", "r", false), ToolResultBlock("t2", "r", true), TextBlock("steer"))}
	if err := Validate(Request{Messages: ok, MaxTokens: 8}); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}
	cases := map[string]struct {
		msgs []Message
		want string
	}{
		"starts with assistant":  {[]Message{a(TextBlock("x")), u(TextBlock("q"))}, "alternates"},
		"two user messages":      {[]Message{u(TextBlock("q")), u(TextBlock("q"))}, "alternates"},
		"ends with assistant":    {[]Message{u(TextBlock("q")), a(TextBlock("x"))}, "last message"},
		"missing result":         {[]Message{u(TextBlock("q")), a(use("t1"), use("t2")), u(ToolResultBlock("t1", "r", false))}, "no tool_result"},
		"answered twice":         {[]Message{u(TextBlock("q")), a(use("t1")), u(ToolResultBlock("t1", "r", false), ToolResultBlock("t1", "r", false))}, "twice"},
		"answers an uncalled id": {[]Message{u(TextBlock("q")), a(use("t1")), u(ToolResultBlock("t1", "r", false), ToolResultBlock("t9", "r", false))}, "did not call"},
		"text before result":     {[]Message{u(TextBlock("q")), a(use("t1")), u(TextBlock("s"), ToolResultBlock("t1", "r", false))}, "first"},
		"tool_use from the user": {[]Message{u(use("t1"))}, "assistant message"},
		"empty message":          {[]Message{u()}, "empty"},
	}
	for name, tc := range cases {
		err := Validate(Request{Messages: tc.msgs, MaxTokens: 8})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", name, err, tc.want)
		}
	}
	if err := Validate(Request{Messages: ok}); err == nil {
		t.Error("max_tokens 0 accepted")
	}
}
