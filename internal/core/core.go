// Package core is the daemon as a library (DESIGN §4.1): it hosts sessions and speaks the protocol (internal/proto)
// over any newline-delimited stream. The daemon serves it on a Unix socket; --no-daemon serves it in-process over a
// pipe (DESIGN §9.1).
package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/sandbox"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
)

// Options configures a Core.
type Options struct {
	Store   *store.Store
	Version string
	// Servers names the configured model servers, for initialize's capabilities ("goinfer@nobara").
	Servers []string
	// Source returns the model source for a session's model: a role ("main") or a server name. The Core calls it once
	// per name and keeps the result.
	Source func(model string) (models.Source, error)
	// OpenSearcher, when set, opens ken's engine over a workspace root, giving its sessions the search tool (DESIGN
	// §12). The Core opens one per root, shares it between that root's sessions, and closes it in Close when it is an
	// io.Closer.
	OpenSearcher func(ctx context.Context, root string) (tools.Searcher, error)
	Budget       loop.Budget
	// Sampling is every session's sampling (the configured server's).
	Sampling server.Sampling
	// Each session gets its own sweep target, a random sandbox mark and its start time. Halt sweeps every session's
	// target with Sweep (internal/sandbox.Sweep on macOS; nil where commands cannot run). HaltDir, when set, is the
	// state directory where the targets are registered for tack halt (internal/halt.Register).
	Sweep   func(t sandbox.Target, dryRun bool) (sandbox.Report, error)
	HaltDir string
	// Edit makes a session in auto mode able to change its workspace: it returns the session's sandboxed shell and the
	// permission engine that decides each call (cmd/tack: macOS only). Without it, a session asking for auto mode is
	// refused, and every session is read-only.
	Edit func(root string, group sandbox.Target) (EditKit, error)
	// EditTask makes every auto-mode session an edit task (loop.Config.EditTask): tool calls required, ended by done.
	EditTask bool
	// FitWindow caps every session's tool results at the room its window has left (loop.Config.FitWindow).
	FitWindow bool
	// StopRepeats ends a session's turn when one call repeats unchanged (loop.Config.StopRepeats).
	StopRepeats bool
	// ResultNotes adds the loop's notes to tool results (loop.Config.ResultNotes).
	ResultNotes bool
	// EditMessages makes a failed edit say where the text or the file is instead (tools.Options.EditMessages).
	EditMessages bool
	// WholeInstructions puts the workspace's instruction file, the first of instructionFiles at its root, whole at the
	// end of the session's first message. tack's design loads them by section (DESIGN §7.4, K9); this is the stopgap
	// until that is built, matching what opencode loads.
	WholeInstructions bool
}

// EditKit is what an auto-mode session changes its workspace with.
type EditKit struct {
	Shell  tools.Shell
	Engine *perms.Engine
	// Close releases what the kit holds (the sandbox's temporary directory); Core.Close calls it. nil holds nothing.
	Close func() error
}

// instructionFiles are the instruction files WholeInstructions looks for at the workspace root, in opencode 1.19.0's
// order (internal/eval/pilot.instructionFiles); the first found is used.
var instructionFiles = []string{"AGENTS.md", "CLAUDE.md", "CONTEXT.md"}

// Core hosts sessions. It is safe for concurrent use by any number of connections.
type Core struct {
	opt Options
	ctx context.Context // turns run under it; cancelling it cancels every running turn

	mu        sync.Mutex
	sessions  map[proto.SessionID]*session
	sources   map[string]models.Source
	searchers map[string]tools.Searcher // by workspace root
	halted    *proto.DaemonHalted       // non-nil while tack is halted: no new turns
}

type session struct {
	mu      sync.Mutex
	id      proto.SessionID
	log     *store.Log
	loop    *loop.Session
	running bool
	cancel  context.CancelCauseFunc // the running turn's
	subs    map[*subscriber]bool
	target  sandbox.Target // what Halt sweeps for this session
	kit     func() error   // the edit kit's Close, or nil
	// instructions is WholeInstructions' text, added to the first prompt; prompted says it has been.
	instructions string
	prompted     bool
}

// subscriber is one connection's subscription to one session. last is the highest persisted seq it has been sent, so
// an event that is both replayed and then emitted live is delivered once.
type subscriber struct {
	conn *conn
	last int64
}

// New returns a Core whose turns run under ctx.
func New(ctx context.Context, opt Options) *Core {
	return &Core{opt: opt, ctx: ctx, sessions: map[proto.SessionID]*session{}, sources: map[string]models.Source{},
		searchers: map[string]tools.Searcher{}}
}

