package core

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/townsendmerino/tack/internal/proto"
)

// TestCompleteThroughTheCore: workspace/complete answers for the session's workspace with its paths, leaves out what
// .gitignore ignores, clamps the limit (default 20, at most 50), and is refused without the client token and for an
// unknown session.
func TestCompleteThroughTheCore(t *testing.T) {
	f, tui, _, sid, ws, _ := askingSession(t)
	os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("build/\n"), 0o644)
	os.MkdirAll(filepath.Join(ws, "build"), 0o755)
	os.WriteFile(filepath.Join(ws, "build", "out.go"), nil, 0o644)
	for i := range 60 {
		os.WriteFile(filepath.Join(ws, "src_"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go"), nil, 0o644)
	}
	var res proto.WorkspaceCompleteResult
	if err := tui.Call(proto.MethodWorkspaceComplete, proto.WorkspaceCompleteParams{Session: sid, Query: "out"}, &res); err != nil {
		t.Fatalf("workspace/complete: %v", err)
	}
	for _, it := range res.Items {
		if it.Path == "build/out.go" || it.Path == "build" {
			t.Errorf("an ignored path was offered: %+v", it)
		}
	}
	res = proto.WorkspaceCompleteResult{}
	if err := tui.Call(proto.MethodWorkspaceComplete, proto.WorkspaceCompleteParams{Session: sid, Query: "src_"}, &res); err != nil || len(res.Items) != 20 {
		t.Errorf("the default limit: %d items, %v; want 20", len(res.Items), err)
	}
	res = proto.WorkspaceCompleteResult{}
	if err := tui.Call(proto.MethodWorkspaceComplete, proto.WorkspaceCompleteParams{Session: sid, Query: "src_", Limit: 500}, &res); err != nil || len(res.Items) != 50 {
		t.Errorf("a limit of 500: %d items, %v; want 50", len(res.Items), err)
	}
	if !slices.IsSortedFunc(res.Items, func(a, b proto.CompletionItem) int { return len(a.Path) - len(b.Path) }) {
		t.Errorf("equal-quality paths are not shortest first: %+v", res.Items[:3])
	}
	var pe *proto.Error
	if err := f.client.Call(proto.MethodWorkspaceComplete, proto.WorkspaceCompleteParams{Session: sid, Query: "x"}, nil); !errors.As(err, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Errorf("without the token: %v; want unauthorized", err)
	}
	if err := tui.Call(proto.MethodWorkspaceComplete, proto.WorkspaceCompleteParams{Session: "s_01JAMISSING000000000000000", Query: "x"}, nil); err == nil {
		t.Error("an unknown session was accepted")
	}
}
