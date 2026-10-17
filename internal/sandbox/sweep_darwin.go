//go:build darwin

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// A process cannot leave its sandbox, so the sandbox is what identifies the processes a command started, whatever they
// did to their session, process group, parent or environment. Every profile allows mach-lookup of two names under
// markPrefix that every other name under it is denied: Config.Group (MarkAll unless a test sets its own), shared
// by every command, and the command's own Config.Mark. sandbox_check asks the kernel what a process's profile decides for a name, so Find can tell tack's
// processes from every other process, sandboxed or not.
const (
	markPrefix = "dev.tack.mark."
	// MarkAll is the default Config.Group: Sweep(MarkAll) reaches every process tack's commands started (tack halt).
	// Tests never sweep it.
	MarkAll = "all"
	// markControl is a name under the prefix that no profile allows. A profile that allows it (one without section
	// mark, or one allowing all lookups) is not tack's.
	markControl = "control"
)

// sandbox_check's filter types (sandbox/private.h, "enum sandbox_filter_type").
const (
	filterNone       = 0
	filterGlobalName = 2
)

const zombie = 5 // SZOMB, sys/proc.h: dead and not yet reaped; it can no longer run, and a kill does nothing

var (
	sandboxCheckOnce sync.Once
	sandboxCheckFn   uintptr
	sandboxCheckErr  error
)

// check asks whether pid's sandbox profile allows mach-lookup of name; with name "" it asks whether pid is sandboxed
// at all. It returns 0 for allowed (or not sandboxed), 1 for denied, -1 for an error such as an exited pid.
//
// sandbox_check is variadic, and on darwin/arm64 a variadic argument is passed on the stack, not in a register: the
// call pads x3–x7 so that purego places the name ninth, which is the first stack slot. Other architectures are refused.
func check(pid int, name string) (int32, error) {
	if runtime.GOARCH != "arm64" {
		return 0, errors.New("sandbox: finding a command's processes is built for darwin/arm64 only")
	}
	sandboxCheckOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			sandboxCheckErr = err
			return
		}
		sandboxCheckFn, sandboxCheckErr = purego.Dlsym(lib, "sandbox_check")
	})
	if sandboxCheckErr != nil {
		return 0, fmt.Errorf("sandbox: sandbox_check: %w", sandboxCheckErr)
	}
	if name == "" {
		r, _, _ := purego.SyscallN(sandboxCheckFn, uintptr(pid), 0, filterNone)
		return int32(r), nil
	}
	op, n := append([]byte("mach-lookup"), 0), append([]byte(name), 0)
	r, _, _ := purego.SyscallN(sandboxCheckFn, uintptr(pid), uintptr(unsafe.Pointer(&op[0])), filterGlobalName,
		0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&n[0])))
	runtime.KeepAlive(op)
	runtime.KeepAlive(n)
	return int32(r), nil
}

// Find returns the live processes of this user whose sandbox carries mark: a Config.Group (MarkAll for every process
// inside any sandbox tack made), or a Config.Mark for one command's.
func Find(mark string) ([]int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	var out []int
	for _, p := range procs {
		pid := int(p.Proc.P_pid)
		if pid == os.Getpid() || p.Proc.P_stat == zombie {
			continue
		}
		if r, err := check(pid, ""); err != nil {
			return nil, err
		} else if r != 1 {
			continue // not sandboxed, or gone
		}
		ours, _ := check(pid, markPrefix+mark)
		control, _ := check(pid, markPrefix+markControl)
		if ours == 0 && control == 1 {
			out = append(out, pid)
		}
	}
	return out, nil
}

// Sweep kills every process Find(mark) returns, and repeats until none is left, so a process forking as it is swept
// is caught on a later pass. It returns how many distinct processes it killed.
func Sweep(mark string) (int, error) {
	killed := map[int]bool{}
	for range 100 {
		pids, err := Find(mark)
		if err != nil {
			return len(killed), err
		}
		if len(pids) == 0 {
			return len(killed), nil
		}
		for _, pid := range pids {
			if unix.Kill(pid, unix.SIGKILL) == nil {
				killed[pid] = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return len(killed), fmt.Errorf("sandbox: processes marked %q were still running after 100 sweeps", mark)
}
