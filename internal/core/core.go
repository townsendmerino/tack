// Package core is the daemon as a library (DESIGN §4.1): it hosts sessions and speaks the protocol (internal/proto)
// over any newline-delimited stream. The daemon serves it on a Unix socket; --no-daemon serves it in-process over a
// pipe (DESIGN §9.1).
package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	"github.com/townsendmerino/tack/internal/snapshot"
	"github.com/townsendmerino/tack/internal/store"
	"github.com/townsendmerino/tack/internal/tools"
	"github.com/townsendmerino/tack/internal/window"
)

// Options configures a Core.
type Options struct {
	Store   *store.Store
	Version string
	// ClientToken is the daemon's client token (internal/daemon). A connection that presents it at initialize is
	// privileged; a wrong token is refused. Empty means there is no token, and any token presented is wrong.
	ClientToken string
	// Shutdown, when set, is what a privileged daemon/shutdown asks for: the daemon drains (Drain) and exits. It is called
	// once, after the request is answered, and must not block. Without it, daemon/shutdown is refused: no daemon hosts
	// this Core.
	Shutdown func()
	// Servers names the configured model servers, for initialize's capabilities ("goinfer@nobara").
	Servers []string
	// Source returns the model source for a session's model: a role ("main") or a server name. The Core calls it once
	// per name and keeps the result.
	Source func(model string) (models.Source, error)
	// OpenSearcher, when set, opens ken's engine over a workspace root, giving its sessions the search tool, and symbol
	// and edit's symbol form when it is also a tools.Symbols (DESIGN §12, §6.5). The Core opens one per root, shares it
	// between that root's sessions, and closes it in Close when it is an io.Closer.
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
	// MaskObservations compacts a session's history past 75% of the window (loop.Config.MaskObservations).
	MaskObservations bool
	// ResultNotes adds the loop's notes to tool results (loop.Config.ResultNotes).
	ResultNotes bool
	// EditMessages makes a failed edit say where the text or the file is instead (tools.Options.EditMessages).
	EditMessages bool
	// InstructionsHead loads the budgeted head of the instruction file instead of the whole file (instructions.go).
	InstructionsHead bool
	// RefuseDuplicateEdits refuses an edit identical to one already applied to the same file this session
	// (tools.Options.RefuseDuplicateEdits).
	RefuseDuplicateEdits bool
	// VerifyDone refuses an edit task's done until a build or test has run since its last change (loop.Config.VerifyDone).
	VerifyDone bool
	// BuildAfterEdit builds the package of each .go file an auto-mode session edits or writes, in its shell, and appends the
	// errors to the result (loop.Config.BuildAfterEdit).
	BuildAfterEdit bool
	// RestateTask quotes the task in the refusal of a done with no change (loop.Config.RestateTask).
	RestateTask bool
	// EditLines gives edit its line form (tools.Options.LineEdits).
	EditLines bool
	// CurrentSymbol gives an auto-mode session's repeated edits whose text is not in the file a note with the current text
	// of the symbol aimed at (tools.Options.CurrentSymbol). Its spans come from the session's symbol index when it has
	// one, else from OpenSpans, opened at the first note; with neither, there is no note.
	CurrentSymbol bool
	// EditTolerances lets an auto-mode session's edits of .go files apply where one tolerance places old_string uniquely
	// (tools.Options.EditTolerances).
	EditTolerances bool
	// LeanResults cuts whole-file reads and grep shorter (tools.Options.LeanResults; calibration §16.1).
	LeanResults bool
	// CompactAtCliff makes the loop react when stuck (loop.Config.CompactAtCliff; calibration §16.2).
	CompactAtCliff bool
	// WidenRepeats widens a repeated whole read or grep (loop.Config.WidenRepeats).
	WidenRepeats bool
	// OpenSpans opens a symbol index over a workspace root for CurrentSymbol alone: it adds no tool. The Core opens one per
	// root on first use, shares it between that root's sessions, and closes it in Close when it is an io.Closer.
	OpenSpans func(ctx context.Context, root string) (tools.Symbols, error)
	// InjectContext puts the code ken finds for a session's first prompt into its first message, after the instruction
	// head and before the task (internal/window/inject.go; calibration §13.1). It needs OpenInject.
	InjectContext bool
	// OpenInject opens the index the injection asks, over a workspace root, at the first prompt. The Core closes it
	// after use when it is an io.Closer.
	OpenInject func(ctx context.Context, root string) (window.InjectIndex, error)
	// RepoMap puts the workspace's generated map (internal/window/repomap.go; calibration §13.3) into a session's first
	// message, after the instruction head. It needs OpenRepoMap.
	RepoMap bool
	// Snapshots takes a snapshot of the working tree before each bash call in a session that writes (DESIGN §6.8), in a shadow
	// repository under Store's root, for trees up to SnapshotMaxBytes.
	Snapshots        bool
	SnapshotMaxBytes int64
	// OpenRepoMap opens the index the map reads, over a workspace root, at the first prompt. The Core closes it after
	// use when it is an io.Closer.
	OpenRepoMap func(ctx context.Context, root string) (window.MapIndex, error)
	// WholeInstructions puts the workspace's instruction file, the first of instructionFiles at its root, whole at the
	// end of the session's first message. tack's design loads them by section (DESIGN §7.4, K9); this is the stopgap
	// until that is built, matching what opencode loads.
	WholeInstructions bool
	// SectionSearch adds ken's route to SectionInstructions: before each prompt, ken's hits for the message in the
	// instruction file bring their sections into it (window.Instructions.ForHits). It needs a searcher that is a
	// window.SectionSearcher, and does nothing for a file loaded whole.
	SectionSearch bool
	// SectionInstructions reads the workspace's instruction file by section instead (window.Instructions; DESIGN §2.3):
	// the first message carries what Initial gives, before the task, and each tool call that ran adds the sections it
	// brings in to its result. WholeInstructions wins when both are set.
	SectionInstructions bool
	// Knowledge reads the workspace's .agents/knowledge/ pages (window.Knowledge; ADR-007): the first message carries
	// their index, and a tool call that reads or edits a page's pinned file adds the page to its result, sharing
	// SectionInstructions' budget. WholeInstructions turns it off, as it does SectionInstructions. Measured runs leave
	// it off.
	Knowledge bool
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

	reopenMu sync.Mutex // held while a session is reopened from the store
	closed   bool       // under mu: Close has run; nothing is reopened or made

	mu        sync.Mutex
	sessions  map[proto.SessionID]*session
	sources   map[string]models.Source
	searchers map[string]tools.Searcher // by workspace root
	spans     map[string]*lazySpans     // by workspace root (Options.OpenSpans)
	halted    *proto.DaemonHalted       // non-nil while tack is halted: no new turns
	draining  bool                      // Drain has begun: no new turns
	started   time.Time

	shutdown sync.Once // Options.Shutdown is called once
}

