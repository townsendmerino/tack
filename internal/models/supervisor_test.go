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

	"github.com/townsendmerino/tack/internal/config"
)

// The supervisor's tests run this test binary as the child: with TACK_FAKE_GOINFER=1 it behaves as a small
// goinfer-serve (the flags, auth and admin routes the supervisor uses, after goinfer's internal/serveapp) and writes
// what it observed to <session-dir>/../fake.json.
func TestMain(m *testing.M) {
	if dir := os.Getenv("TACK_FAKE_DAEMON"); dir != "" {
		fakeDaemon(dir)
		return
	}
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
	Fits       int      `json:"fits"`
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
	admin.HandleFunc("POST /admin/models/fit", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name, Path string }
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		rep.Fits++
		save()
		mu.Unlock()
		switch {
		case strings.Contains(req.Path, "nofit"):
			fmt.Fprint(w, `{"fits":false,"decline":"KV needs 9.0 GB at this context; 2.1 GB is free","decode_path":"unknown"}`)
		case strings.Contains(req.Path, "streams"):
			fmt.Fprint(w, `{"fits":false,"decline":"weights do not fit","retry":"the load retries with -stream-weights","decode_path":"unknown"}`)
		case strings.Contains(req.Path, "cpufit"):
			fmt.Fprint(w, `{"fits":true,"decode_path":"cpu (int4): metal declines this quantization"}`)
		case strings.Contains(req.Path, "resident"):
			fmt.Fprint(w, `{"fits":true,"decode_path":"metal-resident (int4)"}`)
		default:
			fmt.Fprint(w, `{"fits":null,"unknown":"the .giw is not built yet","decode_path":"unknown"}`)
		}
	})
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
		path := "cpu (int4)"
		if strings.Contains(req.Path, "resident") {
			path = "metal-resident (int4)"
		}
		fmt.Fprintf(os.Stderr, "loaded %q: 4-layer model\n  decode path: %s\n  fit: %d-token cap needs 0.5 GB KV (weights 1.0 GB, budget 9.0 GB, 7.5 GB left for a request's own prefill)\n", req.Name, path, *ctxN)
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
	return newSupArgs(t, modelPath, []string{"-backend", "cpu"})
}

// newSupArgs is newSup with the child's extra flags.
func newSupArgs(t *testing.T, modelPath string, args []string) (*Supervisor, *clock, *Pressure) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tksup")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return newSupIn(t, dir, modelPath, args)
}

// newSupDir is newSup with the child's directory given.
func newSupDir(t *testing.T, dir string) (*Supervisor, *clock, *Pressure) {
	t.Helper()
	return newSupIn(t, dir, "/m/model.gguf", []string{"-backend", "cpu"})
}

