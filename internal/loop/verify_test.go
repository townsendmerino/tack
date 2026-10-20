package loop

import (
	"os"
	"testing"

	"github.com/townsendmerino/tack/internal/perms"
)

// TestIsVerification: tack's shell parser decides, so go build, vet or test anywhere in a command line counts, through
// cd, &&, pipes and go -C, and text that only names them does not.
func TestIsVerification(t *testing.T) {
	eng := &perms.Engine{Root: t.TempDir(), Sandboxed: true, Path: os.Getenv("PATH")}
	for cmd, want := range map[string]bool{
		"go build ./...":                      true,
		"go vet ./...":                        true,
		"cd internal/a && go test -run X ./.": true,
		"go test ./... 2>&1 | tail -20":       true,
		"go -C internal test ./...":           true,
		"echo go test":                        false,
		"go version":                          false,
		"gofmt -l .":                          false,
		"printf 'go build'":                   false,
		"go run ./cmd/x":                      false,
	} {
		if got := IsVerification(eng, cmd); got != want {
			t.Errorf("%q: %v, want %v", cmd, got, want)
		}
	}
	if IsVerification(nil, "go test ./...") {
		t.Error("with no engine, nothing is a verification")
	}
}
