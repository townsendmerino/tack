# Brief for goinfer: the night queue stops a tack server still running on its box (tack's G6, a safety net)

<!-- citations-at: goinfer f0d11c70 -->

**From:** tack (DESIGN §13 G6, P1; sent 2026-10-17, made smaller the same day on the owner's word).

**For:** goinfer's usual process: task doc or brief, then vscode-claude, then goinfer's gates.

**Order:** goinfer's tool-call parsing fix first, because the E1 pilot and E1 wait on it. Then G2
(`docs/briefs/goinfer-g2-fit-json.md`). Then this, G6.

## What changed, and what tack needs now

The first version of this brief asked `night.py` to stop tack's server before the queue and start it again after. The
owner has since made nobara's tack-facing server **on demand** (DESIGN §8.2):

- nothing runs all day;
- tack starts `goinfer-serve` when a session needs a model, preferably through a systemd user unit with socket
  activation, and it unloads after an idle timeout (15 minutes by default);
- tack **refuses to start it while goinfer's timing lock is held**, reports nobara as busy, and offers the Mac's
  model.

So the queue no longer needs to start anything. What remains is a safety net for the one case tack cannot prevent: a
tack-facing server still running, inside its idle window, when the queue starts.

## What goinfer already has

- **`scripts/night.py start`** detaches a runner that runs the queue one job at a time.
- **Each job runs under the box's timing lock** (`scripts/timing_lock.py`, TE9). tack's start path checks the same
  lock, so no new start can happen during the queue.

## The ask

When the queue starts, before its first job, `night.py` runs a box-local stop command, and logs what it did in
`SUMMARY.md`.

- **Where the command comes from is goinfer's call.** A per-box config file outside the repo keeps `night.py` free
  of any knowledge of tack, for example `~/.config/goinfer/night-hooks.toml` with
  `before_queue = "/home/francis/tack-runs/serve/stop"`.
- **The stop is a no-op when nothing is running**, and exits 0. tack supplies it; it stops by pidfile, or through the
  systemd unit once that exists.
- **A stop that fails keeps the queue from starting**, and `night.py` says why: timed jobs must not run beside a
  server it could not stop.
- **No start command.** tack starts its server on demand.

## Not asked

- Any change to the timing lock, or to how jobs run.
- Anything around day jobs or ad hoc timed runs, or on the Mac.

## How tack will check it

- A dry run with a no-op job: with tack's server running, the queue's first job starts only after the stop has
  exited 0. With nothing running, the stop is a no-op and the queue starts. A failing stop keeps the queue from
  starting.
