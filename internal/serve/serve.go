// Package serve is `tack serve`, the on-demand model server on the box with the GPU (DESIGN §8.2). systemd starts it
// through a socket unit when the first connection arrives (or the daemon on the Mac starts it over ssh). It runs
// goinfer-serve as a supervised child on loopback (internal/models, with admission) and stands in front of it:
//
//   - the first request starts the child and loads the model, unless goinfer's timing lock is held: nothing of tack's
//     runs beside a timed run, so the start is refused and the box reported busy;
//   - requests are answered 503 while the model loads, and GET /tack/status says how it stands, so a client can show
//     "loading model on nobara";
//   - once ready, /v1/ is proxied to the child under the child's own key;
//   - after the idle timeout the model unloads and the child exits, and then so does tack serve, freeing the GPU until
//     the next connection.
//
// Every route needs the bearer key (x-api-key, or Authorization: Bearer).
package serve

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/townsendmerino/tack/internal/models"
)

// State is how the model stands.
type State string

const (
	Idle    State = "idle"    // no child: the next request starts one
	Loading State = "loading" // the child is starting or loading the model
	Ready   State = "ready"   // requests are proxied
	Busy    State = "busy"    // a timed run holds goinfer's timing lock; nothing was started
	Failed  State = "failed"  // the last start failed; the next request tries again
)

// Status is GET /tack/status's answer, and POST /tack/start's.
type Status struct {
	State   State  `json:"state"`
	Message string `json:"message,omitempty"`
	Model   string `json:"model,omitempty"`
	Window  int    `json:"window,omitempty"`
}

// Options configures a Front.
type Options struct {
	// Child is the goinfer-serve to run. Its IdleUnload and IdleExit are the serve idle timeout: the model unloads and
	// the child exits together.
	Child models.ChildConfig
	// Key is the bearer key every client presents.
	Key string
	// CheckLock reports goinfer's timing lock: an error naming the holder when a timed run holds it.
	CheckLock func() error
	// LoadTimeout bounds a start and load; 0 means 10 minutes.
	LoadTimeout time.Duration
	// Log is told of starts, refusals, loads and unloads; nil discards.
	Log io.Writer
}

// Front stands in front of the supervised child. Its methods are safe for concurrent use.
type Front struct {
	o   Options
	sup *models.Supervisor
	now func() time.Time

	mu       sync.Mutex
	st       Status
	active   time.Time // the last request, or the start
	proxy    http.Handler
	stopping context.CancelFunc // cancels a load in progress
}

// New returns a front with nothing started.
func New(o Options) (*Front, error) {
	if o.Key == "" || o.CheckLock == nil {
		return nil, fmt.Errorf("serve: a key and a timing-lock check are required")
	}
	if o.LoadTimeout == 0 {
		o.LoadTimeout = 10 * time.Minute
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	sup, err := models.NewSupervisor(o.Child)
	if err != nil {
		return nil, err
	}
	return &Front{o: o, sup: sup, now: time.Now, st: Status{State: Idle}, active: time.Now()}, nil
}

// Status is how the model stands now.
func (f *Front) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

// Start starts the child and its load in the background, unless it is loading or ready already, and returns the status:
// busy, without starting anything, when a timed run holds goinfer's timing lock.
func (f *Front) Start() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = f.now()
	if f.st.State == Loading || f.st.State == Ready {
		return f.st
	}
	if err := f.o.CheckLock(); err != nil {
		f.st = Status{State: Busy, Message: "busy: a timed run holds goinfer's timing lock (" + err.Error() + ")"}
		fmt.Fprintln(f.o.Log, "tack serve: not starting:", f.st.Message)
		return f.st
	}
	f.st = Status{State: Loading, Message: "loading model " + f.o.Child.Model, Model: f.o.Child.Model}
	fmt.Fprintln(f.o.Log, "tack serve:", f.st.Message)
	ctx, cancel := context.WithTimeout(context.Background(), f.o.LoadTimeout)
	f.stopping = cancel
	go f.load(ctx, cancel)
	return f.st
}

