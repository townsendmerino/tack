package daemon

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// procInfo is when pid started, as an opaque string two processes with the same pid never share, and whether it is
// still running: a zombie (state Z) or a dying process (X) is not. The start is field 22 of /proc/<pid>/stat, in
// clock ticks since boot, and the state field 3; fields are counted after the command's closing parenthesis, since
// the command may hold spaces.
func procInfo(pid int) (start string, live bool, err error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false, err
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return "", false, errors.New("daemon: /proc stat has no command")
	}
	f := strings.Fields(string(b[i+1:])) // from field 3, the state
	if len(f) < 20 {
		return "", false, errors.New("daemon: /proc stat is short")
	}
	return f[19], f[0] != "Z" && f[0] != "X", nil
}
