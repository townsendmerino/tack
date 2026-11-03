// Package shell is tack's own shell (DESIGN §6.6, "What is approved is what runs"). One command line is parsed and run
// by mvdan.cc/sh's interpreter in a child process, `tack shell`, so that the sandbox can confine it (P1 step 2c) and a
// timeout or cancel can kill everything it started: its process group, and anything that left the group, found by its
// sandbox (Session.Sweep).
//
// The child checks again, at the moment it happens, every program the interpreter executes and every file a
// redirection opens, against what was approved: the programs approval saw, plus rules when commands run inside the
// sandbox. A program, a run-program flag (go test -exec) or a redirection that approval did not see stops the whole
// command. That check covers the shell's own redirections only; where the programs write is the sandbox's to enforce.
//
// The working directory and exported variables carry from one command to the next; aliases and functions do not.
package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/sandbox"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// State is what carries from one command to the next: the working directory and the exported variables set by
// earlier commands.
type State struct {
	Dir string            `json:"dir"`
	Env map[string]string `json:"env,omitempty"`
}

// request is what the session sends the child on stdin.
type request struct {
	Command    string   `json:"command"`
	State      State    `json:"state"`
	Root       string   `json:"root"`
	Approved   []string `json:"approved"`    // programs approval saw, absolute and resolved
	RunFlags   []string `json:"run_flags"`   // "<program> <flag>" for run-program flags approval saw literally
	Asked      []string `json:"asked"`       // why it asks in every mode, as approval read it (perms.AlwaysAsksIn)
	Rules      []string `json:"rules"`       // the engine's rules, as written
	Path       string   `json:"path"`        // the PATH the engine resolves rules on
	Sandboxed  bool     `json:"sandboxed"`   // rules allow a program approval did not see only inside the sandbox
	ApprovedBy string   `json:"approved_by"` // perms.ByUser, ByRule or ByMode: who allowed the command
}

// reply is what the child writes to fd 3 when the command ends.
type reply struct {
	State   State  `json:"state"`
	Exit    int    `json:"exit"`
	Stopped string `json:"stopped,omitempty"`
}

// Result is one command's outcome. Stopped says why the run-time check stopped it, or is empty.
type Result struct {
	Output  string
	Exit    int
	Stopped string
}

// maxOutput caps what a command's output keeps in memory; the loop truncates further for the model and stores the
// rest as a blob.
const maxOutput = 4 << 20

// Session is one session's shell. It is safe for concurrent use, though the loop runs one command at a time.
type Session struct {
	// Exe and Args start the child: tack's own binary and "shell".
	Exe  string
	Args []string
	// Engine supplies the analysis, the rules and whether commands run inside the sandbox.
	Engine *perms.Engine
	// Wrap puts the child inside the sandbox with the session's mark, group, and the command's own, mark
	// (internal/sandbox.Wrap with Config.Group and Config.Mark, on macOS). Group is the session's sweep target, made
	// with sandbox.NewMark when the session begins; halt sweeps it. Sweep kills what is still inside the sandbox for a
	// target (internal/sandbox.Sweep): when Run kills a command it sweeps the command's mark from when the command
	// started, so a process that left the process group (setsid, setpgid) dies with it. Env adds what the sandbox
	// expects: tack's own GOCACHE and the session's TMPDIR. Engine.Sandboxed must be true exactly when Wrap is set, and
	// Wrap needs Sweep and Group.
	Wrap  func(cmd *exec.Cmd, group, mark string) *exec.Cmd
	Sweep func(sandbox.Target) (sandbox.Report, error)
	Group sandbox.Target
	Env   []string

	mu    sync.Mutex
	state State
}