func (f *Front) load(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	_, err := f.sup.Ensure(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopping = nil
	if f.st.State != Loading { // Stop came first
		return
	}
	if err != nil {
		f.st = Status{State: Failed, Message: err.Error(), Model: f.o.Child.Model}
		fmt.Fprintln(f.o.Log, "tack serve: the load failed:", err)
		return
	}
	plan, _ := f.sup.Plan()
	base, key := f.sup.Endpoint()
	target, _ := url.Parse(base)
	f.proxy = &httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Header.Del("Authorization")
		r.Out.Header.Set("x-api-key", key) // the child's own key, never the client's
	}}
	f.st = Status{State: Ready, Model: f.o.Child.Model, Window: plan.ContextWindow}
	fmt.Fprintf(f.o.Log, "tack serve: ready: %s, window %d, decode path %q\n", f.o.Child.Model, plan.ContextWindow, plan.DecodePath)
}

// Tick applies the idle rules (models.Supervisor.Tick): after the idle timeout without a request the model unloads
// and the child exits. It reports whether tack serve itself may exit now: no child, and idle that long.
func (f *Front) Tick(ctx context.Context) (exit bool) {
	f.mu.Lock()
	loading := f.st.State == Loading
	f.mu.Unlock()
	if loading {
		return false
	}
	if err := f.sup.Tick(ctx); err != nil {
		fmt.Fprintln(f.o.Log, "tack serve: idle rules:", err)
	}
	child, _ := f.sup.Running()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.st.State == Ready && !child {
		f.st, f.proxy = Status{State: Idle, Message: "unloaded after " + f.o.Child.IdleUnload.String() + " idle"}, nil
		fmt.Fprintln(f.o.Log, "tack serve:", f.st.Message)
	}
	return f.st.State != Loading && f.st.State != Ready && !child && f.now().Sub(f.active) >= f.o.Child.IdleUnload
}

// Stop ends the child and any load in progress.
func (f *Front) Stop() error {
	f.mu.Lock()
	if f.stopping != nil {
		f.stopping()
	}
	f.st, f.proxy = Status{State: Idle}, nil
	f.mu.Unlock()
	return f.sup.Stop()
}

// Handler is the front's HTTP handler: /tack/status, /tack/start, and /v1/ proxied once ready.
func (f *Front) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tack/status", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, f.Status()) })
	mux.HandleFunc("POST /tack/start", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, f.Start()) })
	mux.HandleFunc("/v1/", f.serveAPI)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !f.authorized(r) {
			writeError(w, http.StatusUnauthorized, "authentication_error", "missing or invalid API key")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// serveAPI proxies a model request once the model is ready. Before that it starts the child (the first connection
// starts the server, DESIGN §8.2) and answers 503 with the status, so the client retries.
func (f *Front) serveAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.active = f.now()
	p := f.proxy
	f.mu.Unlock()
	if p != nil {
		if child, _ := f.sup.Running(); child {
			f.sup.Touch()
			p.ServeHTTP(w, r)
			return
		}
		f.mu.Lock() // the child went away (it crashed): start again
		f.st, f.proxy = Status{State: Idle}, nil
		f.mu.Unlock()
	}
	st := f.Start()
	w.Header().Set("Retry-After", "2")
	writeError(w, http.StatusServiceUnavailable, "overloaded_error", fmt.Sprintf("tack serve: %s: %s", st.State, st.Message))
}

func (f *Front) authorized(r *http.Request) bool {
	got := r.Header.Get("x-api-key")
	if got == "" {
		got, _ = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(f.o.Key)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers in the shape goinfer and the Anthropic API use, which tack's goinfer client reads.
func writeError(w http.ResponseWriter, code int, typ, msg string) {
	writeJSON(w, code, map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}})
}

// Serve answers on ln until ctx ends or the front has been idle with no child for the idle timeout, ticking its idle
// rules every tick. Then it stops the child and returns; under socket activation systemd keeps the socket and starts
// tack serve again at the next connection.
func Serve(ctx context.Context, ln net.Listener, f *Front, tick time.Duration) error {
	srv := &http.Server{Handler: f.Handler(), ReadHeaderTimeout: 30 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	t := time.NewTicker(tick)
	defer t.Stop()
	var err error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err = <-served:
			break loop
		case <-t.C:
			if f.Tick(ctx) {
				fmt.Fprintln(f.o.Log, "tack serve: idle; exiting")
				break loop
			}
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	if serr := f.Stop(); err == nil {
		err = serr
	}
	if err == http.ErrServerClosed {
		err = nil
	}
	return err
}
