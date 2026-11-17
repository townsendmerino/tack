package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/townsendmerino/tack/internal/config"
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

// tuiMain is `tack` or `tack -mode MODE` on a terminal. The flag is checked before anything starts: a value that is not a
// mode, or an argument the screen does not take, is a usage error, and so is having no terminal.
func tuiMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "", "the mode to start in: plan, ask, edits or auto (default: [session] mode in config.toml, else plan)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: tack [-mode plan|ask|edits|auto]")
		return exitUsage
	}
	given := false
	fs.Visit(func(*flag.Flag) { given = true })
	if given && !config.ValidMode(*mode) {
		fmt.Fprintf(stderr, "tack: -mode %q is not a mode (plan, ask, edits or auto)\n", *mode)
		return exitUsage
	}
	if !interactive() {
		if given {
			fmt.Fprintln(stderr, "tack: the screen needs a terminal")
			return exitUsage
		}
		usage(stderr)
		return exitUsage
	}
	return tuiCmd(*mode, stdout, stderr)
}

// startMode is the mode a session starts in: the -mode flag, else the owner's [session] mode, else plan. Both come from the owner
// (the flag, and his own config.toml); a repository's configuration is never read here, and the daemon lowers the result to a
// trusted repository's ceiling.
func startMode(flagMode string, cfg *config.Config) (proto.Mode, error) {
	m := flagMode
	if m == "" {
		m = cfg.Session.Mode
	}
	if m == "" {
		return proto.ModePlan, nil
	}
	if !config.ValidMode(m) {
		return "", fmt.Errorf("%q is not a mode (plan, ask, edits or auto)", m)
	}
	return proto.Mode(m), nil
}

// tuiCmd is the Bubble Tea screen over the daemon (DESIGN §10), starting the daemon if none runs, in the mode startMode gives. It
// answers permission questions (a, s, d), cancels a turn with Esc and completes `@` paths. A lost connection ends the screen.
func tuiCmd(flagMode string, stdout, stderr io.Writer) int {
	cfg, path, stateDir, err := loadConfig("")
	if err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	mode, err := startMode(flagMode, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitUsage
	}
	c, err := daemon.Dial(stateDir, tuiClient, spawner(path, stateDir), spawnWaitFor(stateDir, cfg.Daemon.ShutdownGrace.Duration))
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
	// The screen starts in auto only when the owner said so: by -mode or by [session] mode in his own config.toml (startMode reads
	// nothing else), so a start in auto is confirmed.
	if err := c.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: mode, Confirmed: mode == proto.ModeAuto}, &sess); err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	app.Backend = tuiBackend{c: c, sess: sess.Session}
	// The program runs before the subscribe: p.Send blocks until its loop receives, and the replay arrives on the
	// connection's reader goroutine, which must also deliver the subscribe's answer.
	ran := make(chan error, 1)
	go func() { _, err := p.Run(); ran <- err }()
	if err := c.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		p.Quit()
		<-ran
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	var lost atomic.Bool
	go func() { <-c.Done(); lost.Store(true); p.Quit() }()
	return tuiExit(<-ran, &lost, stderr)
}

// tuiExit is tack's exit code once the screen has ended with err: an error from the program, or a lost daemon
// connection (which makes Run return nil), is an error and says why; a quit by the user is exitOK.
func tuiExit(err error, lost *atomic.Bool, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintln(stderr, "tack:", err)
		return exitError
	}
	if lost.Load() {
		fmt.Fprintln(stderr, "tack: the connection to the daemon was lost; run tack again to reconnect")
		return exitError
	}
	return exitOK
}

// interactive reports whether stdin and stdout are a terminal.
func interactive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// Respond answers the open permission question that the permission.requested event at seq asked for the call id.
func (b tuiBackend) Respond(id string, seq int64, decision, scope, rule string) error {
	return b.c.Call(proto.MethodPermissionRespond, proto.PermissionRespondParams{Session: b.sess, ID: id, Seq: seq, Decision: decision, Scope: scope, Rule: rule}, nil)
}

// Cancel ends the session's running turn.
func (b tuiBackend) Cancel() error {
	return b.c.Call(proto.MethodSessionCancel, proto.SessionCancelParams{Session: b.sess}, nil)
}

// Release lifts the session's hold.
func (b tuiBackend) Release() error {
	return b.c.Call(proto.MethodSessionRelease, proto.SessionReleaseParams{Session: b.sess}, nil)
}

// Complete lists the session's workspace paths that query, the text after an @, matches.
func (b tuiBackend) Complete(query string) ([]proto.CompletionItem, error) {
	var r proto.WorkspaceCompleteResult
	if err := b.c.Call(proto.MethodWorkspaceComplete, proto.WorkspaceCompleteParams{Session: b.sess, Query: query}, &r); err != nil {
		return nil, err
	}
	return r.Items, nil
}
