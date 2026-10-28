# E1 calibration C6: the next direction, mechanical edits or a stronger model (proposal, 2026-10-27)

<!-- citations-at: 03b6979 -->

Francis asked at 18:16 for two directions to be weighed. Five text nudges in a row changed nothing (§12.10, §12.17,
§12.18, §12.19, §12.20). The changes that worked were mechanical (4.4, 4.5, 4.6). Nothing here was run on a model.
Direction 1 is a replay of the captured failed edits; direction 2 is read from the records.

## Direction 1: change what edit does

**The replay.** `tools/replayedits`, as used for the 4.1 replay, rebuilds each file as it was at the failed edit. A
throwaway copy of it tried three matchers on every "text found nowhere" failure. Each match had to be unique. The
matched place was checked against the lines the task's own fix changes, and the edited file against Go's parser.
- **Set A** is the 4.1 replay's runs: C4, C5, 4.6 and 4.4, 216 runs, the source of the 217 figure.
- **Set B** is the bundle-era Qwen3-8B screens and checks: 335 runs.
- Outputs: `~/tack-runs/e1/replayfuzzy-*` on the Mac.

| matcher | set A: calls (distinct, runs) | set B | is the applied edit right? |
|---|---|---|---|
| whitespace removed entirely | 61 (2) | 0 | **yes**: task 11, a space before a comma; the edit lowercases the error string, as the fix does |
| a `;` at a line's end dropped (Go) | 11 (1) | 8 (1) | legal Go, and the model's intended change, but off the fix's lines |
| token fuzzy match ≥ 0.80, unique | 194 (4 runs) | 33 (8) | **no**: 7 of 8 in set B leave a file that does not parse, and set A's task 11 cases leave a `return` with one value where the function returns two |
| none matches (or two places tie) | 12 | 32 | — |

- **Why fuzzy matching is wrong here:** the model's `old_string` leaves out code that the file has (`"", fmt.Errorf(`,
  a closing `)`, `case` written as `if`). Its `new_string` is built on that wrong view, so applying it at the nearest
  place deletes or breaks that code. Of the 278 "nowhere" calls in set A (217 plus the 61 diagnostic), 194 are of this
  kind.
- **Line ranges:** already checked. Qwen3-8B made 0 line edits in 6 runs (§12.19).
- **A safe mechanical rule outside the 217: an edit to a file not yet read.** When `old_string` matches the file
  exactly once, the edit could apply (3 distinct in A, 8 in B). That changes DESIGN §7.5 (edit only what was seen),
  so it is Francis's decision.
- **Size of the win:** the safe rules (whitespace removed, `;` dropped, not read but exact) touch about 1–3 runs per
  200–300. At most about one pass per 72-run screen. Too small for a screen to see; adoption by replay, as §12.14's
  duplicate guard was adopted, fits.

## Direction 2: a stronger model

| model | where it stands | per run, expected | first step and cost |
|---|---|---|---|
| **Qwen3.5-9B `Q4_K_M`** | **E0 eligible at 32,768** (19/20 both runs, 0 malformed, grammar; 65 tok/s; 7,183 MiB of VRAM). Its blocker, no prefix reuse, is **fixed on goinfer main by B35** (#112, 2026-10-21), but not re-measured by tack. Double Qwen3-8B's window. | about 2–3 min against Qwen3-8B's 1.2 min: prefill of each appended result at roughly a third of 8B's rate, decode 20% slower. Unmeasured. | **The registered trigger:** re-run its C3 row with E0 amendment 11's append probe, on a build with B35. By day, about 45 minutes, nobara's lock. Then a C5-style calibration row: 36 runs, about 1.5–2 h. |
| Qwen3.6-35B-A3B, experts streamed | **void at E0**: goinfer's swap guard tripped, host RSS 33 GB | — | Out on nobara until goinfer's expert streaming fits host memory. |
| gpt-oss-20b | **ineligible**: smoke 9/20, 8 malformed names (`greplicate`); owner: out with experts streamed | — | Returns only after goinfer's Harmony parser question, then a new C2. |
| gemma-4 12B `q4_0` | **does not fit**: nothing is left beside the weights under goinfer's reserve | — | Out on this card. |
| the Mac (M1 Pro, 16 GiB) | nothing larger than 8B measured; P28 (Gemma's slow Metal prefill) is open | — | A 14B-class model's ~9 GB of weights plus KV runs only at night under the fit guard. Not now. |

## Pros and cons

- **Direction 1.**
  - Pros: no model change; cheap; judged by replay, with no timed runs.
  - Cons: the safe part is tiny, and the large part (194 calls) would make wrong edits. It does not touch why the
    model's text is wrong.
- **Direction 2 (Qwen3.5-9B).**
  - Pros: a different model, with twice the window, aimed at the failure itself. Its first step is already
    registered and costs 45 minutes.
  - Cons: speed is unmeasured. Each calibration row is a 1.5–3 h job. C6's bundle was tuned on Qwen3-8B. A9B may
    also simply ignore text in the same way.

## Recommendation

1. **First, the bundle comparison** (§12.21, approved), Wednesday by day as planned.
2. **Then direction 2's registered first step:** Qwen3.5-9B's E0 append probe on a B35 build, by day, about 45
   minutes.
   - If reuse works and the smoke still passes, register a C5-style row for it with the bundle on.
3. **Alongside, with no timed runs:** direction 1's two safe tolerances (whitespace removed; a trailing `;` dropped),
   built with tier-0 tests and adopted by replay.
   - Drop fuzzy matching.
   - Put the "not read but exact" rule to Francis as a DESIGN §7.5 question.
