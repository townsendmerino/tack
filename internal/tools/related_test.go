package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeRelater answers Related with hits it was given, recording what it was asked.
type fakeRelater struct {
	anySearch
	relates bool
	hits    []Hit
	asked   []string
}

type anySearch struct{}

func (anySearch) Search(context.Context, string, int) ([]Hit, error) { return nil, nil }

func (f *fakeRelater) Relates() bool { return f.relates }

func (f *fakeRelater) Sync(_ context.Context, paths []string) (bool, error) {
	f.asked = append(f.asked, "sync "+strings.Join(paths, ","))
	return true, nil
}

func (f *fakeRelater) Related(_ context.Context, path string, line, k int) ([]Hit, error) {
	f.asked = append(f.asked, path+":"+strings.Repeat("|", 0)+itoa(line)+" k="+itoa(k))
	if k < len(f.hits) {
		return f.hits[:k], nil
	}
	return f.hits, nil
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func relatedOf(t *testing.T, f *fakeRelater) Tool {
	t.Helper()
	tool, ok := New(Options{Searcher: f, FindRelated: true}).Get("find_related")
	if !ok {
		t.Fatal("find_related is not in the set")
	}
	return tool
}

// TestFindRelatedPrintsTheNearestCode: find_related asks for the code near path:line by meaning, with the path as
// the index names it (repo-relative), and prints each hit as search does.
func TestFindRelatedPrintsTheNearestCode(t *testing.T) {
	ws := workspace(t)
	f := &fakeRelater{relates: true, hits: []Hit{{File: "b/other.go", StartLine: 3, EndLine: 5, Text: "func other() {}\n"}}}
	r, _ := relatedOf(t, f).Run(context.Background(), ws, json.RawMessage(`{"path":"long.txt","line":12}`))
	if r.IsError || !strings.Contains(r.Text, "b/other.go:3-5\nfunc other() {}") {
		t.Fatalf("result %+v", r)
	}
	// The path is synced first: after an edit with no watcher (tack run), ken still holds the old chunks.
	if len(f.asked) != 2 || f.asked[0] != "sync long.txt" || f.asked[1] != "long.txt:12 k=5" {
		t.Fatalf("asked %q; want long.txt synced, then long.txt:12 with the default k of 5", f.asked)
	}
}

// TestFindRelatedChecksItsInput: k is held to 1..10; a path outside the workspace, a missing line, and an empty
// answer each say so.
func TestFindRelatedChecksItsInput(t *testing.T) {
	ws := workspace(t)
	f := &fakeRelater{relates: true}
	tool := relatedOf(t, f)
	for _, c := range []struct{ in, want string }{
		{`{"path":"long.txt","line":3,"k":50}`, "no results"},
		{`{"path":"../outside.go","line":3}`, "outside"},
		{`{"path":"long.txt"}`, "line"},
		{`{"line":3}`, "path"},
	} {
		r, _ := tool.Run(context.Background(), ws, json.RawMessage(c.in))
		if !strings.Contains(r.Text, c.want) {
			t.Errorf("%s: %q; want it to mention %q", c.in, r.Text, c.want)
		}
	}
	if len(f.asked) != 2 || f.asked[1] != "long.txt:3 k=10" {
		t.Fatalf("asked %q; want only the in-workspace call, with k held to 10", f.asked)
	}
}

// TestFindRelatedIsListedOnlyWhenItCanAnswer: the tool is in the set only with FindRelated on and a searcher that
// relates (ken in semantic or hybrid mode); a bm25 index has no vectors, and a tool that only fails is not offered.
func TestFindRelatedIsListedOnlyWhenItCanAnswer(t *testing.T) {
	for _, c := range []struct {
		name string
		o    Options
		want bool
	}{
		{"on, relating", Options{Searcher: &fakeRelater{relates: true}, FindRelated: true}, true},
		{"off", Options{Searcher: &fakeRelater{relates: true}}, false},
		{"on, bm25", Options{Searcher: &fakeRelater{relates: false}, FindRelated: true}, false},
		{"on, a searcher with no Related", Options{Searcher: anySearch{}, FindRelated: true}, false},
		{"on, no searcher", Options{FindRelated: true}, false},
	} {
		if _, ok := New(c.o).Get("find_related"); ok != c.want {
			t.Errorf("%s: listed %v, want %v", c.name, ok, c.want)
		}
	}
}
