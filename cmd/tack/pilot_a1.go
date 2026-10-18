package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/eval"
	"github.com/townsendmerino/tack/internal/eval/pilot"
	"github.com/townsendmerino/tack/tools/gate"
)

// A1 runs tack on the Mac, in its sandbox, against the pilot's server on nobara (pilot pre-registration, amendment 9).
// The Mac's job starts `tack eval pilot-serve` on nobara over ssh and reaches the server through an ssh port forward
// over the tailnet. The capture runs on the Mac, so each request's client-side time can be set beside goinfer's own.

// endpoint is pilot-serve's first line on stdout.
type endpoint struct {
	Port       int    `json:"port"`
	Key        string `json:"key"`
	DecodePath string `json:"decode_path"`
	Fit        string `json:"fit"`
}

// pilotServeCmd is `tack eval pilot-serve` (run on nobara by an A1 job, over ssh). It starts the pilot's server under
// the machine's timing lock (startPilotServer), prints its endpoint as one JSON line, then streams the server's log to
// stdout until stdin closes, when it stops the server and copies its log into -out.
func pilotServeCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval pilot-serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	serve := fs.String("goinfer-serve", "", "the goinfer-serve binary")
	modelPath := fs.String("model-path", "", "the checkpoint, from ~/models")
	ctxN := fs.Int("ctx", 32768, "the served context")
	out := fs.String("out", "", "where the server's log is copied when it stops")
	rev := fs.String("tack-rev", "", "the tack commit this binary must have been built from")
	var opts serveOptionList
	fs.Var(&opts, "serve-option", serveOptionUsage)
	if err := fs.Parse(args); err != nil || *serve == "" || *modelPath == "" || *out == "" {
		fmt.Fprintln(stderr, "usage: tack eval pilot-serve -goinfer-serve BIN -model-path FILE -ctx N -out DIR -tack-rev REV [-serve-option NAME]")
		return exitUsage
	}
	if _, err := eval.Revision(*rev); err != nil {
		fmt.Fprintln(stderr, "tack eval pilot-serve:", err)
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ps, err := startPilotServer(ctx, *serve, *modelPath, *ctxN, *out, opts, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot-serve:", err)
		var void voidError
		if errors.As(err, &void) {
			return exitError
		}
		if errors.Is(err, eval.ErrHeld) {
			return exitHeld
		}
		return exitError
	}
	defer ps.stop()
	port, err := urlPort(ps.url)
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot-serve:", err)
		return exitError
	}
	b, _ := json.Marshal(endpoint{Port: port, Key: ps.key, DecodePath: ps.plan.DecodePath, Fit: ps.plan.FitLine})
	fmt.Fprintf(stdout, "%s\n", b)
	stdinClosed := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(stdinClosed) }()
	followLog(ctx, ps.serveLog, stdout, stdinClosed)
	return exitOK
}

// followLog copies path to w as it grows, until done closes or ctx ends, then copies what is left.
func followLog(ctx context.Context, path string, w io.Writer, done <-chan struct{}) {
	var off int64
	copyNew := func() {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return
		}
		n, _ := io.Copy(w, f)
		off += n
	}
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		copyNew()
		select {
		case <-done:
			copyNew()
			return
		case <-ctx.Done():
			copyNew()
			return
		case <-t.C:
		}
	}
}

func urlPort(u string) (int, error) {
	_, p, err := net.SplitHostPort(u[len("http://"):])
	if err != nil {
		return 0, fmt.Errorf("server url %q: %w", u, err)
	}
	return strconv.Atoi(p)
}

// remote is how the Mac reaches nobara for A1: the host and the paths there.
type remote struct {
	host, tack, serve, modelPath, out, rev string
	ctxN                                   int
	options                                serveOptionList // goinfer-serve options, passed to pilot-serve
}

// serveCmdArgs is the remote command that starts pilot-serve for r.
func (r remote) serveCmdArgs() []string {
	a := []string{r.tack, "eval", "pilot-serve", "-goinfer-serve", r.serve, "-model-path", r.modelPath,
		"-ctx", strconv.Itoa(r.ctxN), "-out", r.out, "-tack-rev", r.rev}
	for _, o := range r.options {
		a = append(a, "-serve-option", o)
	}
	return a
}

