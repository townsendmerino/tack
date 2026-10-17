// Package eval runs tack's measurements: E0, the model survey (DESIGN §15.1), registered in
// docs/measurements/e0-preregistration-2026-10-16.md. E1 arrives in P1.
package eval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Manifest is one box's E0 run: the pinned goinfer-serve, the repository whose text sizes the TTFT prompts, and the
// arms in order. eval/e0/<box>.toml holds them.
type Manifest struct {
	Box          string `toml:"box"`
	GoinferServe string `toml:"goinfer_serve"` // a pre-built binary at a durable path
	GoinferRev   string `toml:"goinfer_rev"`   // the revision it was built from, recorded with every result
	Workspace    string `toml:"workspace"`     // a tack checkout at the queued commit
	Arms         []Arm  `toml:"arm"`
}

// Arm is one model on one backend.
type Arm struct {
	Name      string   `toml:"name"`
	ModelPath string   `toml:"model_path"`
	Args      []string `toml:"args"` // goinfer-serve flags, e.g. ["-backend", "metal"]
	Ctx       []int    `toml:"ctx"`  // contexts to try, largest first (default 16384, 8192)
	// DecodePath is the decode path the arm measures, which goinfer's banner must name or the arm is void (default
	// DecodePathFor(Args): metal-resident, cuda-resident or cpu).
	DecodePath string `toml:"decode_path"`
}

// LoadManifest reads and validates a manifest. Every model must be read from ~/models on this machine, never the
// archive (goinfer docs/benchmarks.md, "Model storage"): a run off the archive does not error, it measures a slow
// disk.
func LoadManifest(path string) (*Manifest, error) {
	var m Manifest
	md, err := toml.DecodeFile(path, &m)
	if err != nil {
		return nil, fmt.Errorf("manifest %s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		var keys []string
		for _, k := range und {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("manifest %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &m, m.validate(filepath.Join(home, "models"))
}

func (m *Manifest) validate(modelsDir string) error {
	if m.Box == "" || m.GoinferServe == "" || m.GoinferRev == "" || m.Workspace == "" {
		return errors.New("manifest: box, goinfer_serve, goinfer_rev and workspace are required")
	}
	if len(m.Arms) == 0 {
		return errors.New("manifest: no [[arm]]")
	}
	m.GoinferServe, m.Workspace = expand(m.GoinferServe), expand(m.Workspace)
	seen := map[string]bool{}
	for i := range m.Arms {
		a := &m.Arms[i]
		if a.Name == "" || a.ModelPath == "" {
			return fmt.Errorf("manifest: arm %d needs name and model_path", i)
		}
		if seen[a.Name] {
			return fmt.Errorf("manifest: two arms named %q", a.Name)
		}
		seen[a.Name] = true
		a.ModelPath = expand(a.ModelPath)
		if !strings.HasPrefix(filepath.Clean(a.ModelPath), modelsDir+string(filepath.Separator)) {
			return fmt.Errorf("manifest: arm %s reads %s; models are read from %s only, never the archive", a.Name, a.ModelPath, modelsDir)
		}
		if len(a.Ctx) == 0 {
			a.Ctx = []int{16384, 8192}
		}
		if a.DecodePath == "" {
			a.DecodePath = DecodePathFor(a.Args)
		}
		if a.DecodePath == "" {
			return fmt.Errorf("manifest: arm %s names no -backend and no decode_path; an arm names the path it measures", a.Name)
		}
	}
	return nil
}

func expand(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}
