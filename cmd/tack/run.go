package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/daemon"
	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server/cassette"
	"github.com/townsendmerino/tack/internal/shell"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

// Exit codes for tack run: 0 the model finished; 1 an error or a cancel; 2 bad usage; 3 a budget tripped; 4 the answer
// hit max_tokens; 5 tack is halted (tack halt); 7 the context window filled (6 is eval pilot's gated "not run"); 8 the
// --preflight command failed, before any model request; 9 one call repeated unchanged until [run] stop_repeats ended the
// turn.
const (
	exitOK         = 0
	exitError      = 1
	exitUsage      = 2
	exitBudget     = 3
	exitMaxTokens  = 4
	exitHalted     = 5
	exitWindowFull = 7
	exitPreflight  = 8
	exitRepeatLoop = 9
)

func runCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file (default ~/.config/tack/config.toml)")
	wsFlag := fs.String("workspace", "", "repository to work in (default: the git root of the current directory)")
	serverName := fs.String("server", "", "server from the config to use (default: its main)")
	jsonOut := fs.Bool("json", false, "stream every event as one JSON line on stdout")
	edit := fs.Bool("edit", false, "an edit task: auto mode in the sandbox, edit, write and bash, every turn a tool call, ended by done (macOS)")
	preflight := fs.String("preflight", "", "with --edit: run this command in the sandbox, as bash would, before the model starts; exit 8 if it fails")
	instructions := fs.String("instructions", "", "\"whole\": add the workspace's AGENTS.md, CLAUDE.md or CONTEXT.md, whole, to the question; "+
		"\"sections\": add it by section, within 15% of the window, its other sections arriving with the tool calls they bear on")
	fs.Bool("no-daemon", true, "run the core in this process (the default)")
	viaDaemon := fs.Bool("daemon", false, "ask through the daemon, starting it if none is running: plan mode, on its main server")
	record := fs.String("record", "", "development: record the model server's traffic to this cassette")
	replay := fs.String("replay", "", "development: answer every model request from this cassette instead of a server")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *instructions != "" && *instructions != "whole" && *instructions != "sections" {
		fmt.Fprintln(stderr, `tack run: --instructions takes "whole" or "sections"`)
		return exitUsage
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		fmt.Fprintln(stderr, `tack run: give one question, quoted: tack run "where is X defined?"`)
		return exitUsage
	}
	if *preflight != "" && !*edit {
		fmt.Fprintln(stderr, "tack run: --preflight is for --edit")
		return exitUsage
	}
	if *record != "" && *replay != "" {
		fmt.Fprintln(stderr, "tack run: --record and --replay exclude each other")
		return exitUsage
	}
	question := fs.Arg(0)
	if *viaDaemon {
		if *edit || *instructions != "" || *serverName != "" || *record != "" || *replay != "" {
			fmt.Fprintln(stderr, "tack run: --daemon asks the daemon's main server in plan mode; --edit, --instructions, --server, --record and --replay run in this process only")
			return exitUsage
		}
		ws, err := clientWorkspace(*wsFlag)
		if err != nil {
			fmt.Fprintln(stderr, "tack run:", err)
			return exitError
		}
		return runViaDaemon(*cfgPath, ws, question, *jsonOut, stdout, stderr)
	}

	if *cfgPath == "" {
		p, err := config.Path()
		if err != nil {
			fmt.Fprintln(stderr, "tack run:", err)
			return exitError
		}
		*cfgPath = p
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "tack run:", err)
		return exitError
	}
	name := cfg.Main
	if *serverName != "" {
		name = *serverName
	}
	stateDir := cfg.StateDir
	if stateDir == "" {
		if stateDir, err = config.StateDir(); err != nil {
			fmt.Fprintln(stderr, "tack run:", err)
			return exitError
		}
	}
	ws := *wsFlag
	if ws == "" {
		ws = gitRoot()
	}
	if h, err := halt.Check(stateDir); err != nil {
		fmt.Fprintln(stderr, "tack run: reading the HALT file:", err)
		return exitHalted
	} else if h != nil {
		fmt.Fprintf(stderr, "tack run: tack is halted (%s); run tack resume\n", halt.Path(stateDir))
		return exitHalted
	}
	st, err := store.Open(stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "tack run:", err)
		return exitError
	}

	var hc *http.Client
	var rec *cassette.Recorder
	var rep *cassette.Replayer
	switch {
	case *record != "":
		rec = cassette.NewRecorder("tack run "+fmt.Sprintf("%q", question)+" against server "+name, nil)
		hc = rec.Client()
	case *replay != "":
		c, err := cassette.Load(*replay)
		if err != nil {
			fmt.Fprintln(stderr, "tack run:", err)
			return exitError
		}
		rep = cassette.NewReplayer(c)
		hc = rep.Client()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := coreOptions(cfg, name, stateDir, st, hc, stderr)
	opts.WholeInstructions = *instructions == "whole" && !cfg.Run.NoInstructions
	opts.SectionInstructions = *instructions == "sections" && !cfg.Run.NoInstructions
	opts.Knowledge = cfg.Run.Knowledge && !cfg.Run.NoInstructions
	opts.SectionSearch = cfg.Run.SectionSearch
	mode := proto.ModePlan
	if *edit {
		mode, opts.EditTask = proto.ModeAuto, true
		opts.Edit = editKit(stateDir, filepath.Dir(*cfgPath), cfg.Sandbox.Read)
		if *preflight != "" {
			if code := runPreflight(ctx, opts.Edit, ws, *preflight, stderr); code != exitOK {
				return code
			}
		}
	}
	c := core.New(ctx, opts)
	defer c.Close()
	go halt.Watch(ctx, stateDir, halt.Interval, c.Halt, c.Resume)

	var code int
	if k := cfg.Run.BestOf; k > 1 && *edit {
		if why := greedy(opts.Sampling); why != "" {
			fmt.Fprintf(stderr, "tack run: best_of needs sampling: at %s every attempt would be the same\n", why)
			return exitUsage
		}
		code = bestOf(ctx, k, ws, opts.Edit, func(_ int, out io.Writer) int {
			return drive(ctx, c, ws, question, mode, *jsonOut, out, stderr)
		}, stdout, stderr)
	} else {
		code = drive(ctx, c, ws, question, mode, *jsonOut, stdout, stderr)
	}

	if rec != nil {
		if err := rec.C.Save(*record); err != nil {
			fmt.Fprintln(stderr, "tack run: saving the cassette:", err)
			return exitError
		}
		fmt.Fprintf(stderr, "tack run: recorded %d interactions to %s\n", len(rec.C.Interactions), *record)
	}
	if rep != nil {
		if err := rep.Done(); err != nil {
			fmt.Fprintln(stderr, "tack run: replay:", err)
			return exitError
		}
	}
	return code
}

