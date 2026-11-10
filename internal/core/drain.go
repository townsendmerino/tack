package core

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/usage"
)

// drainLimit is how long Drain waits for a turn it cancelled to record its end before giving up on it.
const drainLimit = 5 * time.Second

// BeginDrain starts a shutdown without waiting: from now on new connections (initialize) and new turns are refused
// with CodeShuttingDown. Drain does it too; a daemon calls it first, before it stops accepting, so that a connection
// accepted just before is not initialized by a daemon on its way out.
func (c *Core) BeginDrain() {
	c.mu.Lock()
	c.draining = true
	c.mu.Unlock()
}

// Drain stops the Core for a shutdown (DESIGN §6.1). New turns are refused with CodeShuttingDown. Each running turn
// finishes the tool call it is running, starts no other, and ends before its next model request (loop.Session.Drain).
// A turn still running when grace ends is cancelled with loop.ErrShutdown, which kills its commands; Drain then waits
// up to drainLimit for it to record its end, and returns the sessions it cancelled. The caller closes the Core.
func (c *Core) Drain(grace time.Duration) []proto.SessionID {
	c.mu.Lock()
	c.draining = true
	ss := make([]*session, 0, len(c.sessions))
	for _, s := range c.sessions {
		ss = append(ss, s)
	}
	for child := range c.children { // a subagent ends before its next request too; one not started yet is refused
		child.Drain()
	}
	c.mu.Unlock()
	for _, s := range ss {
		s.loop.Drain()
	}
	if c.waitIdle(ss, time.Now().Add(grace)) {
		return nil
	}
	var cancelled []proto.SessionID
	for _, s := range ss {
		s.mu.Lock()
		if s.running && s.cancel != nil {
			s.cancel(loop.ErrShutdown)
			cancelled = append(cancelled, s.id)
		}
		s.mu.Unlock()
	}
	c.waitIdle(ss, time.Now().Add(drainLimit))
	return cancelled
}

// waitIdle waits until none of ss has a turn running, or until deadline; it reports whether none has.
func (c *Core) waitIdle(ss []*session, deadline time.Time) bool {
	for {
		busy := false
		for _, s := range ss {
			s.mu.Lock()
			busy = busy || s.running
			s.mu.Unlock()
		}
		if !busy {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// status is daemon/status's answer.
func (c *Core) status() proto.DaemonStatusResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := proto.DaemonStatusResult{Version: c.opt.Version, Protocol: proto.Version, PID: os.Getpid(),
		Started: c.started.UTC().Truncate(time.Second), Sessions: len(c.sessions), Halted: c.halted != nil, Draining: c.draining}
	for _, s := range c.sessions {
		s.mu.Lock()
		if s.running {
			r.Running++
		}
		s.mu.Unlock()
	}
	return r
}

// unprivileged refuses method on a connection that did not present the client token at initialize (DESIGN §9.2).
func unprivileged(method string) *proto.Error {
	return &proto.Error{Code: proto.CodeUnauthorized,
		Message: method + " needs the client token: connect as an unsandboxed client that reads client.token"}
}

// Tick applies the idle rules of every model source the Core holds that has them (models.Ticker): a supervised child
// unloads its model, and later exits, after its idle times (DESIGN §8.1). A daemon calls it periodically.
func (c *Core) Tick(ctx context.Context) error {
	c.mu.Lock()
	var ts []models.Ticker
	for _, s := range c.sources {
		if t, ok := s.(models.Ticker); ok {
			ts = append(ts, t)
		}
	}
	c.mu.Unlock()
	var errs []error
	for _, t := range ts {
		errs = append(errs, t.Tick(ctx))
	}
	return errors.Join(errs...)
}

// spoken names the protocol versions this daemon answers with, for initialize's refusal: those a client at that
// version is answered with by Negotiate.
func spoken() string {
	var vs []string
	for _, v := range []int{proto.Version, proto.Version - 1} {
		if got, ok := proto.Negotiate(v, proto.Version); ok && got == v {
			vs = append(vs, strconv.Itoa(v))
		}
	}
	return strings.Join(vs, " and ")
}

// usageSummary is usage/summary: the usage table over every session in the store (internal/usage), from index.db when
// the core has it (Options.Usage), and from the logs otherwise. Any client may ask; it only reads.
func (c *Core) usageSummary(p proto.UsageSummaryParams) (proto.UsageSummaryResult, *proto.Error) {
	if c.opt.Store == nil {
		return proto.UsageSummaryResult{}, &proto.Error{Code: proto.CodeInvalidRequest, Message: "usage/summary: this session core has no store"}
	}
	var since time.Time
	if p.Since != nil {
		since = *p.Since
	}
	loc := time.Local
	if p.UTC {
		loc = time.UTC
	}
	var rows []proto.UsageRow
	var err error
	if c.opt.Usage != nil {
		rows, err = c.opt.Usage.Table(since, loc)
	}
	if c.opt.Usage == nil || err != nil { // no index, or it failed (a lock held past its timeout): the logs are the truth
		rows, err = usage.FromStore(c.opt.Store, since, loc)
	}
	if err != nil {
		return proto.UsageSummaryResult{}, &proto.Error{Code: proto.CodeInternalError, Message: "usage/summary: " + err.Error()}
	}
	if rows == nil {
		rows = []proto.UsageRow{}
	}
	return proto.UsageSummaryResult{Rows: rows}, nil
}

// UsageTable is what answers usage/summary: usage.Cache, over index.db.
type UsageTable interface {
	Table(since time.Time, loc *time.Location) ([]proto.UsageRow, error)
}
