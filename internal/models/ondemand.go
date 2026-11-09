package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/server"
)

// StatusFunc is told how a source stands while Ensure waits on it: state "loading" with a message such as "loading
// model on nobara", then "ready".
type StatusFunc func(server, state, message string)

type statusKey struct{}

// WithStatus returns ctx carrying f, which a source's Ensure tells how it stands while a turn waits on it.
func WithStatus(ctx context.Context, f StatusFunc) context.Context {
	return context.WithValue(ctx, statusKey{}, f)
}

func tellStatus(ctx context.Context, srv, state, msg string) {
	if f, ok := ctx.Value(statusKey{}).(StatusFunc); ok && f != nil {
		f(srv, state, msg)
	}
}

// BusyError is an on-demand server refusing to start because a timed run holds its box (DESIGN §8.2). Offer names the
// servers that can stand in, this machine's own.
type BusyError struct {
	Server  string
	Message string
	Offer   []string
}

func (e *BusyError) Error() string {
	s := fmt.Sprintf("%s is busy: %s", e.Server, e.Message)
	if len(e.Offer) > 0 {
		s += fmt.Sprintf("; this machine's model is configured as server %q: tack run --server %s", e.Offer[0], e.Offer[0])
	}
	return s
}

// controlTimeout bounds each call to tack serve's own routes; a variable so tests can shorten it.
var controlTimeout = 10 * time.Second

// exitBusy is tack serve -detach's exit when the box's timing lock is held.
const exitBusy = 75

// OnDemand is a goinfer behind tack serve on another box (DESIGN §8.2): Ensure starts it when it is not serving and
// waits for its load, telling the context's StatusFunc "loading model on <name>"; a box a timed run holds is a
// *BusyError. Once it serves, it is a Remote.
type OnDemand struct {
	*Remote
	name, url, token string
	start            []string // run when nothing answers; nil under socket activation
	offer            []string
	hc               *http.Client // for /tack/status and /tack/start: controlTimeout
	// Poll and Timeout pace the wait for the load; run starts the server (exec.CommandContext, never a shell).
	Poll, Timeout time.Duration
	run           func(ctx context.Context, argv []string) (exit int, out string, err error)
}

// NewOnDemand returns a source for the tack serve at url. start is the command that starts it when nothing answers
// (tack serve -detach over ssh), nil when systemd's socket activation does; offer names the servers to suggest when
// it is busy.
func NewOnDemand(name, url, token, model string, start, offer []string, hc *http.Client) *OnDemand {
	// The model's requests take hc as given: nil is goinfer.New's, with no overall timeout, since a prefill and
	// generation may take minutes. Only tack serve's own routes get the short timeout (an http.Client's Timeout covers
	// reading the body too).
	ctl := &http.Client{Timeout: controlTimeout}
	if hc != nil {
		ctl.Transport = hc.Transport
	}
	return &OnDemand{Remote: NewRemote(name, url, token, model, hc), name: name, url: strings.TrimRight(url, "/"), token: token,
		start: start, offer: offer, hc: ctl, Poll: time.Second, Timeout: 10 * time.Minute, run: runCommand}
}

// serveStatus is tack serve's GET /tack/status (internal/serve.Status).
type serveStatus struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

// Ensure brings the server up and returns it ready. It asks tack serve how it stands; starts it when it is idle,
// failed or busy (a busy status can outlast the timed run that set it, and a start re-checks the timing lock); runs
// the start command when nothing answers and one is set; and waits while it loads, telling the context's
// StatusFunc. A start that finds the lock held is a *BusyError; a failed load is the load's error.
func (o *OnDemand) Ensure(ctx context.Context) (server.Server, error) {
	deadline := time.Now().Add(o.Timeout)
	ran, asked := false, false
	last := ""
	tell := func(state, msg string) { // a change, not every poll
		if state+msg != last {
			last = state + msg
			tellStatus(ctx, o.name, state, msg)
		}
	}
	for {
		st, err := o.call(ctx, http.MethodGet, "/tack/status")
		if se, ok := errors.AsType[*serveError](err); ok && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden) {
			// tack serve answered and refused the token: starting it again, or waiting, would not change that.
			return nil, fmt.Errorf("models: %s refused the token (this server's token_file; tack serve's [serve] key_file must match): %w", o.name, err)
		}
		switch {
		case err != nil && o.start != nil && !ran:
			tell("loading", "starting tack serve on "+o.name)
			code, out, rerr := o.run(ctx, o.start)
			ran = true
			if code == exitBusy {
				return nil, &BusyError{Server: o.name, Message: strings.TrimSpace(out), Offer: o.offer}
			}
			if rerr != nil || code != 0 {
				return nil, fmt.Errorf("models: starting %s (%s): exit %d: %v %s", o.name, strings.Join(o.start, " "), code, rerr, strings.TrimSpace(out))
			}
		case err != nil && !ran:
			return nil, fmt.Errorf("models: %s: %w", o.name, err)
		case err != nil: // started, and not answering yet
		case st.State == "ready":
			tell("ready", "")
			return o.Remote.Ensure(ctx)
		case st.State == "failed" && asked:
			return nil, fmt.Errorf("models: %s failed to load: %s", o.name, st.Message)
		case st.State == "idle" || st.State == "failed" || st.State == "busy": // busy may be stale: a start re-checks the lock
			if st, err = o.call(ctx, http.MethodPost, "/tack/start"); err != nil {
				return nil, fmt.Errorf("models: starting %s: %w", o.name, err)
			}
			asked = true
			if st.State == "busy" {
				return nil, &BusyError{Server: o.name, Message: st.Message, Offer: o.offer}
			}
			tell("loading", "loading model on "+o.name)
		case st.State == "loading":
			tell("loading", "loading model on "+o.name)
		default:
			return nil, fmt.Errorf("models: %s answered state %q", o.name, st.State)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("models: %s did not finish loading within %s", o.name, o.Timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(o.Poll):
		}
	}
}

func (o *OnDemand) call(ctx context.Context, method, path string) (serveStatus, error) {
	var st serveStatus
	req, err := http.NewRequestWithContext(ctx, method, o.url+path, nil)
	if err != nil {
		return st, err
	}
	if o.token != "" {
		req.Header.Set("x-api-key", o.token)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return st, &serveError{Status: resp.StatusCode, Msg: fmt.Sprintf("%s %s: %d %s", method, path, resp.StatusCode, bytes.TrimSpace(b))}
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return st, nil
}

// serveError is tack serve answering a call with a status other than 200: the server is up, and said no.
type serveError struct {
	Status int
	Msg    string
}

func (e *serveError) Error() string { return e.Msg }

// runCommand runs argv directly (no shell) and returns its exit status and combined output.
func runCommand(ctx context.Context, argv []string) (int, string, error) {
	if len(argv) == 0 {
		return 0, "", errors.New("models: an empty start command")
	}
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode(), string(out), nil
	}
	return 0, string(out), err
}