// runPreflight runs command in the workspace through an edit kit's sandboxed shell, made as a session's is, before any
// model request. A failure is the setup's, not the model's: it prints the reason on one line starting
// "tack run: preflight" and returns exitPreflight.
func runPreflight(ctx context.Context, edit func(string, sandbox.Target) (core.EditKit, error), ws, command string, stderr io.Writer) int {
	fail := func(why string) int {
		fmt.Fprintf(stderr, "tack run: preflight %q failed: %s\n", command, strings.Join(strings.Fields(why), " "))
		return exitPreflight
	}
	w, err := tools.NewWorkspace(ws)
	if err != nil {
		return fail(err.Error())
	}
	kit, err := edit(w.Root(), sandbox.Target{Mark: sandbox.NewMark("p"), Since: time.Now()})
	if err != nil {
		return fail(err.Error())
	}
	if kit.Close != nil {
		defer kit.Close()
	}
	res, err := kit.Shell.Run(shell.WithApproval(ctx, perms.ByMode), command, time.Minute)
	switch {
	case err != nil:
		return fail(err.Error())
	case res.Stopped != "":
		return fail(res.Stopped + ": " + res.Output)
	case res.Exit != 0:
		return fail(fmt.Sprintf("exit %d: %s", res.Exit, res.Output))
	}
	fmt.Fprintf(stderr, "tack run: preflight %q: ok: %s\n", command, strings.TrimSpace(res.Output))
	return exitOK
}

// drive runs one turn through the protocol, against the Core over an in-memory pipe (--no-daemon, DESIGN §9.1).
func drive(ctx context.Context, c *core.Core, ws, question string, mode proto.Mode, jsonOut bool, stdout, stderr io.Writer) int {
	a, b := net.Pipe()
	defer a.Close()
	go func() { _ = c.Serve(ctx, b); b.Close() }()
	cl := core.NewClient(a)
	if err := cl.Call(proto.MethodInitialize, proto.InitializeParams{Client: proto.ClientInfo{Name: "tack-cli", Version: version},
		Protocol: proto.Version}, nil); err != nil {
		fmt.Fprintln(stderr, "tack run:", err)
		return exitError
	}
	return ask(ctx, cl, ws, question, mode, jsonOut, false, stdout, stderr, nil)
}

