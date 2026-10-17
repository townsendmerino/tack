// Package perms decides permissions: modes, rules on parsed commands, the sandbox profile, the secrets deny-list and
// halt (DESIGN §6.6). The engine here answers, for one tool call, allow, ask or deny, and why; the sandbox, not the
// rules, is the boundary, and an allow rule only skips the question.
package perms
