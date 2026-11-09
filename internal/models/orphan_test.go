package models

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proc"
)

// fakeDaemon is the test binary standing in for tack's daemon (TACK_FAKE_DAEMON=<dir>): it supervises a fake
// goinfer-serve in dir, has it load, prints "child <pid>" and waits to be killed.
func fakeDaemon(dir string) {
	os.Setenv("TACK_FAKE_GOINFER", "1") // for the child it starts, which inherits this environment
	os.Unsetenv("TACK_FAKE_DAEMON")
	s, err := NewSupervisor(ChildConfig{Binary: os.Args[0], ModelPath: "/m/model.gguf", Model: "m", Ctx: 8192, Dir: dir,
		IdleUnload: time.Hour, IdleExit: time.Hour, Pressure: func() (Pressure, error) { return PressureNormal, nil }})
	if err == nil {
		_, err = s.Ensure(context.Background())
	}
	if err != nil {
		fmt.Println("error", err)
		os.Exit(1)
	}
	fmt.Println("child", s.cmd.Process.Pid)
	select {}
}

// running says whether pid is a live process, not a zombie.
func running(pid int) bool {
	_, live, err := proc.Info(pid)
	return err == nil && live
}

// TestAChildOutlivingItsDaemonIsStopped: a daemon that is SIGKILLed, or crashes, leaves its goinfer-serve running with
// the model loaded; its idle exit came only from the dead daemon's Tick. The next daemon's supervisor, starting its
// own child in the same directory, stops that one first, so the Mac never holds two copies of the model.
func TestAChildOutlivingItsDaemonIsStopped(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "tksup")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := exec.Command(os.Args[0])
	d.Env = append(os.Environ(), "TACK_FAKE_DAEMON="+dir)
	out, err := d.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(out).ReadString('\n')
	old, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(line), "child "))
	if err != nil {
		d.Process.Kill()
		t.Fatalf("the fake daemon said %q; want its child's pid", line)
	}
	t.Cleanup(func() { syscallKill(old) }) // whatever the outcome, no stray child outlives the test
	d.Process.Kill()                       // SIGKILL: no Stop, no idle tick
	d.Wait()
	time.Sleep(100 * time.Millisecond)
	if !running(old) {
		t.Fatal("the child died with its daemon; this test needs one that outlives it")
	}

	s, _, _ := newSupDir(t, dir)
	if _, err := s.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.cmd.Process.Pid == old {
		t.Fatal("the new supervisor's child is the old one")
	}
	for deadline := time.Now().Add(15 * time.Second); running(old); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the child of the killed daemon (pid %d) still runs beside the new one (pid %d)", old, s.cmd.Process.Pid)
		}
	}
}

// syscallKill SIGKILLs pid if it still runs.
func syscallKill(pid int) {
	if running(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// TestAChildRecordNamingAReusedPidStopsNothing: a record whose pid now names another process (its start differs) is
// stale, not a child: it is removed and that process is left alone.
func TestAChildRecordNamingAReusedPidStopsNothing(t *testing.T) {
	dir := t.TempDir()
	other := exec.Command("sleep", "30")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // a group of its own, as a child's would be
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })
	if err := writeChildRecord(dir, childRecord{PID: other.Process.Pid, ProcStart: "not-its-start", Port: 1}); err != nil {
		t.Fatal(err)
	}
	if err := stopStaleChild(dir); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if !running(other.Process.Pid) {
		t.Fatal("a process that only reused the record's pid was stopped")
	}
	if _, ok := readChildRecord(dir); ok {
		t.Fatal("the stale record is still there")
	}
}

// TestAStoppedChildLeavesNoRecord: the record lasts while the child runs; Stop, waiting for its exit, removes it.
func TestAStoppedChildLeavesNoRecord(t *testing.T) {
	s, _, _ := newSup(t, "/m/model.gguf")
	if _, err := s.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec, ok := readChildRecord(s.cfg.Dir); !ok || rec.PID != s.cmd.Process.Pid {
		t.Fatalf("the record while the child runs: %+v, %v; want pid %d", rec, ok, s.cmd.Process.Pid)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, ok := readChildRecord(s.cfg.Dir); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the record outlived the child Stop ended")
		}
	}
}
