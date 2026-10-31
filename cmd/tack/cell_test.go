package main

import (
	"errors"
	"strings"
	"testing"
)

// TestCellVoid is the rule on the timed box (§12.2, §12.5), read with the model loaded: a load over half its cores, or
// under 2 GB available, voids the cell, as does the Mac on battery unless the owner's -allow-battery override is on; a
// light box on mains does not. Memory the loaded model already holds is out of "available", so 8.4 GB available with a
// 6.5 GB model does not void (the first rule did). The override touches only the battery rule.
func TestCellVoid(t *testing.T) {
	for _, c := range []struct {
		name               string
		err                error
		cores              int
		load, avail        float64
		power, wantVoidHas string
		allowBattery       bool
	}{
		{"light", nil, 16, 1.2, 55, "ac", "", false},
		{"at half the cores", nil, 16, 8.0, 55, "ac", "", false},
		{"over half the cores", nil, 16, 8.01, 55, "ac", "over half of 16 cores", false},
		{"headroom above 2 GB, model loaded", nil, 16, 1, 8.4, "ac", "", false},
		{"at 2 GB", nil, 16, 1, 2.0, "ac", "", false},
		{"under 2 GB", nil, 16, 1, 1.9, "ac", "under 2", false},
		{"battery", nil, 16, 1, 55, "battery", "on battery", false},
		{"battery, the owner's override", nil, 16, 1, 55, "battery", "", true},
		{"heavy load still voids with the override", nil, 16, 8.01, 55, "battery", "over half of 16 cores", true},
		{"power unknown off darwin", nil, 16, 1, 55, "unknown", "", false},
		{"unreadable", errors.New("ssh: exit 255"), 0, 0, 0, "ac", "could not read", false},
	} {
		got := cellVoid(c.err, c.cores, c.load, c.avail, c.power, c.allowBattery)
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
