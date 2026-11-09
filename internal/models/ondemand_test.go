package models

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// fakeServe is tack serve as the Mac sees it: /tack/status and /tack/start move through the states it is told, and
// /v1/models answers once it is ready.
type fakeServe struct {
	mu        sync.Mutex
	state     string
	msg       string
	loads     int // polls of loading before ready
	starts    int
	lockKey   string
	startBusy bool // a start finds the timing lock held
}

func (f *fakeServe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("x-api-key") != f.lockKey {
		w.WriteHeader(401)
		return
	}
	switch r.URL.Path {
	case "/tack/start":
		f.starts++
		if f.startBusy {
			f.state = "busy"
		} else if f.state == "idle" || f.state == "busy" { // as serve.Front.Start: busy is re-checked
			f.state = "loading"
		}
	case "/tack/status":
		if f.state == "loading" {
			if f.loads == 0 {
				f.state = "ready"
			} else {
				f.loads--
			}
		}
	case "/v1/models":
		w.Write([]byte(`{"data":[{"id":"q","context_window":16384}]}`))
		return
	}
	w.Write([]byte(`{"state":"` + f.state + `","message":"` + f.msg + `"}`))
}

func onDemand(t *testing.T, f *fakeServe, start []string) (*OnDemand, *[]string) {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	o := NewOnDemand("nobara", ts.URL, "k", "q", start, []string{"mac"}, nil)
	o.Poll = time.Millisecond
	o.Timeout = 2 * time.Second
	var said []string
	return o, &said
}

func withSaid(said *[]string) context.Context {
	var mu sync.Mutex
	return WithStatus(context.Background(), func(_, state, msg string) {
		mu.Lock()
		*said = append(*said, state+": "+msg)
		mu.Unlock()
	})
}

// TestOnDemandLoadsThenServes: an idle tack serve is started and waited on while it loads; the turn hears "loading
// model on nobara" and then ready, and gets the served window.
func TestOnDemandLoadsThenServes(t *testing.T) {
	f := &fakeServe{state: "idle", loads: 3, lockKey: "k"}
	o, said := onDemand(t, f, nil)
	if _, err := o.Ensure(withSaid(said)); err != nil {
		t.Fatal(err)
	}
	if f.starts != 1 || o.Window() != 16384 {
		t.Fatalf("%d starts, window %d; want 1 and 16384", f.starts, o.Window())
	}
	if got := strings.Join(*said, "|"); !strings.Contains(got, "loading: loading model on nobara") || !strings.HasSuffix(got, "ready: ") {
		t.Fatalf("status said %q; want loading model on nobara, then ready", got)
	}
}

// TestOnDemandBusy: a box a timed run holds is a *BusyError that offers this machine's model, and nothing is
// announced as loading. A busy status is asked to start, since it may be stale; the start finding the lock held is
// what makes it busy.
func TestOnDemandBusy(t *testing.T) {
	for _, state := range []string{"busy", "idle"} {
		f := &fakeServe{state: state, startBusy: true, msg: "a timed run holds goinfer's timing lock (pid 7)", lockKey: "k"}
		o, said := onDemand(t, f, nil)
		_, err := o.Ensure(withSaid(said))
		var be *BusyError
		if !errors.As(err, &be) || !strings.Contains(err.Error(), "pid 7") || !strings.Contains(err.Error(), "tack run --server mac") {
			t.Errorf("%s: %v; want a *BusyError naming the holder and offering mac", state, err)
		}
		if len(*said) != 0 {
			t.Errorf("%s: the status line was told %q; a busy box loads nothing", state, *said)
		}
		if f.starts != 1 {
			t.Errorf("%s: %d starts; want one, which re-checks the lock", state, f.starts)
		}
	}
}