// Close releases the model sources (a supervised child is stopped) and each session's edit kit.
func (c *Core) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for _, s := range c.sources {
		errs = append(errs, s.Close())
	}
	for _, s := range c.searchers {
		if cl, ok := s.(io.Closer); ok {
			errs = append(errs, cl.Close())
		}
	}
	for _, s := range c.sessions {
		if s.kit != nil {
			errs = append(errs, s.kit())
		}
	}
	if c.opt.HaltDir != "" {
		for id := range c.sessions {
			errs = append(errs, halt.Unregister(c.opt.HaltDir, string(id)))
		}
	}
	return errors.Join(errs...)
}

// conn is one client connection.
type conn struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (cn *conn) send(v any) error {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	return cn.enc.Encode(v) // Encode writes one line: the protocol's framing
}

// Serve speaks the protocol on rw until it closes or ctx ends. Requests are handled in order.
func (c *Core) Serve(ctx context.Context, rw io.ReadWriter) error {
	cn := &conn{enc: json.NewEncoder(rw)}
	sc := bufio.NewScanner(rw)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	defer c.unsubscribeAll(cn)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var req proto.Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = cn.send(proto.Response{JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &proto.Error{Code: proto.CodeParseError, Message: err.Error()}})
			continue
		}
		if len(req.ID) == 0 {
			continue // P0 defines no client notifications
		}
		result, perr := c.dispatch(cn, req)
		resp := proto.Response{JSONRPC: "2.0", ID: req.ID}
		if perr != nil {
			resp.Error = perr
		} else if resp.Result, perr = marshal(result); perr != nil {
			resp.Error = perr
		}
		if err := cn.send(resp); err != nil {
			return err
		}
		if req.Method == proto.MethodSessionSubscribe && perr == nil {
			c.replay(cn, req)
		}
	}
	return sc.Err()
}

func marshal(v any) (json.RawMessage, *proto.Error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	return b, nil
}

func params[T any](req proto.Request) (T, *proto.Error) {
	var p T
	dec := json.NewDecoder(strings.NewReader(string(req.Params)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, &proto.Error{Code: proto.CodeInvalidParams, Message: fmt.Sprintf("%s: %v", req.Method, err)}
	}
	return p, nil
}

func (c *Core) dispatch(cn *conn, req proto.Request) (any, *proto.Error) {
	switch req.Method {
	case proto.MethodInitialize:
		p, perr := params[proto.InitializeParams](req)
		if perr != nil {
			return nil, perr
		}
		if p.Protocol != proto.Version {
			return nil, &proto.Error{Code: proto.CodeInvalidParams,
				Message: fmt.Sprintf("protocol %d is not supported; this daemon speaks %d", p.Protocol, proto.Version)}
		}
		ken := ""
		if c.opt.OpenSearcher != nil {
			ken = "engine"
		}
		return proto.InitializeResult{Daemon: proto.DaemonInfo{Version: c.opt.Version}, Protocol: proto.Version,
			Capabilities: proto.Capabilities{Ken: ken, Servers: c.opt.Servers}}, nil
	case proto.MethodSessionNew:
		p, perr := params[proto.SessionNewParams](req)
		if perr != nil {
			return nil, perr
		}
		id, err := c.newSession(p)
		if err != nil {
			return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: err.Error()}
		}
		return proto.SessionNewResult{Session: id}, nil
	case proto.MethodSessionSubscribe:
		p, perr := params[proto.SessionSubscribeParams](req)
		if perr != nil {
			return nil, perr
		}
		s, perr := c.session(p.Session)
		if perr != nil {
			return nil, perr
		}
		return proto.SessionSubscribeResult{Head: s.log.Head()}, nil
	case proto.MethodSessionPrompt:
		p, perr := params[proto.SessionPromptParams](req)
		if perr != nil {
			return nil, perr
		}
		return c.prompt(p)
	}
	return nil, &proto.Error{Code: proto.CodeMethodNotFound, Message: "method not found: " + req.Method}
}

func (c *Core) session(id proto.SessionID) (*session, *proto.Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sessions[id]
	if !ok {
		return nil, &proto.Error{Code: proto.CodeUnknownSession, Message: "unknown session " + string(id)}
	}
	return s, nil
}

