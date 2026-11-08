package daemon

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// procStart is when pid started, as an opaque string: two processes with the same pid have different ones. It is
// field 22 of /proc/<pid>/stat, in clock ticks since boot; the fields are counted after the command's closing
// parenthesis, since the command may hold spaces.
func procStart(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return "", errors.New("daemon: /proc stat has no command")
	}
	f := strings.Fields(string(b[i+1:])) // from field 3, the state
	if len(f) < 20 {
		return "", errors.New("daemon: /proc stat is short")
	}
	return f[19], nil
}
