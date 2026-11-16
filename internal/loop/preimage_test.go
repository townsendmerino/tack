package loop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

// writeCall is a model reply that calls write.
func writeCall(id, path, content string) goinfertest.Reply {
	return call(id, "write", map[string]any{"path": path, "content": content})
}

// An edit stores the file's pre-image before it runs, and the checkpoint event naming it comes before the call's result.
func TestEditStoresItsPreImage(t *testing.T) {
	h, _ := checkpointHarness(t, nil, false, readCall("r", "mcp/token_budget.go"), editCall("e", "mcp/token_budget.go", "package mcp", "package mcp // x"), goinfertest.Says("done"))
	p := filepath.Join(h.ws.Root(), "mcp", "token_budget.go")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	cps := h.checkpoints()
	if len(cps) != 1 || cps[0].ID != "e" || len(cps[0].Files) != 1 {
		t.Fatalf("checkpoints %+v", cps)
	}
	f := cps[0].Files[0]
	if f.Path != "mcp/token_budget.go" || f.Absent || f.Blob == "" {
		t.Fatalf("file %+v", f)
	}
	got, err := h.st.Blobs.Get(f.Blob)
	if err != nil || string(got) != string(before) {
		t.Errorf("pre-image %q, %v; want the file before the edit", got, err)
	}
	if now, _ := os.ReadFile(p); string(now) == string(before) {
		t.Error("the edit did not run")
	}
}

// A write to a new path records that the file was absent, so a rewind deletes it.
func TestWriteToANewFileRecordsAbsent(t *testing.T) {
	h, _ := checkpointHarness(t, nil, false, writeCall("w", "new/made.txt", "hi\n"), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	cps := h.checkpoints()
	if len(cps) != 1 || len(cps[0].Files) != 1 || !cps[0].Files[0].Absent || cps[0].Files[0].Blob != "" || cps[0].Files[0].Path != "new/made.txt" {
		t.Fatalf("checkpoints %+v", cps)
	}
}

// A read takes no pre-image, and a refused edit (a path outside the workspace) records none.
func TestReadsAndRefusedEditsStoreNoPreImage(t *testing.T) {
	h, _ := checkpointHarness(t, nil, false, readCall("r", "mcp/token_budget.go"), editCall("e", "../outside.txt", "a", "b"), goinfertest.Says("done"))
	if _, err := h.sess.Turn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if cps := h.checkpoints(); len(cps) != 0 {
		t.Errorf("checkpoints %+v", cps)
	}
}
