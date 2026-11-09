package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// writeLog writes a session of five read calls and a compaction, padded to about extra bytes, as dir/name.
func writeLog(t *testing.T, dir, name string, compacted bool, extra int) {
	t.Helper()
	var b strings.Builder
	seq := int64(0)
	add := func(typ proto.EventType, payload any) {
		seq++
		ev, err := proto.NewEvent(seq, time.Unix(0, 0), typ, 0, payload)
		if err != nil {
			t.Fatal(err)
		}
		line, _ := json.Marshal(ev)
		b.Write(append(line, '\n'))
	}
	add(proto.EvUserMessage, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("fix it")}})
	for i := range 5 {
		id := fmt.Sprintf("c%d", i)
		add(proto.EvAssistantMessage, proto.AssistantMessage{Request: i + 1, Content: []proto.ModelBlock{{Type: "tool_use", ID: id, Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)}}})
		add(proto.EvToolFinished, proto.ToolFinished{ID: id, Outcome: proto.OutcomeOK, Result: "package a" + strings.Repeat(" ", extra/5)})
	}
	if compacted {
		add(proto.EvCompaction, proto.Compaction{Request: 5, Trigger: "window", Masked: []proto.MaskedResult{}})
	}
	os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
	os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644)
}

func headed() string {
	var b strings.Builder
	for _, h := range loop.SummaryHeadings {
		b.WriteString("## " + h + "\n\n(none)\n\n")
	}
	return b.String()
}

// fakeServer serves replies in order; noAlias makes it list no thinking-off alias.
func fakeServer(t *testing.T, noAlias bool, replies ...goinfertest.Reply) (*httptest.Server, *goinfertest.Scripted) {
	t.Helper()
	sc := goinfertest.NewScripted("m", 16384, replies...)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if noAlias && r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "m", "object": "model"}}})
			return
		}
		sc.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, sc
}

func logs(t *testing.T) string {
	dir := t.TempDir()
	for i := range 5 {
		writeLog(t, dir, fmt.Sprintf("c%d/events.jsonl", i), true, 1000*(i+1))
	}
	writeLog(t, dir, "big-but-uncompacted/events.jsonl", false, 50000)
	return dir
}

func TestTheRegisteredRule(t *testing.T) {
	for _, c := range []struct {
		kept int
		want string
	}{{5, "VERDICT: PASS"}, {4, "VERDICT: PASS"}, {3, "VERDICT: INCONCLUSIVE"}, {2, "VERDICT: INCONCLUSIVE"}, {1, "VERDICT: FAIL"}, {0, "VERDICT: FAIL"}} {
		if got := verdictFor(c.kept, 5, "m-fast"); !strings.HasPrefix(got, c.want) {
			t.Errorf("%d of 5: %q, want %s", c.kept, got, c.want)
		}
	}
}

func TestEachBand(t *testing.T) {
	junk := goinfertest.Says("Here is a summary: it went fine.")
	good := goinfertest.Says(headed())
	for name, c := range map[string]struct {
		replies []goinfertest.Reply
		code    int
		want    string
	}{
		"proceed": {[]goinfertest.Reply{good, good, good, good, junk}, 0, "4 of 5"},
		"parked":  {[]goinfertest.Reply{good, good, good, junk, junk}, 2, "parked"},
		"held":    {[]goinfertest.Reply{good, junk, junk, junk, junk}, 1, "held"},
	} {
		ts, sc := fakeServer(t, false, c.replies...)
		var out bytes.Buffer
		replies := filepath.Join(t.TempDir(), "replies.txt")
		code := run([]string{"-server", ts.URL, "-logs", logs(t), "-out", replies}, &out)
		if code != c.code || !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: exit %d, want %d:\n%s", name, code, c.code, out.String())
		}
		if len(sc.Requests) != 5 || strings.Contains(out.String(), "big-but-uncompacted") {
			t.Errorf("%s: %d requests, want 5, and the uncompacted log skipped:\n%s", name, len(sc.Requests), out.String())
		}
		if b, _ := os.ReadFile(replies); strings.Count(string(b), "== ") != 5 {
			t.Errorf("%s: -out has %d replies, want 5", name, strings.Count(string(b), "== "))
		}
	}
}

// TestTheLargestComeFirst: with more sessions than -n, the largest are summarized.
func TestTheLargestComeFirst(t *testing.T) {
	ts, _ := fakeServer(t, false, goinfertest.Says(headed()), goinfertest.Says(headed()))
	var out bytes.Buffer
	run([]string{"-server", ts.URL, "-logs", logs(t), "-n", "2"}, &out)
	if !strings.Contains(strings.SplitN(out.String(), "\n", 2)[0], "c4") ||
		!strings.Contains(out.String(), "c3") || strings.Contains(out.String(), "c0") {
		t.Errorf("want c4 then c3:\n%s", out.String())
	}
}

func TestNoAliasOrTooFewLogsIsInconclusive(t *testing.T) {
	ts, sc := fakeServer(t, true)
	var out bytes.Buffer
	if code := run([]string{"-server", ts.URL, "-logs", logs(t)}, &out); code != 2 || !strings.Contains(out.String(), "-fast-alias") || len(sc.Requests) != 0 {
		t.Errorf("no alias: exit %d, %d requests:\n%s", code, len(sc.Requests), out.String())
	}
	ts2, _ := fakeServer(t, false)
	out.Reset()
	if code := run([]string{"-server", ts2.URL, "-logs", logs(t), "-n", "6"}, &out); code != 2 || !strings.Contains(out.String(), "5 sessions") {
		t.Errorf("too few logs: exit %d:\n%s", code, out.String())
	}
}

// TestAModelNotListedIsNamed: an unmatched -model says it is not listed and what is, rather than that there is no alias.
func TestAModelNotListedIsNamed(t *testing.T) {
	ts, sc := fakeServer(t, false)
	var out bytes.Buffer
	code := run([]string{"-server", ts.URL, "-logs", logs(t), "-model", "nope"}, &out)
	if code != 2 || !strings.Contains(out.String(), `model "nope" not listed: the server lists m`) || len(sc.Requests) != 0 {
		t.Errorf("exit %d, %d requests:\n%s", code, len(sc.Requests), out.String())
	}
}
