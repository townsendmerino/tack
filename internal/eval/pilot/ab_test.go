package pilot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/townsendmerino/tack/internal/config"
)

func TestABOrderInterleaves(t *testing.T) {
	for n, want := range map[int]string{
		3: "A1 B1 B2 A2 A3 B3",
		4: "A1 B1 B2 A2 A3 B3 B4 A4",
		7: "A1 B1 B2 A2 A3 B3 B4 A4 A5 B5 B6 A6 A7 B7",
	} {
		arms, reps := abOrder(Arm{Name: "A"}, Arm{Name: "B"}, n)
		var got []string
		for i, a := range arms {
			got = append(got, fmt.Sprintf("%s%d", a.Name, reps[i]))
		}
		if strings.Join(got, " ") != want {
			t.Errorf("%d per arm: order %v, want %s", n, got, want)
		}
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
	if r := c.Run; !r.FitWindow || !r.StopRepeats || !r.ResultNotes || !r.EditMessages || !r.NoInstructions || !r.InstructionsHead || !r.RefuseDuplicateEdits || !r.VerifyDone || !r.InjectContext || !r.RepoMap || !r.MaskObservations || r.BestOf != 3 {
		t.Fatalf("an arm's settings are not all on in its config: %+v", r)
	}
	os.WriteFile(p, []byte(tackConfig(Server{BaseURL: "http://127.0.0.1:1", Model: "qwen"}, out)), 0o600)
	if c, err := config.Load(p); err != nil || c.Run.FitWindow {
		t.Fatalf("an arm with no settings: fit_window %v, %v", c.Run.FitWindow, err)
	}
	// Every arm's sandboxed commands may read ~/models and nothing else added.
	if c, err := config.Load(p); err != nil || len(c.Sandbox.Read) != 1 || c.Sandbox.Read[0] != ModelsDir() {
		t.Fatalf("an arm's [sandbox] read: %q, %v; want only %s", c.Sandbox.Read, err, ModelsDir())
	}
}

func TestRunPilotRefusesABadComparison(t *testing.T) {
	for name, o := range map[string]Options{
		"three arms":      {A1: "tack", A1Arms: []Arm{{Name: "A"}, {Name: "B"}, {Name: "C"}}, Out: t.TempDir()},
		"reps, no arm":    {A1: "tack", A1Reps: 2, Out: t.TempDir()},
		"unknown setting": {A1: "tack", A1Arms: []Arm{{Name: "A"}, {Name: "B", Settings: []string{"fit_windw"}}}, Out: t.TempDir()},
	} {
		if err := RunPilot(context.Background(), o); err == nil {
			t.Errorf("%s: RunPilot ran", name)
		}
	}
}
