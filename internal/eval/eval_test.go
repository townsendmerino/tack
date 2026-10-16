package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/models"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
	"github.com/townsendmerino/tack/internal/tools"
)

// With TACK_FAKE_GOINFER=1 this test binary is a small goinfer-serve: it refuses an -ctx above TACK_FAKE_MAX_CTX,
// answers smoke prompts by calling grep with the prompt's quoted word, counts a token per four bytes, streams one
// token per word, and implements `check`.
func TestMain(m *testing.M) {
	if os.Getenv("TACK_FAKE_GOINFER") == "1" {
		fakeServe(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

func fakeServe(args []string) {
	if len(args) > 0 && args[0] == "check" {
		fmt.Println("goinfer check → fake\n\nmessages ................... ok    \n\nall 1 checks passed")
		return
	}
	fs := flag.NewFlagSet("fake", flag.ExitOnError)
	addr := fs.String("addr", "", "")
	sock := fs.String("admin-socket", "", "")
	fs.String("session-dir", "", "")
	ctxN := fs.Int("ctx", 0, "")
	fs.String("backend", "", "")
	_ = fs.Parse(args)
	maxCtx := 0
	fmt.Sscan(os.Getenv("TACK_FAKE_MAX_CTX"), &maxCtx)
	loaded := ""
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		var data []any
		if loaded != "" {
			data = append(data, map[string]any{"id": loaded, "context_window": 32768}) // the model maximum, as goinfer reports it
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	api.HandleFunc("POST /v1/messages/count_tokens", func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]any{"input_tokens": len(body) / 4})
	})
	api.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content []struct{ Text string } `json:"content"`
			} `json:"messages"`
			Tools     []any `json:"tools"`
			MaxTokens int   `json:"max_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		text := req.Messages[0].Content[0].Text
		w.Header().Set("Content-Type", "text/event-stream")
		if len(req.Tools) > 0 && req.MaxTokens > 1 && !strings.Contains(text, "tool") {
			// grep for the prompt's first capitalised or path-like word: right for some tasks, wrong for others.
			word := "x"
			for _, f := range strings.Fields(text) {
				if strings.ContainsAny(f, "ABCDEFGHIJKLMNOPQRSTUVWXYZ./_") && f != "Repository:" {
					word = strings.Trim(f, "?.,")
					break
				}
			}
			in, _ := json.Marshal(map[string]string{"pattern": word})
			fmt.Fprint(w, goinfertest.SSE("tool_use", proto.ModelBlock{Type: "tool_use", ID: "t", Name: "grep", Input: in}))
			return
		}
		n := min(req.MaxTokens, 8)
		var blocks []proto.ModelBlock // a block per word, so a delta per token as goinfer streams
		for range n {
			blocks = append(blocks, proto.ModelBlock{Type: "text", Text: "word "})
		}
		fmt.Fprint(w, goinfertest.SSE("end_turn", blocks...))
	})
	admin := http.NewServeMux()
	admin.HandleFunc("POST /admin/models/load", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name, Path string }
		json.NewDecoder(r.Body).Decode(&req)
		if maxCtx > 0 && *ctxN > maxCtx {
			w.WriteHeader(400)
			fmt.Fprintf(w, `{"error":{"message":"resident context %d does not fit"}}`, *ctxN)
			return
		}
		loaded = req.Name
		decode := os.Getenv("TACK_FAKE_DECODE")
		if decode == "" {
			decode = "cpu (int4)"
		}
		fmt.Fprintf(os.Stderr, "  decode path: %s\n  fit: %d-token cap needs 0.5 GB KV (weights 1.0 GB, budget 9.0 GB)\n", decode, *ctxN)
		json.NewEncoder(w).Encode(map[string]any{"status": "loaded"})
	})
	al, err := net.Listen("unix", *sock)
	if err != nil {
		os.Exit(1)
	}
	go http.Serve(al, admin)
	go http.ListenAndServe(*addr, api)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
}

func TestSmokeTasksAreWellFormed(t *testing.T) {
	if len(SmokeTasks) != 20 {
		t.Fatalf("%d smoke tasks; the registration says 20", len(SmokeTasks))
	}
	set := tools.NewSet(nil)
	noTool := 0
	for i, task := range SmokeTasks {
		if task.NoTool {
			noTool++
			continue
		}
		for _, name := range task.Tools {
			tl, ok := set.Get(name)
			if !ok {
				t.Errorf("task %d expects %s, which tack does not offer", i, name)
				continue
			}
			if !strings.Contains(string(tl.Spec().InputSchema), `"`+task.Arg+`"`) {
				t.Errorf("task %d checks argument %q, which %s does not take", i, task.Arg, name)
			}
		}
	}
	if noTool != 2 {
		t.Fatalf("%d no-tool tasks; the registration says 2", noTool)
	}
}

func call(name, input string) *server.Response {
	return &server.Response{Content: []proto.ModelBlock{{Type: "tool_use", ID: "t", Name: name, Input: json.RawMessage(input)}}}
}

func TestScoreSmoke(t *testing.T) {
	cases := []struct {
		task int
		resp *server.Response
		pass bool
		mal  bool
	}{
		{0, call("grep", `{"pattern":"func applyTokenBudget"}`), true, false},
		{0, call("grep", `{"pattern":"applytokenbudget\\("}`), true, false}, // case and escapes ignored
		{0, call("grep", `{"pattern":"budget"}`), false, false},
		{0, call("glob", `{"pattern":"applyTokenBudget"}`), false, false},
		{0, &server.Response{Content: []proto.ModelBlock{{Type: "text", Text: "in mcp"}}}, false, false},
		{16, &server.Response{Content: []proto.ModelBlock{{Type: "text", Text: "51"}}}, true, false},
		{16, call("grep", `{"pattern":"x"}`), false, false},
		{9, call("glob", `{"pattern":"docs/**/*.md"}`), true, false},
		{9, call("glob", `{"pattern":"**/*.md"}`), false, false}, // lacks "docs"
	}
	for i, tc := range cases {
		r := ScoreSmoke(tc.task, tc.resp)
		if r.Pass != tc.pass || r.Malformed != tc.mal {
			t.Errorf("case %d: %+v", i, r)
		}
	}
	mal := call("rm", `{}`)
	mal.Malformed = map[string]string{"t": "no tool named \"rm\""}
	if r := ScoreSmoke(0, mal); r.Pass || !r.Malformed {
		t.Errorf("malformed call: %+v", r)
	}
}

func TestGrade(t *testing.T) {
	cases := []struct {
		window, check bool
		fails         int
		smoke         [2]int
		mal           int
		want          string
	}{
		{true, true, 0, [2]int{17, 18}, 0, "eligible"},
		{true, true, 0, [2]int{18, 16}, 0, "parked"}, // both runs must reach 17
		{true, true, 0, [2]int{20, 20}, 1, "fail"},   // a malformed call on goinfer
		{true, false, 1, [2]int{19, 19}, 0, "parked"},
		{true, false, 2, [2]int{19, 19}, 0, "fail"},
		{true, true, 0, [2]int{13, 20}, 0, "fail"},
		{false, true, 0, [2]int{20, 20}, 0, "fail"},
	}
	for i, tc := range cases {
		if got, _ := grade(tc.window, tc.check, tc.fails, tc.smoke, tc.mal); got != tc.want {
			t.Errorf("case %d: %s, want %s", i, got, tc.want)
		}
	}
}

func TestFailedRows(t *testing.T) {
	if n := failedRows("x\n2 of 9 checks FAILED\n", 1); n != 2 {
		t.Fatal(n)
	}
	if n := failedRows("all 9 checks passed\n", 0); n != 0 {
		t.Fatal(n)
	}
	if n := failedRows("panic\n", 2); n != -1 {
		t.Fatal(n)
	}
}

func TestTimingLock(t *testing.T) {
	t.Setenv(tokenEnv, "")
	path := filepath.Join(t.TempDir(), "lock")
	l, err := AcquireTimingLock(path, "first")
	if err != nil || l.Inherited {
		t.Fatalf("first acquire: %v", err)
	}
	// A child of the holder inherits it through the token.
	if l2, err := AcquireTimingLock(path, "child"); err != nil || !l2.Inherited {
		t.Fatalf("inherit: %v", err)
	}
	if err := CheckTimingLock(path); err != nil {
		t.Fatalf("check under our own lock: %v", err)
	}
	// Anyone else is refused, by name.
	os.Setenv(tokenEnv, "someone-else")
	var held *HeldError
	if _, err := AcquireTimingLock(path, "other"); !errors.As(err, &held) || !strings.Contains(held.Holder, "first") {
		t.Fatalf("second acquire: %v", err)
	}
	if err := CheckTimingLock(path); !errors.As(err, &held) {
		t.Fatalf("check by another run: %v", err)
	}
	l.Release()
	if _, err := AcquireTimingLock(path, "after"); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestManifestRefusesTheArchive(t *testing.T) {
	m := Manifest{Box: "mac", GoinferServe: "/b", GoinferRev: "r", Workspace: "/w",
		Arms: []Arm{{Name: "a", ModelPath: "/Volumes/archive/models/q.gguf"}}}
	if err := m.validate("/Users/x/models"); err == nil || !strings.Contains(err.Error(), "never the archive") {
		t.Fatalf("validate = %v", err)
	}
	m.Arms[0].ModelPath = "/Users/x/models/q.gguf"
	if err := m.validate("/Users/x/models"); err != nil || len(m.Arms[0].Ctx) != 2 {
		t.Fatalf("validate = %v, ctx %v", err, m.Arms[0].Ctx)
	}
}

// TestRunEndToEnd runs two arms through every phase against the fake goinfer-serve: the first refuses -ctx 16384 and
// is admitted at 8192; the second is admitted at 16384 but its window is too small for anything past smoke.
func TestRunEndToEnd(t *testing.T) {
	t.Setenv("TACK_FAKE_GOINFER", "1")
	t.Setenv("TACK_FAKE_MAX_CTX", "8192")
	t.Setenv(tokenEnv, "")
	ws := filepath.Join(t.TempDir(), "tack")
	os.MkdirAll(ws, 0o755)
	os.WriteFile(filepath.Join(ws, "big.go"), bytes.Repeat([]byte("package big // some code text here\n"), 3000), 0o644)
	out := t.TempDir()
	m := &Manifest{Box: "test", GoinferServe: os.Args[0], GoinferRev: "fake", Workspace: ws, Arms: []Arm{
		{Name: "fits-8k", ModelPath: "/m/a.gguf", Ctx: []int{16384, 8192}},
		{Name: "tiny-window", ModelPath: "/m/b.gguf", Ctx: []int{4096}},
	}}
	var log bytes.Buffer
	err := Run(context.Background(), Options{Manifest: m, Out: out, TackRev: "test", LockPath: filepath.Join(t.TempDir(), "lock"),
		Log: &log, Beat: 20 * time.Millisecond, Pressure: func() (models.Pressure, error) { return models.PressureNormal, nil }})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, "results.jsonl"))
	type rec struct {
		Arm, Phase, Verdict, Cell string
		Admitted                  bool
		Ctx, Size                 int
		TTFTMS                    *int64 `json:"ttft_ms"`
	}
	byPhase := map[string][]rec{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r rec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		byPhase[r.Arm+"/"+r.Phase] = append(byPhase[r.Arm+"/"+r.Phase], r)
	}
	if f := byPhase["fits-8k/fit"]; len(f) != 2 || f[0].Admitted || !f[1].Admitted || f[1].Ctx != 8192 {
		t.Fatalf("fit records: %+v", f)
	}
	if n := len(byPhase["fits-8k/smoke"]); n != 40 {
		t.Fatalf("%d smoke records, want 40", n)
	}
	ttft := byPhase["fits-8k/ttft"]
	if len(ttft) != 12 { // 4096 and 7168 (the window minus 1024), each 3 cold + 3 reused
		t.Fatalf("%d ttft records: %+v", len(ttft), ttft)
	}
	if v := byPhase["fits-8k/verdict"]; len(v) != 1 || v[0].Verdict == "" {
		t.Fatalf("verdict %+v", v)
	}
	if len(byPhase["fits-8k/decode_summary"]) != 1 || len(byPhase["fits-8k/footprint"]) != 1 {
		t.Fatalf("decode/footprint missing: %v", byPhase)
	}
	if v := byPhase["tiny-window/verdict"]; len(v) != 1 || v[0].Verdict != "fail" || len(byPhase["tiny-window/ttft"]) != 0 {
		t.Fatalf("a 4096 window must fail and skip timing: %+v", byPhase["tiny-window/verdict"])
	}
	for _, want := range []string{"enter fit", "leave smoke", "heartbeat", "-ctx 16384 refused"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

func TestPressureSkipsAnArm(t *testing.T) {
	t.Setenv(tokenEnv, "")
	defer func(p time.Duration) { settlePoll = p }(settlePoll)
	settlePoll = time.Millisecond
	out := t.TempDir()
	m := &Manifest{Box: "test", GoinferServe: "/nonexistent", GoinferRev: "r", Workspace: t.TempDir(),
		Arms: []Arm{{Name: "a", ModelPath: "/m/a.gguf", Ctx: []int{8192}}}}
	err := Run(context.Background(), Options{Manifest: m, Out: out, LockPath: filepath.Join(t.TempDir(), "lock"),
		SettleWait: 10 * time.Millisecond, Pressure: func() (models.Pressure, error) { return models.PressureWarning, nil }})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, "results.jsonl"))
	if !strings.Contains(string(data), `"skipped":"pressure"`) || !strings.Contains(string(data), `"verdict":"skipped"`) {
		t.Fatalf("results: %s", data)
	}
}

// TestPressureSettlesBeforeAnArm: pressure at warning when the arm starts, normal a moment later (the previous arm's
// child just exited). The arm waits and runs instead of being skipped.
func TestPressureSettlesBeforeAnArm(t *testing.T) {
	t.Setenv("TACK_FAKE_GOINFER", "1")
	t.Setenv(tokenEnv, "")
	defer func(p time.Duration) { settlePoll = p }(settlePoll)
	settlePoll = 10 * time.Millisecond
	calls := 0
	pressure := func() (models.Pressure, error) {
		calls++
		if calls <= 2 {
			return models.PressureWarning, nil
		}
		return models.PressureNormal, nil
	}
	out := t.TempDir()
	m := &Manifest{Box: "test", GoinferServe: os.Args[0], GoinferRev: "fake", Workspace: t.TempDir(),
		Arms: []Arm{{Name: "a", ModelPath: "/m/a.gguf", Ctx: []int{8192}}}}
	if err := Run(context.Background(), Options{Manifest: m, Out: out, FitOnly: true, LockPath: filepath.Join(t.TempDir(), "lock"),
		SettleWait: 5 * time.Second, Pressure: pressure}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, "results.jsonl"))
	if !strings.Contains(string(data), `"admitted":true`) || strings.Contains(string(data), `"skipped"`) {
		t.Fatalf("results: %s", data)
	}
}

func TestFellBackFrom(t *testing.T) {
	cases := []struct {
		args   []string
		decode string
		fell   bool
	}{
		{[]string{"-backend", "cuda"}, "cuda-resident (int4)", false},
		{[]string{"-backend", "cuda"}, "cpu (int4)", true},
		{[]string{"-backend", "metal"}, "metal-resident (int4)", false},
		{[]string{"-backend", "metal"}, "cpu (int8int8)", true},
		{[]string{"-backend", "cpu"}, "cpu (int4)", false},
		{nil, "cpu (int4)", false},                // no backend asked for
		{[]string{"-backend", "cuda"}, "", false}, // unknown decode path: not judged
	}
	for _, tc := range cases {
		if got := fellBackFrom(tc.args, tc.decode) != ""; got != tc.fell {
			t.Errorf("fellBackFrom(%v, %q) fell=%v, want %v", tc.args, tc.decode, got, tc.fell)
		}
	}
}

// TestFallbackStopsTheArm: an arm that asked for CUDA and was served on the CPU records its fit and a fail verdict
// naming the fallback, and nothing else: no check, no smoke, no timing.
func TestFallbackStopsTheArm(t *testing.T) {
	t.Setenv("TACK_FAKE_GOINFER", "1")
	t.Setenv("TACK_FAKE_DECODE", "cpu (int4)")
	t.Setenv(tokenEnv, "")
	out := t.TempDir()
	m := &Manifest{Box: "test", GoinferServe: os.Args[0], GoinferRev: "fake", Workspace: t.TempDir(),
		Arms: []Arm{{Name: "big", ModelPath: "/m/big.gguf", Args: []string{"-backend", "cuda"}, Ctx: []int{8192}}}}
	if err := Run(context.Background(), Options{Manifest: m, Out: out, LockPath: filepath.Join(t.TempDir(), "lock"),
		Pressure: func() (models.Pressure, error) { return models.PressureNormal, nil }}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, "results.jsonl"))
	s := string(data)
	if !strings.Contains(s, `"fell_back":"asked for -backend cuda, goinfer is decoding on \"cpu (int4)\""`) ||
		!strings.Contains(s, `"verdict":"fail"`) {
		t.Fatalf("results:\n%s", s)
	}
	for _, phase := range []string{`"phase":"check"`, `"phase":"smoke"`, `"phase":"ttft"`, `"phase":"decode"`} {
		if strings.Contains(s, phase) {
			t.Errorf("a fallen-back arm ran %s", phase)
		}
	}
}
