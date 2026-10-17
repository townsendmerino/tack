package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// instrumentSum is the sha256 of E0's frozen instrument (p0SystemPrompt, p0Tools, p0UserText). It was taken when the
// copies were made, at a commit where they equalled tack's own loop.SystemPrompt, read/glob/grep schemas and
// loop.UserText unchanged since E0 ran at 352fba0. Change it only with an E0 pre-registration amendment.
const instrumentSum = "9f6505c35b36ce720fe852630ed3c5766109872706b42da1ba29fc00fec37b5f"

func instrumentDigest() string {
	tl, _ := json.Marshal(p0Tools)
	h := sha256.New()
	h.Write([]byte(p0SystemPrompt))
	h.Write(tl)
	h.Write([]byte(p0UserText("/w/tack", "q")))
	return hex.EncodeToString(h.Sum(nil))
}

// TestInstrumentIsFrozen fails when E0's frozen prompt, tool schemas or user text change.
func TestInstrumentIsFrozen(t *testing.T) {
	if got := instrumentDigest(); got != instrumentSum {
		t.Fatalf("E0's instrument changed (sha256 %s, frozen %s): E0's requests must send what its runs sent", got, instrumentSum)
	}
}