// Run runs one command line that the permission engine has already allowed, with its run-time check, killing its
// process group when ctx ends or timeout passes.
func (s *Session) Run(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Engine.Sandboxed != (s.Wrap != nil) { // the engine's rules trust the sandbox: never without one
		return Result{}, fmt.Errorf("shell: the engine says sandboxed=%v, but the shell %s sandbox", s.Engine.Sandboxed,
			map[bool]string{true: "has a", false: "has no"}[s.Wrap != nil])
	}
	if s.Wrap != nil && (s.Sweep == nil || s.Group.Mark == "" || s.Group.Since.IsZero()) {
		return Result{}, errors.New("shell: a sandboxed shell needs Sweep and a Group, or a cancel or halt would miss detached processes")
	}
	if s.state.Dir == "" {
		s.state.Dir = s.Engine.Root
	}
	req := request{Command: command, State: s.state, Root: s.Engine.Root, Path: s.Engine.Path, Sandboxed: s.Engine.Sandboxed,
		ApprovedBy: approvedBy(ctx)}
	for _, c := range s.Engine.Analyze(command) {
		if c.Program == "" {
			continue
		}
		req.Approved = append(req.Approved, c.Program)
		for i, a := range c.Args {
			if c.Literal[i] && perms.RunFlag(c.Program, a) != "" {
				req.RunFlags = append(req.RunFlags, c.Program+" "+perms.RunFlag(c.Program, a))
			}
		}
	}
	req.Asked = s.Engine.AlwaysAsksIn(command)
	for _, r := range s.Engine.Rules {
		req.Rules = append(req.Rules, r.String())
	}
	in, err := json.Marshal(req)
	if err != nil {
		return Result{}, err
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		return Result{}, err
	}
	defer rd.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(s.Exe, s.Args...)
	cmd.Stdin = bytes.NewReader(in)
	out := &capped{max: maxOutput}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.ExtraFiles = []*os.File{wr}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), s.Env...)
	target := sandbox.Target{Mark: sandbox.NewMark("c"), Since: time.Now()} // before the child exists
	if s.Wrap != nil {
		cmd = s.Wrap(cmd, s.Group.Mark, target.Mark)
	}
	if err := cmd.Start(); err != nil {
		wr.Close()
		return Result{}, fmt.Errorf("shell: starting the child: %w", err)
	}
	wr.Close()
	done := make(chan struct{})
	killed := make(chan string, 1) // what the kill adds to Stopped
	go func() {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // the whole process group
			note := ""
			if s.Sweep != nil {
				// A detached process may hold the reply pipe or the output open, so this sweep is also what lets the
				// reads below finish.
				switch r, err := s.Sweep(target); {
				case err != nil:
					note = "; killing what it left running failed: " + err.Error()
				case r.Refused != "":
					note = "; killing what it left running was refused: " + r.Refused
				case r.Killed > 0:
					note = fmt.Sprintf("; %d process(es) it had detached from its process group were killed too", r.Killed)
				}
			}
			killed <- note
		case <-done:
			killed <- ""
		}
	}()
	b, _ := io.ReadAll(rd)
	werr := cmd.Wait()
	close(done)
	note := <-killed
	res := Result{Output: out.String()}
	var rep reply
	if err := json.Unmarshal(b, &rep); err != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			res.Stopped = fmt.Sprintf("the command ran past its timeout (%s) and was killed", timeout) + note
		case ctx.Err() != nil:
			res.Stopped = "the command was cancelled and killed" + note
		default:
			return res, fmt.Errorf("shell: the child ended without a reply: %v", werr)
		}
		res.Exit = -1
		return res, nil
	}
	s.state = rep.State
	s.Engine.SessionEnv = maps.Clone(rep.State.Env) // approval sees what later commands inherit
	s.Engine.SessionDir = rep.State.Dir             // and where they start
	res.Exit, res.Stopped = rep.Exit, rep.Stopped
	if out.dropped > 0 {
		res.Output += fmt.Sprintf("\n[… %d more bytes of output were not kept …]\n", out.dropped)
	}
	return res, nil
}

// capped keeps the first max bytes written to it and counts the rest.
type capped struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	max     int
	dropped int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := min(len(p), max(c.max-c.buf.Len(), 0))
	c.buf.Write(p[:keep])
	c.dropped += len(p) - keep
	return len(p), nil
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Child is `tack shell`: it reads a request from stdin, runs the command with the run-time check, writing the command's
// output to out, and writes its reply to replyTo (fd 3). It returns the process exit code.
func Child(stdin io.Reader, out io.Writer, replyTo io.Writer) int {
	var req request
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		fmt.Fprintln(out, "tack shell: bad request:", err)
		return 2
	}
	rep := run(req, out)
	if err := json.NewEncoder(replyTo).Encode(rep); err != nil {
		return 2
	}
	return 0
}

