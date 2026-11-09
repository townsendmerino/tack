package serve

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/models"
)

// The tests run this binary as goinfer-serve: with TACK_FAKE_GOINFER=1 it serves the flags, auth and admin routes the
// supervisor uses, as internal/models' tests do.
func TestMain(m *testing.M) {
	if os.Getenv("TACK_FAKE_GOINFER") == "1" {
		fakeGoinfer(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

func fakeGoinfer(args []string) {
	if len(args) == 1 && args[0] == "--version" { // the release tack pins, as goinfer-serve prints it
		fmt.Printf("goinfer-serve %s\nbackends: cpu cuda\n", models.GoinferVersion)
		return
	}
	fs := flag.NewFlagSet("fake", flag.ExitOnError)
	addr := fs.String("addr", "", "")
	sock := fs.String("admin-socket", "", "")
	fs.String("session-dir", "", "")
	ctxN := fs.Int("ctx", 0, "")
	fs.String("backend", "", "")
	_ = fs.Parse(args)
	key := os.Getenv("GOINFER_API_KEY")
	loaded := ""
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != key {
			w.WriteHeader(401)
			return
		}
		data := []map[string]any{}
		if loaded != "" {
			data = append(data, map[string]any{"id": loaded, "context_window": *ctxN})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	api.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if d, err := time.ParseDuration(os.Getenv("TACK_FAKE_GOINFER_MESSAGES_SLEEP")); err == nil {
			time.Sleep(d) // a long prefill and generation
		}
		fmt.Fprint(w, `{}`)
	})
	admin := http.NewServeMux()
	admin.HandleFunc("POST /admin/models/fit", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"fits":null,"unknown":"not built","decode_path":"unknown"}`)
	})
	admin.HandleFunc("POST /admin/models/load", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name string }
		json.NewDecoder(r.Body).Decode(&req)
		loaded = req.Name
		fmt.Fprintf(os.Stderr, "  decode path: cuda-resident (int4)\n  fit: %d-token cap needs 0.9 GB KV\n", *ctxN)
		fmt.Fprint(w, `{"status":"loaded"}`)
	})
	admin.HandleFunc("POST /admin/models/unload", func(w http.ResponseWriter, r *http.Request) { loaded = ""; fmt.Fprint(w, `{}`) })
	admin.HandleFunc("GET /admin/status", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"generations_inflight":0}`) })
	al, err := net.Listen("unix", *sock)
	if err != nil {
		os.Exit(1)
	}
	go http.Serve(al, admin)
	go http.ListenAndServe(*addr, api)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	os.Exit(0)
}

const key = "the-serve-key"

// front is a Front over this binary as goinfer-serve, with the lock check given, behind an HTTP test server.
func front(t *testing.T, idle time.Duration, lock func() error) (*Front, *httptest.Server) {
	t.Helper()
	t.Setenv("TACK_FAKE_GOINFER", "1")
	dir, err := os.MkdirTemp("/tmp", "tksrv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f, err := New(Options{Key: key, CheckLock: lock, Child: models.ChildConfig{Binary: os.Args[0], ModelPath: "/m/q.gguf", Model: "q",
		Ctx: 16384, Args: []string{"-backend", "cuda"}, Dir: dir, IdleUnload: idle, IdleExit: idle, Admission: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Stop() })
	ts := httptest.NewServer(f.Handler())
	t.Cleanup(ts.Close)
	return f, ts
}

