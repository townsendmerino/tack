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
// markPrefix that every other name under it is denied: the session's Config.Group and the command's own Config.Mark,
// both random. sandbox_check asks the kernel what a process's profile decides for a name, so Carries can tell a
// session's processes from every other process, sandboxed or not. Plan adds the second, independent condition: the
// process started after its session or command did.
const (
	markPrefix = "dev.tack.mark."
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

// Processes returns this user's live processes, except this one.
func Processes() ([]Proc, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	var out []Proc
	for _, kp := range kps {
		pid := int(kp.Proc.P_pid)
		if pid == os.Getpid() || kp.Proc.P_stat == zombie {
			continue
		}
		st := kp.Proc.P_starttime
		out = append(out, Proc{PID: pid, Name: unix.ByteSliceToString(kp.Proc.P_comm[:]),
			Start: time.Unix(st.Sec, int64(st.Usec)*1000)})
	}
	return out, nil
}

// Carries reports whether p is sandboxed and its profile allows mark and denies the control name.
func Carries(p Proc, mark string) bool {
	if r, err := check(p.PID, ""); err != nil || r != 1 {
		return false // not sandboxed, gone, or the check is unavailable
	}
	ours, _ := check(p.PID, markPrefix+mark)
	control, _ := check(p.PID, markPrefix+markControl)
	return ours == 0 && control == 1
}

// Sweep kills what Plan selects for t, and repeats until nothing is left, so a process forking as it is swept is
// caught on a later pass. A refused plan kills nothing: the report says why and lists the processes. With dryRun it
// plans once and kills nothing.
func Sweep(t Target, dryRun bool) (Report, error) {
	if t.Mark == "" || t.Since.IsZero() {
		return Report{}, errors.New("sandbox: a sweep needs a mark and a start time")
	}
	if _, err := check(os.Getpid(), ""); err != nil {
		return Report{}, err
	}
	killed := map[int]bool{}
	for range 100 {
		procs, err := Processes()
		if err != nil {
			return Report{Killed: len(killed)}, err
		}
		r := Plan(t, procs, Carries)
		r.Killed = len(killed)
		if r.Refused != "" || dryRun || len(r.Kill) == 0 {
			return r, nil
		}
		for _, p := range r.Kill {
			if unix.Kill(p.PID, unix.SIGKILL) == nil {
				killed[p.PID] = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return Report{Killed: len(killed)}, fmt.Errorf("sandbox: processes marked %q were still running after 100 sweeps", t.Mark)
}
