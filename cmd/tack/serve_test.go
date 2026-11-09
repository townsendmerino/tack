package main

import (
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestServeListenerTakesSystemdsSocket: under socket activation tack serve answers on the socket systemd passed, at
// the fd it names, and only when LISTEN_PID is this process; otherwise it listens on [serve] listen.
func TestServeListenerTakesSystemdsSocket(t *testing.T) {
	passed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer passed.Close()
	f, err := passed.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	env := map[string]string{"LISTEN_PID": "4242", "LISTEN_FDS": "1", "LISTEN_FDNAMES": "tack-serve.socket"}
	var unsets []string
	unset := func(k string) error { unsets = append(unsets, k); return nil }
	ln, err := serveListener(func(k string) string { return env[k] }, unset, 4242, f.Fd(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ln.Addr().String() != passed.Addr().String() {
		t.Fatalf("listening on %s, want systemd's socket %s", ln.Addr(), passed.Addr())
	}
	// As sd_listen_fds(3) with unset_environment: the children it starts (goinfer-serve) do not see them.
	if got := strings.Join(unsets, " "); got != "LISTEN_PID LISTEN_FDS LISTEN_FDNAMES" {
		t.Fatalf("unset %q after taking the socket; want LISTEN_PID LISTEN_FDS LISTEN_FDNAMES", got)
	}
	if _, err := serveListener(func(k string) string { return env[k] }, unset, 1, f.Fd(), ""); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("activation meant for another process, and no listen: %v, want refused", err)
	}
	env["LISTEN_FDS"] = "2"
	if _, err := serveListener(func(k string) string { return env[k] }, unset, 4242, f.Fd(), ""); err == nil {
		t.Fatal("two sockets from systemd were taken")
	}
	own, err := serveListener(func(string) string { return "" }, func(string) error { return nil }, os.Getpid(), 0, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	own.Close()
}

// TestServeDetachIsBusyWhileTheTimingLockIsHeld: tack serve -detach, as the Mac runs it over ssh, exits 75 and
// starts nothing while a timed run holds goinfer's timing lock.
func TestServeDetachIsBusyWhileTheTimingLockIsHeld(t *testing.T) {
	dir := t.TempDir()
	lock := dir + "/timing.lock"
	held, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := flockEx(held); err != nil {
		t.Fatal(err)
	}
	cfg := dir + "/config.toml"
	os.WriteFile(dir+"/key", []byte("k\n"), 0o600)
	os.WriteFile(cfg, []byte("state_dir = \""+dir+"/s\"\n[servers.local]\nkind = \"goinfer-child\"\nbinary = \"/nonexistent\"\n"+
		"model_path = \"/m.gguf\"\nctx = 1024\n[serve]\nserver = \"local\"\nkey_file = \""+dir+"/key\"\nlisten = \"127.0.0.1:0\"\n"+
		"timing_lock = \""+lock+"\"\n"), 0o600)
	var out, errb strings.Builder
	if code := dispatch([]string{"serve", "-detach", "-config", cfg}, &out, &errb); code != exitBusy || !strings.Contains(errb.String(), "busy") {
		t.Fatalf("serve -detach under the timing lock: exit %d, %q; want %d and busy", code, errb.String(), exitBusy)
	}
}

func flockEx(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
