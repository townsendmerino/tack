package swiftgen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedSwiftIsCurrent fails when internal/proto changed without `go run ./tools/protogen`.
func TestGeneratedSwiftIsCurrent(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "..", "..", OutPath))
	if err != nil {
		t.Fatalf("%v (run go run ./tools/protogen)", err)
	}
	if !bytes.Equal(committed, Generate()) {
		t.Fatalf("%s is stale: run go run ./tools/protogen and commit it with the protocol change", OutPath)
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{"ID": "id", "FromSeq": "fromSeq", "JSONRPC": "jsonrpc",
		"PrefillMS": "prefillMS", "Protocol": "`protocol`", "Request": "request"} {
		if got := prop(in); got != want {
			t.Errorf("prop(%q) = %q, want %q", in, got, want)
		}
	}
	if got := eventCase("tool.finished"); got != "toolFinished" {
		t.Errorf("eventCase = %q", got)
	}
	if got := methodIdent("session/subscribe"); got != "sessionSubscribe" {
		t.Errorf("methodIdent = %q", got)
	}
}
