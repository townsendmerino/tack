package perms

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
	"mvdan.cc/sh/v3/syntax"
)

// Action is the engine's answer for one tool call.
type Action string

const (
	Allow Action = "allow"
	Ask   Action = "ask"
	Deny  Action = "deny"
)

// Who decided, as recorded in permission.resolved.
const (
	ByMode     = "mode"
	ByRule     = "rule"
	ByUser     = "user"
	ByNoUser   = "no_user" // it needed the user, and no one was attached to answer
	ByReadOnly = "read_only"
)

// Verdict is the engine's answer for one tool call: what to do, who decided, and for Ask or Deny, why, one reason per
// cause. Parsed holds a shell command's simple commands as approval saw them.
type Verdict struct {
	Action  Action
	By      string
	Reasons []string
	Parsed  []Simple
}

// Rule allows a command without asking: a program, and the arguments it must start with. "go test" allows go test with
// any further arguments; it does not allow anything else on the same command line (DESIGN §6.6).
type Rule struct {
	Program string   // a name looked up on PATH, or a path
	Args    []string // the leading arguments, literal
}

func (r Rule) String() string { return strings.TrimSpace(r.Program + " " + strings.Join(r.Args, " ")) }

// ParseRule reads a rule written as a command: a program and its leading arguments, all literal.
func ParseRule(s string) (Rule, error) {
	f, err := syntax.NewParser().Parse(strings.NewReader(s), "")
	if err != nil {
		return Rule{}, fmt.Errorf("perms: rule %q: %v", s, err)
	}
	if len(f.Stmts) != 1 {
		return Rule{}, fmt.Errorf("perms: rule %q must be one simple command", s)
	}
	call, ok := f.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 || len(call.Assigns) > 0 || len(f.Stmts[0].Redirs) > 0 {
		return Rule{}, fmt.Errorf("perms: rule %q must be a program and its leading arguments", s)
	}
	var r Rule
	for i, w := range call.Args {
		lit, ok := literal(w)
		if !ok {
			return Rule{}, fmt.Errorf("perms: rule %q: %s expands; rules are literal", s, source(w))
		}
		if i == 0 {
			r.Program = lit
		} else {
			r.Args = append(r.Args, lit)
		}
	}
	return r, nil
}

// Engine decides tool calls for one workspace. Rules may grow during a session (a user's "always"); it is not safe for
// concurrent use.
type Engine struct {
	Root  string // the workspace root
	Path  string // the PATH programs resolve on
	Rules []Rule

	tools map[string]bool // tools the user allowed for the session (AllowForSession)
}

func (e *Engine) root() string {
	if r, err := filepath.EvalSymlinks(e.Root); err == nil {
		return r
	}
	return e.Root
}

// Decide answers one tool call in mode. readOnly is the tool's own claim that it changes nothing (tools.Tool.ReadOnly).
//
//   - Read-only tools are allowed in every mode: file tools are confined to the workspace already.
//   - edit and write ask in ask mode, are allowed in edits and auto, and are refused in plan.
//   - bash is refused in plan and allowed in auto, where the sandbox is the boundary. In ask and edits it is allowed
//     only when every simple command is covered by a rule and nothing writes outside the workspace; otherwise it asks.
//   - Any other tool that changes something asks, except in plan, where it is refused.
func (e *Engine) Decide(mode proto.Mode, tool string, readOnly bool, input json.RawMessage) Verdict {
	if readOnly {
		return Verdict{Action: Allow, By: ByReadOnly}
	}
	if mode == proto.ModePlan {
		return Verdict{Action: Deny, By: ByMode, Reasons: []string{"plan mode is read-only: propose the plan, and the " +
			"user switches the mode to carry it out"}}
	}
	if e.tools[tool] {
		return Verdict{Action: Allow, By: ByUser}
	}
	switch tool {
	case "edit", "write":
		if mode == proto.ModeAsk {
			return Verdict{Action: Ask, By: ByMode, Reasons: []string{"file changes ask in ask mode"}}
		}
		return Verdict{Action: Allow, By: ByMode}
	case "bash":
		var in struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(input, &in); err != nil || in.Command == "" {
			return Verdict{Action: Deny, By: ByMode, Reasons: []string{"bash needs a command"}}
		}
		cmds, why := e.checkShell(in.Command)
		if mode == proto.ModeAuto {
			return Verdict{Action: Allow, By: ByMode, Parsed: cmds}
		}
		if len(why) > 0 {
			return Verdict{Action: Ask, By: ByRule, Reasons: why, Parsed: cmds}
		}
		return Verdict{Action: Allow, By: ByRule, Parsed: cmds}
	}
	if mode == proto.ModeAuto {
		return Verdict{Action: Allow, By: ByMode}
	}
	return Verdict{Action: Ask, By: ByMode, Reasons: []string{tool + " changes something, and no rule covers it"}}
}

// AllowForSession extends a user's allow to the rest of the session and returns what was added, as written in
// permission.resolved. For bash, each simple command becomes a rule: its resolved program and, when literal, its first
// argument (approving "go vet ./..." for the session allows "go vet"). For another tool, the tool itself is allowed.
func (e *Engine) AllowForSession(tool string, parsed []Simple) []string {
	var added []string
	if tool != "bash" {
		if e.tools == nil {
			e.tools = map[string]bool{}
		}
		e.tools[tool] = true
		return []string{"tool " + tool}
	}
	for _, c := range parsed {
		if c.Builtin != "" || c.Program == "" {
			continue
		}
		r := Rule{Program: c.Program}
		if len(c.Args) > 0 && c.Literal[0] {
			r.Args = []string{c.Args[0]}
		}
		e.Rules = append(e.Rules, r)
		added = append(added, r.String())
	}
	return added
}
