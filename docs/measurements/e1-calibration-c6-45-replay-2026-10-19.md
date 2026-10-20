# E1 calibration C6, change 4.5: the stop rule, judged by replay, 2026-10-19

<!-- citations-at: 1b39d48 -->

Change 4.5 under C6, judged by §12.7's criterion, registered at 21:21 PDT (`1b39d48`) before the replay ran: adopted if
the rule would have stopped no passing run, and not adopted if it would have stopped any.

**Result: adopted.** Replayed over 144 captured runs, the rule would have stopped 36 and **no passing run**. It would
have saved 4,243 model requests and 102 minutes of wall time.

## What ran

- **The rule:** tack `898f97a` (local branch `c6-45`, unpushed pending the owner's review).
  - `loop.RepeatGuard`: the 8th unchanged repeat of one call (same tool and input, same result as its last) ends the
    turn with `repeat_loop`.
- **The replay:** `tools/replayrepeats`, built from that commit, feeds each run's tool calls, with the results the model
  saw, through the same `RepeatGuard`. The stop takes effect after the stopping response's results.
  - `TestReplayRepeatsMatchesTheLiveRule` holds the replay to the live rule.
- **The runs:** 4.6's 72 (both arms), C5's 36 and C4's 36, from their `results.jsonl` and each run's `events.jsonl`.
  No run's events were missing.
- **Output:** `~/tack-runs/e1/c6-45-replay-2026-10-19.txt` on the Mac.

## Results

| job | runs | stopped | passing runs stopped | requests saved | wall time saved |
|---|---|---|---|---|---|
| 4.6 (A and B) | 72 | 18 (9 per arm) | 0 | 1,150 | 24 min |
| C5 (Qwen3-8B, 16k) | 36 | 6 | 0 | 314 | 6 min |
| C4 (Qwen3-4B, 30k) | 36 | 12 | 0 | 2,779 | 72 min |
| **all** | **144** | **36** | **0** | **4,243** | **102 min** |

- **Where the stops fall:**
  - after request 9 to 56, with most between 10 and 20;
  - the runs went on to 26–384 requests.
- **The stopping tool:** grep 18, glob 8, read 6, edit 3, bash 1.
  - An `edit` stops when the same failing edit comes back with the same answer.
- **The largest savings are C4's grep loops:** tasks 1, 5 and 7 ran 330–384 requests and were stopped at 10–20, each
  saving 350–575 s.
- **No passing run reached 8 unchanged repeats.** The three passes in 4.6, the four in C5 and the none in C4 all
  finished first.

**Against the prediction (§12.7):** 25–40 runs stopped, and no passing run. Both held: 36 stopped, 0 passing.

The per-run list (task, arm, the request where it stopped, the tool, the requests and seconds saved) is in the output
file.

## What follows

- **4.5 is adopted.** It was pushed as `3e2da1e` (a cherry-pick of `898f97a`). Under §12.1's
  stacking, it is on in both arms of every later change, beside §12.6's `fit_window` background condition.
- **No separate live check (owner).** 4.4's runs, with `stop_repeats` on in both arms, are the live check. Their record
  replays them and confirms every `repeat_loop` stop and the exit-9 count.
