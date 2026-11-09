package daemon

import "github.com/townsendmerino/tack/internal/proc"

// procInfo is proc.Info: when pid started, and whether it still runs.
func procInfo(pid int) (string, bool, error) { return proc.Info(pid) }
