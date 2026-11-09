package loop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

func usesTool(id string) proto.AssistantMessage {
	return proto.AssistantMessage{Content: []proto.ModelBlock{{Type: "tool_use", ID: id, Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)}}}
}

func replies(s string) proto.AssistantMessage {
	return proto.AssistantMessage{Content: []proto.ModelBlock{{Type: "text", Text: s}}}
}

// roles is each message's role and first text, or "tool" for a message of tool results.
func roles(msgs []server.Message) []string {
	var out []string
	for _, m := range msgs {
		b := m.Content[0]
		switch {
		case b.Type == "tool_result":
			out = append(out, string(m.Role)+":result "+b.ToolUseID)
		case b.Type == "tool_use":
			out = append(out, string(m.Role)+":use "+b.ID)
		default:
			out = append(out, string(m.Role)+":"+b.Text)
		}
	}
	return out
}

// TestRebuildAppliesASummary: a summarizing compaction replaces every message before the first assistant.message after
// what it covers with its summary, word for word; a second one cuts at a message the first kept, where the first left it.
func TestRebuildAppliesASummary(t *testing.T) {
	evs := summaryLog(t,
		proto.EvUserMessage, user("one"), // 1
		proto.EvAssistantMessage, usesTool("c1"), // 2
		proto.EvToolFinished, finished("c1", proto.OutcomeOK, "package a"), // 3
		proto.EvAssistantMessage, replies("read it"), // 4
		proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn}, // 5
		proto.EvUserMessage, user("two"), // 6
		proto.EvAssistantMessage, usesTool("c2"), // 7
		proto.EvToolFinished, finished("c2", proto.OutcomeOK, "package a again"), // 8
		proto.EvCompaction, proto.Compaction{Summary: "[Summary 1]", Summarized: []int64{1, 2, 3}}, // 9
		proto.EvAssistantMessage, replies("read it again"), // 10
		proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn}, // 11
		proto.EvUserMessage, user("three"), // 12
		proto.EvAssistantMessage, replies("done"), // 13
		proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn}, // 14
		proto.EvCompaction, proto.Compaction{Summary: "[Summary 2]", Summarized: []int64{4, 5, 6}}, // 15
	)
	conv, err := Rebuild(evs[:9], "/w", server.Conversation{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(roles(conv.Messages()), " | "), "user:[Summary 1] | assistant:read it | user:two | assistant:use c2 | user:result c2"; got != want {
		t.Errorf("after the first summary:\n got %s\nwant %s", got, want)
	}
	if conv, err = Rebuild(evs, "/w", server.Conversation{}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(roles(conv.Messages()), " | "), "user:[Summary 2] | assistant:use c2 | user:result c2 | assistant:read it again | user:three | assistant:done"; got != want {
		t.Errorf("after the second summary:\n got %s\nwant %s", got, want)
	}
}

// TestRebuildRefusesASummaryWithNothingKept: a summary that covers the last assistant message has nothing to keep, and
// a log that says so is refused rather than rebuilt into another conversation.
func TestRebuildRefusesASummaryWithNothingKept(t *testing.T) {
	evs := summaryLog(t,
		proto.EvUserMessage, user("one"),
		proto.EvAssistantMessage, replies("hi"),
		proto.EvTurnEnd, proto.TurnEnd{StopReason: proto.StopEndTurn},
		proto.EvCompaction, proto.Compaction{Summary: "[S]", Summarized: []int64{1, 2, 3}},
	)
	if _, err := Rebuild(evs, "/w", server.Conversation{}); err == nil || !strings.Contains(err.Error(), "keeps no assistant message") {
		t.Errorf("rebuild: %v", err)
	}
}

// TestAMaskOnASummarizedResultStillApplies: masks run before the summary, so one naming a result the summary removes
// is not an error.
func TestAMaskOnASummarizedResultStillApplies(t *testing.T) {
	evs := summaryLog(t,
		proto.EvUserMessage, user("one"),
		proto.EvAssistantMessage, usesTool("c1"),
		proto.EvToolFinished, finished("c1", proto.OutcomeOK, "package a"),
		proto.EvAssistantMessage, replies("ok"),
		proto.EvCompaction, proto.Compaction{Masked: []proto.MaskedResult{{ID: "c1", Stub: "[masked]"}}, Summary: "[S]", Summarized: []int64{1, 2, 3}},
	)
	conv, err := Rebuild(evs, "/w", server.Conversation{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(roles(conv.Messages()), " | "); got != "user:[S] | assistant:ok" {
		t.Errorf("messages %s", got)
	}
}
