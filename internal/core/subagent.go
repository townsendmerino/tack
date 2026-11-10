package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

// subagent is the task tool of session parent (Options.Subagents; DESIGN §6.3). Each call makes a child session, its
// own log with SessionCreated.Parent set, in plan mode on the parent's workspace and model: the read-only tools, no task
// of its own, and Options.SubagentBudget (open). Its one turn runs in the parent's tool call, on the parent turn's ctx,
// so a halt or a drain of the parent reaches it, and its log records how it ended. The answer is its last assistant
// text. Its limits are its own budget clamped to what the parent has left (loop.RemainingFrom), and its tokens and tool
// calls count toward the parent's budgets (tools.SubagentAnswer). The child is not in the core's sessions, so no client
// subscribes to it live and its log opens later like any other; its halt target is registered (open), as every
// session's is.
//
// The child gets no workspace instructions (CLAUDE.md, sections, knowledge pages), by design: it is read-only and
// answers one question, project rules are mostly about changing things, which it cannot, and its window is better
// spent on the question. The task tool's description asks the parent to put what the helper needs in the prompt.
func (c *Core) subagent(parent proto.SessionID, ws *tools.Workspace, pc proto.SessionCreated) tools.Subagent {
	return func(ctx context.Context, prompt string) (tools.SubagentAnswer, error) {
		// The child takes no more than the parent has left, tokens and tool calls both, so a task cannot carry the
		// parent past its budgets; with nothing left there is no child.
		b := c.opt.SubagentBudget
		if rem, ok := loop.RemainingFrom(ctx); ok {
			b.TurnTokens, b.SessionTokens = min(b.TurnTokens, rem.Tokens), min(b.SessionTokens, rem.Tokens)
			b.MaxToolCalls, b.SessionToolCalls = min(b.MaxToolCalls, rem.ToolCalls), min(b.SessionToolCalls, rem.ToolCalls)
			if rem.Tokens <= 0 || rem.ToolCalls <= 0 {
				return tools.SubagentAnswer{}, fmt.Errorf("the turn has no budget left for a helper (%d tokens, %d tool calls)", rem.Tokens, rem.ToolCalls)
			}
		}
		// Refused before its log exists, so a drain leaves no empty child behind; the check under c.mu below catches a
		// drain that begins while the child opens.
		c.mu.Lock()
		draining := c.draining
		c.mu.Unlock()
		if draining {
			return tools.SubagentAnswer{}, errNoHelperWhileDraining
		}
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
		child.loop.Limit(b)
		c.mu.Lock()
		draining = c.draining
		if !draining {
			c.children[child.loop] = true // under c.mu, as Drain reads it: a Drain either sees it or came first
		}
		c.mu.Unlock()
		if draining {
			return tools.SubagentAnswer{}, errNoHelperWhileDraining
		}
		defer func() {
			c.mu.Lock()
			delete(c.children, child.loop)
			c.mu.Unlock()
		}()
		reason, err := child.loop.Turn(ctx, prompt)
		a := tools.SubagentAnswer{Text: lastAnswer(child), Tokens: child.loop.Tokens(), Calls: child.loop.ToolCalls(), Session: string(id)}
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

var errNoHelperWhileDraining = errors.New("the daemon is shutting down: no helper starts now")

// orphanCost is what the children of session parent made since a time spent. Given a cut-off task call's tool.started,
// that is the call's own child: tool calls run one at a time, and a child has no task of its own. Crash recovery
// charges the call with it (closeInterrupted), so the parent's budgets still count it on a resume. Children are found by
// their SessionCreated.Parent and told apart by when their logs began, not by a name in a result: a task that failed,
// was cancelled or was itself closed by recovery names no child, though its tool.finished carries the child's spend.
// Their own logs are closed by the same recovery.
func (c *Core) orphanCost(parent proto.SessionID) func(since time.Time) (int, int) {
	return func(since time.Time) (int, int) {
		ids, err := c.opt.Store.Sessions()
		if err != nil {
			return 0, 0
		}
		tokens, calls := 0, 0
		for _, id := range ids {
			if id == parent {
				continue
			}
			cevs, err := c.opt.Store.Events(id, 0)
			if err != nil || len(cevs) == 0 {
				continue
			}
			if v, err := cevs[0].Decode(); err != nil {
				continue
			} else if cr, ok := v.(proto.SessionCreated); !ok || cr.Parent != parent || cevs[0].Time.Before(since) {
				continue
			}
			t, n := loop.Spent(cevs)
			tokens, calls = tokens+t, calls+n
		}
		return tokens, calls
	}
}
