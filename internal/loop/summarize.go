package loop

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
)

// The summarizer call (DESIGN §7.6, step 1; P1 step 6, S2). The model's thinking-off alias (server.ModelInfo.FastModel)
// reads the turns being compacted in chunks of about summaryChunkTokens and updates the previous summary's body under
// SummaryHeadings, never rewriting it from nothing. Every user message and the anchor list are added around the body
// mechanically, so they are exact whatever the model writes; any failure keeps the previous body, so a stretch is never
// dropped with nothing in its place.

const (
	// summaryChunkTokens is about how much of the stretch one summarizer call reads.
	summaryChunkTokens = 4000
	// summaryBytesPerToken sizes chunks without a tokenizer call: chunking bounds what one call reads, so an estimate
	// is enough.
	summaryBytesPerToken = 4
	// summaryMaxTokens caps the summarizer's reply.
	summaryMaxTokens = 1024
	// summaryItemBytes is how much of one message or tool result a chunk carries.
	summaryItemBytes = summaryChunkTokens * summaryBytesPerToken / 4
)

// summarySystem is the summarizer's fixed system prompt. It carries nothing per session or per day, so the server's
// cached prefix serves every summarizer call.
var summarySystem = "You keep the running summary of a coding session between a user and an assistant that uses tools. " +
	"You are given the previous summary and the next turns. Write the updated summary: keep what still holds, add what the " +
	"new turns did, decided or left open, and drop nothing the user asked for. Write only the summary, as markdown, with " +
	"exactly these headings in this order, each on its own line starting with \"## \": " +
	strings.Join(SummaryHeadings, ", ") + ". Under a heading with nothing to say, write \"(none)\". " +
	"Copy file paths, names, commands and error text exactly."

// Summary is a compaction's summary.
type Summary struct {
	// Text is the whole summary as the model will see it: the user messages word for word, the task first; the body;
	// the anchor list.
	Text string
	// Body is the part under SummaryHeadings, the next compaction's previous body; "" when there has never been one.
	Body string
	// FellBack says the summarizer's text was not used, and Reason why.
	FellBack bool
	Reason   string
}

// SummarySend sends one summarizer request and returns the reply: a server's Stream, or the live loop's, which logs
// the request and its usage (Session.summarize).
type SummarySend func(ctx context.Context, req server.Request) (*server.Response, error)

// StreamTo is the SummarySend of srv alone, with nothing logged.
func StreamTo(srv server.Server) SummarySend {
	return func(ctx context.Context, req server.Request) (*server.Response, error) {
		return srv.Stream(ctx, req, func(server.Delta) {})
	}
}

// Summarize is the summary after stretch, the turns now compacted, given prior, the turns compacted before, and
// previous, the body of the summary that covered them. model is the summarizing model (the thinking-off alias); ""
// means none is served, and the previous body is kept with no call. Each request goes through send.
func Summarize(ctx context.Context, send SummarySend, model string, prior, stretch []proto.Event, previous string) Summary {
	all := append(append([]proto.Event(nil), prior...), stretch...)
	keep := func(reason string) Summary {
		return Summary{Text: summaryText(all, previous), Body: previous, FellBack: true, Reason: reason}
	}
	if model == "" {
		return keep("no summarizing model: the server lists no thinking-off alias")
	}
	body := previous
	for i, chunk := range summaryChunks(stretch) {
		resp, err := send(ctx, summaryRequest(model, body, chunk))
		if err != nil {
			return keep(fmt.Sprintf("chunk %d: %v", i+1, err))
		}
		var b strings.Builder
		for _, blk := range resp.Content {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		next := strings.TrimSpace(b.String())
		if !HasSummaryHeadings(next) {
			return keep(fmt.Sprintf("chunk %d: the reply lacks the summary's headings", i+1))
		}
		body = next
	}
	return Summary{Text: summaryText(all, body), Body: body}
}

// summaryRequest asks model to update body with chunk.
func summaryRequest(model, body, chunk string) server.Request {
	if body == "" {
		body = "(none yet)"
	}
	msg := "Previous summary:\n\n" + body + "\n\nNext turns:\n\n" + chunk + "\n\nWrite the updated summary."
	return server.Request{Model: model, System: summarySystem, MaxTokens: summaryMaxTokens,
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock(msg)}}}}
}

