package main

import (
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/townsendmerino/tack/tools/gate"
)

// target is one platform the daemon binary is released for.
type target struct{ GOOS, GOARCH string }

func (t target) String() string { return t.GOOS + "/" + t.GOARCH }

// targets are the released platforms: Apple silicon (DESIGN D5) and nobara.
var targets = []target{{"darwin", "arm64"}, {"linux", "amd64"}}

// darwinLibs are the only libraries a darwin binary may load: the ones Go's own runtime and standard library call
// with cgo off. darwin has no static binaries, so this list, not an empty one, is what "no cgo" allows there. A cgo
// build loads the same four, which is why judge reads the build settings as well.
var darwinLibs = []string{
	"/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation",
	"/System/Library/Frameworks/Security.framework/Versions/A/Security",
	"/usr/lib/libSystem.B.dylib",
	"/usr/lib/libresolv.9.dylib",
}

// buildCommand is the one pinned build of pkg for t: cgo off, no workspace, a read-only go.mod, no local paths in
// the binary. It returns the environment to add and go's arguments.
func buildCommand(t target, out, pkg string) (env, args []string) {
	return []string{"CGO_ENABLED=0", "GOOS=" + t.GOOS, "GOARCH=" + t.GOARCH, "GOWORK=off", "GOFLAGS=-mod=readonly"},
		[]string{"build", "-trimpath", "-o", out, pkg}
}

// facts is what a built binary says about itself.
type facts struct {
	Go       string            // the toolchain, e.g. "go1.27.3"
	Settings map[string]string // go's recorded build settings: CGO_ENABLED, GOOS, GOARCH, -trimpath
	Libs     []string          // libraries the loader is asked for
	Interp   bool              // ELF only: the binary names a dynamic loader
}

// inspect reads a built binary's facts for t's format.
func inspect(path string, t target) (facts, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return facts{}, err
	}
	f := facts{Go: info.GoVersion, Settings: map[string]string{}}
	for _, s := range info.Settings {
		f.Settings[s.Key] = s.Value
	}
	if t.GOOS == "darwin" {
		m, err := macho.Open(path)
		if err != nil {
			return facts{}, err
		}
		defer m.Close()
		f.Libs, err = m.ImportedLibraries()
		return f, err
	}
	e, err := elf.Open(path)
	if err != nil {
		return facts{}, err
	}
	defer e.Close()
	for _, p := range e.Progs {
		if p.Type == elf.PT_INTERP {
			f.Interp = true
		}
	}
	f.Libs, err = e.ImportedLibraries()
	return f, err
}

// judge lists what in f breaks the no-cgo rule for a binary meant for t and built by toolchain wantGo.
func judge(f facts, t target, wantGo string) []string {
	var bad []string
	for _, w := range [][2]string{{"CGO_ENABLED", "0"}, {"GOOS", t.GOOS}, {"GOARCH", t.GOARCH}, {"-trimpath", "true"}} {
		if got := f.Settings[w[0]]; got != w[1] {
			bad = append(bad, fmt.Sprintf("built with %s=%q, want %q", w[0], got, w[1]))
		}
	}
	if f.Go != wantGo {
		bad = append(bad, fmt.Sprintf("built by %s, want go.mod's %s", f.Go, wantGo))
	}
	for _, lib := range f.Libs {
		switch {
		case t.GOOS != "darwin":
			bad = append(bad, "dynamically linked against "+lib+": the linux binary is static")
		case !slices.Contains(darwinLibs, lib):
			bad = append(bad, "loads "+lib+", which is not one of the system libraries Go itself uses (darwinLibs)")
		}
	}
	if f.Interp {
		bad = append(bad, "names a dynamic loader (PT_INTERP): the linux binary is static")
	}
	return bad
}

// goModVersion returns the toolchain go.mod pins, as build info spells it ("go1.27.3").
func goModVersion(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`(?m)^(?:toolchain go|go )(\d\S*)$`).FindAllStringSubmatch(string(b), -1)
	if m == nil {
		return "", fmt.Errorf("go.mod has no go directive")
	}
	return "go" + m[len(m)-1][1], nil // a toolchain line, when present, follows the go line and wins
}

// check builds pkg in root for every target with the pinned command and judges each binary: two cells per target.
// Binaries are written to outDir as tack-<goos>-<goarch>.
func check(root, pkg, outDir string) []gate.Cell {
	wantGo, verr := goModVersion(root)
	var cells []gate.Cell
	for _, t := range targets {
		out := filepath.Join(outDir, "tack-"+t.GOOS+"-"+t.GOARCH)
		build := gate.Cell{Name: "build/" + t.String()}
		bin := gate.Cell{Name: "binary/" + t.String()}
		env, args := buildCommand(t, out, pkg)
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), env...)
		if o, err := cmd.CombinedOutput(); err != nil {
			build.Outcome, build.Detail = gate.Fail, strings.Join(env, " ")+" go "+strings.Join(args, " ")+"\n"+string(o)
			bin.Outcome, bin.Detail = gate.Inconclusive, "not judged: the build failed"
			cells = append(cells, build, bin)
			continue
		}
		build.Outcome = gate.OK
		f, err := inspect(out, t)
		switch {
		case verr != nil:
			bin.Outcome, bin.Detail = gate.Inconclusive, verr.Error()
		case err != nil:
			bin.Outcome, bin.Detail = gate.Inconclusive, err.Error()
		default:
			bin.Fields = []gate.Field{gate.F("libs", len(f.Libs))}
			if bad := judge(f, t, wantGo); len(bad) > 0 {
				bin.Outcome, bin.Detail = gate.Fail, strings.Join(bad, "\n")
			} else {
				bin.Outcome = gate.OK
			}
		}
		cells = append(cells, build, bin)
	}
	return cells
}