// withRemoteServer holds this Mac's timing lock and runs fn against the pilot's server on r.host: pilot-serve over one
// ssh connection, its port forwarded to a loopback port here over another. The server's log is mirrored into
// out/goinfer-serve.log as it is written, and the capture records every request here, with its client-side timing.
func withRemoteServer(ctx context.Context, r remote, out string, log io.Writer, fn func(pilot.Server, string, *pilot.Capture) error) error {
	lockPath, err := eval.TimingLockPath()
	if err != nil {
		return err
	}
	lock, err := eval.AcquireTimingLock(lockPath, "tack eval pilot -a1")
	if err != nil {
		return fmt.Errorf("%w: %v", eval.ErrHeld, err)
	}
	defer lock.Release()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	serveLog := filepath.Join(out, "goinfer-serve.log")
	mirror, err := os.Create(serveLog)
	if err != nil {
		return err
	}
	defer mirror.Close()
	srv := exec.Command("ssh", append([]string{"-o", "BatchMode=yes", r.host}, r.serveCmdArgs()...)...)
	stdin, err := srv.StdinPipe()
	if err != nil {
		return err
	}
	srvOut, err := srv.StdoutPipe()
	if err != nil {
		return err
	}
	srv.Stderr = log
	t0 := time.Now()
	if err := srv.Start(); err != nil {
		return fmt.Errorf("ssh %s pilot-serve: %w", r.host, err)
	}
	defer func() {
		stdin.Close() // pilot-serve stops its server and copies its log
		done := make(chan error, 1)
		go func() { done <- srv.Wait() }()
		select {
		case <-done:
		case <-time.After(2 * time.Minute):
			_ = srv.Process.Kill()
		}
	}()
	br := bufio.NewReader(srvOut)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("pilot-serve on %s gave no endpoint (see its stderr above): %w", r.host, err)
	}
	var ep endpoint
	if err := json.Unmarshal(line, &ep); err != nil || ep.Port == 0 {
		return fmt.Errorf("pilot-serve on %s: endpoint %q: %v", r.host, line, err)
	}
	go func() { _, _ = io.Copy(mirror, br) }()
	logf(log, "pilot a1: server on %s up in %s, decode path %q; %s", r.host, time.Since(t0).Round(time.Second), ep.DecodePath, ep.Fit)
	local, err := freePort()
	if err != nil {
		return err
	}
	fwd := forwardCmd(r.host, local, ep.Port)
	fwd.Stderr = log
	if err := fwd.Start(); err != nil {
		return fmt.Errorf("ssh port forward: %w", err)
	}
	defer func() { _ = fwd.Process.Kill(); _ = fwd.Wait() }()
	url := fmt.Sprintf("http://127.0.0.1:%d", local)
	if err := waitListening(fmt.Sprintf("127.0.0.1:%d", local), 30*time.Second); err != nil {
		return fmt.Errorf("the forward to %s never opened: %w", r.host, err)
	}
	capture, err := pilot.StartCapture(url)
	if err != nil {
		return err
	}
	defer capture.Close()
	logf(log, "pilot a1: requests are recorded raw through %s, forwarded to %s; sampling %v", capture.URL, r.host, pilot.Sampling)
	return fn(pilot.Server{BaseURL: capture.URL, APIKey: ep.Key, Model: "qwen", Context: r.ctxN, Direct: url}, serveLog, capture)
}

// forwardCmd is the ssh port forward from local on this machine to port on host's loopback. ControlPath=none gives it
// a connection of its own: through a shared one (ControlMaster in the owner's ssh config) the master owns the forward,
// which then keeps listening after this ssh is killed.
func forwardCmd(host string, local, port int) *exec.Cmd {
	return exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes", "-o", "ControlPath=none", "-N",
		"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", local, port), host)
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func waitListening(addr string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// a1Cmd is `tack eval pilot -a1`: tack's arm alone over the tasks, on this Mac against the server on -remote-host.
func a1Cmd(ctx context.Context, ts []pilot.Task, mirrors, out string, capD time.Duration, tackRev string, r remote, stdout, stderr io.Writer) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		return exitError
	}
	err = withRemoteServer(ctx, r, out, stderr, func(srv pilot.Server, serveLog string, capture *pilot.Capture) error {
		return pilot.RunPilot(ctx, pilot.Options{Capture: capture, Tasks: ts, Mirrors: mirrors, A1: self,
			Server: srv, ServeLog: serveLog, Out: out, Cap: capD, Log: stderr, CountTokens: counter(srv), TackRev: tackRev,
			ServerOptions: r.options})
	})
	if err != nil {
		fmt.Fprintln(stderr, "tack eval pilot:", err)
		if errors.Is(err, eval.ErrHeld) {
			return exitHeld
		}
		fmt.Fprintln(stdout, gate.Verdict(gate.Inconclusive, "eval pilot -a1: "+err.Error()))
		return exitError
	}
	return exitOK
}