func (c *Core) source(model string) (models.Source, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sources[model]; ok {
		return s, nil
	}
	s, err := c.opt.Source(model)
	if err != nil {
		return nil, err
	}
	c.sources[model] = s
	return s, nil
}

func (c *Core) newSession(p proto.SessionNewParams) (proto.SessionID, error) {
	switch p.Mode {
	case proto.ModeAsk, proto.ModePlan, proto.ModeEdits, proto.ModeAuto:
	default:
		return "", fmt.Errorf("mode %q", p.Mode)
	}
	root := p.Workspace
	if rest, ok := strings.CutPrefix(root, "~/"); ok {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, rest)
	}
	ws, err := tools.NewWorkspace(root)
	if err != nil {
		return "", err
	}
	src, err := c.source(p.Model)
	if err != nil {
		return "", err
	}
	searcher, err := c.searcher(ws.Root())
	if err != nil {
		return "", fmt.Errorf("ken: %w", err)
	}
	id := store.NewSessionID(time.Now())
	log, err := c.opt.Store.Create(id)
	if err != nil {
		return "", err
	}
	s := &session{id: id, log: log, subs: map[*subscriber]bool{}, target: sandbox.Target{Mark: sandbox.NewMark("s"), Since: time.Now()}}
	if c.opt.HaltDir != "" {
		if err := halt.Register(c.opt.HaltDir, string(id), s.target); err != nil {
			return "", err
		}
	}
	cfg := loop.Config{Source: src, Tools: tools.NewSet(searcher), WS: ws, Log: log,
		Blobs: c.opt.Store.Blobs, Budget: c.opt.Budget, Sampling: c.opt.Sampling, Emit: s.emit, FitWindow: c.opt.FitWindow, StopRepeats: c.opt.StopRepeats, ResultNotes: c.opt.ResultNotes}
	if p.Mode == proto.ModeAuto {
		if c.opt.Edit == nil {
			c.unregister(id)
			return "", errors.New("auto mode changes the workspace, and this tack has no sandbox to run its commands in")
		}
		kit, err := c.opt.Edit(ws.Root(), s.target)
		if err != nil {
			c.unregister(id)
			return "", fmt.Errorf("auto mode: %w", err)
		}
		cfg.Tools = tools.New(tools.Options{Searcher: searcher, Write: true, Shell: kit.Shell, Done: c.opt.EditTask,
			EditMessages: c.opt.EditMessages})
		cfg.Perms, cfg.Mode, cfg.EditTask = kit.Engine, p.Mode, c.opt.EditTask
		s.kit = kit.Close
		root := ws.Root()
		cfg.Changed = func() (bool, error) { return workspaceChanged(root) }
	}
	if c.opt.WholeInstructions {
		s.instructions = readInstructions(ws.Root())
	}
	s.loop = loop.New(cfg)
	c.mu.Lock()
	c.sessions[id] = s
	c.mu.Unlock()
	return id, nil
}

// emit fans an event out to the session's subscribers.
func (s *session) emit(ev proto.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.subs {
		if ev.Seq != 0 {
			if ev.Seq <= sub.last {
				continue
			}
			sub.last = ev.Seq
		}
		_ = sub.conn.send(notification(s.id, ev))
	}
}

func notification(id proto.SessionID, ev proto.Event) proto.Request {
	p, _ := json.Marshal(proto.SessionEventParams{Session: id, Event: ev})
	return proto.Request{JSONRPC: "2.0", Method: proto.MethodSessionEvent, Params: p}
}

// replay sends a new subscriber the persisted events from from_seq, then registers it for live ones. Both happen under
// the session's lock, so no event falls between them; one appended during the replay is skipped live by seq.
func (c *Core) replay(cn *conn, req proto.Request) {
	p, _ := params[proto.SessionSubscribeParams](req)
	s, perr := c.session(p.Session)
	if perr != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := &subscriber{conn: cn}
	evs, _ := c.opt.Store.Events(s.id, p.FromSeq)
	for _, ev := range evs {
		_ = cn.send(notification(s.id, ev))
		sub.last = ev.Seq
	}
	s.subs[sub] = true
}

func (c *Core) unsubscribeAll(cn *conn) {
	c.mu.Lock()
	ss := make([]*session, 0, len(c.sessions))
	for _, s := range c.sessions {
		ss = append(ss, s)
	}
	c.mu.Unlock()
	for _, s := range ss {
		s.mu.Lock()
		for sub := range s.subs {
			if sub.conn == cn {
				delete(s.subs, sub)
			}
		}
		s.mu.Unlock()
	}
}