func newSupIn(t *testing.T, dir, modelPath string, args []string) (*Supervisor, *clock, *Pressure) {
	t.Helper()
	t.Setenv("TACK_FAKE_GOINFER", "1")
	t.Setenv("GOINFER_API_KEY", "leaked-from-the-parent")
	s, err := NewSupervisor(ChildConfig{Binary: os.Args[0], ModelPath: modelPath, Model: "m", Ctx: 8192,
		Args: args, Dir: dir, IdleUnload: 10 * time.Minute, IdleExit: 30 * time.Minute, Admission: true})
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
	if !loaded || plan.ContextWindow != 8192 || !strings.HasPrefix(plan.FitLine, "fit: 8192-token cap") || plan.DecodePath != "cpu (int4)" {
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

// ChildConfig.Pressure is the reading the load guard uses, in place of the host's: a caller that injects "normal" is
// not refused because the machine running the test is under pressure, and one that injects "warning" is refused on an
// idle machine.
func TestChildConfigPressureIsTheGuardsReading(t *testing.T) {
	t.Setenv("TACK_FAKE_GOINFER", "1")
	for _, want := range []Pressure{PressureNormal, PressureWarning} {
		dir, err := os.MkdirTemp("/tmp", "tksup")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		s, err := NewSupervisor(ChildConfig{Binary: os.Args[0], ModelPath: "/m/model.gguf", Model: "m", Ctx: 8192,
			Args: []string{"-backend", "cpu"}, Dir: dir, IdleUnload: 10 * time.Minute, IdleExit: 30 * time.Minute,
			Pressure: func() (Pressure, error) { return want, nil }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Stop() })
		_, err = s.Ensure(context.Background())
		refused := err != nil && strings.Contains(err.Error(), "memory pressure is warning")
		if refused != (want == PressureWarning) {
			t.Fatalf("injected %v: Ensure = %v", want, err)
		}
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

// TestFitDeclineRefusesTheLoad: before each load goinfer is asked whether it would fit (G2, POST /admin/models/fit,
// p1-notes "P1's order and decisions"). fits:false is refused with goinfer's decline, and nothing loads; fits:null
// loads, to find out; fits:false with a retry loads too, since goinfer's own load would not refuse it.
func TestFitDeclineRefusesTheLoad(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSup(t, "/m/nofit.gguf")
	if _, err := s.Ensure(ctx); err == nil || !strings.Contains(err.Error(), "KV needs 9.0 GB") {
		t.Fatalf("Ensure on a load that does not fit: %v, want goinfer's decline", err)
	}
	if r := report(t, s); r.Fits != 1 || r.Loads != 0 {
		t.Fatalf("fit asked %d times, %d loads; want 1 and 0", r.Fits, r.Loads)
	}
	for _, path := range []string{"/m/notbuilt.gguf", "/m/streams.gguf"} {
		s, _, _ := newSup(t, path)
		if _, err := s.Ensure(ctx); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		if r := report(t, s); r.Fits != 1 || r.Loads != 1 {
			t.Errorf("%s: fit asked %d times, %d loads; want 1 and 1", path, r.Fits, r.Loads)
		}
	}
}

// TestDecodePathIsChecked: a load meant for the GPU that would decode on the CPU is refused. When fit already knows,
// it is refused before the load; when only the load can tell, it is refused after, from goinfer's banner, and the
// model is unloaded. A load that lands on the path its flags ask for is kept.
func TestDecodePathIsChecked(t *testing.T) {
	ctx := context.Background()
	metal := []string{"-backend", "metal"}
	s, _, _ := newSupArgs(t, "/m/cpufit.giw", metal)
	if _, err := s.Ensure(ctx); err == nil || !strings.Contains(err.Error(), "cpu (int4)") || !strings.Contains(err.Error(), "metal-resident") {
		t.Fatalf("fit says cpu for a metal load: %v, want refused before the load", err)
	}
	if r := report(t, s); r.Loads != 0 {
		t.Fatalf("%d loads; want none", r.Loads)
	}
	s, _, _ = newSupArgs(t, "/m/fallsback.giw", metal)
	if _, err := s.Ensure(ctx); err == nil || !strings.Contains(err.Error(), "banner") {
		t.Fatalf("a metal load the banner puts on the cpu: %v, want refused after the load", err)
	}
	if r := report(t, s); r.Loads != 1 || r.Unloads != 1 {
		t.Fatalf("%d loads, %d unloads; want the fallen-back load unloaded", r.Loads, r.Unloads)
	}
	if _, loaded := s.Running(); loaded {
		t.Fatal("the supervisor counts the refused load as loaded")
	}
	s, _, _ = newSupArgs(t, "/m/resident.giw", metal)
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatalf("a load that lands on metal: %v", err)
	}
}

// TestWithoutAdmissionAFallbackIsKept: measurement (E0's runner) leaves admission off. A GPU load that falls back to
// the CPU loads and is kept, its decode path in the plan for the arm to judge, and goinfer is not asked fit.
func TestWithoutAdmissionAFallbackIsKept(t *testing.T) {
	s, _, _ := newSupArgs(t, "/m/fallsback.giw", []string{"-backend", "metal"})
	s.cfg.Admission = false
	if _, err := s.Ensure(context.Background()); err != nil {
		t.Fatalf("a fallback without admission: %v, want it loaded", err)
	}
	if p, _ := s.Plan(); !strings.HasPrefix(p.DecodePath, "cpu") {
		t.Fatalf("plan %+v; want the banner's cpu decode path", p)
	}
	if r := report(t, s); r.Fits != 0 || r.Unloads != 0 {
		t.Fatalf("fit asked %d times, %d unloads; want neither", r.Fits, r.Unloads)
	}
}

// TestOpenAdmitsTacksOwnLoads: the child a session's config makes (Open) goes through admission; only measurement,
// which makes its supervisor itself, leaves it off.
func TestOpenAdmitsTacksOwnLoads(t *testing.T) {
	cfg := &config.Config{Servers: map[string]config.Server{"mac": {Kind: config.KindChild, Binary: "/bin/goinfer-serve",
		ModelPath: "/m/q.gguf", Model: "q", Ctx: 16384}}}
	src, err := Open(cfg, "mac", "/tmp/tk", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := src.(*childSource); !ok || !c.sup.cfg.Admission {
		t.Fatalf("Open made %T without admission", src)
	}
}

// TestCriticalPressureUnloads: at critical memory pressure the idle tick unloads the model, generating or not, and the
// next load waits for the pressure to fall (DESIGN §8.1, memory guard: at critical the model unloads). Measurement,
// with admission off, is left alone.
func TestCriticalPressureUnloads(t *testing.T) {
	ctx := context.Background()
	s, _, p := newSup(t, "/m/busy.gguf") // busy: a generation is in flight
	if _, err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	*p = PressureCritical
	if err := s.Tick(ctx); err == nil || !strings.Contains(err.Error(), "critical") {
		t.Fatalf("Tick at critical pressure: %v, want the unload reported", err)
	}
	if _, loaded := s.Running(); loaded || report(t, s).Unloads != 1 {
		t.Fatalf("loaded %v, %d unloads; want the model unloaded", loaded, report(t, s).Unloads)
	}
	if _, err := s.Ensure(ctx); err == nil || !strings.Contains(err.Error(), "memory pressure") {
		t.Fatalf("a load at critical pressure: %v, want refused", err)
	}

	m, _, mp := newSup(t, "/m/busy.gguf")
	m.cfg.Admission = false
	if _, err := m.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	*mp = PressureCritical
	if err := m.Tick(ctx); err != nil || report(t, m).Unloads != 0 {
		t.Fatalf("measurement at critical pressure: %v, %d unloads; want it left alone", err, report(t, m).Unloads)
	}
}
