package eval

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestRevisionFrom(t *testing.T) {
	const full = "b10d501a2c3d4e5f60718293a4b5c6d7e8f90123"
	info := func(kv ...string) *debug.BuildInfo {
		bi := &debug.BuildInfo{}
		for i := 0; i+1 < len(kv); i += 2 {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return bi
	}
	clean := info("vcs", "git", "vcs.revision", full, "vcs.modified", "false")
	cases := []struct {
		name    string
		bi      *debug.BuildInfo
		ok      bool
		claimed string
		want    string // the revision, or a fragment of the refusal
		refused bool
	}{
		{"clean, nothing claimed", clean, true, "", full, false},
		{"clean, short claim", clean, true, "b10d501", full, false},
		{"clean, full claim", clean, true, full, full, false},
		{"claim of another commit", clean, true, "f209e4b", "is not this binary's revision", true},
		{"claim too short to mean anything", clean, true, "b10", "is not this binary's revision", true},
		{"dirty tree", info("vcs.revision", full, "vcs.modified", "true"), true, "", "uncommitted changes", true},
		{"modified unrecorded", info("vcs.revision", full), true, "", "uncommitted changes", true},
		{"no VCS info", info("-compiler", "gc"), true, "", "records no VCS revision", true},
		{"no build info", nil, false, "", "records no VCS revision", true},
	}
	for _, tc := range cases {
		rev, err := revisionFrom(tc.bi, tc.ok, tc.claimed)
		switch {
		case tc.refused && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: rev %q err %v, want a refusal containing %q", tc.name, rev, err, tc.want)
		case !tc.refused && (err != nil || rev != tc.want):
			t.Errorf("%s: rev %q err %v, want %q", tc.name, rev, err, tc.want)
		}
	}
}
