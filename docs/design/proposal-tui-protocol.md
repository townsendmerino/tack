# Proposal: the protocol methods the TUI needs (P1 step 8)

Status: **proposal**, 2026-11-09, for the owner. It was drafted by mbp2-tack-p1s4, and the TUI's needs come from
mbp2-tack-p1s5, who builds it. Nothing here is built.

The TUI (DESIGN §10) needs three methods that do not exist yet:
- a permission answer, for inline approvals;
- `session/cancel`;
- a path search, for `@`-completion.

Three smaller ones follow from the same work: a session list, a mode change, and nothing for re-attach, which
`session/subscribe {from_seq}` already covers. DESIGN §9.2 sketches the first two; this doc fixes their fields. All
positions are UTF-16 code units (DESIGN §9.1). Every name below is the one DESIGN §9.2 already uses, where it uses
one: p1s5 called the approval `session/approve`, and it is `permission/respond` here, with the fields the TUI asked for.

**What an old daemon answers.** A daemon from before this work answers each new method with "method not found"
(-32601). There is no protocol version bump. The client degrades:
- approvals stay with today's rule, where a question with no answerer is denied (`no_user`);
- Esc has nothing to cancel with, and the TUI says the daemon is older and should be restarted;
- `@`-completion is off.

`usage/summary` set this pattern (p1-notes, step 6).

## 1. `permission/respond`: an inline approval