func (c *Core) prompt(p proto.SessionPromptParams) (any, *proto.Error) {
	s, perr := c.session(p.Session)
	if perr != nil {
		return nil, perr
	}
	var text strings.Builder
	for _, b := range p.Content {
		if b.Type != "text" {
			return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: "content type " + b.Type + " is not supported yet"}
		}
		text.WriteString(b.Text)
	}
	// c.mu is held from the halt check until the turn's cancel is in place, so a Halt cannot fall between them.
	c.mu.Lock()
	if h := c.halted; h != nil {
		c.mu.Unlock()
		return nil, &proto.Error{Code: proto.CodeHalted, Message: (&halt.Halted{DaemonHalted: *h}).Error() + "; run tack resume"}
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		c.mu.Unlock()
		return nil, &proto.Error{Code: proto.CodeBusy, Message: "a turn is already running in " + string(s.id)}
	}
	ctx, cancel := context.WithCancelCause(c.ctx)
	s.running, s.cancel = true, cancel
	// The instructions join the first message itself, so the log holds exactly what the model was sent.
	if s.instructions != "" && !s.prompted {
		text.WriteString("\n\n" + s.instructions)
	}
	s.prompted = true
	s.mu.Unlock()
	c.mu.Unlock()
	seq, err := s.loop.Start(text.String())
	if err != nil {
		cancel(nil)
		s.mu.Lock()
		s.running, s.cancel = false, nil
		s.mu.Unlock()
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	go func() {
		_, _ = s.loop.Run(ctx)
		cancel(nil)
		s.mu.Lock()
		s.running, s.cancel = false, nil
		s.mu.Unlock()
	}()
	return proto.SessionPromptResult{Seq: seq}, nil
}

// searcher returns the root's search engine, opening it on first use; nil when ken is not configured.
func (c *Core) searcher(root string) (tools.Searcher, error) {
	if c.opt.OpenSearcher == nil {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.searchers[root]; ok {
		return s, nil
	}
	s, err := c.opt.OpenSearcher(c.ctx, root)
	if err != nil {
		return nil, err
	}
	c.searchers[root] = s
	return s, nil
}

// Halt cancels every running turn, each recording daemon.halted, and refuses new turns until Resume (DESIGN §6.6).
// Then it sweeps each session's sandbox for what its commands left running. A sweep that refuses kills nothing, and
// the session's log records daemon.halt_refused with the processes it listed.
func (c *Core) Halt(h proto.DaemonHalted) {
	c.mu.Lock()
	c.halted = &h
	ss := make([]*session, 0, len(c.sessions))
	for _, s := range c.sessions {
		ss = append(ss, s)
		s.mu.Lock()
		if s.cancel != nil {
			s.cancel(&halt.Halted{DaemonHalted: h})
		}
		s.mu.Unlock()
	}
	c.mu.Unlock()
	if c.opt.Sweep == nil {
		return
	}
	for _, s := range ss {
		r, err := c.opt.Sweep(s.target, false)
		if err != nil {
			r.Refused = "the sweep failed: " + err.Error()
		}
		if r.Refused == "" {
			continue
		}
		listed := r.Older
		if len(listed) == 0 {
			listed = r.Kill
		}
		ev := proto.DaemonHaltRefused{Reason: r.Refused}
		for _, p := range listed {
			ev.Processes = append(ev.Processes, proto.HaltProcess{PID: p.PID, Name: p.Name, Start: p.Start})
		}
		if e, err := s.log.Append(proto.EvDaemonHaltRefused, 0, ev); err == nil {
			s.emit(e)
		}
	}
}

// Resume lets new turns start again.
func (c *Core) Resume() {
	c.mu.Lock()
	c.halted = nil
	c.mu.Unlock()
}

// unregister drops a session's sweep target when the session is not created after all.
func (c *Core) unregister(id proto.SessionID) {
	if c.opt.HaltDir != "" {
		_ = halt.Unregister(c.opt.HaltDir, string(id))
	}
}

// workspaceChanged reports whether git sees any change in root, tracked or untracked, since its last commit: an edit
// task's done asks it (loop.Config.Changed).
func workspaceChanged(root string) (bool, error) {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// readInstructions returns the first of instructionFiles at root, whole and labelled with its name, or "".
func readInstructions(root string) string {
	for _, name := range instructionFiles {
		if b, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			return "Project instructions, from " + name + ":\n\n" + string(b)
		}
	}
	return ""
}