func get(t *testing.T, ts *httptest.Server, method, path, auth string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, nil)
	if auth != "" {
		req.Header.Set("x-api-key", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func waitState(t *testing.T, f *Front, want State) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for f.Status().State != want {
		if time.Now().After(deadline) {
			t.Fatalf("state %+v; want %s", f.Status(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestNothingStartsWhileTheTimingLockIsHeld: a start while a timed run holds goinfer's timing lock starts nothing,
// and the front says busy, naming the holder, to the status route and to a model request alike (DESIGN §8.2).
func TestNothingStartsWhileTheTimingLockIsHeld(t *testing.T) {
	f, ts := front(t, time.Minute, func() error { return errors.New("the timing lock is held by pid 7 (night.py)") })
	if code, body := get(t, ts, "POST", "/tack/start", key); code != 200 || !strings.Contains(body, `"busy"`) || !strings.Contains(body, "pid 7") {
		t.Fatalf("start: %d %s; want busy naming the holder", code, body)
	}
	if code, body := get(t, ts, "GET", "/v1/models", key); code != 503 || !strings.Contains(body, "busy") {
		t.Fatalf("a model request: %d %s; want 503 busy", code, body)
	}
	if child, _ := f.sup.Running(); child {
		t.Fatal("a child was started while the timing lock was held")
	}
}

// TestTheFirstRequestLoadsThenIsProxied: the first model request starts the child and is answered 503 while the model
// loads; the status route says loading, then ready; then requests reach the child under its own key.
func TestTheFirstRequestLoadsThenIsProxied(t *testing.T) {
	f, ts := front(t, time.Minute, func() error { return nil })
	if code, body := get(t, ts, "GET", "/v1/models", key); code != 503 || !strings.Contains(body, "loading") {
		t.Fatalf("the first request: %d %s; want 503 loading", code, body)
	}
	waitState(t, f, Ready)
	if code, body := get(t, ts, "GET", "/tack/status", key); code != 200 || !strings.Contains(body, `"ready"`) || !strings.Contains(body, `"window":16384`) {
		t.Fatalf("status: %d %s", code, body)
	}
	if code, body := get(t, ts, "GET", "/v1/models", key); code != 200 || !strings.Contains(body, `"q"`) {
		t.Fatalf("a request once ready: %d %s; want the child's answer (it checks its own key)", code, body)
	}
}

// TestEveryRouteNeedsTheKey: without the key, or with another, every route is 401 and nothing starts.
func TestEveryRouteNeedsTheKey(t *testing.T) {
	f, ts := front(t, time.Minute, func() error { return nil })
	for _, r := range [][2]string{{"GET", "/tack/status"}, {"POST", "/tack/start"}, {"GET", "/v1/models"}} {
		for _, auth := range []string{"", "wrong"} {
			if code, _ := get(t, ts, r[0], r[1], auth); code != 401 {
				t.Errorf("%s %s with %q: %d, want 401", r[0], r[1], auth, code)
			}
		}
	}
	if f.Status().State != Idle {
		t.Fatalf("state %+v after unauthorized requests; want idle", f.Status())
	}
}

// TestIdleUnloadsThenExits: after the idle timeout without a request the model unloads and the child exits, and the
// front says it may exit too; until then it may not.
func TestIdleUnloadsThenExits(t *testing.T) {
	f, ts := front(t, 300*time.Millisecond, func() error { return nil })
	get(t, ts, "POST", "/tack/start", key)
	waitState(t, f, Ready)
	if f.Tick(context.Background()) {
		t.Fatal("the front would exit with the model just loaded")
	}
	time.Sleep(400 * time.Millisecond)
	exit := f.Tick(context.Background())
	if child, _ := f.sup.Running(); child || f.Status().State != Idle || !exit {
		t.Fatalf("after the idle timeout: child %v, state %+v, exit %v; want no child, idle, exit", child, f.Status(), exit)
	}
}

// TestServeReturnsWhenIdle: Serve, ticking, returns once the front is idle with no child, leaving nothing running.
func TestServeReturnsWhenIdle(t *testing.T) {
	f, _ := front(t, 200*time.Millisecond, func() error { return nil })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), ln, f, 20*time.Millisecond) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return when idle")
	}
}

// TestALongRequestCountsAsUseUntilItEnds: a proxied request longer than the idle timeout is use until its end, not
// only at its start: a tick just after it ends keeps the model and the child.
func TestALongRequestCountsAsUseUntilItEnds(t *testing.T) {
	t.Setenv("TACK_FAKE_GOINFER_MESSAGES_SLEEP", "600ms")
	f, ts := front(t, 300*time.Millisecond, func() error { return nil })
	get(t, ts, "POST", "/tack/start", key)
	waitState(t, f, Ready)
	if code, body := get(t, ts, "POST", "/v1/messages", key); code != http.StatusOK {
		t.Fatalf("the proxied request: %d %s", code, body)
	}
	f.Tick(context.Background())
	if child, loaded := f.sup.Running(); !child || !loaded || f.Status().State != Ready {
		t.Fatalf("a tick just after a long request: child %v, loaded %v, state %+v; want the model kept", child, loaded, f.Status())
	}
}