- **Request:** `{session, id, decision, scope, note?, rule?}`.
  - `id` is `permission.requested`'s `id`: the tool call's id, unique within a session, which a re-attached client
    has without knowing seqs.
  - `decision` is `allow` or `deny`.
  - `scope` is `once` or `session`. `session` is today's `loop.Approval.Session`: a rule over each simple command's
    program and first argument (`perms.Engine.AllowForSession`), or over the tool itself for a tool other than bash.
  - `note` is optional. On a deny, it reaches the model inside the refused tool result ("not run: the user declined:
    <note>"), so the model can change course. It is content, so it grants nothing.
  - `rule` is reserved for the TUI's "edit the rule" key (DESIGN §10). It is refused (invalid params) until it is
    built, since an edited rule needs a check that it is no wider than what was shown.
- **Response:** `{}`. The answer's truth is the `permission.resolved` event it causes.
  - A second answer, from another client or a late one, to an `id` already resolved gets a new error code,
    `not_pending` (-32006).
  - With several clients attached, the first answer wins.
- **Privilege:** the client token. DESIGN §9.2 and §6.6 already refuse this on a connection without it.
- **In the log:** no new event. The answer becomes the `permission.resolved` that `loop.Session.permit` writes now:
  `by: user`, `scope`, `rule_added`. `PermissionResolved` gains one field, `note` (fields are only added). The
  refused `tool.finished` carries the note to the model, so `loop.Rebuild` rebuilds the conversation from the log as
  it does today.
- **In the daemon:** the core gives the loop an `Approver` (`internal/loop.Approver`).
  - While a privileged client is subscribed to the session, `Approve` waits for `permission/respond`, for
    `session/cancel`, or for the last such client to leave. Each of the last two ends it as a deny.
  - With no privileged subscriber when the question is asked, it is today's immediate `no_user` deny, so a turn
    never waits on nobody.
  - The question is the `permission.requested` event the subscribers already receive, and it stays the question's
    only source. A client that attaches, or re-attaches with `session/subscribe {from_seq}`, finds an open question
    as a `permission.requested` with no `permission.resolved` after it, and offers its keys. So subscribe's result
    gains no "pending questions" field.

## 2. `session/cancel`

- **Request:** `{session}`.
- **Response:** `{}`, also when no turn is running, so a double Esc is harmless.
- **Effect:** it ends the running turn only. There is no prompt queue to clear: `session/prompt` on a busy session is
  refused with `busy` today.
  - The turn's context is cancelled, so a running command is killed by the path a halt uses, without a
    `daemon.halted` event, and its call ends `cancelled`.
  - A question pending at the time is resolved as a deny, `by: user`, reason "cancelled".
  - The turn ends with `turn.end {stop_reason: "cancelled"}` (`proto.StopCancelled`, which exists).
- **Privilege:** the client token. It changes a session's state, and a sandboxed client, such as a command the model
  runs, must not be able to end turns.
- **In the log:** what the cancel causes is logged: `permission.resolved` if a question was pending, `tool.finished`
  (`cancelled`) for a running call, then `turn.end`. The request itself is not an event; the log records outcomes, as
  it does for a halt. Rebuild already handles a cancelled turn.

## 3. `workspace/complete`: paths for `@`

- **Request:** `{session, query, limit?}`. `query` is what follows `@`, as typed. `limit` defaults to 20, at most 50.
- **Response:** `{items: [{path, kind}]}`.
  - `path` is workspace-relative, with forward slashes.
  - `kind` is `file` or `dir`. `symbol` is reserved for ken's symbols (DESIGN §10, "files and symbols from ken"),
    which will add `line`.
  - Matching is a subsequence (fuzzy) match on the path, ranked by a basename match first, then by path length.
- **Privilege:** the client token. A session names a workspace, and a sandboxed client must not list another one.
- **Filter:**
  - The read tools' own filter applies: the workspace root, no escape through `..` or a symlink, and the secrets
    deny-list (`internal/tools.Workspace.Resolve`). The walk skips what glob's walk skips: `skipDir`'s
    version-control and dependency trees, secrets and symlinks. A path a read would refuse is never listed.
  - `.gitignore` is new here. No read tool applies it today; `recently_changed` reads it through go-git's matcher,
    which this would reuse. Proposed: ignored paths are left out of completion, since a user typing `@` wants
    sources, not build output.
  - The read tools are unchanged, and still reach ignored files when asked by name.
- **Speed:** under 20 ms per keystroke on a 10,000-file tree.
  - That means an in-memory path list per workspace: ken's file list where ken runs, else a walk cached and
    refreshed on change. Not a walk per call.
  - The TUI debounces about 50 ms and drops stale answers by request id.
- **In the log:** nothing. It only reads, and changes no session state.

## 4. The smaller ones

- **`session/list {workspace?}`** returns `{sessions: [{id, workspace, created, model, mode, parent, last_turn_at,
  status}]}`, for the TUI's start picker and attach.
  - It needs the client token, since it lists every workspace.
  - Nothing is logged. It is read from the store, and from `index.db`'s session metadata once that holds it
    (DESIGN §6.2).
- **`session/setMode {session, mode}`** (DESIGN §9.2's name) returns `{}`. It needs the client token (DESIGN §6.6:
  mode is a permission state).
  - **In the log:** a new persisted event, `mode.changed {mode, by: "user"}`. Today a session's mode is fixed at
    `session.created`, so this is the first change of it, and "every change a client can make is an event" (DESIGN
    §6.2).
  - A reopened session takes the last `mode.changed`'s mode, else `session.created`'s.
  - It applies from the next turn. A running turn keeps the mode it started with, so an approval in flight is never
    judged by two modes.
- **Re-attach:** `session/subscribe {session, from_seq}` exists, and replays from `from_seq`. Nothing is needed.

## Design calls for the owner

1. Whether `session/cancel`, `workspace/complete` and `session/list` need the client token. Proposed: yes, all three.
   DESIGN names the token only for methods that change permissions, mode, trust or halt state.
2. Whether `permission/respond` waits indefinitely while a privileged client is attached, as proposed, or has a
   timeout after which it becomes `no_user`. A timeout would make an unattended TUI deny, rather than hold the turn.
3. Whether `session/setMode` applies from the next turn, as proposed, or at once.
4. Whether `@`-completion leaves out `.gitignore`d paths, as proposed. Nothing else in tack applies `.gitignore` to
   what the user or the model may name.

## Order

Approvals and cancel come first: step 8's inline approvals are not usable without them. Then `workspace/complete`,
then `session/list` and `session/setMode`. Each lands with its protocol types, the regenerated Swift, a fixture in
`proto/fixtures/`, and tier-0 tests through the core.
