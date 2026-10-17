# Brief for goinfer: the night queue stops tack's model server on its box before it runs (tack's G6)

<!-- citations-at: goinfer f0d11c70 -->

**From:** tack (DESIGN §13 G6, P1; sent on the owner's word 2026-10-17).

**For:** goinfer's usual process: task doc or brief, then vscode-claude, then goinfer's gates.

**Order:** goinfer's tool-call parsing fix first, because the E1 pilot and E1 wait on it. Then G2
(`docs/briefs/goinfer-g2-fit-json.md`). Then this, G6.

## What tack needs

The owner's default is tack on the Mac with the model on nobara's RTX 2070 SUPER (DESIGN §8.2). For daily work, a
tack-facing `goinfer-serve` runs on nobara with the 7B loaded, listening on the tailnet. goinfer's night queue measures
on the same card, and goinfer's rule is that nothing runs beside a timed job. So that server must be stopped before
the queue's first job and started again after its last, without the owner remembering to. tack reports the server as
unavailable meanwhile.

## What goinfer already has

- **`scripts/night.py start`** detaches a runner that runs the queue one job at a time until the queue is empty, a
  `STOP` file, or the morning deadline.
- **Each job runs under the box's timing lock** (`scripts/timing_lock.py`, TE9): `run_job` acquires it, and children
  inherit it through the environment.
- **Nothing in `night.py` knows about other servers on the box.** A tack-facing `goinfer-serve` would keep its 4–7 GB
  of VRAM, and answer tack's requests, through every timed job.

## The ask

`night.py` runs a box-local stop command before the queue's first job and a start command after its last job,
whatever ended the queue: empty, `STOP`, the deadline, or a crash it can catch. It also logs both in `SUMMARY.md`.

- **Where the commands come from is goinfer's call.** A small per-box config file outside the repo keeps `night.py`
  free of any knowledge of tack. For example `~/.config/goinfer/night-hooks.toml`:

  ```toml
  before_queue = "/home/francis/tack-runs/serve/stop"
  after_queue  = "/home/francis/tack-runs/serve/start"
  ```

- **A stop that fails** means the queue does not start, and `night.py` says why. Running timed jobs beside a server it
  could not stop is the failure this exists to prevent.
- **A start that fails** is logged in `SUMMARY.md` and does not fail the queue.
- tack supplies both commands, which stop and start by pidfile.

## Not asked

- Any change to the timing lock, or to how jobs run.
- Stopping the server around day jobs or ad hoc timed runs. Those already go through the timing lock and the owner's
  eye.
- Anything on the Mac. tack's Mac server is a supervised child that tack itself stops.

## How tack will check it

- A goinfer test, or a dry run with two no-op jobs: the hooks run once each, in order, around the jobs, including when
  the queue ends on `STOP` or the deadline. A failing `before_queue` stops the queue from starting.
- On nobara, after a real queue: the tack-facing server's log shows it stopped before the first job's `=== START` and
  started after the last `=== END`.

## Until it lands

The owner stops and starts the server by hand around the queue. These are the commands, run from the Mac.

Bedtime, before starting the queue:

```sh
ssh nobara ~/tack-runs/serve/stop
ssh nobara ~/mycode/goinfer/scripts/night.py start
```

Morning, after the queue has finished (`night.py status` says "no runner going"):

```sh
ssh nobara ~/tack-runs/serve/start
```
