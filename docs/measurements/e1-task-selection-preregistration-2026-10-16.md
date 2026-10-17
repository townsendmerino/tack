# E1 tasks — selection rules: pre-registration

**Registered 2026-10-16, before the miner was written or any candidate looked at.** DESIGN §15.1 defines E1's tasks.
This page fixes how they are found, verified, chosen and stated. A rule changes only with a mechanism, recorded as a
dated amendment above the first selection record, and never after a pilot or E1 result has been seen, except for the
one adjustment registered under "Difficulty".

**Amendment 2026-10-16, after stage 1 and the first verification pass, before any statement or agent run.** These are
procedural. None of them changes which candidates a full run would choose:

- **Verification order.** Stage 2 verifies candidates in stage 3's order, and may stop once a set is full
  (`-until-pilot`). Every candidate ahead of a chosen one has then been verified, so stopping early yields the same
  choice as verifying all of them.
- **Stage 3.** A candidate the pilot skips for its per-repo cap stays in order and is eligible for E1's set. "The next
  60" is read that way.
- **Stage 2 environment.** Verification works in a clone that shares the repository's objects (`git clone --shared`),
  checked out at the parent and then the commit. Later commits are reachable there, which does not matter because no
  agent runs in it. Agents' task repositories are still built from the parent and its ancestors only.
- **Check command.** It carries its environment: `GOWORK=off GOFLAGS=-mod=mod CGO_ENABLED=0 go test … -buildvcs=false`,
  exactly as verified.

**Amendment 2, 2026-10-16, before any statement or agent run** (owner). Stage 1 gains rule 8, "no literal leak",
 below. It applies to all 227 candidates, and the pilot's choice and E1's verification are re-run from the new candidate
 list. Three of the first pilot set were exact-text tasks: their tests asserted literal strings that the fix adds, so
 any statement would have to quote the fix. The rule is mechanical, so the selection stays blind to outcomes.

**Amendment 3, 2026-10-16, after the owner's statement review and before any agent task run** (owner). Stage 2 gains
the **stub check**. Task 8's oracle only checked that a call does not panic, so a fix that does nothing would pass it;
rather than drop task 8 by hand, a rule now applies to all candidates alike:

- At the fix, in every non-test Go file the commit changed, each top-level function or method whose declaration
  differs from the parent's, or is new, has its body replaced by a bare return of zero values: an empty body for no
  results, `return` for named results, `return *new(T1), *new(T2), …` otherwise. Imports left unused are removed so
  the package builds. The named tests stage 2 found (the oracle) then run once.
- **If every one of them passes, the candidate is rejected:** the oracle cannot tell the fix from no fix. **If the
  stubbed tree does not build, or the check cannot be run, the candidate is also rejected**: the check did not run,
  and a build failure is not a test telling a fix from no fix (owner, added before the check had run on any
  candidate). Otherwise (a named test fails or skips on the stubbed tree) the candidate is kept. A fix that changed
  no function body (only types, variables or constants) has nothing to stub and is kept.
- Stage 3 reports the rejections by reason: passed while stubbed, could not run, did not build
  (`internal/eval/mine/stub.go:stubVerdict`).
- Candidates verified before this amendment are stub-checked in stage 3's order by the same `-stage verify`, and
  stage 3 refuses any verified candidate without the check (`internal/eval/mine/stub.go:StubCheck`). The fixed-order
  choice is then re-run, and how many candidates the check removed is reported. A task that replaces a removed one
  gets a drafted statement and the owner's review, as the others did.

**Amendment 4, 2026-10-17, after full verification and before any E1 run** (owner). **Stage 3 writes E1's full order,
not a cut of it.**

- E1's order is every verified candidate in stage 3's order, after the pilot, less every task in the committed pilot
  set (`eval/e1/pilot.jsonl`). Tasks the stub check removed from the pilot are left out too.
- It is not cut to 60. **E1 takes the first N** once D13 is decided after the pilot, at most 30 from any one
  repository, skipping a candidate that would exceed it. This is the rule's "next 60" and D13's "60 more" with N left
  open (`internal/eval/mine/verify.go:Take`).
- Stage 3 refuses to write if the pilot it chooses is not the committed pilot's tasks that are not marked removed.
  A mismatch would mean the order or the verification changed under an approved pilot (`cmd/tack/mine.go:mineChoose`).
- The order is committed as `eval/e1/e1-order.jsonl`.

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

8. **No literal leak** (amendment 2). Every string literal of 20 or more characters inside the named tests' bodies is
   collected, as its value and as written. A candidate is rejected if any of them appears verbatim in a line the commit
   adds to a non-test file. A test that asserts exact output text would otherwise need a statement that spells out the
   fix.

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

**Applied 2026-10-17, before any E1 run** (owner). The pilot's A0 passed 0 of 10 (`e1-pilot-2026-10-17.md`), below
20%, so the cap halves to 40.

- **E1's order is now `eval/e1/e1-order-cap40.jsonl`:** the candidates of `eval/e1/e1-order.jsonl` whose
  `why.non_test_lines` is at most 40.
  - 71 of the 101 remain, in the same order: aikit 20, goinfer 38, ken 13. Each line is byte-identical to its line
    in the full order.
  - The full order stays as the record of how stage 3 chose; E1 takes its first N from the capped one (amendment 4).
- **No other rule moves.** Every remaining candidate already met rules 1–8 at the 80-line cap, and lowering the cap
  only removes candidates, so filtering the chosen order is the whole adjustment. The miner's `maxNonTestLines`
  stays 80 in code: it describes how these candidates were mined.
- **Note for whoever reads E1:** the pilot's failures do not look size-driven. Of its 20 runs:
  - 13 answered in prose instead of editing;
  - 5 edited the wrong thing;
  - 2 made the right one-line fix and missed an import.

  A smaller task may not change any of those. The adjustment is applied because it is the registered one, not
  because the evidence points at size (DESIGN §17, R13).

## What this does not decide

- Which arm is better. The pilot informs; it decides nothing (DESIGN §15.1).
- E1's run count or margin (D13), which come from the pilot's variance.
