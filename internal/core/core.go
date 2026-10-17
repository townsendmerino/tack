// Package core is the daemon as a library (DESIGN §4.1): it hosts sessions and speaks the protocol (internal/proto)
// over any newline-delimited stream. The daemon serves it on a Unix socket; --no-daemon serves it in-process over a
// pipe (DESIGN §9.1).
package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
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
}

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

// Close releases the model sources (a supervised child is stopped).
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
	s := &session{id: id, log: log, subs: map[*subscriber]bool{}}
	s.loop = loop.New(loop.Config{Source: src, Tools: tools.NewSet(searcher), WS: ws, Log: log,
		Blobs: c.opt.Store.Blobs, Budget: c.opt.Budget, Emit: s.emit})
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
// Killing what the turns' commands left running is the shell's on cancel, and `tack halt`'s sweep beyond that.
func (c *Core) Halt(h proto.DaemonHalted) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.halted = &h
	for _, s := range c.sessions {
		s.mu.Lock()
		if s.cancel != nil {
			s.cancel(&halt.Halted{DaemonHalted: h})
		}
		s.mu.Unlock()
	}
}

// Resume lets new turns start again.
func (c *Core) Resume() {
	c.mu.Lock()
	c.halted = nil
	c.mu.Unlock()
}
