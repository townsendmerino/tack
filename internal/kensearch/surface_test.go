package kensearch

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbidden is ken/engine's surface that is for ken's own binaries (DESIGN §12, "How tack uses it"): the escape hatches,
// and the aliases into ken's internal packages.
var forbidden = regexp.MustCompile(`\.(WatchedIndex|StructuralIfBuilt|SetReranker|SetExtraChunks)\(|` +
	`engine\.(Index|WatchedIndex|StructuralIndex|VecCache|Reranker|RerankerOption|WithRerankN|WithRerankBlendBeta|Mode)\b`)

var kenImport = regexp.MustCompile(`"github\.com/townsendmerino/ken(/[^"]*)?"`)

// TestOnlyEnginesPublicSurface fails if any tack source imports a ken package other than ken/engine, or reaches past
// engine.Open, the Workspace methods and the value types.
func TestOnlyEnginesPublicSurface(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || d.Name() == ".git" || strings.HasPrefix(d.Name(), ".")) && p != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "surface_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src := string(b)
		for _, m := range kenImport.FindAllStringSubmatch(src, -1) {
			if m[1] != "/engine" {
				t.Errorf("%s imports %s: tack uses only ken/engine", p, m[0])
			}
		}
		if strings.Contains(src, `"github.com/townsendmerino/ken/engine"`) {
			checked++
			if loc := forbidden.FindString(src); loc != "" {
				t.Errorf("%s uses %s, which is ken's own (DESIGN §12)", p, loc)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no file imports ken/engine: the scan found nothing to check")
	}
}