func run(req request, out io.Writer) reply {
	rep := reply{State: req.State}
	file, err := syntax.NewParser().Parse(strings.NewReader(req.Command), "")
	if err != nil {
		fmt.Fprintln(out, "tack shell:", err)
		rep.Exit = 2
		return rep
	}
	ck := newChecker(req)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ck.cancel = cancel
	env := os.Environ()
	for k, v := range req.State.Env {
		env = append(env, k+"="+v)
	}
	r, err := interp.New(
		interp.Dir(req.State.Dir),
		interp.Env(expand.ListEnviron(env...)),
		interp.StdIO(nil, out, out),
		interp.ExecHandlers(ck.exec),
		interp.OpenHandler(ck.open),
	)
	if err != nil {
		fmt.Fprintln(out, "tack shell:", err)
		rep.Exit = 2
		return rep
	}
	err = r.Run(ctx, file)
	var st interp.ExitStatus
	switch {
	case ck.stopped != "":
		rep.Stopped, rep.Exit = ck.stopped, 126
	case errors.As(err, &st):
		rep.Exit = int(st)
	case err != nil:
		fmt.Fprintln(out, "tack shell:", err)
		rep.Exit = 1
	}
	rep.State.Dir = r.Dir
	if perms.Outside(req.Root, r.Dir) { // a session's shell starts inside the repository each time
		rep.State.Dir = req.Root
		fmt.Fprintln(out, "tack shell: the working directory left the repository; the next command starts at its root")
	}
	rep.State.Env = map[string]string{}
	for k, v := range req.State.Env {
		rep.State.Env[k] = v
	}
	names := make([]string, 0, len(r.Vars))
	for k := range r.Vars {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		switch v := r.Vars[k]; {
		case !v.IsSet(): // unset carries too, or an export could never be undone
			delete(rep.State.Env, k)
		case v.Exported && v.Kind == expand.String:
			rep.State.Env[k] = v.Str
		}
	}
	return rep
}

// checker is the child's run-time check.
type checker struct {
	req      request
	engine   *perms.Engine
	approved map[string]bool
	runFlags map[string]bool
	cancel   func()
	stopped  string
}

func newChecker(req request) *checker {
	ck := &checker{req: req, approved: map[string]bool{}, runFlags: map[string]bool{},
		engine: &perms.Engine{Root: req.Root, Path: req.Path, Sandboxed: req.Sandboxed}}
	for _, p := range req.Approved {
		ck.approved[p] = true
	}
	for _, f := range req.RunFlags {
		ck.runFlags[f] = true
	}
	for _, s := range req.Rules {
		if r, err := perms.ParseRule(s); err == nil {
			ck.engine.Rules = append(ck.engine.Rules, r)
		}
	}
	return ck
}

// stop ends the whole command line: the message is the reason, and the interpreter's context is cancelled.
func (ck *checker) stop(format string, args ...any) error {
	if ck.stopped == "" {
		ck.stopped = fmt.Sprintf(format, args...)
	}
	ck.cancel()
	return interp.ExitStatus(126)
}

// everyMode stops the command for a reason that asks in every mode (perms.AlwaysAsks, PathAsks). It is the second layer,
// behind the parse: a user who was asked approved this command, and it runs. It returns the stop's error, or nil.
func (ck *checker) everyMode(why []string, ran string) error {
	if ck.req.ApprovedBy == perms.ByUser || len(why) == 0 {
		return nil
	}
	return ck.stop("%s; it asks in every mode, and nothing approved it (%s)", why[0], ran)
}