type session struct {
	// wmu is held from taking the log back to appending to it, by a turn's start and by Halt, and around a turn's
	// end giving it up. So a log taken back is not given up before its append, and what another process wrote while
	// it was idle reaches subscribers before anything appended after it. Taken before mu.
	wmu     sync.Mutex
	mu      sync.Mutex
	id      proto.SessionID
	log     *store.Log
	hold    *store.Hold // this Core's claim on the session while it has it in memory; released by Close
	loop    *loop.Session
	running bool
	cancel  context.CancelCauseFunc // the running turn's
	subs    map[*subscriber]bool
	target  sandbox.Target // what Halt sweeps for this session
	kit     func() error   // the edit kit's Close, or nil
	// instructions is the instruction file's text for the first prompt (WholeInstructions, SectionInstructions);
	// prompted says it has been. notesFirst puts it before the prompt, which then follows under "Task:" (the budgeted
	// head, instructions.go; the sections); else it follows the prompt.
	instructions string
	notesFirst   bool
	// sectionSearch finds the instruction file's sections for a user message (ken's exact SearchFiles); nil without ken.
	sectionSearch window.SectionSearcher
	// sections is the instruction file's section tracker under SectionInstructions, nil otherwise. A session resumed
	// from its log after loop.Rebuild rebuilds it with Replay over the same events before its next turn.
	sections *window.Instructions
	// know is the workspace's knowledge tracker under Knowledge, nil otherwise; a reopened session rebuilds it too.
	know     *window.Knowledge
	prompted bool
	// injectRoot is the workspace the first prompt's injection is built over (Options.InjectContext); "" for none.
	injectRoot string
	// mapRoot is the workspace the first prompt's repository map is built over (Options.RepoMap); "" for none.
	mapRoot string
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
		searchers: map[string]tools.Searcher{}, spans: map[string]*lazySpans{}, started: time.Now()}
}

