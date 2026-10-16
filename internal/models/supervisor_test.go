package models

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The supervisor's tests run this test binary as the child: with TACK_FAKE_GOINFER=1 it behaves as a small
// goinfer-serve (the flags, auth and admin routes the supervisor uses, after goinfer's internal/serveapp) and writes
// what it observed to <session-dir>/../fake.json.
func TestMain(m *testing.M) {
	if os.Getenv("TACK_FAKE_GOINFER") == "1" {
		fakeGoinfer(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

type fakeReport struct {
	Args       []string `json:"args"`
	EnvKey     bool     `json:"env_key"`
	Loads      int      `json:"loads"`
	Unloads    int      `json:"unloads"`
	BadAuth    int      `json:"bad_auth"`
	SessionDir string   `json:"session_dir"`
}

func fakeGoinfer(args []string) {
	fs := flag.NewFlagSet("fake", flag.ExitOnError)
	addr := fs.String("addr", "", "")
	sock := fs.String("admin-socket", "", "")
	sessions := fs.String("session-dir", "", "")
	ctxN := fs.Int("ctx", 0, "")
	fs.String("backend", "", "")
	_ = fs.Parse(args)
	key := os.Getenv("GOINFER_API_KEY")
	var mu sync.Mutex
	rep := fakeReport{Args: args, EnvKey: key != "", SessionDir: *sessions}
	loaded := map[string]bool{}
	busy := false
	save := func() {
		b, _ := json.Marshal(rep)
		_ = os.WriteFile(filepath.Join(*sessions, "..", "fake.json"), b, 0o600)
	}
	save()

	api := http.NewServeMux()
	api.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("x-api-key") != key {
			rep.BadAuth++
			save()
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"missing or invalid API key","type":"invalid_request_error"}}`)
			return
		}
		var data []map[string]any
		for name := range loaded {
			data = append(data, map[string]any{"id": name, "object": "model", "context_window": *ctxN})
		}
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	})
	admin := http.NewServeMux()
	admin.HandleFunc("POST /admin/models/load", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name, Path string }
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(req.Path, "toobig") {
			w.WriteHeader(400)
			fmt.Fprintf(w, `{"error":{"message":"resident context %d does not fit: KV needs 9.0 GB, 2.1 GB free","type":"invalid_request_error"}}`, *ctxN)
			return
		}
		busy = strings.Contains(req.Path, "busy")
		loaded[req.Name] = true
		rep.Loads++
		save()
		fmt.Fprintf(os.Stderr, "loaded %q: 4-layer model\n  fit: %d-token cap needs 0.5 GB KV (weights 1.0 GB, budget 9.0 GB, 7.5 GB left for a request's own prefill)\n", req.Name, *ctxN)
		json.NewEncoder(w).Encode(map[string]any{"id": req.Name, "status": "loaded"})
	})
	admin.HandleFunc("POST /admin/models/unload", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name string }
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		defer mu.Unlock()
		delete(loaded, req.Name)
		rep.Unloads++
		save()
		json.NewEncoder(w).Encode(map[string]any{"id": req.Name, "status": "unloaded"})
	})
	admin.HandleFunc("GET /admin/status", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		if busy {
			n = 1
		}
		json.NewEncoder(w).Encode(map[string]any{"halted": false, "generations_inflight": n})
	})
	al, err := net.Listen("unix", *sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Chmod(*sock, 0o600)
	go http.Serve(al, admin)
	go http.ListenAndServe(*addr, api)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	os.Exit(0)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newSup returns a supervisor running this binary as the fake child, with a short Dir (Unix socket paths are limited)
// and a controllable clock and memory pressure.
func newSup(t *testing.T, modelPath string) (*Supervisor, *clock, *Pressure) {
	t.Helper()
	t.Setenv("TACK_FAKE_GOINFER", "1")
	t.Setenv("GOINFER_API_KEY", "leaked-from-the-parent")
	dir, err := os.MkdirTemp("/tmp", "tksup")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := NewSupervisor(ChildConfig{Binary: os.Args[0], ModelPath: modelPath, Model: "m", Ctx: 8192,
		Args: []string{"-backend", "cpu"}, Dir: dir, IdleUnload: 10 * time.Minute, IdleExit: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	p := PressureNormal
	s.now = c.now
	s.pressure = func() (Pressure, error) { return p, nil }
	t.Cleanup(func() { s.Stop() })
	return s, c, &p
}

func report(t *testing.T, s *Supervisor) fakeReport {
	t.Helper()
	var r fakeReport
	b, err := os.ReadFile(filepath.Join(s.cfg.Dir, "fake.json"))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(b, &r)
	return r
}

func TestSupervisorStartsLoadsWithItsOwnKey(t *testing.T) {
	s, _, _ := newSup(t, "/m/model.gguf")
	ctx := context.Background()
	srv, err := s.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := srv.Models(ctx)
	if err != nil || len(ms) != 1 || ms[0].ID != "m" || ms[0].ContextWindow != 8192 {
		t.Fatalf("models through the returned client: %+v, %v", ms, err)
	}
	r := report(t, s)
	if !r.EnvKey || strings.Contains(strings.Join(r.Args, " "), s.key) {
		t.Fatalf("the key must reach the child through its environment and never its argv: %+v", r)
	}
	if s.key == "leaked-from-the-parent" || len(s.key) != 64 {
		t.Fatalf("the child got key %q, not a fresh random one", s.key)
	}
	if r.SessionDir != filepath.Join(s.cfg.Dir, "sessions") {
		t.Fatalf("session dir %q is not under the supervisor's dir", r.SessionDir)
	}
	if st, err := os.Stat(s.cfg.Dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, %v", st.Mode().Perm(), err)
	}
	plan, loaded := s.Plan()
	if !loaded || plan.ContextWindow != 8192 || !strings.HasPrefix(plan.FitLine, "fit: 8192-token cap") {
		t.Fatalf("plan %+v loaded %v", plan, loaded)
	}
	// A second Ensure reuses the child and the load.
	pid := s.cmd.Process.Pid
	if _, err := s.Ensure(ctx); err != nil || s.cmd.Process.Pid != pid || report(t, s).Loads != 1 {
		t.Fatalf("second Ensure restarted or reloaded (err %v)", err)
	}
	// Without the key the child refuses: the API is not open to other local processes.
	resp, err := http.Get(s.baseURL + "/v1/models")
	if err != nil || resp.StatusCode != 401 {
		t.Fatalf("unauthenticated request: %v %v", resp, err)
	}
}

func TestGoinferRefusalIsReported(t *testing.T) {
	s, _, _ := newSup(t, "/m/toobig.gguf")
	_, err := s.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not fit") || !strings.Contains(err.Error(), "-ctx 8192") {
		t.Fatalf("Ensure = %v, want goinfer's own refusal", err)
	}
}

func TestMemoryPressureBlocksALoad(t *testing.T) {
	s, _, p := newSup(t, "/m/model.gguf")
	*p = PressureWarning
	if _, err := s.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "memory pressure is warning") {
		t.Fatalf("Ensure under warning = %v", err)
	}
	if r := report(t, s); r.Loads != 0 {
		t.Fatalf("a load was attempted under pressure: %+v", r)
	}
	*p = PressureNormal
	if _, err := s.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIdleUnloadThenExitThenRestart(t *testing.T) {
	s, c, _ := newSup(t, "/m/model.gguf")
	ctx := context.Background()
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	c.add(9 * time.Minute)
	s.Tick(ctx)
	if _, loaded := s.Running(); !loaded {
		t.Fatal("unloaded before idle_unload")
	}
	s.Touch()
	c.add(9 * time.Minute)
	s.Tick(ctx)
	if _, loaded := s.Running(); !loaded {
		t.Fatal("Touch did not defer the unload")
	}
	c.add(2 * time.Minute)
	s.Tick(ctx)
	if child, loaded := s.Running(); !child || loaded || report(t, s).Unloads != 1 {
		t.Fatalf("after idle_unload: child %v loaded %v", child, loaded)
	}
	c.add(20 * time.Minute)
	s.Tick(ctx)
	if child, _ := s.Running(); child {
		t.Fatal("child still running after idle_exit")
	}
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, loaded := s.Running(); !loaded {
		t.Fatal("Ensure after exit did not restart and reload")
	}
}

func TestNoUnloadWhileGenerating(t *testing.T) {
	s, c, _ := newSup(t, "/m/busy.gguf")
	ctx := context.Background()
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	c.add(11 * time.Minute)
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, loaded := s.Running(); !loaded {
		t.Fatal("unloaded with a generation in flight")
	}
}

func TestCrashedChildIsRestarted(t *testing.T) {
	s, _, _ := newSup(t, "/m/model.gguf")
	ctx := context.Background()
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	s.cmd.Process.Kill()
	<-s.exited
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if child, loaded := s.Running(); !child || !loaded {
		t.Fatal("not restarted")
	}
}

func TestSocketPathLimit(t *testing.T) {
	_, err := NewSupervisor(ChildConfig{Binary: "b", ModelPath: "m", Model: "m", Ctx: 1, Dir: "/" + strings.Repeat("d", 100)})
	if err == nil {
		t.Fatal("a Dir too long for a Unix socket was accepted")
	}
}

func TestRemote(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "tok" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"qwen","context_window":16384}]}`)
	}))
	defer ts.Close()
	ctx := context.Background()
	r := NewRemote("nobara", ts.URL, "tok", "", nil)
	srv, err := r.Ensure(ctx)
	if err != nil || r.Model() != "qwen" || r.Window() != 16384 || srv.Name() != "goinfer@nobara" {
		t.Fatalf("remote: %v model %q window %d", err, r.Model(), r.Window())
	}
	if _, err := NewRemote("nobara", ts.URL, "tok", "other", nil).Ensure(ctx); err == nil {
		t.Fatal("a model the server does not serve was accepted")
	}
	if _, err := NewRemote("nobara", ts.URL, "wrong", "qwen", nil).Ensure(ctx); err == nil {
		t.Fatal("a bad token was accepted")
	}
}
