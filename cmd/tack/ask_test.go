package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/core"
	"github.com/townsendmerino/tack/internal/proto"
)

// TestAskEndsWhenTheDaemonGoes: a daemon that takes the prompt and then goes away mid-turn (a crash, a second signal)
// ends ask with an error saying so, rather than leaving it waiting for a turn.end that will not come.
func TestAskEndsWhenTheDaemonGoes(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go func() {
		defer b.Close()
		sc, enc := bufio.NewScanner(b), json.NewEncoder(b)
		for sc.Scan() {
			var req proto.Request
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				continue
			}
			var result any = struct{}{}
			switch req.Method {
			case proto.MethodSessionNew:
				result = proto.SessionNewResult{Session: "s_1"}
			case proto.MethodSessionPrompt:
				result = proto.SessionPromptResult{Seq: 2}
			}
			r, _ := json.Marshal(result)
			enc.Encode(proto.Response{JSONRPC: "2.0", ID: req.ID, Result: r})
			if req.Method == proto.MethodSessionPrompt {
				return // gone mid-turn
			}
		}
	}()
	var stdout, stderr bytes.Buffer
	code := make(chan int, 1)
	go func() {
		code <- ask(context.Background(), core.NewClient(a), "/w", "q", proto.ModePlan, false, true, &stdout, &stderr)
	}()
	select {
	case c := <-code:
		if c != exitError || !strings.Contains(stderr.String(), "connection") {
			t.Fatalf("ask = %d, stderr %q; want an error naming the lost connection", c, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask still waits for the turn.end of a daemon that has gone")
	}
}

// TestTheDaemonIsGivenAnAbsoluteWorkspace: a relative --workspace is this process's, so it is made absolute here; the
// daemon runs in another directory.
func TestTheDaemonIsGivenAnAbsoluteWorkspace(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := clientWorkspace("sub")
	if err != nil || got != filepath.Join(wd, "sub") {
		t.Fatalf("clientWorkspace(%q) = %q, %v; want %q", "sub", got, err, filepath.Join(wd, "sub"))
	}
	if got, err := clientWorkspace(""); err != nil || !filepath.IsAbs(got) {
		t.Fatalf("clientWorkspace(\"\") = %q, %v; want the git root, absolute", got, err)
	}
}

// TestASpawnWaitsOutADrain: a client that starts a daemon while the last one drains gets the new socket only once the
// old daemon lets go of the lock, after its grace window; so the wait for the socket covers the configured grace, as
// tack daemon stop's wait does.
func TestASpawnWaitsOutADrain(t *testing.T) {
	for _, grace := range []time.Duration{10 * time.Second, time.Minute} {
		if w := drainWait(grace); w < grace+5*time.Second {
			t.Errorf("drainWait(%s) = %s; want the grace and the drain's 5s to record cancelled turns, at least", grace, w)
		}
	}
}
