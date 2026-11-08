package daemon

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// procStart is when pid started, as an opaque string: two processes with the same pid have different ones.
func procStart(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(kp.Proc.P_pid) != pid {
		return "", syscall.ESRCH
	}
	st := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", st.Sec, st.Usec), nil
}
