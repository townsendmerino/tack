package eval

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCommittedManifestsLoad keeps eval/e0/*.toml valid: a manifest that fails to load would fail its night job.
func TestCommittedManifestsLoad(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "eval", "e0", "*.toml"))
	if len(paths) == 0 {
		t.Fatal("no manifests in eval/e0")
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}