// runViaDaemon asks question through the daemon (tack run --daemon), starting it if none is running.
func runViaDaemon(cfgPath, ws, question string, jsonOut bool, stdout, stderr io.Writer) int {
	cfg, path, stateDir, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "tack run:", err)
		return exitError
	}
	c, err := daemon.Dial(stateDir, cliClient, spawner(path, stateDir), spawnWaitFor(stateDir, cfg.Daemon.ShutdownGrace.Duration))
	if err != nil {
		fmt.Fprintln(stderr, "tack run:", err)
		return exitError
	}
	defer func() { c.Close() }() // the connection in use at the end: redial replaces it
	if c.Warning != "" {
		fmt.Fprintln(stderr, "tack:", c.Warning)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	redial := func() (*core.Client, error) { // the daemon dropped this client, or went: connect as at the start
		next, err := daemon.Dial(stateDir, cliClient, spawner(path, stateDir), spawnWaitFor(stateDir, cfg.Daemon.ShutdownGrace.Duration))
		if err != nil {
			return nil, err
		}
		if note := newDaemonNote(c.PID, next.PID); note != "" {
			fmt.Fprintln(stderr, note)
		}
		c.Close()
		c = next
		return next.Client, nil
	}
	return ask(ctx, c.Client, ws, question, proto.ModePlan, jsonOut, true, stdout, stderr, redial)
}

// askRedials is how many times in a row ask connects again to a daemon that closed its connection mid-turn: a
// connection that carried events starts the count again.
const askRedials = 3

