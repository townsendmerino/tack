package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The timing lock is goinfer's (scripts/timing_lock.py): one timed run per box. A timed run holds an exclusive flock on
// ~/.goinfer-timing.lock and writes a holder record with a token; it exports the token as GOINFER_TIMING_LOCK, and a
// child that finds its own token in the record proceeds under its parent's lock. A night.py job holds it for its
// whole command, so `tack eval models` run by night.py inherits it.
//
// Reading GOINFER_TIMING_LOCK is that protocol's, not a tack setting: it is how a holder's children are recognised.

// TimingLockPath is goinfer's lock file.
func TimingLockPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".goinfer-timing.lock"), nil
}

const tokenEnv = "GOINFER_TIMING_LOCK"

// TimingLock is a held (or inherited) lock.
type TimingLock struct {
	f         *os.File
	token     string
	Inherited bool
}

// Release drops the lock. An inherited lock is the parent's to drop.
func (l *TimingLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
	if os.Getenv(tokenEnv) == l.token {
		os.Unsetenv(tokenEnv)
	}
	l.f = nil
	return err
}

// HeldError names the run holding the lock.
type HeldError struct{ Holder string }

func (e *HeldError) Error() string { return "the timing lock is held by " + e.Holder }

type holderRecord struct {
	PID   int    `json:"pid"`
	Host  string `json:"host"`
	Label string `json:"label"`
	Token string `json:"token"`
	Cmd   string `json:"cmd"`
	Start string `json:"start"`
}

// AcquireTimingLock takes the lock at path for a timed run, or inherits it from a parent holding it. It never waits:
// another holder is a *HeldError naming it.
func AcquireTimingLock(path, label string) (*TimingLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		defer f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, err
		}
		h := readHolder(path)
		if mine := os.Getenv(tokenEnv); mine != "" && h.Token == mine {
			return &TimingLock{Inherited: true}, nil
		}
		return nil, &HeldError{Holder: describe(h)}
	}
	host, _ := os.Hostname()
	tok := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	rec, _ := json.Marshal(holderRecord{PID: os.Getpid(), Host: host, Label: label, Token: tok,
		Cmd: strings.Join(os.Args, " "), Start: time.Now().Format(time.RFC3339)})
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt(append(rec, '\n'), 0)
		_ = f.Sync()
	}
	os.Setenv(tokenEnv, tok) // children (goinfer-serve check, a nested harness) inherit it
	return &TimingLock{f: f, token: tok}, nil
}

// CheckTimingLock is for a day tool that loads the box without timing anything (the fit check): it reports a
// *HeldError when a timed run other than this process's own parent holds the lock, so the tool does not disturb it.
func CheckTimingLock(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		h := readHolder(path)
		if mine := os.Getenv(tokenEnv); mine != "" && h.Token == mine {
			return nil
		}
		return &HeldError{Holder: describe(h)}
	}
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func readHolder(path string) holderRecord {
	var h holderRecord
	b, _ := os.ReadFile(path)
	_ = json.Unmarshal(b, &h)
	return h
}

func describe(h holderRecord) string {
	if h.PID == 0 {
		return "an unknown run (no holder record)"
	}
	return fmt.Sprintf("pid %d on %s since %s (%s): %s", h.PID, h.Host, h.Start, h.Label, h.Cmd)
}
