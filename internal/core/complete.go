package core

import (
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tools"
)

const (
	completeDefaultLimit = 20
	completeMaxLimit     = 50
)

// complete is workspace/complete: the session's workspace's paths for `@`, from the root's Completer, which holds the
// path list so a keystroke does not walk the disk. It changes nothing and logs nothing.
func (c *Core) complete(p proto.WorkspaceCompleteParams) (any, *proto.Error) {
	s, perr := c.session(p.Session)
	if perr != nil {
		return nil, perr
	}
	limit := p.Limit
	if limit <= 0 {
		limit = completeDefaultLimit
	}
	limit = min(limit, completeMaxLimit)
	c.mu.Lock()
	cp, ok := c.completers[s.root]
	if !ok {
		ws, err := tools.NewWorkspace(s.root)
		if err != nil {
			c.mu.Unlock()
			return nil, &proto.Error{Code: proto.CodeInternalError, Message: "workspace/complete: " + err.Error()}
		}
		cp = tools.NewCompleter(ws)
		c.completers[s.root] = cp
	}
	c.mu.Unlock()
	items, err := cp.Complete(p.Query, limit)
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: "workspace/complete: " + err.Error()}
	}
	res := proto.WorkspaceCompleteResult{Items: []proto.CompletionItem{}}
	for _, it := range items {
		res.Items = append(res.Items, proto.CompletionItem{Path: it.Path, Kind: it.Kind})
	}
	return res, nil
}