// ask runs one turn on an initialized connection: a new session in ws, subscribed, prompted with question, until its
// turn.end, whose stop reason is the exit code. When ctx ends, an in-process turn is cancelled with it and ask waits
// for its end; a detached one (the daemon's) runs on, and ask leaves it.
//
// redial, when not nil, connects again: a connection that closes before the turn ends (the daemon dropped a client that
// stopped reading, a terminal paused with Ctrl-S) is replaced, up to askRedials times, and the session subscribed again
// from the event after the last one heard, so the replay gives what was missed. Streamed text (assistant.delta, never
// persisted) sent during the gap is not replayed. Without redial a lost connection ends ask with an error.
func ask(ctx context.Context, cl *core.Client, ws, question string, mode proto.Mode, jsonOut, detached bool, stdout, stderr io.Writer,
	redial func() (*core.Client, error)) int {
	var mu sync.Mutex
	var last int64 // the last persisted seq heard, under mu
	heard := false // an event arrived since the last redial, under mu: the redials count again from 0
	done := make(chan string, 1)
	var answer strings.Builder
	noRewind := map[string]bool{}
	onEvent := func(p proto.SessionEventParams) {
		mu.Lock()
		defer mu.Unlock()
		ev := p.Event
		if ev.Seq > last { // progress is a persisted event not heard before; live-only (seq 0) chatter is not
			last, heard = ev.Seq, true
		}
		if jsonOut {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(stdout, "%s\n", b)
		}
		v, err := ev.Decode()
		if err != nil {
			return
		}
		switch e := v.(type) {
		case proto.AssistantDelta:
			if e.Kind == "text" && !jsonOut {
				fmt.Fprint(stdout, e.Text)
				answer.WriteString(e.Text)
			}
		case proto.AssistantMessage:
			if jsonOut {
				break
			}
			// The deltas may not have carried all of it: text streamed while the connection was gone is never replayed.
			var full strings.Builder
			for _, b := range e.Content {
				if b.Type == "text" {
					full.WriteString(b.Text)
				}
			}
			if rest, ok := strings.CutPrefix(full.String(), answer.String()); ok && rest != "" {
				fmt.Fprint(stdout, rest)
				answer.WriteString(rest)
			}
			if answer.Len() > 0 && !strings.HasSuffix(answer.String(), "\n") {
				fmt.Fprintln(stdout)
			}
			answer.Reset()
		case proto.ToolCall:
			fmt.Fprintf(stderr, "· %s %s\n", e.Name, e.Input)
		case proto.ToolFinished:
			fmt.Fprintf(stderr, "  %s, %d lines\n", e.Outcome, strings.Count(e.Result, "\n"))
		case proto.Checkpoint:
			warnNoRewind(stderr, noRewind, e.Unavailable)
		case proto.ModelStatus:
			if e.State == "loading" && e.Message != "" {
				fmt.Fprintf(stderr, "tack: %s\n", e.Message)
			}
		case proto.ErrorEvent:
			fmt.Fprintf(stderr, "tack: %s error: %s\n", e.Class, e.Message)
		case proto.DaemonHalted:
			fmt.Fprintf(stderr, "tack: halted (%s) %s\n", e.Trigger, e.Reason)
		case proto.BudgetExceeded:
			fmt.Fprintf(stderr, "tack: budget exceeded: %s (limit %g)\n", e.Budget, e.Limit)
		case proto.TurnEnd:
			select {
			case done <- e.StopReason:
			default:
			}
		}
	}
	cl.OnEvent = onEvent

	var sess proto.SessionNewResult
	if err := cl.Call(proto.MethodSessionNew, proto.SessionNewParams{Workspace: ws, Model: "main", Mode: mode}, &sess); err != nil {
		fmt.Fprintln(stderr, "tack run:", err)
		return exitError
	}
	if err := cl.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session}, nil); err != nil {
		fmt.Fprintln(stderr, "tack run:", err)
		return exitError
	}
	if err := cl.Call(proto.MethodSessionPrompt, proto.SessionPromptParams{Session: sess.Session,
		Content: []proto.ContentBlock{proto.Text(question)}}, nil); err != nil {
		fmt.Fprintln(stderr, "tack run:", err)
		if pe, ok := errors.AsType[*proto.Error](err); ok && pe.Code == proto.CodeHalted {
			return exitHalted
		}
		return exitError
	}
	var reason string
	for redials := 0; reason == ""; {
		select {
		case reason = <-done:
			continue
		case <-ctx.Done():
			if detached {
				fmt.Fprintf(stderr, "tack run: left session %s running in the daemon\n", sess.Session)
				return exitError
			}
			reason = <-done // the turn records its cancel and ends
			continue
		case <-cl.Done():
		}
		select {
		case reason = <-done: // the turn.end came before the connection closed
			continue
		default:
		}
		mu.Lock()
		if heard {
			redials = 0 // the last connection carried events: only drops in a row count
		}
		heard = false
		mu.Unlock()
		if redial == nil || redials == askRedials {
			fmt.Fprintf(stderr, "tack run: the connection to the daemon closed before session %s's turn ended\n", sess.Session)
			return exitError
		}
		redials++
		next, err := redial()
		if err != nil {
			fmt.Fprintf(stderr, "tack run: the connection to the daemon closed before session %s's turn ended, and connecting again failed: %v\n", sess.Session, err)
			return exitError
		}
		next.OnEvent = onEvent
		mu.Lock()
		from := last + 1
		mu.Unlock()
		if err := next.Call(proto.MethodSessionSubscribe, proto.SessionSubscribeParams{Session: sess.Session, FromSeq: from}, nil); err != nil {
			fmt.Fprintf(stderr, "tack run: subscribing again to session %s: %v\n", sess.Session, err)
			return exitError
		}
		fmt.Fprintf(stderr, "tack run: connected to the daemon again; session %s goes on from seq %d\n", sess.Session, from)
		cl = next
	}
	switch reason {
	case proto.StopEndTurn, proto.StopDone:
		return exitOK
	case proto.StopBudget:
		return exitBudget
	case proto.StopMaxTokens:
		return exitMaxTokens
	case proto.StopWindowFull:
		return exitWindowFull
	case proto.StopRepeatLoop:
		return exitRepeatLoop
	case proto.StopHalted:
		return exitHalted
	}
	return exitError
}

// gitRoot is the current directory's git root, or the current directory.
func gitRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	wd, _ := os.Getwd()
	return filepath.Clean(wd)
}

// clientWorkspace is the workspace a --daemon run asks for: --workspace made absolute in this process's directory, or
// the git root of it. The daemon runs in another directory, where a relative path would name something else.
func clientWorkspace(flag string) (string, error) {
	if flag == "" {
		return gitRoot(), nil
	}
	return filepath.Abs(flag)
}

// newDaemonNote is what tack run says when a redial reached a daemon other than the one it lost (by PID), "" when it is
// the same one or either PID is unknown. The old daemon's turn ended with it: the new one's recovery closes it.
func newDaemonNote(oldPID, newPID int) string {
	if oldPID == 0 || newPID == 0 || oldPID == newPID {
		return ""
	}
	return fmt.Sprintf("tack run: a new daemon (pid %d) has taken over from pid %d; the turn the old one was running ended with it", newPID, oldPID)
}
