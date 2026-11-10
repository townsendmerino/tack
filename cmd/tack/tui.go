package main

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/townsendmerino/tack/internal/daemon"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tui"
)

// tuiBackend sends the composer's text to one session as a prompt.
type tuiBackend struct {
	c    *daemon.Conn
	sess proto.SessionID
}

func (b tuiBackend) Prompt(text string) error {
	return b.c.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: b.sess, Content: []proto.ContentBlock{proto.Text(text)}}, nil)
}

// tuiCmd is `tack` with no arguments on a terminal: a Bubble Tea screen over the daemon (DESIGN §10), starting the daemon
// if none runs. It asks in plan mode, as `tack run --daemon` does, because approvals, cancel and completion wait on their
// protocol methods; a lost connection ends the screen.
func tuiCmd(stdout, stderr io.Writer) int {
	cfg, path, stateDir, err := loadConfig("")
	if err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	c, err := daemon.Dial(stateDir, cliClient, spawner(path, stateDir), spawnWaitFor(stateDir, cfg.Daemon.ShutdownGrace.Duration))
	if err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	defer c.Close()
	ws, err := clientWorkspace("")
	if err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	app := &tui.App{}
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithOutput(stdout))
	c.OnEvent = func(e proto.SessionEventParams) { p.Send(tui.EventMsg(e.Event)) }
	var sess proto.SessionNewResult
	if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: proto.ModePlan}, &sess); err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	app.Backend = tuiBackend{c: c, sess: sess.Session}
	if err := c.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	go func() { <-c.Done(); p.Quit() }()
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	return exitOK
}

// interactive reports whether stdin and stdout are a terminal.
func interactive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}
