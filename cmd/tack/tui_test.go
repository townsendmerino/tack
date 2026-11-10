package main

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// TestTheScreenEndingExitsByWhy: a quit by the user exits 0 silently; a lost daemon connection, which makes the
// program's Run return nil, exits non-zero and says so; a program error exits non-zero with its text.
func TestTheScreenEndingExitsByWhy(t *testing.T) {
	var lost atomic.Bool
	var out bytes.Buffer
	if code := tuiExit(nil, &lost, &out); code != exitOK || out.Len() != 0 {
		t.Errorf("a quit: exit %d, stderr %q", code, out.String())
	}
	lost.Store(true)
	if code := tuiExit(nil, &lost, &out); code != exitError || !strings.Contains(out.String(), "connection to the daemon was lost") {
		t.Errorf("a lost connection: exit %d, stderr %q", code, out.String())
	}
	out.Reset()
	if code := tuiExit(errors.New("boom"), &lost, &out); code != exitError || !strings.Contains(out.String(), "boom") {
		t.Errorf("a program error: exit %d, stderr %q", code, out.String())
	}
}
