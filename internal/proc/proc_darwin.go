package proc

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// szomb is a zombie's p_stat (sys/proc.h).
const szomb = 5

// Info is when pid started, as an opaque string two processes with the same pid never share, and whether it is
// still running: a zombie, exited and not yet reaped, is not.
func Info(pid int) (start string, live bool, err error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", false, err
	}
	if int(kp.Proc.P_pid) != pid {
		return "", false, syscall.ESRCH
	}
	st := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", st.Sec, st.Usec), kp.Proc.P_stat != szomb, nil
}
