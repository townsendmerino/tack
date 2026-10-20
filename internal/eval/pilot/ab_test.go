package pilot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/config"
)

func TestABOrderInterleaves(t *testing.T) {
	arms, reps := abOrder(Arm{Name: "A"}, Arm{Name: "B"})
	var got []string
	for i, a := range arms {
		got = append(got, a.Name+string(rune('0'+reps[i])))
	}
	if strings.Join(got, " ") != "A1 B1 B2 A2 A3 B3" {
		t.Fatalf("order %v, want A B B A A B with each arm numbered 1-3", got)
	}
}

// TestTackConfigSwitchesSettingsOn: an arm's settings reach tack's own config, and every RunSettings name is a [run]
// key tack accepts (config.Load refuses an unknown key).
func TestTackConfigSwitchesSettingsOn(t *testing.T) {
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "token"), []byte("k\n"), 0o600)
	p := filepath.Join(out, "config.toml")
	var all []string
	for s := range RunSettings {
		all = append(all, s)
	}
	os.WriteFile(p, []byte(tackConfig(Server{BaseURL: "http://127.0.0.1:1", Model: "qwen"}, out, all...)), 0o600)
	c, err := config.Load(p)
	if err != nil {
		t.Fatalf("tack refuses an arm's settings %v: %v", all, err)
	}
	if !c.Run.FitWindow {
		t.Fatal("fit_window is not on in the arm's config")
	}
	os.WriteFile(p, []byte(tackConfig(Server{BaseURL: "http://127.0.0.1:1", Model: "qwen"}, out)), 0o600)
	if c, err := config.Load(p); err != nil || c.Run.FitWindow {
		t.Fatalf("an arm with no settings: fit_window %v, %v", c.Run.FitWindow, err)
	}
}

func TestRunPilotRefusesABadComparison(t *testing.T) {
	for name, o := range map[string]Options{
		"one arm":         {A1: "tack", A1Arms: []Arm{{Name: "A"}}, Out: t.TempDir()},
		"unknown setting": {A1: "tack", A1Arms: []Arm{{Name: "A"}, {Name: "B", Settings: []string{"fit_windw"}}}, Out: t.TempDir()},
	} {
		if err := RunPilot(context.Background(), o); err == nil {
			t.Errorf("%s: RunPilot ran", name)
		}
	}
}
