# CB0: ContextBench & LEANN External Environment, Break-It-First Smoke, and Sizing (2026-10-28)

**Status:** Complete (`VERDICT: PASS`)
**Parent design & research:** [`docs/research/contextbench-leann-2026-10-28.md`](../research/contextbench-leann-2026-10-28.md) §4.1 (`CB0`), §9.1 (Track 3)
**Companion brief for `ken`:** [`docs/briefs/ken-k16-k18-contextbench-memory.md`](../briefs/ken-k16-k18-contextbench-memory.md) (`K18`)

---

## 1. Provenance & External Layout

Per §1.3 and §4.1 of [`docs/research/contextbench-leann-2026-10-28.md`](../research/contextbench-leann-2026-10-28.md), ContextBench carries no license ("All Rights Reserved" by default). Nothing from ContextBench or LEANN is vendored into `tack` or `ken`; all repositories, virtual environments, and repository caches live outside both repositories under `~/tack-runs/cb0/`:

| Component | Path | Commit / Version | Size on Disk |
|---|---|---|---|
| ContextBench checkout | `~/tack-runs/cb0/ContextBench` | `cd5f62e4f3a426ed2a497ad7d1584e1c4b219058` (`2026-03-31`) | `294 MB` |
| LEANN checkout | `~/tack-runs/cb0/LEANN` | `11ca8a5350e23f89d110120bfa95467e6d87acd4` (`2026-04-06`, head on `main`) | `108 MB` |
| ContextBench evaluator venv | `~/tack-runs/cb0/venv-cb` | `Python 3.10.19` (`/opt/homebrew/opt/python@3.10/bin/python3.10`) | `360 MB` |
| Repo cache (after 1 Go repo) | `~/tack-runs/cb0/repo_cache` | `github.com__flipt-io__flipt` bare/cached clone | `12 MB` |

### Pinned Evaluator Packages (`~/tack-runs/cb0/venv-cb`)

`tree-sitter==0.20.4` and `tree-sitter-languages==1.10.2` require `Python <= 3.11` for prebuilt `darwin/arm64` / `manylinux` wheels (`tree-sitter-languages 1.10.2` has no `cp312+` wheels, and `tree-sitter 0.21+` breaks the 2-argument `Language(path, name)` API used by `tree_sitter_languages`).

| Package | Version |
|---|---|
| `python` | `3.10.19` (`darwin/arm64`) |
| `tree-sitter` | `0.20.4` |
| `tree-sitter-languages` | `1.10.2` |
| `pandas` | `2.3.3` |
| `pyarrow` | `25.0.1` |
| `unidiff` | `1.0.1` |
| `datasets` | `4.8.4` |
| `tqdm` | `4.67.3` |

---

## 2. Dataset Verification & `S53` Mapping

All 6 parquet splits under `~/tack-runs/cb0/ContextBench/Context-Bench/data/` load cleanly:

| Parquet File | Rows | Unique Repos |
|---|---:|---:|
| `full.parquet` | 1,136 | 66 |
| `contextbench_verified.parquet` (ContextBench-Lite) | 500 | 58 |
| `poly.parquet` | 485 | 25 |
| `pro.parquet` | 295 | 23 |
| `multi.parquet` | 192 | 12 |
| `verified.parquet` | 164 | 11 |

### `S53` Subset (`53` tasks committed in `LEANN/benchmarks/contextbench/scripts/`)

Every `instance_id` in `contextbench_official_eval_claude_baseline.jsonl` (`53` rows) and `contextbench_official_eval_claude_leann.jsonl` (`53` rows) maps `1:1` to `full.parquet` across **7 repositories** and **3 languages** (`Python`, `Go`, `JavaScript`/`TypeScript`):

| Repository | Language | `S53` Tasks | `ken` Chunker Support (`regex` + `treesitter`) |
|---|---|---:|---|
| `ansible/ansible` | Python | 20 | Supported (`.py`) |
| `internetarchive/openlibrary` | Python | 12 | Supported (`.py`) |
| `qutebrowser/qutebrowser` | Python | 11 | Supported (`.py`) |
| `flipt-io/flipt` | Go | 5 | Supported (`.go`) |
| `NodeBB/NodeBB` | JavaScript | 2 | Supported (`.js`) |
| `element-hq/element-web` | TypeScript / JS | 2 | Supported (`.ts`, `.tsx`, `.js`) |
| `future-architect/vuls` | Go | 1 | Supported (`.go`) |
| **Total (`S53`)** | **3 languages** | **53** | **53 / 53 supported** |

