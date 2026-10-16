# E1 tasks — selection rules: pre-registration

**Registered 2026-10-16, before the miner was written or any candidate looked at.** DESIGN §15.1 defines E1's tasks.
This page fixes how they are found, verified, chosen and stated. A rule changes only with a mechanism, recorded as a
dated amendment above the first selection record, and never after a pilot or E1 result has been seen, except for the
one adjustment registered under "Difficulty".

## Population

The first-parent history of each repository's `main` branch, frozen at:

| repo | frozen at | first-parent commits |
|---|---|---|
| goinfer | `5d597aca` (2026-10-16) | 4,363 |
| ken | `2853c7b` (2026-10-16, v1.9.0) | 463 |
| aikit | `fd12e14` (2026-10-15) | 979 |

Only each repository's root Go module counts. goinfer's and aikit's GPU modules (`metal/`, `cuda/`, `gpu/`, the
`gpu/*` modules) need hardware an agent's task cannot rely on, and their tests skip without it.

## Stage 1 — static filter (cheap, by day)

A commit is a candidate when all of these hold:

1. It is a non-merge commit, and its subject does not start with `Revert` or `Merge`.
2. It changes 1 to 3 non-test `.go` files in the root module. "Non-test" means not `_test.go`, not under `testdata/`,
   and not generated (a `Code generated … DO NOT EDIT.` header).
3. Its non-test change is 1 to 80 lines (added plus deleted), and its whole diff is at most 400 lines.
4. Every changed non-test file is at most 1,000 lines after the commit. That keeps the edit site readable in a 16k
   window.
5. It adds or changes at least one top-level `func TestXxx(t *testing.T)` in a `_test.go` file of the same module. These
   are its **named tests**. A test file is changed in whole: the oracle applies the commit's version.
6. No changed file carries a `//go:build` constraint, and no named test's file imports `C`.
7. The named tests' packages are not under a GPU or model directory, and they read no file larger than 1 MB from
   `testdata/`. A test that needs a real checkpoint skips without one, and a skip fails the oracle anyway.

## Stage 2 — verification (a night job; each candidate once)

Each candidate is checked in a fresh repository holding only its parent commit and that commit's ancestors (`git init`,
then `git fetch <repo> <parent>`), so no later commit is reachable, `git log --all` included. Then:

1. The commit's versions of the named tests' files are copied in, and
   `go test -json -count=1 -timeout 120s -run '^(<named tests>)$' <their packages>` runs, with `GOWORK=off` and
   `GOFLAGS=-mod=mod`.
2. **On the parent**, every named test must fail. A compile failure of the test package counts as failing: a test that
   calls a function the commit adds cannot compile before it. A named test that passes on the parent is dropped from the
   oracle, and a candidate left with none is rejected. A named test that skips rejects the candidate.
3. **On the commit**, run twice, every remaining named test must report `pass`, not `skip`, on both runs, and each run
   must take at most 60 s in total.

## Stage 3 — choice

Verified candidates are ordered by `sha256(repo + "/" + full commit hash)`, ascending. The order is fixed by the data
and has nothing to do with the result. Then:

- **The pilot set** is the first 10 in that order, at most 4 from any one repository, skipping a candidate that would
  break the cap.
- **E1's set** is the next 60, at most 30 from any one repository (the per-repo breakdown, §15.1, needs every repo
  represented). D13 may add 60 more, taken from the same order.

The pilot set and E1's set do not overlap. Whatever the pilot teaches is applied to tasks it never saw.

## Statement and oracle

- **The statement** is an issue, not a spec of the fix: what is wrong or wanted, and the interface the named tests call
  (functions, types, flags, files). It is drafted by Claude Code from:
  - the named tests' bodies;
  - the signatures those tests call;
  - the commit message.

  It never quotes or paraphrases the non-test diff. The owner reviews each statement for leakage before the task is
  used. A statement the owner rejects is rewritten once, and a second rejection drops the task. Each task records its
  statement's author and review state.
- **The oracle** is the commit's named-test files, applied after the run, and the stage-2 command. Every named test must
  report `pass` in `go test -json`; a skip fails the task.
- **The manifest** (`eval/e1/tasks.jsonl`, one task per line) records:
  - the repo, the parent ("base") and the commit;
  - the named tests and their packages;
  - the check command and the statement;
  - why the task was picked: its stage-1 measurements;
  - its stage-2 outcomes (fail-on-parent, and pass and time on both commit runs).

## Difficulty

DESIGN §15.1 wants A0's pass rate between about 20% and 80%. The pilot measures it.

- **Inside the band:** E1 uses the rules above unchanged.
- **Outside the band:** one adjustment is allowed, registered here before any E1 run. Below 20% (too hard), the
  non-test line cap (stage 1, rule 3) halves to 40. Above 80% (too easy), its floor rises to 20. No other rule moves.

## What this does not decide

- Which arm is better. The pilot informs; it decides nothing (DESIGN §15.1).
- E1's run count or margin (D13), which come from the pilot's variance.