// Close releases the model sources (a supervised child is stopped), each session's edit kit, and each session's log, so
// another process may open it. No turn may be running. It is final: it waits for a reopen in progress, and after it
// no session is reopened or made, so none is left open behind it. Closing again is a no-op.
func (c *Core) Close() error {
	c.reopenMu.Lock()
	defer c.reopenMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	var errs []error
	for _, s := range c.sources {
		errs = append(errs, s.Close())
	}
	for _, s := range c.searchers {
		if cl, ok := s.(io.Closer); ok {
			errs = append(errs, cl.Close())
		}
	}
	for _, l := range c.spans {
		errs = append(errs, l.Close())
	}
	for _, s := range c.sessions {
		if s.kit != nil {
			errs = append(errs, s.kit())
		}
		errs = append(errs, s.log.Close(), s.hold.Release())
	}
	if c.opt.HaltDir != "" {
		for id := range c.sessions {
			errs = append(errs, halt.Unregister(c.opt.HaltDir, string(id)))
		}
	}
	c.sessions, c.sources, c.searchers, c.spans = map[proto.SessionID]*session{}, map[string]models.Source{}, map[string]tools.Searcher{}, map[string]*lazySpans{}
	return errors.Join(errs...)
}

// conn is one client connection. privileged is set by an initialize that presented the client token; requests on a
// connection are handled in order, so it needs no lock.
type conn struct {
	mu         sync.Mutex
	enc        *json.Encoder
	privileged bool
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
		if req.Method == proto.MethodDaemonShutdown && perr == nil {
			c.shutdown.Do(func() { go c.opt.Shutdown() }) // answered first: the shutdown closes this connection
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
		cn.privileged = false // a re-initialize starts over, and one that fails for any reason leaves none
		p, perr := params[proto.InitializeParams](req)
		if perr != nil {
			return nil, perr
		}
		version, ok := proto.Negotiate(p.Protocol, proto.Version)
		if !ok {
			return nil, &proto.Error{Code: proto.CodeInvalidParams,
				Message: fmt.Sprintf("protocol %d is not supported; this daemon speaks %s", p.Protocol, spoken())}
		}
		if p.Token != "" {
			if c.opt.ClientToken == "" || subtle.ConstantTimeCompare([]byte(p.Token), []byte(c.opt.ClientToken)) != 1 {
				return nil, &proto.Error{Code: proto.CodeUnauthorized, Message: "initialize: the client token is wrong; read it again from client.token"}
			}
			cn.privileged = true
		}
		ken := ""
		if c.opt.OpenSearcher != nil {
			ken = "engine"
		}
		return proto.InitializeResult{Daemon: proto.DaemonInfo{Version: c.opt.Version}, Protocol: version,
			Capabilities: proto.Capabilities{Ken: ken, Servers: c.opt.Servers}, Privileged: cn.privileged}, nil
	case proto.MethodSessionNew:
		p, perr := params[proto.SessionNewParams](req)
		if perr != nil {
			return nil, perr
		}
		id, err := c.newSession(p)
		var pe *proto.Error
		switch {
		case errors.As(err, &pe):
			return nil, pe // errClosed
		case err != nil:
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
	case proto.MethodDaemonStatus:
		if _, perr := params[proto.DaemonStatusParams](req); perr != nil {
			return nil, perr
		}
		return c.status(), nil
	case proto.MethodDaemonShutdown:
		if _, perr := params[proto.DaemonShutdownParams](req); perr != nil {
			return nil, perr
		}
		if !cn.privileged {
			return nil, unprivileged(req.Method)
		}
		if c.opt.Shutdown == nil {
			return nil, &proto.Error{Code: proto.CodeInvalidRequest, Message: "daemon/shutdown: no daemon hosts this session core (tack run without --daemon)"}
		}
		return proto.DaemonShutdownResult{}, nil
	}
	return nil, &proto.Error{Code: proto.CodeMethodNotFound, Message: "method not found: " + req.Method}
}

// session returns the session named id, reopening it from the store when this Core does not hold it (reopen.go).
func (c *Core) session(id proto.SessionID) (*session, *proto.Error) {
	c.mu.Lock()
	s, ok := c.sessions[id]
	c.mu.Unlock()
	if ok {
		return s, nil
	}
	c.reopenMu.Lock()
	defer c.reopenMu.Unlock()
	c.mu.Lock()
	s, ok = c.sessions[id]
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errClosed
	}
	if ok {
		return s, nil // another connection reopened it while this one waited
	}
	s, perr := c.reopen(id)
	if perr != nil {
		return nil, perr
	}
	c.mu.Lock()
	c.sessions[id] = s
	c.mu.Unlock()
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
	// Held throughout, as a reopen holds it, so a Close waits for the session being made and closes it with the rest.
	c.reopenMu.Lock()
	defer c.reopenMu.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return "", errClosed
	}
	if madeHook != nil {
		madeHook()
	}
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
	// The source and ken are opened before the log, so a session that cannot run leaves no log behind; open finds them
	// cached.
	if _, err := c.source(p.Model); err != nil {
		return "", err
	}
	if _, err := c.searcher(ws.Root()); err != nil {
		return "", fmt.Errorf("ken: %w", err)
	}
	id := store.NewSessionID(time.Now())
	log, hold, err := c.opt.Store.CreateHeld(id)
	if err != nil {
		return "", err
	}
	discard := func() {
		c.opt.Store.Discard(log) // a session that could not be made leaves no log
		hold.Release()
	}
	created := proto.SessionCreated{Workspace: ws.Root(), Model: p.Model, Mode: p.Mode}
	if _, err := log.Append(proto.EvSessionCreated, 0, created); err != nil {
		discard()
		return "", err
	}
	s, err := c.open(id, log, ws, created, nil)
	if err != nil {
		discard()
		return "", err
	}
	s.hold = hold
	c.mu.Lock() // Close waits for the reopen lock this holds, so it has not run
	c.sessions[id] = s
	c.mu.Unlock()
	_ = s.log.Idle() // until its first prompt: another process may write it meanwhile
	return id, nil
}

