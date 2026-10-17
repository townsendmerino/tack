// Package halt is tack's one-command stop, after goinfer's kill switch (DESIGN §6.6, "Halt"). Halting creates the HALT
// file in tack's state directory; whatever runs turns watches it, cancels every running turn and refuses new ones until
// the file is gone. `tack halt` writes it with who halted; a script may simply create it. Sandboxed commands cannot
// reach the file (internal/sandbox, section tack), so nothing a model runs can lift a halt.
package halt

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

// FileName is the HALT file's name in the state directory.
const FileName = "HALT"

// Interval is how often Watch reads the file: goinfer's halt file uses the same.
const Interval = 250 * time.Millisecond

// Path is the HALT file in stateDir.
func Path(stateDir string) string { return filepath.Join(stateDir, FileName) }

// Halt creates the HALT file, recording by and reason, and reports whether tack was already halted (the file is then
// rewritten).
func Halt(stateDir, by, reason string) (bool, error) {
	was, err := Check(stateDir)
	if err != nil {
		return false, err
	}
	b, err := json.Marshal(proto.DaemonHalted{Trigger: proto.HaltByCommand, By: by, Reason: reason})
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return false, err
	}
	tmp := Path(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return false, err
	}
	return was != nil, os.Rename(tmp, Path(stateDir))
}

// Resume removes the HALT file and reports whether tack was halted.
func Resume(stateDir string) (bool, error) {
	err := os.Remove(Path(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Check returns the halt the HALT file records, or nil when there is none. A file that is not tack's own (an empty one
// a script touched) is a halt by file.
func Check(stateDir string) (*proto.DaemonHalted, error) {
	b, err := os.ReadFile(Path(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h proto.DaemonHalted
	if json.Unmarshal(b, &h) != nil || h.Trigger != proto.HaltByCommand {
		h = proto.DaemonHalted{Trigger: proto.HaltByFile}
	}
	return &h, nil
}

// Watch reads the HALT file every interval until ctx ends, calling onHalt when a halt appears and onResume when it goes.
// The first read calls onHalt if tack is already halted. A file it cannot read counts as a halt, with the error as its
// reason.
func Watch(ctx context.Context, stateDir string, interval time.Duration, onHalt func(proto.DaemonHalted), onResume func()) {
	halted := false
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		h, err := Check(stateDir)
		if err != nil {
			h = &proto.DaemonHalted{Trigger: proto.HaltByFile, Reason: "the HALT file could not be read: " + err.Error()}
		}
		switch {
		case h != nil && !halted:
			halted = true
			onHalt(*h)
		case h == nil && halted:
			halted = false
			onResume()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Halted is the cause a halt cancels a turn's context with (context.WithCancelCause): the loop records it as
// daemon.halted and ends the turn with stop reason halted.
type Halted struct{ proto.DaemonHalted }

func (h *Halted) Error() string {
	if h.Reason != "" {
		return "tack was halted: " + h.Reason
	}
	return "tack was halted"
}