// exec checks a program as the interpreter is about to run it, after expansion.
func (ck *checker) exec(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)
		path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
		if err != nil {
			return next(ctx, args) // not found: the interpreter reports it, and nothing runs
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		expanded := perms.Simple{Program: path, Name: args[0], Args: args[1:], Literal: make([]bool, len(args)-1)}
		for i := range expanded.Literal {
			expanded.Literal[i] = true // expanded already: these are the real arguments
		}
		why := ck.engine.AlwaysAsks(expanded, hc.Dir)
		// Whatever the program, since it may be the one that runs git: env, or a shell.
		for _, name := range []string{"HUSKY", "HUSKY_SKIP_HOOKS"} {
			if v := hc.Env.Get(name).String(); v != os.Getenv(name) {
				if r := perms.HooksOff(name, v); r != "" {
					why = append(why, r)
				}
			}
		}
		if err := ck.everyMode(why, strings.Join(args, " ")); err != nil {
			return err
		}
		// The parse told the user it couldn't see what this runs, and they approved it: the program it became is what they
		// approved, so the checks below, of what approval saw, don't apply.
		if ck.req.ApprovedBy == perms.ByUser && len(ck.req.Asked) > 0 {
			return next(ctx, args)
		}
		if ck.req.ApprovedBy == perms.ByMode && ck.req.Sandboxed { // auto mode, inside the sandbox: the sandbox is the boundary
			return next(ctx, args)
		}
		for _, a := range args[1:] {
			if f := perms.RunFlag(path, a); f != "" && !ck.runFlags[path+" "+f] {
				return ck.stop("%s %s runs another program, and approval did not see it (the command expanded to: %s)",
					filepath.Base(path), f, strings.Join(args, " "))
			}
		}
		if !ck.approved[path] {
			if !ck.engine.Covers(expanded) {
				return ck.stop("%s was not approved: it appeared only when the command ran (%s)",
					path, strings.Join(args, " "))
			}
		}
		// A program runs with the variables that change what programs run (perms.ChangesPrograms) as approved: as in
		// tack's own environment, unless the user approved this command and saw this program in it.
		if !(ck.req.ApprovedBy == perms.ByUser && ck.approved[path]) {
			if changed := changedEnv(hc.Env); changed != "" {
				return ck.stop("%s would run with %s, which approval did not see; a variable that changes what "+
					"programs run is approved only by the user, for the command that sets it", filepath.Base(path), changed)
			}
		}
		return next(ctx, args)
	}
}

// open checks a file the shell itself opens for a redirection: writes must stay inside the repository, and the
// secrets deny-list is never read. Programs' own file access is the sandbox's to enforce.
func (ck *checker) open(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	hc := interp.HandlerCtx(ctx)
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(hc.Dir, p)
	}
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0
	if write && p != "/dev/null" {
		if err := ck.everyMode(ck.engine.PathAsks(p), "a redirection to "+path); err != nil {
			return nil, err
		}
	}
	switch {
	case p == "/dev/null":
	case write && perms.Outside(ck.req.Root, p):
		return nil, ck.stop("a redirection writes outside the repository: %s (this checks the shell's redirections "+
			"only; the sandbox enforces where programs write)", path)
	case perms.IsSecret(p):
		return nil, ck.stop("a redirection opens %s, which is on the secrets deny-list", path)
	}
	return interp.DefaultOpenHandler()(ctx, path, flag, perm)
}

// changedEnv returns, as NAME=value, the first exported variable that changes what programs run and differs from
// tack's own environment (the child's, which it inherits from the daemon), or "".
func changedEnv(env expand.Environ) string {
	var out string
	env.Each(func(name string, vr expand.Variable) bool {
		if vr.Exported && vr.Kind == expand.String && perms.ChangesPrograms(name) && vr.Str != os.Getenv(name) {
			out = name + "=" + vr.Str
			return false
		}
		return true
	})
	return out
}

type approvalKey struct{}

// WithApproval tells Run who allowed the command: perms.ByUser, perms.ByRule or perms.ByMode. Without it Run treats
// the command as allowed by a rule, the strictest.
func WithApproval(ctx context.Context, by string) context.Context {
	return context.WithValue(ctx, approvalKey{}, by)
}

func approvedBy(ctx context.Context) string {
	if by, ok := ctx.Value(approvalKey{}).(string); ok && by != "" {
		return by
	}
	return perms.ByRule
}
