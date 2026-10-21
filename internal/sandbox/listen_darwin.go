//go:build darwin

package sandbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// The kernel's TCP socket table, as sysctl net.inet.tcp.pcblist_n returns it: an xinpgen header, then one record per
// socket made of tagged items, each a 32-bit length and a 32-bit kind followed by its body and padded to 8 bytes, then
// a closing xinpgen. The layouts are XNU's (bsd/netinet/in_pcb.h); the public SDK does not declare them. Only the
// XSO_INPCB item is read, and only its two ports: inp_fport at byte 16 and inp_lport at byte 18, both in network order.
// TestListeningTCPPortsMatchesNetstat holds the parse to netstat's reading of the same table.
const (
	pcbListSysctl = "net.inet.tcp.pcblist_n"
	xinpgenLen    = 24   // u32 xig_len, u32 xig_count, u64 xig_gen, u64 xig_sogen
	xsoInpcb      = 0x10 // XSO_INPCB
	inpFportOff   = 16
	inpLportOff   = 18
)

// ListeningTCPPorts is every local TCP port the kernel holds a socket for that has a local port and no peer: every
// listener, on any address (one on all addresses answers loopback too), and the rare socket bound but not yet listening,
// which costs nothing to deny. The ports are sorted and distinct. An error means the table could not be read as
// expected; the caller must then leave loopback closed (Config.Loopback).
func ListeningTCPPorts() ([]int, error) {
	buf, err := unix.SysctlRaw(pcbListSysctl)
	if err != nil {
		return nil, fmt.Errorf("sysctl %s: %w", pcbListSysctl, err)
	}
	return parsePCBList(buf)
}

// parsePCBList reads the ports out of a pcblist_n buffer, refusing one whose framing is not as described above.
func parsePCBList(buf []byte) ([]int, error) {
	le := binary.LittleEndian
	if len(buf) < 2*xinpgenLen || le.Uint32(buf) != xinpgenLen {
		return nil, errors.New("pcblist_n: no xinpgen header")
	}
	seen := map[int]bool{}
	off, end := xinpgenLen, len(buf)-xinpgenLen
	for off < end {
		if off+8 > end {
			return nil, fmt.Errorf("pcblist_n: item header past the end at byte %d", off)
		}
		n, kind := int(le.Uint32(buf[off:])), le.Uint32(buf[off+4:])
		if n < 8 || off+n > end {
			return nil, fmt.Errorf("pcblist_n: item of %d bytes at byte %d", n, off)
		}
		if kind == xsoInpcb {
			if n < inpLportOff+2 {
				return nil, fmt.Errorf("pcblist_n: inpcb item of %d bytes", n)
			}
			fport := binary.BigEndian.Uint16(buf[off+inpFportOff:])
			lport := binary.BigEndian.Uint16(buf[off+inpLportOff:])
			if lport != 0 && fport == 0 {
				seen[int(lport)] = true
			}
		}
		off += (n + 7) &^ 7
	}
	if off != end || le.Uint32(buf[end:]) != xinpgenLen {
		return nil, errors.New("pcblist_n: records do not end at the closing xinpgen")
	}
	ports := make([]int, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports, nil
}

// LoopbackPorts is what a loopback profile is made from: the kernel's ephemeral port range, from which a server that
// binds port 0 gets its port, and the ports listening when the command starts (ListeningTCPPorts).
type LoopbackPorts struct {
	First, Last int
	Listening   []int
}

// ReadLoopbackPorts reads the ephemeral range (net.inet.ip.portrange.first and .last) and the listening ports now.
func ReadLoopbackPorts() (*LoopbackPorts, error) {
	first, err := unix.SysctlUint32("net.inet.ip.portrange.first")
	if err != nil {
		return nil, err
	}
	last, err := unix.SysctlUint32("net.inet.ip.portrange.last")
	if err != nil {
		return nil, err
	}
	if first == 0 || last < first || last > 65535 {
		return nil, fmt.Errorf("ephemeral port range %d-%d", first, last)
	}
	listening, err := ListeningTCPPorts()
	if err != nil {
		return nil, err
	}
	return &LoopbackPorts{First: int(first), Last: int(last), Listening: listening}, nil
}

// outboundRuleSize is how many ports one allowance names; a single rule naming the whole range does not compile.
const outboundRuleSize = 256

// outbound is the profile's allowances to connect on loopback: each port of the range by name, leaving out the
// listening ports when exclude is set.
func (p *LoopbackPorts) outbound(exclude bool) []string {
	skip := map[int]bool{}
	if exclude {
		for _, l := range p.Listening {
			skip[l] = true
		}
	}
	var rules []string
	var b strings.Builder
	n := 0
	flush := func() {
		if n > 0 {
			rules = append(rules, "(allow network-outbound"+b.String()+")")
			b.Reset()
			n = 0
		}
	}
	for port := p.First; port <= p.Last; port++ {
		if skip[port] {
			continue
		}
		fmt.Fprintf(&b, ` (remote tcp "localhost:%d")`, port)
		if n++; n == outboundRuleSize {
			flush()
		}
	}
	flush()
	return rules
}
