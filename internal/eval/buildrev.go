package eval

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
)

// Revision is the tack commit this binary was built from, as Go's embedded build info records it (vcs.revision),
// checked against claimed (the -tack-rev flag; "" claims nothing). A measurement records the revision with every
// result, so a binary that cannot say exactly what it is refuses to measure: one built without VCS info (go run,
// go test, a tree from git archive, -buildvcs=false) or from a tree with uncommitted changes (vcs.modified).
func Revision(claimed string) (string, error) {
	info, ok := debug.ReadBuildInfo()
	return revisionFrom(info, ok, claimed)
}

func revisionFrom(info *debug.BuildInfo, ok bool, claimed string) (string, error) {
	rev, modified := "", ""
	if ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
	}
	switch {
	case rev == "":
		return "", errors.New("this binary records no VCS revision (built by go run or go test, from a git archive, " +
			"or with -buildvcs=false); build it with go build from a clean checkout of the commit to measure")
	case modified != "false": // absent counts as unknown, not clean
		return "", fmt.Errorf("this binary was built from %s with uncommitted changes (vcs.modified=%q); build it "+
			"from a clean checkout to measure", rev, modified)
	case claimed != "" && (len(claimed) < 7 || !strings.HasPrefix(rev, claimed)):
		return "", fmt.Errorf("-tack-rev %s is not this binary's revision %s", claimed, rev)
	}
	return rev, nil
}