// open makes the session over its log, as session/new does and a reopen does: the model source, ken, the tools, the
// edit kit in auto mode, the sweep target, and the loop. history is a reopened session's events, nil for a new one;
// a session whose log has a user.message has been prompted, so the first prompt's additions are not made again.
func (c *Core) open(id proto.SessionID, log *store.Log, ws *tools.Workspace, created proto.SessionCreated, history []proto.Event) (*session, error) {
	src, err := c.source(created.Model)
	if err != nil {
		return nil, err
	}
	searcher, err := c.searcher(ws.Root())
	if err != nil {
		return nil, fmt.Errorf("ken: %w", err)
	}
	prompted := slices.ContainsFunc(history, func(ev proto.Event) bool { return ev.Type == proto.EvUserMessage })
	s := &session{id: id, log: log, subs: map[*subscriber]bool{}, target: sandbox.Target{Mark: sandbox.NewMark("s"), Since: time.Now()},
		prompted: prompted}
	if c.opt.HaltDir != "" {
		if err := halt.Register(c.opt.HaltDir, string(id), s.target); err != nil {
			return nil, err
		}
	}
	if c.opt.InjectContext && c.opt.OpenInject != nil && !prompted {
		s.injectRoot = ws.Root()
	}
	if c.opt.RepoMap && c.opt.OpenRepoMap != nil && !prompted {
		s.mapRoot = ws.Root()
	}
	symbols, _ := searcher.(tools.Symbols)
	ro := tools.Options{Searcher: searcher, Symbols: symbols, LeanResults: c.opt.LeanResults}
	cfg := loop.Config{Source: src, Tools: tools.New(ro), ReadLines: ro.WholeReadLines(), WS: ws, Log: log,
		Blobs: c.opt.Store.Blobs, Budget: c.opt.Budget, Sampling: c.opt.Sampling, Emit: s.emit, FitWindow: c.opt.FitWindow, StopRepeats: c.opt.StopRepeats, MaskObservations: c.opt.MaskObservations, CompactAtCliff: c.opt.CompactAtCliff, WidenRepeats: c.opt.WidenRepeats, ResultNotes: c.opt.ResultNotes,
		VerifyDone: c.opt.VerifyDone, RestateTask: c.opt.RestateTask}
	if created.Mode == proto.ModeAuto {
		if c.opt.Edit == nil {
			c.unregister(id)
			return nil, errors.New("auto mode changes the workspace, and this tack has no sandbox to run its commands in")
		}
		kit, err := c.opt.Edit(ws.Root(), s.target)
		if err != nil {
			c.unregister(id)
			return nil, fmt.Errorf("auto mode: %w", err)
		}
		cfg.Tools = tools.New(tools.Options{Searcher: searcher, Symbols: symbols, Write: true, Shell: kit.Shell, Done: c.opt.EditTask,
			EditMessages: c.opt.EditMessages, RefuseDuplicateEdits: c.opt.RefuseDuplicateEdits, LineEdits: c.opt.EditLines,
			CurrentSymbol: c.currentSymbol(ws.Root(), symbols), EditTolerances: c.opt.EditTolerances, LeanResults: c.opt.LeanResults})
		cfg.Perms, cfg.Mode, cfg.EditTask = kit.Engine, created.Mode, c.opt.EditTask
		cfg.BuildAfterEdit, cfg.Shell = c.opt.BuildAfterEdit, kit.Shell
		s.kit = kit.Close
		root := ws.Root()
		cfg.Changed = func() (bool, error) { return workspaceChanged(root) }
		if c.opt.Snapshots {
			cfg.Snapshots = snapshot.New(c.opt.Store.Root(), root, c.opt.SnapshotMaxBytes)
		}
	}
	switch {
	case prompted: // the instructions joined the first prompt, which the log holds
	case c.opt.WholeInstructions && c.opt.InstructionsHead:
		if name, content := readInstructionFile(ws.Root()); name != "" {
			s.instructions, s.notesFirst = instructionHead(name, content, servedWindow(c.ctx, src)), true
		}
	case c.opt.WholeInstructions:
		s.instructions = readInstructions(ws.Root())
	}
	// Sections and knowledge pages are tracked in a resumed session too: its tool calls still bring them in. Its first
	// message, which the log holds, is not built again.
	if (c.opt.SectionInstructions || c.opt.Knowledge) && !c.opt.WholeInstructions {
		w := servedWindow(c.ctx, src)
		budget := window.NewBudget(w)
		var parts []string
		var loads multiSections
		// The knowledge index is priced first, at its own small cap, so the instruction file gets what is left and the
		// two together stay in the budget; in the message the instructions come first.
		index, wholeFile := "", false // wholeFile: the instruction file is loaded whole
		var know *window.Knowledge
		if c.opt.Knowledge {
			pages, err := window.LoadPages(ws.Root())
			if err != nil {
				c.unregister(id)
				if s.kit != nil {
					s.kit()
				}
				return nil, fmt.Errorf("knowledge pages: %w", err)
			}
			if len(pages) > 0 {
				know = window.NewKnowledge(ws.Root(), pages, w, budget)
				if prompted {
					// A reopened session's tracker is rebuilt from its log before anything is priced: first its index
					// as logged, with each page's freshness as the session judged it; what it loaded comes after the
					// instruction file is sized, so the file sees the budget a new session's did.
					if err := know.ReplayIndex(history); err != nil {
						c.unregister(id)
						if s.kit != nil {
							s.kit()
						}
						return nil, err
					}
				} else {
					index = know.Index()
				}
			}
		}
		if c.opt.SectionInstructions {
			if name, content := readInstructionFile(ws.Root()); name != "" {
				in := window.NewInstructions(name, content, budget)
				if first := in.Initial(); first != "" { // with no room at all, Initial gives nothing
					parts = append(parts, first)
				}
				loads, wholeFile = append(loads, in), in.Whole()
				s.sections = in
				s.sectionSearch, _ = searcher.(window.SectionSearcher)
			}
		}
		if know != nil && prompted {
			if err := know.ReplayNotes(history); err != nil {
				c.unregister(id)
				if s.kit != nil {
					s.kit()
				}
				return nil, err
			}
		}
		if know != nil {
			if index != "" {
				parts = append(parts, index)
			}
			loads = append(loads, know)
			s.know = know
		}
		if len(parts) > 0 && !prompted {
			// A whole file alone follows the task, as WholeInstructions puts it; sections and the knowledge index are notes
			// before it.
			s.instructions, s.notesFirst = strings.Join(parts, "\n\n"), !(wholeFile && len(parts) == 1)
		}
		if len(loads) > 0 {
			cfg.Sections = loads
		}
	}
	if history == nil {
		s.loop = loop.New(cfg)
		return s, nil
	}
	// A reopened session's section tracker is rebuilt from its log, so what is already in its window is not loaded
	// again; its knowledge tracker was rebuilt as it was made, before anything was priced. Each takes back only its own
	// share of the budget they share.
	if s.sections != nil {
		if err = s.sections.Replay(history); err != nil {
			c.unregister(id)
			if s.kit != nil {
				s.kit()
			}
			return nil, err
		}
	}
	if s.loop, err = loop.Resume(cfg, history); err != nil {
		c.unregister(id)
		if s.kit != nil {
			s.kit()
		}
		return nil, err
	}
	return s, nil
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
	sub := &subscriber{conn: cn, last: max(p.FromSeq-1, 0)} // nothing before from_seq, replayed or live
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
	// The injection is built before any lock is taken: opening ken's index takes seconds.
	s.mu.Lock()
	injectRoot, mapRoot := "", ""
	if !s.prompted {
		injectRoot, mapRoot = s.injectRoot, s.mapRoot
	}
	s.mu.Unlock()
	inject := ""
	if injectRoot != "" {
		var err error
		if inject, err = c.injection(injectRoot, text.String()); err != nil {
			return nil, &proto.Error{Code: proto.CodeInternalError, Message: "inject_context: " + err.Error()}
		}
	}
	repoMap := ""
	if mapRoot != "" {
		var err error
		if repoMap, err = c.repoMap(mapRoot); err != nil {
			return nil, &proto.Error{Code: proto.CodeInternalError, Message: "repo_map: " + err.Error()}
		}
	}
	// The instruction file's sections for this message, through ken (DESIGN §2.3), are found before any lock is taken:
	// a search can take a moment. One that fails brings none: the tool calls' sections still come. A prompt the checks
	// below will refuse (a halt, a drain, a turn already running) is not searched for: a first look here, under the
	// locks, spares that search, and the checks below still decide.
	c.mu.Lock()
	refused := c.halted != nil || c.draining
	c.mu.Unlock()
	s.mu.Lock()
	refused = refused || s.running
	s.mu.Unlock()
	var paths [][]string
	if !refused && c.opt.SectionSearch && s.sections != nil && !s.sections.Whole() && s.sectionSearch != nil {
		paths, _ = s.sectionSearch.SectionPaths(c.ctx, text.String(), sectionHits, s.sections.Name())
	}
	// c.mu is held from the halt check until the turn's cancel is in place, so a Halt cannot fall between them.
	c.mu.Lock()
	if h := c.halted; h != nil {
		c.mu.Unlock()
		return nil, &proto.Error{Code: proto.CodeHalted, Message: (&halt.Halted{DaemonHalted: *h}).Error() + "; run tack resume"}
	}
	if c.draining {
		c.mu.Unlock()
		return nil, &proto.Error{Code: proto.CodeShuttingDown, Message: "the daemon is shutting down; prompt again once it has restarted"}
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		c.mu.Unlock()
		return nil, &proto.Error{Code: proto.CodeBusy, Message: "a turn is already running in " + string(s.id)}
	}
	ctx, cancel := context.WithCancelCause(c.ctx)
	s.running, s.cancel = true, cancel
	// The instructions, the map and the injection join the first message itself, so the log holds exactly what the model
	// was sent. Map and injection sit after the head and before the task, so the message still ends with the task.
	// The sections ken found go last before the task, so the message still ends with it and Replay finds their note's
	// end at window.MessageTask.
	statement := text.String()
	hits := ""
	if len(paths) > 0 {
		hits = s.sections.ForHits(paths)
	}
	switch {
	case s.prompted:
		if hits != "" {
			text.Reset()
			text.WriteString(strings.Trim(hits, "\n") + window.MessageTask + statement)
		}
	case s.notesFirst && s.instructions != "":
		text.Reset()
		text.WriteString(s.instructions)
		for _, part := range []string{repoMap, inject, strings.Trim(hits, "\n")} {
			if part != "" {
				text.WriteString("\n\n" + part)
			}
		}
		text.WriteString(window.MessageTask + statement)
	case repoMap != "" || inject != "":
		text.Reset()
		for _, part := range []string{repoMap, inject} {
			if part != "" {
				text.WriteString(part + "\n\n")
			}
		}
		text.WriteString(headTaskLabel + statement)
		if s.instructions != "" {
			text.WriteString("\n\n" + s.instructions)
		}
	case s.instructions != "":
		text.WriteString("\n\n" + s.instructions)
	}
	s.mu.Unlock()
	c.mu.Unlock()
	// Take the log back first, and pass on what another process wrote while it was idle (tack rewind's event), so
	// subscribers see it before this turn's own events. The loop needs nothing from it: a rewind changes files, and
	// edit checks a file's current text against what the model saw.
	s.wmu.Lock()
	foreign, err := s.log.Wake()
	if err == nil {
		for _, ev := range foreign {
			s.emit(ev)
		}
	}
	var seq int64
	if err == nil {
		seq, err = s.loop.Start(text.String())
	}
	s.mu.Lock()
	if err != nil {
		s.running, s.cancel = false, nil
		_ = s.log.Idle()
	} else {
		// Only once the user.message is logged: a prompt refused here added nothing, so the next one is still the
		// first. s.running kept any other prompt out until now.
		s.prompted = true
	}
	s.mu.Unlock()
	s.wmu.Unlock()
	if err != nil {
		cancel(nil)
		if errors.Is(err, store.ErrLogBusy) { // tack rewind, or another tack process, is writing it between turns
			return nil, &proto.Error{Code: proto.CodeBusy, Message: fmt.Sprintf("session %s's log is open in another tack process; prompt again when it is done", s.id)}
		}
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	go func() {
		_, _ = s.loop.Run(ctx)
		cancel(nil)
		if h := turnEndHook.Load(); h != nil {
			(*h)()
		}
		// Under wmu, so not between a Halt's wake and append; and under s.mu with the end of running, so a Halt never
		// sees a turn that has let its log go, nor idles one that has not.
		s.wmu.Lock()
		s.mu.Lock()
		_ = s.log.Idle() // between turns another process may write the log (tack rewind)
		s.running, s.cancel = false, nil
		s.mu.Unlock()
		s.wmu.Unlock()
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
		s.recordAside(proto.EvDaemonHaltRefused, ev)
	}
}

// recordAside appends an event to the session's log from outside a turn's own writes (Halt's daemon.halt_refused) and
// passes it on, after what another process wrote while the log was idle. While another process is writing the log
// the event goes unrecorded: appending would take the log back and swallow what that process wrote. A running turn
// keeps its log; an idle session's is given up again.
func (s *session) recordAside(typ proto.EventType, payload any) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	foreign, err := s.log.Wake()
	if err != nil {
		return
	}
	for _, ev := range foreign {
		s.emit(ev)
	}
	if haltHook != nil {
		haltHook()
	}
	if e, err := s.log.Append(typ, 0, payload); err == nil {
		s.emit(e)
	}
	s.mu.Lock() // the check and the idle together, as a turn's end does them
	if !s.running {
		_ = s.log.Idle() // the append took the lock back; a running turn keeps it
	}
	s.mu.Unlock()
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

// multiSections is several loop.Sections as one: each call's texts in order, and every compaction told to all.
type multiSections []loop.Sections

func (m multiSections) ForCall(id, tool string, input json.RawMessage) string {
	var b strings.Builder
	for _, s := range m {
		b.WriteString(s.ForCall(id, tool, input))
	}
	return b.String()
}

func (m multiSections) Masked(ids []string) {
	for _, s := range m {
		s.Masked(ids)
	}
}

func (m multiSections) Drop(id string) {
	for _, s := range m {
		s.Drop(id)
	}
}

// readInstructions returns the first of instructionFiles at root, whole and labelled with its name, or "".
func readInstructions(root string) string {
	if name, content := readInstructionFile(root); name != "" {
		return "Project instructions, from " + name + ":\n\n" + content
	}
	return ""
}

// readInstructionFile is the first of instructionFiles at root and its content, or "".
func readInstructionFile(root string) (name, content string) {
	for _, name := range instructionFiles {
		if b, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			return name, string(b)
		}
	}
	return "", ""
}

// currentSymbol is the index an auto-mode session's current-symbol note reads (Options.CurrentSymbol): symbols when the
// session has one, else the root's lazily opened OpenSpans index, else nil, which turns the note off.
func (c *Core) currentSymbol(root string, symbols tools.Symbols) tools.Symbols {
	switch {
	case !c.opt.CurrentSymbol:
		return nil
	case symbols != nil:
		return symbols
	case c.opt.OpenSpans == nil:
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.spans[root]
	if !ok {
		l = &lazySpans{root: root, open: c.opt.OpenSpans}
		c.spans[root] = l
	}
	return l
}
