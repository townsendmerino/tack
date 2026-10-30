package window

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/townsendmerino/tack/internal/tools"
)

// The generated repository map (e1-calibration-preregistration-2026-10-18.md §13.3, as fixed before its gate 0): the
// most-referenced Go files and their exported symbols, from ken's structural index, within RepoMapBytes.
const (
	// RepoMapBytes is the map's cap: 1,000 tokens at 3 bytes a token.
	RepoMapBytes = 3000
	// RepoMapTitle opens the map.
	RepoMapTitle    = "Repository map (the most-referenced Go files and their exported symbols):"
	repoMapSymbols  = 8
	repoMapWordExpr = `[A-Za-z_][A-Za-z0-9_]*`
)

// MapIndex is what the map asks ken: each file's definitions. kensearch.Searcher is one.
type MapIndex interface {
	Outline(ctx context.Context, path string) ([]tools.Def, error)
}

var repoMapWord = regexp.MustCompile(repoMapWordExpr)

// RepoMap is root's map:
//   - its rows are the .go files outside _test.go, testdata/ and vendor/, each with its exported definitions (a method
//     kept when its own name is exported);
//   - a symbol's references are the other .go files whose text holds its name (a method's own name) as a word;
//   - a file's score is its symbols' sum;
//   - files go by descending score, then by path, each as `path: A, B, …` with its 8 most-referenced symbols, while
//     the map stays within RepoMapBytes.
//
// It is "" when no file has an exported definition.
func RepoMap(ctx context.Context, ix MapIndex, root string) (string, error) {
	var goFiles, mapFiles []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		goFiles = append(goFiles, rel)
		if !strings.HasSuffix(rel, "_test.go") && !strings.Contains("/"+rel, "/testdata/") && !strings.Contains("/"+rel, "/vendor/") {
			mapFiles = append(mapFiles, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	files := map[string]int{} // how many .go files hold each word
	for _, f := range goFiles {
		b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		set := map[string]bool{}
		for _, w := range repoMapWord.FindAllString(string(b), -1) {
			set[w] = true
		}
		for w := range set {
			files[w]++
		}
	}
	type sym struct {
		name string
		refs int
	}
	type file struct {
		path  string
		score int
		syms  []sym
	}
	var ranked []file
	for _, f := range mapFiles {
		defs, err := ix.Outline(ctx, f)
		if err != nil {
			continue // not indexed: no structure to map
		}
		fi := file{path: f}
		for _, d := range defs {
			if !exportedName(leafName(d.Name)) {
				continue
			}
			n := max(0, files[leafName(d.Name)]-1) // other files: its own holds the name
			fi.syms = append(fi.syms, sym{d.Name, n})
			fi.score += n
		}
		if len(fi.syms) == 0 {
			continue
		}
		sort.SliceStable(fi.syms, func(i, j int) bool { return fi.syms[i].refs > fi.syms[j].refs })
		ranked = append(ranked, fi)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].path < ranked[j].path
	})
	var b strings.Builder
	b.WriteString(RepoMapTitle + "\n")
	for _, f := range ranked {
		var names []string
		for _, s := range f.syms[:min(len(f.syms), repoMapSymbols)] {
			names = append(names, s.name)
		}
		line := f.path + ": " + strings.Join(names, ", ") + "\n"
		if b.Len()+len(line) > RepoMapBytes {
			break
		}
		b.WriteString(line)
	}
	if b.Len() == len(RepoMapTitle)+1 {
		return "", nil // no file to map
	}
	return b.String(), nil
}

func leafName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func exportedName(name string) bool {
	for _, r := range name {
		return unicode.IsUpper(r)
	}
	return false
}