// summaryChunks is stretch as transcript text, one line group per message, call or result, split before a group that
// would take a chunk past summaryChunkTokens. Each message and result is cut to summaryItemBytes.
func summaryChunks(stretch []proto.Event) []string {
	var chunks []string
	var cur strings.Builder
	add := func(s string) {
		if cur.Len() > 0 && cur.Len()+len(s) > summaryChunkTokens*summaryBytesPerToken {
			chunks = append(chunks, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
		}
		cur.WriteString(s + "\n\n")
	}
	for _, ev := range stretch {
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		switch ev.Type {
		case proto.EvUserMessage:
			add("User: " + cut(blocksText(v.(proto.UserMessage).Content), summaryItemBytes))
		case proto.EvAssistantMessage:
			var t []string
			for _, b := range v.(proto.AssistantMessage).Content {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					t = append(t, b.Text)
				}
			}
			if len(t) > 0 {
				add("Assistant: " + cut(strings.Join(t, "\n"), summaryItemBytes))
			}
		case proto.EvToolCall:
			c := v.(proto.ToolCall)
			add("Tool call " + c.Name + ": " + cut(string(c.Input), summaryItemBytes))
		case proto.EvToolFinished:
			r := v.(proto.ToolFinished)
			add("Tool result (" + r.Outcome + "): " + cut(r.Result, summaryItemBytes))
		}
	}
	if cur.Len() > 0 {
		chunks = append(chunks, strings.TrimRight(cur.String(), "\n"))
	}
	return chunks
}

func blocksText(bs []proto.ContentBlock) string {
	t := make([]string, len(bs))
	for i, b := range bs {
		t[i] = b.Text
	}
	return strings.Join(t, "\n")
}

// summaryText is the summary of all with body: every user message word for word, the task first; then body; then the
// anchor list. Each part is left out when empty.
func summaryText(all []proto.Event, body string) string {
	var msgs []string
	for _, ev := range all {
		if ev.Type != proto.EvUserMessage {
			continue
		}
		if v, err := ev.Decode(); err == nil {
			msgs = append(msgs, blocksText(v.(proto.UserMessage).Content))
		}
	}
	var parts []string
	if len(msgs) > 0 {
		parts = append(parts, "## Task\n\n"+msgs[0])
	}
	if len(msgs) > 1 {
		parts = append(parts, "## Later messages\n\n"+strings.Join(msgs[1:], "\n\n"))
	}
	if body != "" {
		parts = append(parts, body)
	}
	if a := AnchorsOf(all).Text(); a != "" {
		parts = append(parts, a)
	}
	return strings.Join(parts, "\n\n")
}

// summaryCuts finds where a summarizing compaction cuts the history: the message index of each assistant.message, as
// it is now. The live loop and Rebuild both keep one, so both cut at the same message.
type summaryCuts struct {
	at    []int64       // the assistant.message seqs, in order
	index map[int64]int // each one's message index when it was added
	shift int           // how far earlier summaries moved every index down
}

// add notes that the assistant.message at seq became message index.
func (c *summaryCuts) add(seq int64, index int) {
	if c.index == nil {
		c.index = map[int64]int{}
	}
	c.at = append(c.at, seq)
	c.index[seq] = index + c.shift
}

// cut is the first assistant.message after every seq in summarized: its index, as it is now, and its seq.
func (c *summaryCuts) cut(summarized []int64) (int, int64, error) {
	if len(summarized) == 0 {
		return 0, 0, fmt.Errorf("a summary that covers no event")
	}
	last := slices.Max(summarized)
	for _, seq := range c.at {
		if seq > last {
			return c.index[seq] - c.shift, seq, nil
		}
	}
	return 0, 0, fmt.Errorf("a summary covering up to seq %d keeps no assistant message after it", last)
}

// summarized notes that a summary replaced the messages before keep with one.
func (c *summaryCuts) summarized(keep int) { c.shift += keep - 1 }

// summaryFrame opens the summary message, so the model reads what follows as what came before, not a new request.
const summaryFrame = "[Summary of the session so far: what the earlier turns asked, did and found. It replaces them.]\n\n"

// summaryMessage is the user message a summary puts in the history's place: the workspace preamble the first user
// message carried (UserText), the frame, then the summary's text. The summary keeps every user message word for word,
// so the first message's instruction notes stay with it.
func summaryMessage(root string, s Summary) string { return UserText(true, root, summaryFrame+s.Text) }
