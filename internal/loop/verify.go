package loop

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/tools"
)

// The done-verification rule (Config.VerifyDone; e1-calibration-preregistration-2026-10-18.md §4.2): after an edit or
// write succeeds, an edit task's done is refused until a verification has run since, at most VerifyRefusals times in a
// turn. A verification is a bash call whose parsed command line (perms.Engine.Analyze) runs go build, go vet or go
// test; it need not pass.

// VerifyRefusals is how many times a turn refuses done for want of a verification; the next done ends the turn.
const VerifyRefusals = 3

// verifyTailLines is how many lines of a verification's output a refusal quotes.
const verifyTailLines = 20

// What done answers when it ends the turn with Config.VerifyDone on and the last change unverified, or verified by a
// build or test that failed; otherwise it answers "done". The calibration's records read them (pilot.TackSummary).
// VerifyRefusal begins every refusal of done for want of a verification.
const VerifyRefusal = "done refused: no build or test has run since"

// VerifyAdvice ends every refusal: the cheapest verification that counts. A whole-repository go test can fail for
// reasons the change cannot fix (ken's, on the sandbox's loopback refusal), so it is not what the refusal asks for.
const VerifyAdvice = "Run go build ./..., or go test on the package you changed, then call done."

const (
	DoneUnverified   = "done (unverified: no build or test ran after the last change)"
	DoneVerifyFailed = "done (the last build or test after the last change failed)"
)

// verification is one bash call that ran go build, go vet or go test: the turn's call number, its command line, its
// exit status (-1 when it was stopped) and the last lines of its output.
type verification struct {
	call    int
	command string
	exit    int
	tail    string
}

// verifyState is a turn's record for the rule: the call number of its latest successful edit or write (0 for none),
// its latest verification, and how many times done has been refused.
type verifyState struct {
	change   int
	last     *verification
	refusals int
}

var exitStatusRe = regexp.MustCompile(`\[exit status (\d+)\]\n?$`)

// observe records a call the tool ran: call is its number in the turn, ran what the tool returned.
func (v *verifyState) observe(call int, name string, input json.RawMessage, ran tools.Result, eng *perms.Engine) {
	switch name {
	case "edit", "write":
		if !ran.IsError {
			v.change = call
		}
	case "bash":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) != nil || !IsVerification(eng, in.Command) {
			return
		}
		exit := 0
		if ran.IsError {
			exit = -1
			if m := exitStatusRe.FindStringSubmatch(ran.Text); m != nil {
				exit, _ = strconv.Atoi(m[1])
			}
		}
		lines := strings.Split(strings.TrimRight(ran.Text, "\n"), "\n")
		if len(lines) > verifyTailLines {
			lines = lines[len(lines)-verifyTailLines:]
		}
		v.last = &verification{call: call, command: in.Command, exit: exit, tail: strings.Join(lines, "\n")}
	}
}

// verified reports whether a verification has run since the latest change (true when nothing has changed).
func (v *verifyState) verified() bool {
	return v.change == 0 || v.last != nil && v.last.call > v.change
}

// refusal is done's answer when the latest change is unverified: the call that made it, the latest verification before
// it, if any, and what to do.
func (v *verifyState) refusal() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s your change at call %d.", VerifyRefusal, v.change)
	if l := v.last; l != nil {
		status := fmt.Sprintf("exited %d", l.exit)
		if l.exit < 0 {
			status = "was stopped"
		}
		fmt.Fprintf(&b, " The last one, `%s` at call %d, %s:\n%s\n", l.command, l.call, status, l.tail)
	} else {
		b.WriteString(" ")
	}
	b.WriteString(VerifyAdvice)
	return b.String()
}

// finish is done's answer when it ends the turn.
func (v *verifyState) finish() string {
	switch {
	case !v.verified():
		return DoneUnverified
	case v.change > 0 && v.last.exit != 0:
		return DoneVerifyFailed
	}
	return "done"
}

// IsVerification reports whether command, as eng parses it, runs go build, go vet or go test in one of its simple
// commands. Text that only names them (echo go test) does not; without an engine nothing does.
func IsVerification(eng *perms.Engine, command string) bool {
	if eng == nil {
		return false
	}
	for _, s := range eng.Analyze(command) {
		if sub := perms.GoSubcommand(s); sub == "build" || sub == "vet" || sub == "test" {
			return true
		}
	}
	return false
}