### 5-Task Pilot List for `CB1` (`flipt-io/flipt`, Go)

The 5 `flipt-io/flipt` Go tasks in `S53` share a single `12 MB` cached repository clone (`github.com__flipt-io__flipt`) and serve as the fast 5-task pilot for `CB1` (`K18`):

1. `SWE-Bench-Pro__go__maintenance__bugfix__40a717e5` (`266e5e143e87`)
2. `SWE-Bench-Pro__go__maintenance__bugfix__b91d5788` (`9c3cab439846`)
3. `SWE-Bench-Pro__go__maintenance__bugfix__1177cd53` (`165ba79a4473`)
4. `SWE-Bench-Pro__go__maintenance__bugfix__997c7afd` (`e432032cf2d1`)
5. `SWE-Bench-Pro__go__maintenance__bugfix__52d866b3` (`4e066b8b836c`)

---

## 3. Break-It-First Evaluator Smoke (`evaluate.py`) & Parity Check

Per [`docs/research/contextbench-leann-2026-10-28.md`](../research/contextbench-leann-2026-10-28.md) §4.1 ("Break-it-first on the evaluator"), we constructed 3 synthetic `traj_data` inputs on `SWE-Bench-Pro__go__maintenance__bugfix__40a717e5` (`flipt-io/flipt` at `266e5e143e87519047b9be3202a0aba273b83de3`, which has `413` merged gold lines across `13` gold files and `17` tree-sitter gold symbols) and scored them through `contextbench.evaluate.evaluate_instance` and `contextbench.metrics.coverage_precision`:

| Smoke Case | Input Construction | Hand-Computed Expected | `evaluate_instance` Output | `tack` `ScoreContext` Output | Parity Verdict |
|---|---|---|---|---|---|
| **Case 1: Exact Gold** | 1 step containing the exact merged gold intervals (`413` lines across `13` files) | `line.cov = 1.000000`, `line.prec = 1.000000`, `file.cov = 1.000000`, `symbol.cov = 1.000000`, `auc.line = 1.000000`, `red.line = 0.000000` | `line.cov = 1.000000`, `line.prec = 1.000000`, `file.cov = 1.000000`, `symbol.cov = 1.000000`, `auc.line = 1.000000`, `red.line = 0.000000` | `FinalCov = 1.000000`, `Precision = 1.000000`, `FileCov = 1.000000`, `AUCCov = 1.000000`, `Redundancy = 0.000000` | **PASS (`< 1e-9`)** |
| **Case 2a: Empty Prediction** | `pred_files = []`, `pred_spans = {}` | `coverage_precision(0, 413, 0)` $\to$ `cov = 0.000000`, `prec = 1.000000` | `coverage_precision`: `cov = 0.000000`, `prec = 1.000000`; `evaluate_instance`: `{'error': 'no_context_extracted'}` | `FinalCov = 0.000000`, `Precision = 1.000000`, `FileCov = 0.000000`, `AUCCov = 0.000000` | **PASS** (see edge-case note below) |
| **Case 2b: Disjoint Non-Gold (Red)** | 1 step viewing `README.md:1-10` (0 overlap with the 13 gold files) | `line.cov = 0.000000`, `line.prec = 0.000000`, `file.cov = 0.000000`, `auc.line = 0.000000` | `line.cov = 0.000000`, `line.prec = 0.000000`, `file.cov = 0.000000`, `auc.line = 0.000000` | `FinalCov = 0.000000`, `Precision = 0.000000`, `FileCov = 0.000000`, `AUCCov = 0.000000` | **PASS (`< 1e-9`)** |
| **Case 3: Hand Partial** | 1 step viewing `CHANGELOG.md:5-10` (`6` gold lines) + `CHANGELOG.md:12-13` (`2` non-gold lines) = `8` predicted lines in `1` gold file | `line.cov = 6/413 = 0.014528`, `line.prec = 6/8 = 0.750000`, `file.cov = 1/13 = 0.076923`, `file.prec = 1/1 = 1.000000`, `auc.line = 0.014528` | `line.cov = 0.014528`, `line.prec = 0.750000`, `file.cov = 0.076923`, `file.prec = 1.000000`, `auc.line = 0.014528` | `FinalCov = 0.014528`, `Precision = 0.750000`, `FileCov = 0.076923`, `AUCCov = 0.014528` | **PASS (`< 1e-9`)** |

### Evaluator Edge-Case Findings for `K18` (`bench/contextbench`)

