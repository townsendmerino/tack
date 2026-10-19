package pilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProviderRequest is what @ai-sdk/openai-compatible sends for opencode's first request: the configured model's
// options spread into the body as written (dist/index.js, getArgs). TACK_FAKE_OPENCODE_DROP names an option it drops,
// as the provider drops its own topK.
func fakeProviderRequest(wd string) {
	b, _ := os.ReadFile(filepath.Join(wd, "opencode.json"))
	var cfg struct {
		Provider map[string]struct {
			Options map[string]string                           `json:"options"`
			Models  map[string]struct{ Options map[string]any } `json:"models"`
		} `json:"provider"`
	}
	_ = json.Unmarshal(b, &cfg)
	p := cfg.Provider["goinfer"]
	body := map[string]any{"model": "qwen", "messages": []any{map[string]string{"role": "user", "content": "hi"}}, "stream": true}
	for k, v := range p.Models["qwen"].Options {
		if k != os.Getenv("TACK_FAKE_OPENCODE_DROP") {
			body[k] = v
		}
	}
	j, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", p.Options["baseURL"]+"/chat/completions", bytes.NewReader(j))
	req.Header.Set("Authorization", "Bearer "+p.Options["apiKey"])
	if resp, err := http.DefaultClient.Do(req); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// fakeGoinfer streams two SSE chunks, and fails a request that does not carry the key.
func fakeGoinfer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-key" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"chunk\":1}\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCaptureRecordsRawBodies: the request and the streamed response are kept raw, with the method, path and status;
// the API key, a header, is not; and nothing is recorded while no run's directory is set.
func TestCaptureRecordsRawBodies(t *testing.T) {
	up := fakeGoinfer(t)
	c, err := StartCapture(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	post := func() string {
		req, _ := http.NewRequest("POST", c.URL+"/v1/chat/completions", strings.NewReader(`{"top_k":20}`))
		req.Header.Set("Authorization", "Bearer secret-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	post() // no directory yet: passed through, not recorded
	dir := filepath.Join(t.TempDir(), "requests")
	c.SetDir(dir)
	if got := post(); !strings.Contains(got, `{"chunk":1}`) || !strings.Contains(got, "[DONE]") {
		t.Fatalf("the stream through the proxy: %q", got)
	}
	c.SetDir("")
	req, _ := os.ReadFile(filepath.Join(dir, "0001.req.json"))
	resp, _ := os.ReadFile(filepath.Join(dir, "0001.resp"))
	meta, _ := os.ReadFile(filepath.Join(dir, "0001.meta"))
	if string(req) != `{"top_k":20}` || !strings.Contains(string(resp), "[DONE]") || !strings.HasPrefix(string(meta), "POST /v1/chat/completions 200 ttfb_ms=") {
		t.Fatalf("recorded %q / %q / %q", req, resp, meta)
	}
	if tm, ok := ReadTiming(filepath.Join(dir, "0001.meta")); !ok || tm.TotalMs < tm.TTFBms || tm.TotalMs < 0 {
		t.Fatalf("timing %+v %v from %q", tm, ok, meta)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if strings.Contains(string(b), "secret-key") {
			t.Fatalf("%s holds the API key", e.Name())
		}
	}
	if len(entries) != 3 {
		t.Fatalf("%d files recorded, want 3 (one request)", len(entries))
	}
}

// TestSamplingCheckThroughThePilot: with the recording, the job's first run must show every registered sampling
// field reaching goinfer. The provider-shaped fake passes them; when it drops top_k, the job stops after that run.
func TestSamplingCheckThroughThePilot(t *testing.T) {
	for _, drop := range []string{"", "top_k"} {
		t.Run("drop="+drop, func(t *testing.T) {
			t.Setenv("GOFLAGS", "")
			f := newFixture(t)
			mirrors := t.TempDir()
			if out, err := exec.Command("git", "clone", "-q", "--mirror", f.src, filepath.Join(mirrors, "m.git")).CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, out)
			}
			t.Setenv("TACK_FAKE_OPENCODE", "ok")
			t.Setenv("TACK_FAKE_KENMCP", "1")
			t.Setenv("TACK_FAKE_OPENCODE_POST", "1")
			t.Setenv("TACK_FAKE_OPENCODE_DROP", drop)
			up := fakeGoinfer(t)
			c, err := StartCapture(up.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			out := t.TempDir()
			err = RunPilot(context.Background(), Options{Tasks: []Task{f.task}, Mirrors: mirrors, Opencode: os.Args[0],
				A0: Arm{Name: "A0"}, A0k: Arm{Name: "A0k", KenMCP: os.Args[0], KenModel: "/m"},
				Server: Server{BaseURL: c.URL, APIKey: "secret-key", Model: "qwen", Context: 32768},
				Out:    out, Cap: time.Minute, Log: io.Discard, Capture: c, MinFree: 1}) // the test machine's free space is not under test
			records, _ := os.ReadFile(filepath.Join(out, "results.jsonl"))
			runs := strings.Count(string(records), "\n")
			switch drop {
			case "":
				if err != nil || runs != 2 || !strings.Contains(string(records), `"captured_requests":1`) {
					t.Fatalf("err %v, %d runs:\n%s", err, runs, records)
				}
			default:
				if err == nil || !strings.Contains(err.Error(), "does not carry top_k") || runs != 1 {
					t.Fatalf("a dropped top_k: err %v, %d runs", err, runs)
				}
			}
		})
	}
}

// TestCaptureNamesTheRequestTheHostSleptThrough: the capture checks every request, and the gap before it, for the host
// sleeping (wall-clock time the monotonic clock did not count). The first sleep over MaxSleepGap is named by where it
// fell, a sleep under it is ignored, and a new run's recording starts clean.
func TestCaptureNamesTheRequestTheHostSleptThrough(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer up.Close()
	c, err := StartCapture(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	post := func() {
		resp, err := http.Post(c.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	// sleepOn names the one interval that reports a sleep of d.
	run := func(sleepOn string, d time.Duration, requests int) string {
		c.SetDir(t.TempDir())
		c.SleepGap = func(start, end time.Time, where string) time.Duration {
			if where == sleepOn {
				return d
			}
			return 0
		}
		for i := 0; i < requests; i++ {
			post()
		}
		return c.Slept()
	}
	for _, tc := range []struct {
		sleepOn  string
		d        time.Duration
		requests int
		want     string
	}{
		{"during request 2", 45 * time.Second, 3, "host slept 45 s during request 2"},
		{"before request 2", 14 * time.Minute, 3, "host slept 840 s before request 2"},
		{"before request 2", 20 * time.Second, 3, ""}, // under MaxSleepGap
		{"", 0, 3, ""},
	} {
		if got := run(tc.sleepOn, tc.d, tc.requests); got != tc.want {
			t.Errorf("a sleep of %s %s: Slept() = %q, want %q", tc.d, tc.sleepOn, got, tc.want)
		}
	}
	// The real gap: no sleep between two instants a moment apart.
	if g := hostSlept(time.Now(), time.Now()); g < -time.Second || g > time.Second {
		t.Errorf("hostSlept with no sleep = %s", g)
	}
}

// TestParsePower reads pmset -g batt's first line.
func TestParsePower(t *testing.T) {
	for out, want := range map[string]string{
		"Now drawing from 'AC Power'\n -InternalBattery-0 (id=1)\t100%; charged":         "ac",
		"Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\t69%; discharging": "battery",
		"": "unknown",
	} {
		if got := parsePower(out); got != want {
			t.Errorf("parsePower(%q) = %q, want %q", out, got, want)
		}
	}
}

// TestASleptOpencodeRunIsVoidNotAFail: an opencode run the host slept through is void: it is still graded, for the
// record, but its pass is false, its capped time 0, and the job's log reports it VOID rather than as a run. The next
// run starts clean.
func TestASleptOpencodeRunIsVoidNotAFail(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	f := newFixture(t)
	mirrors := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", "--mirror", f.src, filepath.Join(mirrors, "m.git")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	t.Setenv("TACK_FAKE_OPENCODE", "ok")
	t.Setenv("TACK_FAKE_KENMCP", "1")
	t.Setenv("TACK_FAKE_OPENCODE_POST", "1")
	up := fakeGoinfer(t)
	c, err := StartCapture(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	first := true // only the first run's request 1
	c.SleepGap = func(start, end time.Time, where string) time.Duration {
		if where == "during request 1" && first {
			first = false
			return 45 * time.Second
		}
		return 0
	}
	out := t.TempDir()
	var log bytes.Buffer
	err = RunPilot(context.Background(), Options{Tasks: []Task{f.task}, Mirrors: mirrors, Opencode: os.Args[0],
		A0: Arm{Name: "A0"}, A0k: Arm{Name: "A0k", KenMCP: os.Args[0], KenModel: "/m"},
		Server: Server{BaseURL: c.URL, APIKey: "secret-key", Model: "qwen", Context: 32768},
		Out:    out, Cap: time.Minute, KenWarmup: time.Minute, Log: &log, Capture: c, MinFree: 1})
	b, _ := os.ReadFile(filepath.Join(out, "results.jsonl"))
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, b, log.String())
	}
	var recs []Record
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r Record
		if json.Unmarshal([]byte(l), &r) != nil {
			t.Fatalf("record %s", l)
		}
		recs = append(recs, r)
	}
	if len(recs) != 2 {
		t.Fatalf("%d records:\n%s", len(recs), b)
	}
	slept, next := recs[0], recs[1]
	if slept.Run.Void != "host slept 45 s during request 1" || slept.Pass || slept.CappedSecs != 0 || len(slept.Oracle) == 0 {
		t.Errorf("the slept run: void %q pass %v capped %v oracle %v", slept.Run.Void, slept.Pass, slept.CappedSecs, slept.Oracle)
	}
	if next.Run.Void != "" {
		t.Errorf("the next run inherited the sleep: %q", next.Run.Void)
	}
	if !strings.Contains(log.String(), "VOID: host slept 45 s during request 1") || strings.Count(log.String(), "pass=") != 1 {
		t.Errorf("the job's log:\n%s", log.String())
	}
}
