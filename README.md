# tack

A coding agent for the Mac that runs entirely on local models: a Go daemon with a CLI and TUI, then a native macOS app,
built on [ken](https://github.com/townsendmerino/ken) for code search and
[goinfer](https://github.com/townsendmerino/goinfer) for inference.

P0 is built and P1 (the daily-driver CLI) is in progress: `tack run` answers questions about a repository and makes
edits on a local model, under a sandbox. The design is [`docs/design/DESIGN.md`](docs/design/DESIGN.md); its §16.1
says what is built so far.

## Status

tack is pre-release. It is not yet usable as a daily tool, and nothing here should be read as a claim that it works
well.

**What works today.** `tack run` answers questions about a repository and makes edits with a local model, with shell
commands under a macOS sandbox, through a per-user daemon. The terminal UI is in progress. The macOS app, the context
inspector and editor integration are not built. [`DESIGN.md` §16.1](docs/design/DESIGN.md) has the full table.

**What has been measured.** The numbers below are from a 12-task calibration set mined from the author's own
repositories (ken, goinfer, aikit). It is small, and it was built to tune the loop, not to compare tack with anything.
Each record is in [`docs/measurements/`](docs/measurements/), with the rule it was read under registered before the
run.

| what ran | result | record |
|---|---|---|
| pilot, Qwen2.5-7B at a 16k window | 0 of 20 runs passed | [e1-pilot](docs/measurements/e1-pilot-2026-10-17.md) |
| baseline, 12 tasks × 3 | 0 of 36 | [baseline](docs/measurements/e1-calibration-baseline-2026-10-18.md) |
| Qwen3-8B at 16k, two nights, 168 runs per arm | 9 of 168 with no loop settings, 26 of 168 with the bundle of loop changes | [c6-powered](docs/measurements/e1-calibration-c6-powered-2026-10-31.md) |
| Qwen3.6-35B-A3B on the author's Linux GPU machine, with the bundle | 12 of 36 at 16k, 13 of 36 at 32k | [c8](docs/measurements/e1-calibration-c8-qwen36-2026-10-30.md) |

So the best configuration measured passes about a third of these tasks.

**Most changes screened did not show an effect.** Each screen compares one setting against the same loop without it,
36 runs per arm, and keeps the setting off unless it clears a registered margin.

| setting | difference in passes | verdict | record |
|---|---|---|---|
| the bundle, at screen size (Qwen3-8B) | +1 | not shown | [c6-bundle](docs/measurements/e1-calibration-c6-bundle-2026-10-28.md) |
| the bundle, two nights (Qwen3-8B) | +17 of 168 | shown, adopted | [c6-powered](docs/measurements/e1-calibration-c6-powered-2026-10-31.md) |
| the bundle with snapshots on, one night (Qwen3-8B) | +3 of 48 | parked | [c11](docs/measurements/e1-calibration-c11-onenight-2026-11-07.md) |
| `mask_observations` | −1 | not shown | [c9](docs/measurements/e1-calibration-c9-screens-2026-11-01.md) |
| `best_of` | +3 | parked | [c9](docs/measurements/e1-calibration-c9-screens-2026-11-01.md) |
| `lean_results` | +2 | parked | [c10-lean](docs/measurements/e1-calibration-c10-lean-2026-11-01.md) |
| `compact_at_cliff` | +1 | not shown | [c10-cliff](docs/measurements/e1-calibration-c10-cliff-2026-11-02.md) |
| turn-1 context injection (a no-model ceiling check) | 4 of 12 tasks' fixes land in the injection | did not pass its gate | [c7](docs/measurements/e1-c7-injection-ceiling-2026-10-30.md) |

"Parked" means inside the margin where chance is a likely explanation; the setting stays off. One comparison has
cleared its rule so far, the two-night bundle run.

**What has not been measured.** tack against another agent on the same model, the full evaluation the design
describes (E1), and any of the context features the design bets on. Those bets are still bets.

## License

MIT. See [`LICENSE`](LICENSE). To report a vulnerability, see [`SECURITY.md`](SECURITY.md).
