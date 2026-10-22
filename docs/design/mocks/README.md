# Mocks: how tack shows a model's work

Three mocks of how a turn's results read in tack's macOS app and terminal, built from two real calibration runs (2026-10-20, Qwen3-8B on nobara):

| File | What it shows | Source run |
|---|---|---|
| `Main.dc.html` | Mac app, a finished run | 4.1 screen, task 11 (ken), arm B, run 2: 9 requests, 22 s, passed |
| `Stopped.dc.html` | Mac app, a run tack stopped | 4.1 screen, task 2 (goinfer), arm A, run 1: 28 requests, 68 s, `repeat_loop` |
| `Terminal.dc.html` | The finished run in the terminal | same as `Main` |

They are Design-canvas artboards (`.dc.html`, with `canvas.json` as the index), so they render on the canvas, not as standalone pages. The two Mac boards are interactive: groups fold and unfold, Expand all / Collapse all work, and the rail and "Jump to latest" scroll to each step. Window chrome and the session list are illustrative; every step, number and quote comes from the runs.

## Principles

1. **Say what was learned, not what was called.** Each group folds to one line in plain words ("Found resolveRepo at mcp/server.go:74 and read lines 70–89"), with the step count and time beside it. The calls are one click away.
2. **Group by intent.** Consecutive searches and reads become "Explored"; repeated calls become one "Repeated itself" or "Tried the same edit ×8" row. A loop never shows as 16 identical rows.
3. **Never fold a change.** Edits show as diffs, open by default, with the file, line and +/− count in the header.
4. **Summarize tests as counts and the first failure.** "9 packages passed, 1 failed: TestFetch_…", raw output behind a toggle with its size. When tack knows the cause (a sandbox denial), it says so: "Blocked by tack's sandbox, not caused by this change."
5. **Check the model's claims against what tack saw.** If `done` says the tests passed and the last test run failed, say so on the result card.
6. **Explain a stop in plain words, from tack's own state.** "The project notes took 65% of the window … there was no room left to show it, so it edited from a guess", with next actions (retry with shorter notes, retry starting at the file).
7. **Keep the window visible.** A meter in the header, split into project notes, tack's prompt and the task, and this run's work. It turns red near the limit.
8. **Prose in proportional type; code and output in monospace; numbers in tabular figures.** Line numbers muted; diff lines tinted, not just colored text.
9. **Scrolling.** Follow the newest step while streaming; stop following once the person scrolls up, and show "Jump to latest". A rail lists the turn's steps for jumping. Folding keeps the toggled header in place, so nothing jumps under the reader. Smooth scrolling, off under reduced motion.
10. **Same model in the terminal.** ▸/▾ fold markers, one line per group, the same summaries and claim check; keys: ↑↓ move, space fold, `o` output, `a` expand all, `G` latest, `d` diff, `u` undo turn.

## What tack would need to emit

The summaries are computed by tack from tool results and loop state, never written by the model (a small model's own summaries are the least reliable part of a run). That implies a few additions to the protocol's events:

- **A group id and kind on each tool call** (explore, change, verify, repeat), assigned by the core as calls arrive.
- **A deterministic one-line summary on each tool result**: match counts, line ranges read, `go test` package counts and the first failing test, "changes nothing", "not shown: window nearly full".
- **A cause on failures tack can classify**: a sandbox denial (bind, connect, file write), the window, a missing path.
- **A claim check on `done`**: the reason the model gave, next to the last verification's command and exit status.
- **A stop explanation**: the stop reason plus the state behind it (window use by part, the repeated call and its count).
- **Window use by part** on each request: instruction notes, fixed prompt and task, conversation.

None of this is built yet; the TUI and the app are planned (DESIGN §11). These mocks are for discussion before that work starts.