// TestOnDemandStartsOverSSH: when nothing answers and a start command is set, it is run once, as arguments, and the
// load is waited on; exit 75 from it is busy.
func TestOnDemandStartsOverSSH(t *testing.T) {
	f := &fakeServe{state: "idle", lockKey: "k"}
	ts := httptest.NewUnstartedServer(f)
	t.Cleanup(ts.Close)
	ts.Start()
	url := ts.URL
	ts.Close() // nothing answers until the start command "starts" it
	o := NewOnDemand("nobara", url, "k", "q", []string{"ssh", "nobara", "tack", "serve", "-detach"}, nil, nil)
	o.Poll = time.Millisecond
	var argv []string
	restart := httptest.NewUnstartedServer(f)
	o.run = func(_ context.Context, a []string) (int, string, error) {
		argv = a
		restart.Listener.Close()
		l, err := netListen(strings.TrimPrefix(url, "http://"))
		if err != nil {
			return 1, err.Error(), nil
		}
		restart.Listener = l
		restart.Start()
		return 0, "tack serve: started", nil
	}
	t.Cleanup(restart.Close)
	if _, err := o.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, " ") != "ssh nobara tack serve -detach" || f.starts != 1 {
		t.Fatalf("ran %q, %d starts", argv, f.starts)
	}
	o2 := NewOnDemand("nobara", "http://127.0.0.1:1", "k", "q", []string{"ssh", "nobara"}, []string{"mac"}, nil)
	o2.run = func(context.Context, []string) (int, string, error) {
		return exitBusy, "tack serve: busy: pid 7\n", nil
	}
	if _, err := o2.Ensure(context.Background()); !errors.As(err, new(*BusyError)) {
		t.Fatalf("a start that exits 75: %v, want busy", err)
	}
}

func netListen(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

// TestOnDemandGenerationOutlastsTheControlTimeout: the short timeout on tack serve's own routes (/tack/status,
// /tack/start) is not the model's. A prefill and generation longer than that timeout is answered, not cut off.
func TestOnDemandGenerationOutlastsTheControlTimeout(t *testing.T) {
	defer func(d time.Duration) { controlTimeout = d }(controlTimeout)
	controlTimeout = 200 * time.Millisecond
	f := &fakeServe{state: "ready", lockKey: "k"}
	model := goinfertest.NewScripted("q", 16384, goinfertest.Says("slow but here"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			w.Header().Set("Content-Type", "text/event-stream") // the stream opens at once
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			time.Sleep(3 * controlTimeout) // a long prefill and generation, inside the body
			model.ServeHTTP(w, r)
			return
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	o := NewOnDemand("nobara", ts.URL, "k", "q", nil, nil, nil)
	o.Poll = time.Millisecond
	srv, err := o.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Stream(context.Background(), server.Request{Model: "q", MaxTokens: 16,
		Messages: []server.Message{{Role: server.User, Content: []server.Block{server.TextBlock("hi")}}}}, func(server.Delta) {})
	if err != nil {
		t.Fatalf("a generation longer than the control timeout: %v; want it answered", err)
	}
	if got := fmt.Sprint(resp.Content); !strings.Contains(got, "slow but here") {
		t.Fatalf("the reply: %s; want the model's", got)
	}
}

// TestOnDemandBusyIsCheckedAgain: a status of busy may be stale (tack serve checks the timing lock on a start, and
// the run that held it may be over), so it is asked to start; only a start that finds the lock held is busy.
func TestOnDemandBusyIsCheckedAgain(t *testing.T) {
	f := &fakeServe{state: "busy", msg: "a timed run holds goinfer's timing lock (pid 7)", lockKey: "k"}
	o, said := onDemand(t, f, nil)
	f.mu.Lock()
	f.state = "busy" // stale: the timed run is over, and the next start finds the lock free
	f.mu.Unlock()
	if _, err := o.Ensure(withSaid(said)); err != nil {
		t.Fatalf("a busy status left by a run that has ended: %v; want the start re-checked and the model loaded", err)
	}
	if f.starts != 1 {
		t.Fatalf("%d starts; want one, to re-check the lock", f.starts)
	}
}

// TestOnDemandAWrongTokenFailsAtOnce: tack serve refusing the token (401) answers; it is not a server that is down. So
// the start command is not run, and Ensure does not wait out its load timeout, but says the token is wrong.
func TestOnDemandAWrongTokenFailsAtOnce(t *testing.T) {
	f := &fakeServe{state: "idle", lockKey: "the-right-key"}
	o, _ := onDemand(t, f, []string{"ssh", "nobara", "tack", "serve", "-detach"})
	ran := false
	o.run = func(context.Context, []string) (int, string, error) { ran = true; return 0, "", nil }
	o.Timeout = 10 * time.Second
	began := time.Now()
	_, err := o.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a wrong token: %v; want the refusal", err)
	}
	if ran || time.Since(began) > 2*time.Second {
		t.Fatalf("a wrong token ran the start command (%v) and took %s; want neither", ran, time.Since(began))
	}
}
