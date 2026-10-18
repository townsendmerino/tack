package tools

import (
	"context"
	"encoding/json"

	"github.com/townsendmerino/tack/internal/server"
)

// doneTool ends an edit task (loop.Config.EditTask). The loop answers it itself, because whether it ends the turn
// depends on the turn: a first call with no file changed is refused once (loop.DoneNoChange). Run is only reached in a
// session that is not an edit task.
type doneTool struct{}

func (doneTool) ReadOnly() bool { return true }
func (doneTool) Spec() server.Tool {
	return server.Tool{Name: "done",
		Description: "Finish the task. Call it once the change is made and checked.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string","description":"what you changed, or why no change is needed"}},"required":["reason"]}`)}
}

func (doneTool) Run(context.Context, *Workspace, json.RawMessage) (Result, error) {
	return Result{Text: "done is for edit tasks; this session is not one", IsError: true}, nil
}
