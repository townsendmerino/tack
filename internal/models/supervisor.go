package models

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer"
)

// Pressure is the OS's memory-pressure level.
type Pressure int

const (
	PressureUnknown Pressure = iota
	PressureNormal
	PressureWarning
	PressureCritical
)

func (p Pressure) String() string {
	return [...]string{"unknown", "normal", "warning", "critical"}[p]
}

// ChildConfig configures a supervised goinfer-serve (DESIGN §8.1).
type ChildConfig struct {
	Binary    string   // goinfer-serve
	ModelPath string   // the checkpoint
	Model     string   // the name it is loaded and requested under
	Ctx       int      // explicit -ctx: goinfer's planner admits or refuses it as asked
	Args      []string // extra flags
	// Dir holds the child's admin socket, session directory and log, under tack's state directory. A Unix socket path
	// is limited to 104 bytes on macOS, so Dir must be short enough for Dir/admin.sock.
	Dir        string
	IdleUnload time.Duration
	IdleExit   time.Duration
	// HTTPClient, when set, carries the API requests (a cassette recorder in a test or a recording run).
	HTTPClient *http.Client
}

// Plan is what goinfer reported about a load: its banner's fit line, and what /v1/models says it serves.
type Plan struct {
	FitLine string // e.g. "fit: 16384-token cap needs 0.9 GB KV (weights 4.4 GB, budget 9.1 GB, …)"; empty if none was printed
	// DecodePath is the banner's "decode path:" value, e.g. "cuda-resident (int4)" or "cpu (int4)": where goinfer actually
	// runs the model, which can differ from the -backend asked for when a GPU load falls back.
	DecodePath    string
	ContextWindow int
	ResidentBytes int64
}

// Supervisor runs one goinfer-serve child on loopback: a random API key per start (passed in the environment, not on
// the command line), an admin socket for load and unload, a session directory under tack's state directory. The model
// loads on demand and unloads after IdleUnload; the child exits after IdleExit. It is safe for concurrent use.
type Supervisor struct {
	cfg ChildConfig

	// pressure, now and hc are replaced by tests.
	pressure func() (Pressure, error)
	now      func() time.Time

	mu       sync.Mutex
	cmd      *exec.Cmd
	exited   chan struct{} // closed when the child exits
	exitErr  error         // written before exited closes; read only after
	baseURL  string
	key      string
	loaded   bool
	plan     Plan
	lastUse  time.Time
	fitLines chan string
}

// NewSupervisor validates cfg and returns a supervisor; nothing starts until Ensure.
func NewSupervisor(cfg ChildConfig) (*Supervisor, error) {
	if cfg.Binary == "" || cfg.ModelPath == "" || cfg.Model == "" || cfg.Ctx <= 0 || cfg.Dir == "" {
		return nil, errors.New("models: binary, model path, model name, ctx and dir are required")
	}
	if n := len(filepath.Join(cfg.Dir, "admin.sock")); n > 103 {
		return nil, fmt.Errorf("models: admin socket path is %d bytes; Unix sockets allow 103", n)
	}
	return &Supervisor{cfg: cfg, pressure: MemoryPressure, now: time.Now}, nil
}

func (s *Supervisor) adminSock() string { return filepath.Join(s.cfg.Dir, "admin.sock") }

// Ensure starts the child if it is not running and loads the model if it is not loaded, then returns a client for it.
// A load is refused when the Mac is under memory pressure (warning or worse), and refused by goinfer itself when its
// plan does not fit the requested context.
func (s *Supervisor) Ensure(ctx context.Context) (server.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.dead() {
		if err := s.start(ctx); err != nil {
			return nil, err
		}
	}
	if !s.loaded {
		if p, err := s.pressure(); err == nil && p >= PressureWarning {
			return nil, fmt.Errorf("models: not loading %s: memory pressure is %s (DESIGN §8.1)", s.cfg.Model, p)
		}
		if err := s.load(ctx); err != nil {
			return nil, err
		}
	}
	s.lastUse = s.now()
	return goinfer.New("goinfer@local", s.baseURL, s.key, s.cfg.HTTPClient), nil
}

