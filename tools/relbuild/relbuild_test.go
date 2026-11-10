package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/tools/gate"
)

func good(t target) facts {
	f := facts{Go: "go1.27.3", Settings: map[string]string{"CGO_ENABLED": "0", "GOOS": t.GOOS, "GOARCH": t.GOARCH, "-trimpath": "true"}}
	if t.GOOS == "darwin" {
		f.Libs = append([]string{}, darwinLibs...)
	}
	return f
}

// TestJudgeCatchesEachBreak: a binary with the pinned build's facts passes; each case changes one fact and is
// reported by that fact alone.
func TestJudgeCatchesEachBreak(t *testing.T) {
	darwin, linux := targets[0], targets[1]
	cases := []struct {
		name   string
		t      target
		break_ func(*facts)
		want   []string
	}{
		{"darwin as built", darwin, func(*facts) {}, nil},
		{"linux as built", linux, func(*facts) {}, nil},
		{"cgo on, with the same four libraries", darwin, func(f *facts) { f.Settings["CGO_ENABLED"] = "1" },
			[]string{`built with CGO_ENABLED="1", want "0"`}},
		{"darwin loads a library that is not the system's", darwin, func(f *facts) { f.Libs = append(f.Libs, "/opt/homebrew/lib/libfoo.dylib") },
			[]string{"loads /opt/homebrew/lib/libfoo.dylib, which is not one of the system libraries Go itself uses (darwinLibs)"}},
		{"linux linked against libc", linux, func(f *facts) { f.Libs, f.Interp = []string{"libc.so.6"}, true },
			[]string{"dynamically linked against libc.so.6: the linux binary is static", "names a dynamic loader (PT_INTERP): the linux binary is static"}},
		{"built for another platform", linux, func(f *facts) { f.Settings["GOARCH"] = "arm64" },
			[]string{`built with GOARCH="arm64", want "amd64"`}},
		{"local paths left in", linux, func(f *facts) { delete(f.Settings, "-trimpath") },
			[]string{`built with -trimpath="", want "true"`}},
		{"another toolchain", linux, func(f *facts) { f.Go = "go1.26.0" }, []string{"built by go1.26.0, want go.mod's go1.27.3"}},
	}
	for _, tc := range cases {
		f := good(tc.t)
		tc.break_(&f)
		if got := judge(f, tc.t, "go1.27.3"); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// scratch writes a module whose cmd/tack uses net/http, the part of the standard library that takes a cgo path when
// cgo is on, and returns its directory.
func scratch(t *testing.T) string {
	dir := t.TempDir()
	ver := strings.TrimPrefix(runtime.Version(), "go")
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo "+ver+"\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "cmd", "tack"), 0o755)
	os.WriteFile(filepath.Join(dir, "cmd", "tack", "main.go"),
		[]byte("package main\n\nimport (\n\t\"fmt\"\n\t\"net/http\"\n)\n\nfunc main() { fmt.Println(http.DefaultClient) }\n"), 0o644)
	return dir
}

// TestCheckBuildsAndReads runs check on a scratch module, so the pinned command and the readers of both binary
// formats are exercised on real binaries: both targets build and pass, and the darwin reader finds its libraries.
func TestCheckBuildsAndReads(t *testing.T) {
	dir, out := scratch(t), t.TempDir()
	for _, c := range check(dir, "./cmd/tack", out) {
		if c.Outcome != gate.OK {
			t.Errorf("%s: %s\n%s", c.Name, c.Outcome, c.Detail)
		}
	}
	f, err := inspect(filepath.Join(out, "tack-darwin-arm64"), targets[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Libs) == 0 {
		t.Errorf("darwin: read no libraries; a darwin binary always loads libSystem")
	}
}

// TestCgoBuildIsCaught builds the scratch program for the host with cgo on and judges the real binary: it fails on
// its recorded CGO_ENABLED setting. A cgo build needs the host's C compiler, so only the host's format is built.
func TestCgoBuildIsCaught(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler (cc) on PATH")
	}
	host := target{runtime.GOOS, runtime.GOARCH}
	dir, bin := scratch(t), filepath.Join(t.TempDir(), "tack-cgo")
	cmd := exec.Command("go", "build", "-trimpath", "-o", bin, "./cmd/tack")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1", "GOWORK=off")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cgo-on build: %v\n%s", err, o)
	}
	f, err := inspect(bin, host)
	if err != nil {
		t.Fatal(err)
	}
	if bad := judge(f, host, runtime.Version()); !strings.Contains(strings.Join(bad, "\n"), `built with CGO_ENABLED="1", want "0"`) {
		t.Errorf("a cgo-on %s binary was judged %q, want its CGO_ENABLED setting reported", host, bad)
	}
}

func TestGoModVersion(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]string{
		"module m\n\ngo 1.27.3\n":                     "go1.27.3",
		"module m\n\ngo 1.27\n\ntoolchain go1.27.4\n": "go1.27.4",
	} {
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644)
		if got, err := goModVersion(dir); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", body, got, err, want)
		}
	}
}
