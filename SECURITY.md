# Security Policy

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security
vulnerabilities. Instead, use GitHub's private vulnerability reporting:
the **Security** tab → **Report a vulnerability** on
[townsendmerino/tack](https://github.com/townsendmerino/tack/security/advisories/new).

This is a small project maintained on a best-effort basis; expect an
acknowledgment within a few days. Please include a description, the
affected commit, and a reproduction if you have one. Coordinated
disclosure is appreciated — give us a chance to ship a fix before going
public.

## Supported versions

tack is pre-release and has no tagged release yet. Security fixes target
`main` only.

## Security model — what to know

tack is a coding agent: a local model reads a repository and proposes
edits and shell commands, which tack runs for the user. It runs locally,
is pure Go with no cgo, and calls no hosted model and sends no telemetry.
The network is used only to download models the user asks for and to
reach a model server the user configures.

- **The sandbox is the boundary.** On macOS, commands the model runs go
  through a Seatbelt profile that starts from `(deny default)`
  (`docs/design/DESIGN.md` §6.6). There is no Linux sandbox yet, and
  without a sandbox tack refuses to run commands or edit.
- **Content never grants anything.** Files, search results and tool
  output are data. Nothing in them is meant to change a permission or a
  mode.
- **What is approved is what runs.** Shell commands are parsed and
  executed by the same library, never handed to a shell as a string.
- **The daemon is per user.** Its socket is `0600` in a `0700` directory
  and it checks the peer's credentials.

## Scope

In scope: anything that lets content the agent reads (a repository, a
file, tool output, a model's reply) or another local user escape the
sandbox, run a command the permission rules should have stopped or asked
about, reach tack's socket or state, read secrets the redaction and
deny-list cover, or make tack contact a network host the user did not
configure.

Out of scope: a model writing wrong or harmful code inside the workspace
it was given write access to; anything requiring the user to approve the
harmful action when asked.
