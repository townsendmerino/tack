# E1 pilot — A0 against A0k: pre-registration

**Registered 2026-10-16, before the runner exists and before any agent run.** DESIGN §15.1 and §15.2 define the
pilot: a first read on whether ken helps a local agent on real tasks, before tack exists. **It informs; it decides
nothing.** Its other job is to size E1: minutes per run, A0's pass rate (the difficulty band of
`e1-task-selection-preregistration-2026-10-16.md`), and the spread D13 needs.

## Tasks

The 10 tasks in `eval/e1/pilot.jsonl`, chosen by the registered selection rules with amendment 2, each with the
statement the owner approved in `eval/e1/pilot-statements.md`. A task whose statement the owner drops is left out. The
pilot then runs with 9, and records that.

## Box, server and model

- **nobara** (RTX 2070 SUPER 8 GB). Its night queue (goinfer's `scripts/night.py`) runs the job under the timing lock.
- **`goinfer-serve` CUDA 41e13b26**, cross-built (`eval/e0/nobara.toml`): `-backend cuda -ctx 16384 -log-requests`.
- **Qwen2.5-7B-Instruct `q4_k_m`** from `~/models/qwen2.5-7b-instruct-q4_k_m.gguf`. It was fit-checked on 2026-10-16
  at 13:33 (`~/tack-runs/e0/fitcheck-2026-10-16-nobara`), admitted at 16384, `cuda-resident (int4)`, using 6.3 GB of
  VRAM.
- One server for the whole job, started by the runner. Every run of both arms talks to it.

## Arms

- **A0: opencode 1.19.0**, as installed in `~/harnesses` on nobara, with a clean configuration. Its project-local
  `opencode.json` is the one goinfer's `connect opencode` writes:
  - the `@ai-sdk/openai-compatible` provider at the server;
  - `limit.context` set to the served 16384;
  - `small_model` set to the `-fast` alias.

  It runs as `opencode run --format json --auto -m goinfer/<model> --dir <task repo> "<statement>"`. opencode's own
  tools and agent are used unchanged.
- **A0k: A0 plus ken-mcp v1.9.0**, cross-built from the tagged ken (`2853c7b`). It is configured as a local MCP server
  in the same `opencode.json`, over the task repository, with its embedding model in `~/.ken/model`. Nothing else
  differs.
- **Unmodified repositories.** Both arms see each repository's instruction files (`CLAUDE.md`, `AGENTS.md`) exactly
  as they are at the base commit, as a user's setup would (DESIGN §15.1).

## A run

- **The task repository** holds only the base commit and its ancestors: `git init`, then `git fetch` of the base from a
  mirror on nobara. No later commit is reachable from its refs. It is built before the timed run starts, so its setup
  is not counted.
- **The cap** is 10 minutes of wall time, measured from launching opencode until it exits. At the cap the runner kills
  opencode's process group, and the run counts as failed.
- **The oracle.** After the run, the commit's named test files are copied into the task repository and its check
  command runs (`eval/e1/pilot.jsonl`, `check_cmd`). The task passes only if every oracle test reports `pass`; a skip
  or a build failure is a fail.
- **One run per task per arm: 20 runs** (owner, 2026-10-16). Repeats belong to E1.
- **Order.** Tasks run in `pilot.jsonl` order. Within a task, A0 goes first on odd-numbered tasks and A0k first on
  even-numbered ones, so neither arm always meets a cold server.

## Recorded per run

- pass or fail;
- wall time and **capped time** (a failed run counts as the full 10 minutes, PAR-1);
- whether the cap was hit;
- opencode's JSON event stream, kept whole;
- from the server's `-log-requests` lines for the run's window: per request prompt tokens, reused tokens, time to first
  token and total time;
- the number of tool calls by tool name;
- the edit's diff against the base;
- **any tool call that reads or writes a path outside the task repository.** opencode runs unsandboxed as the owner's
  user, and nobara's clones of goinfer, ken and aikit hold the fixes, so such a call is a possible leak. A run that
  makes one is marked `left_repo` and reported both ways: counted, and excluded.

## What it reports

- A0's and A0k's pass rates over the tasks, with their paired difference. Bootstrap intervals are shown, but at 10
  tasks they are wide, and no claim is made from them.
- Mean capped time per arm, and the paired ratio on the tasks both solved.
- Minutes per run, which replace DESIGN §15.2's 5–10-minute placeholder for E1's machine-time plan (D10).
- Whether A0's pass rate falls inside 20–80%. Outside it, the selection rules' single registered difficulty adjustment
  applies before E1.
- Per repository, and per task.

## Queue plan, against the worst case

- Every run reaching its 10-minute cap is 200 minutes of agent time. The oracle, at most 2 minutes, makes 12 minutes
  per run worst case: **240 minutes for all 20**.
- That is split into **two night jobs of 5 tasks each, both arms, 120 minutes each worst case**. Each is queued with
  `--est 120` and a 150-minute timeout, so neither breaks goinfer's three-hour job cap.
- Repository setup and the server's load (about 30 s) are done at a job's start, before the first timed run, and fit
  inside each job's margin.
- Before queueing, the job's server configuration is fit-checked again on the day, with the same binary, model and
  flags.

## What it does not decide

- Whether ken helps. E1 decides that, with A2 and A3 against A1, at a margin sized from variance this pilot supplies.
- Anything about tack's loop, which is not an arm here.
