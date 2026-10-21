//go:build darwin

package sandbox

import (
	"net"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestListeningTCPPortsMatchesNetstat: the parse of the kernel's socket table finds this test's own listeners on
// 127.0.0.1, [::1] and all addresses, and every port netstat reads as LISTEN from the same table, so a change to XNU's
// layout shows here.
func TestListeningTCPPortsMatchesNetstat(t *testing.T) {
	var mine []int
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0", ":0"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		mine = append(mine, ln.Addr().(*net.TCPAddr).Port)
	}
	before, err := ListeningTCPPorts()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("netstat", "-anv", "-p", "tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	after, err := ListeningTCPPorts()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range mine {
		if !slices.Contains(before, p) {
			t.Errorf("this test's listener on port %d is not in %v", p, before)
		}
	}
	netstat := 0
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || f[5] != "LISTEN" {
			continue
		}
		p, err := strconv.Atoi(f[3][strings.LastIndex(f[3], ".")+1:])
		if err != nil {
			continue
		}
		netstat++
		if !slices.Contains(before, p) && !slices.Contains(after, p) {
			t.Errorf("netstat lists port %d as LISTEN; the parse does not (%v)", p, before)
		}
	}
	if netstat < len(mine) {
		t.Fatalf("netstat listed %d listeners, fewer than this test's own %d: its output was not read", netstat, len(mine))
	}
}

// TestParsePCBListRefusesABadTable: a buffer cut short, or whose items overrun it, is an error, never a short list.
func TestParsePCBListRefusesABadTable(t *testing.T) {
	for name, buf := range map[string][]byte{
		"empty":            nil,
		"header only":      append([]byte{24, 0, 0, 0}, make([]byte, 20)...),
		"item overrunning": append(append([]byte{24, 0, 0, 0}, make([]byte, 20)...), append([]byte{200, 0, 0, 0, 0x10, 0, 0, 0}, make([]byte, 40)...)...),
	} {
		if ports, err := parsePCBList(buf); err == nil {
			t.Errorf("%s: parsed as %v", name, ports)
		}
	}
}

// TestProfileLoopbackFailsClosed: with Loopback set but no ports read (Ports nil), the profile is the one without
// loopback. With them, it allows connecting to each port of the range by name except the listening ones, unless
// section listeners is dropped, and the whole profile stays well under the 1 MiB macOS allows for sandbox-exec's
// arguments.
func TestProfileLoopbackFailsClosed(t *testing.T) {
	c := Config{Workspace: t.TempDir(), Temp: t.TempDir(), GoCache: t.TempDir(), Home: t.TempDir(), Loopback: true}
	closed := Config{Workspace: c.Workspace, Temp: c.Temp, GoCache: c.GoCache, Home: c.Home}
	if Profile(c) != Profile(closed) {
		t.Errorf("Loopback with no ports read opened the network:\n%s", Profile(c))
	}
	c.Ports = &LoopbackPorts{First: 49152, Last: 65535, Listening: []int{5000, 50814}}
	p := Profile(c)
	if !strings.Contains(p, `(remote tcp "localhost:49152")`) || !strings.Contains(p, `(remote tcp "localhost:65535")`) ||
		strings.Contains(p, `"localhost:50814"`) || strings.Contains(p, `(remote tcp "localhost:*")`) {
		t.Errorf("the allowance is not the range by name less the listeners")
	}
	if len(p) > 700<<10 {
		t.Errorf("profile of %d bytes, too near the 1 MiB argument limit", len(p))
	}
	c.Without = []string{SecListeners}
	if !strings.Contains(Profile(c), `(remote tcp "localhost:50814")`) {
		t.Error("with listeners dropped, the listening port is not allowed, so the section is not what leaves it out")
	}
}