1. **Empty `pred_files` in `evaluate_instance`:** When `pred_files` is empty (`[]`), `parse_trajectory` returns `([], [], {})` and `evaluate_instance` exits early with `{'instance_id': ..., 'error': 'no_context_extracted'}` instead of emitting a `final` dictionary. `aggregate_results` in `evaluate.py` skips records with `'error'` when computing the mean over valid instances! Therefore, in `CB1` (`K18`), if a retrieval arm returns 0 hits on a task (e.g., `Grep` when no literal matches), the JSONL emitter or aggregator must account for `no_context_extracted` so zero-hit tasks are scored as `coverage = 0.0, precision = 1.0` across all `N` tasks rather than silently dropped from the denominator.
2. **Worktree checkouts in `/tmp/contextbench_worktrees`:** `evaluate.py` clones bare/cached repositories into `--cache` (`~/tack-runs/cb0/repo_cache`) and checks out working trees under `$TMPDIR/contextbench_worktrees/<repo>/<commit>` (`56 MB` for `flipt-io/flipt@266e5e143e87`). Between large batches (`Lite` / `full`), pruning `$TMPDIR/contextbench_worktrees` prevents temporary worktree accumulation while preserving `repo_cache`.

---

## 4. Real Task 4: Committed `S53` Evaluation Records (`flipt-io/flipt`)

On `SWE-Bench-Pro__go__maintenance__bugfix__40a717e5`, LEANN's two committed evaluation files (`contextbench_official_eval_claude_baseline.jsonl` and `contextbench_official_eval_claude_leann.jsonl`) record:

| Metric | Claude Code Baseline (`26` steps) | Claude Code + LEANN (`18` steps) | Delta (`LEANN - Baseline`) |
|---|---:|---:|---:|
| `final.file.coverage` | `0.6923` (`9 / 13`) | `0.6923` (`9 / 13`) | `0.0000` |
| `final.file.precision` | `0.4737` (`9 / 19`) | `0.9000` (`9 / 10`) | `+0.4263` |
| `final.symbol.coverage` | `0.1765` (`3 / 17`) | `0.1765` (`3 / 17`) | `0.0000` |
| `final.symbol.precision` | `0.6000` (`3 / 5`) | `1.0000` (`3 / 3`) | `+0.4000` |
| `final.line.coverage` | `0.2857` (`118 / 413`) | `0.2494` (`103 / 413`) | `-0.0363` |
| `final.line.precision` | `0.2511` (`118 / 470`) | `0.2379` (`103 / 433`) | `-0.0132` |
| `trajectory.auc_coverage.line` | `0.0640` | `0.0558` | `-0.0082` |
| `trajectory.redundancy.line` | `0.2432` | `0.1054` | `-0.1378` |
| `editloc.recall` | `0.8529` (`29 / 34`) | `0.8485` (`28 / 33`) | `-0.0045` |

---

## 5. Disk Sizing & Containment Rules (`CB0`)

### Disk Sizing Summary

- **Single Go repository cache (`flipt-io/flipt`):** `12 MB` in `repo_cache` + `56 MB` per active worktree in `$TMPDIR/contextbench_worktrees`.
- **Projected `S53` (7 repositories):** `< 2.5 GB` in `repo_cache` (`ansible/ansible` and `internetarchive/openlibrary` are the largest of the 7).
- **Projected `ContextBench-Lite` (58 repositories):** `~8–15 GB` in `repo_cache`; deleting worktrees and indexes after each task keeps active disk overhead under `5 GB`.

### Containment Rules

1. **LEANN (`L` arm in `CB1`):**
   - LEANN's benchmark script (`prepare_repos_with_leann.py`) loads `jinaai/jina-embeddings-v2-base-code` with `trust_remote_code=True`.
   - When running the `L` arm, pin the Hugging Face model revision to a specific commit SHA, run inside its own isolated virtual environment (`~/tack-runs/cb0/venv-leann`) with no credentials or access to owner repositories, and index only disposable checkouts under `~/tack-runs/cb0/`.
2. **Claude Code agent runs (`CB2`, if unparked after `CB1`):**
   - Never run `claude --dangerously-skip-permissions` on a host checkout with access to the owner's home directory, SSH keys, or repositories.
   - Run inside a dedicated unprivileged container/user workspace containing only the disposable target repository checkout and an explicit `--allowedTools` list (`Read`, `Grep`, `Glob`, `Edit`, and the arm's MCP search tool).

