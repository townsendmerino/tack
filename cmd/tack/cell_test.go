package main

import (
	"errors"
	"strings"
	"testing"
)

// TestCellVoid is the 2026-10-19 rule on the timed box (§12.2): a load over half its cores, or less available memory
// than the fit's need plus 2 GB, voids the cell, as does the Mac on battery; a light box on mains does not.
func TestCellVoid(t *testing.T) {
	for _, c := range []struct {
		name               string
		err                error
		cores              int
		load, avail, need  float64
		power, wantVoidHas string
	}{
		{"light", nil, 16, 1.2, 55, 6.5, "ac", ""},
		{"at half the cores", nil, 16, 8.0, 55, 6.5, "ac", ""},
		{"over half the cores", nil, 16, 8.01, 55, 6.5, "ac", "over half of 16 cores"},
		{"memory under need plus 2", nil, 16, 1, 8.4, 6.5, "ac", "under the fit's 6.5 GB plus 2"},
		{"battery", nil, 16, 1, 55, 6.5, "battery", "on battery"},
		{"power unknown off darwin", nil, 16, 1, 55, 6.5, "unknown", ""},
		{"unreadable", errors.New("ssh: exit 255"), 0, 0, 0, 6.5, "ac", "could not read"},
	} {
		got := cellVoid(c.err, c.cores, c.load, c.avail, c.need, c.power)
		if (c.wantVoidHas == "") != (got == "") || !strings.Contains(got, c.wantVoidHas) {
			t.Errorf("%s: void %q, want one containing %q", c.name, got, c.wantVoidHas)
		}
	}
}

func TestSettingListIsAnAllowlist(t *testing.T) {
	var l settingList
	if err := l.Set("fit_window"); err != nil || l.Set("fit_window") != nil || len(l) != 1 {
		t.Fatalf("fit_window: %v, %v", err, l)
	}
	if err := l.Set("fit_windw"); err == nil {
		t.Fatal("a typo was accepted")
	}
}
