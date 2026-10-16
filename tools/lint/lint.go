package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/townsendmerino/tack/tools/gate"
)

// canaryFinding is what a working staticcheck prints for tools/lint/canary built with canaryTag.
const (
	canaryFinding = "field deadField is unused (U1000)"
	canaryTag     = "tack_staticcheck_canary"
	canaryPkg     = "./tools/lint/canary"
	pinGoMod      = ".github/actions/staticcheck/go.mod"
)

var goosTargets = []string{"darwin", "linux"}

// pinnedVersion reads the staticcheck module version (e.g. "0.8.1") from the CI action's go.mod, the one place the
// pin lives.
func pinnedVersion(gomod string) (string, error) {
	m := regexp.MustCompile(`(?m)^\s*(?:require\s+)?honnef\.co/go/tools\s+v(\S+)`).FindStringSubmatch(gomod)
	if m == nil {
		return "", fmt.Errorf("%s does not require honnef.co/go/tools", pinGoMod)
	}
	return m[1], nil
}

// versionMatches reports whether `staticcheck -version` output (e.g. "staticcheck 2026.2.1 (0.8.1)") is the pin.
func versionMatches(out, pin string) bool {
	return strings.Contains(out, "("+pin+")")
}

// canaryFired reports whether staticcheck's output on the canary package names the dead field.
func canaryFired(out string) bool {
	return strings.Contains(out, "canary.go") && strings.Contains(out, canaryFinding)
}

// checks returns the gate: the pinned version, then for each GOOS the canary and the real run. The real run is only
// judged when that GOOS's canary fired; otherwise a clean result means nothing and the cell is INCONCLUSIVE.
func checks(root, bin string) []gate.Cell {
	var cells []gate.Cell
	verCell := gate.Cell{Name: "staticcheck version"}
	gomod, err := os.ReadFile(root + "/" + pinGoMod)
	pin := ""
	if err == nil {
		pin, err = pinnedVersion(string(gomod))
	}
	out, verr := runIn(root, nil, bin, "-version")
	switch {
	case err != nil:
		verCell.Outcome, verCell.Detail = gate.Inconclusive, err.Error()
	case verr != nil:
		verCell.Outcome, verCell.Detail = gate.Inconclusive, fmt.Sprintf("%s -version: %v\n%s", bin, verr, out)
	case !versionMatches(out, pin):
		verCell.Outcome = gate.Inconclusive
		verCell.Detail = fmt.Sprintf("%s is %q; the pin is v%s. Build it: GOWORK=off go build -C .github/actions/staticcheck "+
			"-o \"$(go env GOPATH)/bin/staticcheck\" honnef.co/go/tools/cmd/staticcheck", bin, strings.TrimSpace(out), pin)
	default:
		verCell.Outcome = gate.OK
		verCell.Fields = []gate.Field{gate.F("version", strings.TrimSpace(out))}
	}
	cells = append(cells, verCell)

	for _, goos := range goosTargets {
		env := []string{"GOOS=" + goos, "CGO_ENABLED=0"}
		can := gate.Cell{Name: "canary/" + goos}
		out, _ := runIn(root, env, bin, "-tags", canaryTag, canaryPkg)
		if canaryFired(out) {
			can.Outcome = gate.OK
		} else {
			can.Outcome = gate.Inconclusive
			can.Detail = "staticcheck did not report \"" + canaryFinding + "\" on " + canaryPkg +
				": it is not analysing, so a clean run proves nothing\n" + out
		}
		cells = append(cells, can)

		full := gate.Cell{Name: "staticcheck/" + goos}
		out, err := runIn(root, env, bin, "./...")
		switch {
		case can.Outcome != gate.OK:
			full.Outcome, full.Detail = gate.Inconclusive, "not judged: the canary did not fire"
		case err != nil:
			full.Outcome, full.Detail = gate.Fail, out
		default:
			full.Outcome = gate.OK
		}
		cells = append(cells, full)
	}
	return cells
}

func runIn(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
