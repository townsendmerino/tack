package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/shell"
)

// Shell runs one command line for bash: internal/shell.Session, whose run-time check stops what approval did not see.
type Shell interface {
	Run(ctx context.Context, command string, timeout time.Duration) (shell.Result, error)
}

const (
	bashDefaultTimeout = 2 * time.Minute
	bashMaxTimeout     = 10 * time.Minute
)

// bashTool runs a command line in the session's shell. The permission engine has decided it before Run is called.
type bashTool struct{ sh Shell }

func (bashTool) ReadOnly() bool { return false }
func (bashTool) Spec() server.Tool {
	return server.Tool{Name: "bash",
		Description: "Run a shell command. It starts where the last one left off, at first the repo root; exported variables carry over.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout_ms":{"type":"integer","description":"default 120000, at most 600000"}},"required":["command"]}`)}
}

func (t bashTool) Run(ctx context.Context, _ *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Command   string `json:"command"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decode(input, &in); err != nil || strings.TrimSpace(in.Command) == "" {
		return errorf("bad input: bash takes a command, and optionally timeout_ms"), nil
	}
	timeout := bashDefaultTimeout
	if in.TimeoutMS > 0 {
		timeout = min(time.Duration(in.TimeoutMS)*time.Millisecond, bashMaxTimeout)
	}
	r, err := t.sh.Run(ctx, in.Command, timeout)
	if err != nil {
		return Result{}, err
	}
	out := r.Output
	if out == "" {
		out = "(no output)\n"
	}
	res := Result{Text: out, Ran: r.Ran, Wrote: r.Wrote}
	switch {
	case r.Stopped != "":
		res.Text, res.IsError = out+"error: stopped: "+r.Stopped+"\n", true
	case r.Exit != 0:
		res.Text, res.IsError = out+fmt.Sprintf("[exit status %d]\n", r.Exit), true
	}
	return res, nil
}