// Touch records use, deferring the idle unload. The loop calls it after every request.
func (s *Supervisor) Touch() {
	s.mu.Lock()
	s.lastUse = s.now()
	s.mu.Unlock()
}

// Plan returns what goinfer reported for the current load.
func (s *Supervisor) Plan() (Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plan, s.loaded
}

func (s *Supervisor) dead() bool {
	select {
	case <-s.exited:
		return true
	default:
		return false
	}
}

func (s *Supervisor) start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(s.cfg.Dir, "sessions"), 0o700); err != nil {
		return err
	}
	_ = os.Remove(s.adminSock()) // a stale socket from a crashed child; only this supervisor uses the path
	port, err := freePort()
	if err != nil {
		return err
	}
	var kb [32]byte
	if _, err := rand.Read(kb[:]); err != nil {
		return err
	}
	s.key = hex.EncodeToString(kb[:])
	s.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	args := append([]string{
		"-addr", fmt.Sprintf("127.0.0.1:%d", port),
		"-admin-socket", s.adminSock(),
		"-session-dir", filepath.Join(s.cfg.Dir, "sessions"),
		"-ctx", fmt.Sprint(s.cfg.Ctx),
	}, s.cfg.Args...)
	cmd := exec.Command(s.cfg.Binary, args...)
	cmd.Env = append(withoutKey(os.Environ()), "GOINFER_API_KEY="+s.key)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // the daemon's signals are not the child's
	logf, err := os.OpenFile(filepath.Join(s.cfg.Dir, "goinfer-serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	cmd.Stdout = logf
	cmd.Stderr = io.MultiWriter(logf, pw)
	s.fitLines = make(chan string, 16)
	go scanFit(pr, s.fitLines)
	if err := cmd.Start(); err != nil {
		logf.Close()
		pw.Close()
		return fmt.Errorf("models: starting %s: %w", s.cfg.Binary, err)
	}
	s.cmd, s.exited, s.loaded = cmd, make(chan struct{}), false
	exited := s.exited
	go func() {
		err := cmd.Wait()
		logf.Close()
		pw.Close()
		// Not under s.mu: Stop holds it while it waits for this close. Readers read exitErr only after <-exited.
		s.exitErr = err
		close(exited)
	}()
	return s.waitReady(ctx, exited)
}

// waitReady polls /v1/models with the key until the child answers, exits, or ctx ends.
func (s *Supervisor) waitReady(ctx context.Context, exited chan struct{}) error {
	c := goinfer.New("goinfer@local", s.baseURL, s.key, &http.Client{Timeout: 2 * time.Second})
	deadline := time.After(60 * time.Second)
	for {
		if _, err := c.Models(ctx); err == nil {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("models: goinfer-serve exited during start (%v); see %s", s.exitErr,
				filepath.Join(s.cfg.Dir, "goinfer-serve.log"))
		case <-deadline:
			s.kill()
			return errors.New("models: goinfer-serve did not answer within 60s")
		case <-ctx.Done():
			s.kill()
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Supervisor) load(ctx context.Context) error {
	for len(s.fitLines) > 0 {
		<-s.fitLines // lines from an earlier load
	}
	var out struct {
		Status string `json:"status"`
	}
	if err := s.admin(ctx, "POST", "/admin/models/load", map[string]string{"name": s.cfg.Model, "path": s.cfg.ModelPath}, &out); err != nil {
		return fmt.Errorf("models: goinfer refused to load %s at -ctx %d: %w", s.cfg.Model, s.cfg.Ctx, err)
	}
	s.loaded = true
	s.plan = Plan{}
	ms, err := goinfer.New("goinfer@local", s.baseURL, s.key, nil).Models(ctx)
	if err == nil {
		for _, m := range ms {
			if m.ID == s.cfg.Model {
				s.plan.ContextWindow = m.ContextWindow
			}
		}
	}
	// The banner prints "decode path:" then "fit:"; take whichever arrive within half a second.
	deadline := time.After(500 * time.Millisecond)
	for s.plan.FitLine == "" || s.plan.DecodePath == "" {
		select {
		case line := <-s.fitLines:
			if rest, ok := strings.CutPrefix(line, "decode path: "); ok {
				s.plan.DecodePath = rest
			} else {
				s.plan.FitLine = line
			}
		case <-deadline:
			return nil
		}
	}
	return nil
}

// Tick applies the idle rules: unload the model after IdleUnload without use, stop the child after IdleExit. A
// daemon calls it periodically; nothing is unloaded while a generation is in flight.
func (s *Supervisor) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.dead() {
		return nil
	}
	idle := s.now().Sub(s.lastUse)
	if idle >= s.cfg.IdleExit {
		return s.stopLocked()
	}
	if s.loaded && idle >= s.cfg.IdleUnload {
		var st struct {
			Inflight int `json:"generations_inflight"`
		}
		if err := s.admin(ctx, "GET", "/admin/status", nil, &st); err != nil {
			return err
		}
		if st.Inflight > 0 {
			return nil
		}
		if err := s.admin(ctx, "POST", "/admin/models/unload", map[string]string{"name": s.cfg.Model}, nil); err != nil {
			return err
		}
		s.loaded = false
	}
	return nil
}

// Running reports whether the child is up and whether the model is loaded.
func (s *Supervisor) Running() (child, loaded bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up := s.cmd != nil && !s.dead()
	return up, up && s.loaded
}

// Stop ends the child: SIGTERM, then SIGKILL after 10s.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

func (s *Supervisor) stopLocked() error {
	if s.cmd == nil || s.dead() {
		return nil
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-s.exited:
	case <-time.After(10 * time.Second):
		s.kill()
		<-s.exited
	}
	s.loaded = false
	return nil
}

func (s *Supervisor) kill() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// RefusedError is the child's admin API answering with an error status: the server is up and said why it would not,
// as its planner does for a load that does not fit. Any other error from a load means no answer came.
type RefusedError struct {
	Status  int
	Message string
}

func (e *RefusedError) Error() string { return fmt.Sprintf("%d: %s", e.Status, e.Message) }

// admin calls the child's admin socket (HTTP over a Unix socket, goinfer's admin_socket.go).
func (s *Supervisor) admin(ctx context.Context, method, path string, body, out any) error {
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", s.adminSock())
	}}}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://admin"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		msg := strings.TrimSpace(string(b))
		if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return &RefusedError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// scanFit forwards each banner "fit:" and "decode path:" line from the child's stderr.
func scanFit(r io.Reader, out chan<- string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "fit: ") || strings.HasPrefix(line, "decode path: ") {
			select {
			case out <- line:
			default:
			}
		}
	}
	_, _ = io.Copy(io.Discard, r)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func withoutKey(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "GOINFER_API_KEY=") {
			out = append(out, e)
		}
	}
	return out
}

// Endpoint is the running child's base URL and API key, for tools that drive it directly (goinfer-serve check).
func (s *Supervisor) Endpoint() (url, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseURL, s.key
}

// PeakRSS is the last child's peak resident set in bytes, from getrusage once it has exited; 0 while it runs. It is
// RSS, which Darwin lowers under memory pressure: a measurement's context, never a guard (CLAUDE.md).
func (s *Supervisor) PeakRSS() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || !s.dead() { // ProcessState is written by the reaper; read it only after exited closes
		return 0
	}
	ru, ok := s.cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return ru.Maxrss // bytes on Darwin
	}
	return ru.Maxrss * 1024 // KiB on Linux
}
