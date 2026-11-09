package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/townsendmerino/tack/internal/proc"
)

// childRecordName is the file in a supervisor's Dir naming the goinfer-serve it started, while that child runs.
const childRecordName = "child.json"

// childRecord names a running child: its pid, when that process started (proc.Info), so a later process reusing the
// pid is not taken for it, and its API port.
type childRecord struct {
	PID       int    `json:"pid"`
	ProcStart string `json:"proc_start"`
	Port      int    `json:"port"`
}

// writeChildRecord writes rec whole, through a rename. Only the supervisor owning dir writes it.
func writeChildRecord(dir string, rec childRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, childRecordName)
	if err := os.WriteFile(path+".tmp", append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// removeChildRecord removes dir's record if it still names pid: a child that exited is no longer there to stop.
func removeChildRecord(dir string, pid int) {
	if rec, ok := readChildRecord(dir); ok && rec.PID == pid {
		_ = os.Remove(filepath.Join(dir, childRecordName))
	}
}

func readChildRecord(dir string) (childRecord, bool) {
	var rec childRecord
	b, err := os.ReadFile(filepath.Join(dir, childRecordName))
	if err != nil || json.Unmarshal(b, &rec) != nil || rec.PID <= 0 || rec.ProcStart == "" {
		return childRecord{}, false
	}
	return rec, true
}

// staleStopWait is how long stopStaleChild gives a stale child after SIGTERM, and then after SIGKILL; a variable so
// tests can shorten it.
var staleStopWait = 10 * time.Second

// stopStaleChild stops the goinfer-serve that dir's record names, if that process still runs: one whose daemon was
// SIGKILLed or crashed, and so never stopped it or applied its idle exit. A second child beside it would hold a second
// copy of the model in memory (DESIGN §8.1). The child leads its own process group (Setpgid), so the group is
// signalled: SIGTERM, then SIGKILL. A record naming a process that is gone, or a pid since reused, is just removed. It
// is an error only when the child will not stop.
func stopStaleChild(dir string) error {
	rec, ok := readChildRecord(dir)
	if !ok {
		return nil
	}
	path := filepath.Join(dir, childRecordName)
	same := func() bool {
		ps, live, err := proc.Info(rec.PID)
		return err == nil && live && ps == rec.ProcStart
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if !same() {
			return os.Remove(path)
		}
		if err := syscall.Kill(-rec.PID, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("models: stopping the goinfer-serve an earlier daemon left (pid %d): %w", rec.PID, err)
		}
		for deadline := time.Now().Add(staleStopWait); same() && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if same() {
		return fmt.Errorf("models: the goinfer-serve an earlier daemon left (pid %d) did not stop; not starting another beside it", rec.PID)
	}
	return os.Remove(path)
}

// childProcInfo is proc.Info; tests replace it.
var childProcInfo = proc.Info
