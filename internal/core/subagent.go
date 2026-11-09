package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

// subagent is the task tool of session parent (Options.Subagents; DESIGN §6.3). Each call makes a child session, its
// own log with SessionCreated.Parent set, in plan mode on the parent's workspace and model: the read-only tools, no task
// of its own, and Options.SubagentBudget (open). Its one turn runs in the parent's tool call, on the parent turn's ctx,
// so a halt or a drain of the parent reaches it, and its log records how it ended. The answer is its last assistant
// text, and its tokens count toward the parent's budgets (tools.SubagentAnswer.Tokens). The child is not registered with
// the core: no client subscribes to it live; its log can be opened later like any other.
func (c *Core) subagent(parent proto.SessionID, ws *tools.Workspace, pc proto.SessionCreated) tools.Subagent {
	return func(ctx context.Context, prompt string) (tools.SubagentAnswer, error) {
		id := store.NewSessionID(time.Now())
		log, hold, err := c.opt.Store.CreateHeld(id)
		if err != nil {
			return tools.SubagentAnswer{}, err
		}
		created := proto.SessionCreated{Workspace: ws.Root(), Model: pc.Model, Mode: proto.ModePlan, Parent: parent}
		if _, err := log.Append(proto.EvSessionCreated, 0, created); err != nil {
			c.opt.Store.Discard(log)
			hold.Release()
			return tools.SubagentAnswer{}, err
		}
		child, err := c.open(id, log, ws, created, nil)
		if err != nil {
			c.opt.Store.Discard(log)
			hold.Release()
			return tools.SubagentAnswer{}, err
		}
		defer func() {
			c.unregister(id)
			if child.kit != nil {
				_ = child.kit()
			}
			_ = log.Close()
			hold.Release()
		}()
		reason, err := child.loop.Turn(ctx, prompt)
		a := tools.SubagentAnswer{Text: lastAnswer(child), Tokens: child.loop.Tokens(), Session: string(id)}
		switch {
		case err != nil:
			return a, err
		case reason != proto.StopEndTurn && reason != proto.StopDone:
			return a, fmt.Errorf("its turn ended with %s", reason)
		}
		return a, nil
	}
}

// lastAnswer is the text of a session's last assistant message.
func lastAnswer(s *session) string {
	msgs := s.loop.Conversation().Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != server.Assistant {
			continue
		}
		var b strings.Builder
		for _, blk := range msgs[i].Content {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	return ""
}
