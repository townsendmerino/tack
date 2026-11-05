package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// TestTTFTAppendCellAppends: through ttft, each request of the append cell is the previous one with its reply and one
// user message added at the end, byte for byte, as tack's loop grows a conversation (E0 amendment 11); the rewrite
// cell keeps its single rewritten message.
func TestTTFTAppendCellAppends(t *testing.T) {
	var replies []goinfertest.Reply
	for range ttftRequests {
		replies = append(replies, goinfertest.Says("w"))
	}
	script := goinfertest.NewScripted("m", 8192, replies...)
	script.Tokens = 4096 // count_tokens: the whole filler sizes to n
	ts := httptest.NewServer(script)
	defer ts.Close()
	var out bytes.Buffer
	r := &runner{o: Options{Manifest: &Manifest{}}, results: &out}
	filler := strings.Repeat("package big // some code text here\n", 3000)
	if err := r.ttft(context.Background(), goinfer.New("t", ts.URL, "", nil), Arm{Name: "m"}, 4096, filler, &progress{}); err != nil {
		t.Fatal(err)
	}
	type msg struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	var reqs [][]msg
	for _, b := range script.Requests {
		var req struct{ Messages []msg }
		json.Unmarshal(b, &req)
		reqs = append(reqs, req.Messages)
	}
	if len(reqs) != ttftRequests {
		t.Fatalf("%d requests, want %d", len(reqs), ttftRequests)
	}
	for i := 5; i < 8; i++ { // the append cell: its warm-up is request 4, its repeats 5-7
		prev, cur := reqs[i-1], reqs[i]
		pb, _ := json.Marshal(prev)
		cb, _ := json.Marshal(cur[:len(prev)])
		if len(cur) != len(prev)+2 || !bytes.Equal(pb, cb) || cur[len(prev)].Role != "assistant" || cur[len(prev)+1].Role != "user" {
			t.Fatalf("append request %d is not request %d plus a reply and a user message:\nprev %s\ncur  %s", i, i-1, pb, cb)
		}
	}
	for i := 8; i < 12; i++ {
		if len(reqs[i]) != 1 {
			t.Fatalf("rewrite request %d has %d messages", i, len(reqs[i]))
		}
	}
	cells := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		var rec struct{ Cell string }
		json.Unmarshal([]byte(line), &rec)
		cells[rec.Cell]++
	}
	if cells["cold"] != 3 || cells["append"] != 3 || cells["rewrite"] != 3 || len(cells) != 3 {
		t.Fatalf("recorded cells %v, want 3 each of cold, append and rewrite", cells)
	}
}

// TestAppendedAnswersToolCalls: a reply that called a tool is followed by its result, then the appended text, so the
// request stays one a server accepts.
func TestAppendedAnswersToolCalls(t *testing.T) {
	base := ttftRequest("m", "sys", "first", false)
	next := appended(base, []proto.ModelBlock{{Type: "tool_use", ID: "c1", Name: "grep", Input: json.RawMessage(`{}`)}}, "more")
	if len(next.Messages) != 3 || len(base.Messages) != 1 {
		t.Fatalf("messages %d (base now %d)", len(next.Messages), len(base.Messages))
	}
	u := next.Messages[2]
	if u.Role != server.User || len(u.Content) != 2 || u.Content[0].Type != "tool_result" || u.Content[0].ToolUseID != "c1" || u.Content[1].Text != "more" {
		t.Fatalf("user message %+v", u)
	}
	if e := appended(base, nil, "x"); e.Messages[1].Role != server.Assistant || len(e.Messages[1].Content) != 1 {
		t.Fatalf("an empty reply must still take its turn: %+v", e.Messages[1])
	}
}
