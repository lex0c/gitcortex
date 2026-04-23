# gitcortex

**Repository behavior analyzer.** Reads git history — commits, authors, dates, file paths, line counts — and surfaces signals about how people and processes interact with a codebase: hotspots, bus factor, coupling, churn risk, working patterns, collaboration networks. It analyzes the behavior recorded in git, not the source code itself — every metric is derived from who touched what, when, and with whom.

## Performance

See [`docs/PERF.md`](docs/PERF.md) for extended benchmarks.

Benchmarked on open-source repositories. `extract` reads bare clones; `stats` and `report` read the resulting JSONL. Measurements taken with a pre-built binary on a single machine (not a controlled lab benchmark; directional, not absolute).

| Repository | Commits | Devs | Extract | Stats (JSON) | Report (HTML) | JSONL size |
|------------|---------|------|---------|-------------|--------------|------------|
| [Pi-hole](https://github.com/pi-hole/pi-hole) | 7,077 | 281 | 1.5s | 0.18s | 0.24s | 23K lines / 6.5 MB |
| [Praat](https://github.com/praat/praat) | 10,221 | 19 | 25s | 0.96s | 0.95s | 95K lines / 30 MB |
| [WordPress](https://github.com/WordPress/WordPress) | 52,466 | 131 | 47s | 2.9s | 2.8s | 298K lines / 96 MB |
| [Kubernetes](https://github.com/kubernetes/kubernetes) | 137,016 | 5,295 | 2m 4s | 11.7s | 14s | 943K lines / 314 MB |
| [Linux kernel](https://github.com/torvalds/linux) | 1,438,634 | 38,832 | 12m 57s | 1m 15s | 1m 53s | 6M lines / 1.9 GB |

`extract`, `stats`, and `report` scale roughly linearly with dataset size. The per-dev collaborator map in `report` is pre-computed in a single pass over files (O(F × D_per_file²)); on the kubernetes snapshot that adds ~2 seconds over `stats`, on linux ~40 seconds. A previous implementation computed this nested inside the per-dev loop (O(D × F × D_per_file)) and was 6× slower on kubernetes and 11× slower on linux. If you only need the aggregate data, `stats --format json` is always the fastest path; reach for `report` when you actually want the HTML dashboard.

## Privacy and reliability

All processing is **100% local**. No external services, no network calls, **no AI**, no telemetry. gitcortex reads only git metadata (commits, authors, dates, file paths, line counts) — it never reads source code content. Commit messages are excluded by default and only included with `--include-commit-messages`. Data stays on your machine as a JSONL file that you control.

## Install

### Download binary (no Go required)

Pre-built binaries for Linux, macOS, and Windows are available on [GitHub Releases](https://github.com/lex0c/gitcortex/releases/latest):

```bash
# Linux (x64)
curl -L https://github.com/lex0c/gitcortex/releases/latest/download/gitcortex-linux-amd64 -o gitcortex
chmod +x gitcortex
sudo mv gitcortex /usr/local/bin/

# macOS (Apple Silicon)
curl -L https://github.com/lex0c/gitcortex/releases/latest/download/gitcortex-darwin-arm64 -o gitcortex
chmod +x gitcortex
sudo mv gitcortex /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/lex0c/gitcortex/releases/latest/download/gitcortex-darwin-amd64 -o gitcortex
chmod +x gitcortex
sudo mv gitcortex /usr/local/bin/
```

### Go install

```bash
go install github.com/lex0c/gitcortex/cmd/gitcortex@latest
```

### Build from source

```bash
git clone https://github.com/lex0c/gitcortex.git
cd gitcortex
make build
```

Other targets: `make test`, `make vet`, `make check` (vet + test), `make install`, `make clean`.

Check version: `gitcortex --version`

Requires Git 2.31+ and Go 1.21+. CI runs automatically on push/PR via GitHub Actions.

### Release

```bash
git tag v0.1.0
git push origin main --tags
```

The version is injected at build time from `git describe --tags`. After tagging, `make build && gitcortex --version` shows `v0.1.0`.

## Usage

### Extract

```bash
# Extract from current directory
gitcortex extract

# Extract from a specific repo and branch
gitcortex extract --repo /path/to/repo --branch main

# Include commit messages in output
gitcortex extract --repo /path/to/repo --include-commit-messages

# Custom output path
gitcortex extract --repo /path/to/repo --output data.jsonl

# Normalize author identities via .mailmap
gitcortex extract --repo /path/to/repo --mailmap

# Exclude files from extraction
gitcortex extract --repo /path/to/repo --ignore package-lock.json --ignore "*.min.js"

# Exclude entire directories
gitcortex extract --repo /path/to/repo --ignore "dist/*" --ignore "vendor/*"
```

The default branch is auto-detected from `origin/HEAD`, falling back to `main`, `master`, or `HEAD`.

The `--mailmap` flag uses git's built-in `.mailmap` support to unify developer identities. Without it, the same person with different emails (e.g., `alice@work.com` and `alice@personal.com`) appears as separate contributors.

### What gitcortex collects from git

Extraction runs two git commands against the local repository and streams their output. No source-code bytes are read.

```
git log -M --raw --numstat --format=<metadata> <branch>    → commits, parents, per-file diffs (counts only)
git cat-file --batch-check                                 → blob sizes (old/new) for each file change
```

Per-commit metadata (populates the `commit` record):

| Field | Source | Used by |
|---|---|---|
| `sha`, `tree`, `parents` | `git log --format` | commit graph, merge detection |
| `author_name`, `author_email`, `author_date` | `git log --format` | contributors, activity, working patterns, bus factor |
| `committer_name`, `committer_email`, `committer_date` | `git log --format` | committer identity feeds the `dev` registry (so a committer who is never an author still appears as a known developer); no other stat consumes these fields |
| `additions`, `deletions`, `files_changed` | summed from `--numstat` | summary totals, hotspots, churn-risk |
| `message` | `git log --format` | opt-in only (`--include-commit-messages`); truncated to 80 chars in `top-commits` when present |

Per-file-change metadata (populates the `commit_file` record):

| Field | Source | Used by |
|---|---|---|
| `path_current`, `path_previous`, `status` | `git log --raw` | hotspots, directories, extensions, rename tracking (`R100` / `C075` trigger merges) |
| `additions`, `deletions` | `git log --numstat` | per-file churn, recent churn, coupling |
| `old_hash`, `new_hash`, `old_size`, `new_size` | `git cat-file --batch-check` | retained but not currently used in stats |

**Not collected:**
- File contents / diff hunks — only line counts from `--numstat`.
- Commit messages (unless `--include-commit-messages` is passed).
- Tags, refs other than the traversed branch, reflog, notes.
- Any network traffic — extraction is 100% local to the git directory.

**Opt-ins that change what ships in the JSONL:**
- `--include-commit-messages` — adds the commit subject to each `commit` record (off by default).
- `--mailmap` — normalizes author/committer names+emails via git's `.mailmap` before recording (off by default; warned when a `.mailmap` exists but the flag is omitted).
- `--ignore <glob>` — drops matching `commit_file` records entirely at extract time (counts in the `commit` record are recomputed so totals remain consistent).
- `--first-parent` — traverses only the first-parent chain, skipping merged branch history.

Full per-record schema (every field, types, enums): see [`docs/RUNBOOK.md`](docs/RUNBOOK.md#jsonl-format).

Output is a JSONL file with one record per line. Four record types:

```jsonl
{"type":"commit","sha":"abc...","tree":"def...","parents":["ghi..."],"author_name":"Alice","author_email":"alice@example.com","author_date":"2024-01-15T10:30:00Z","committer_name":"Alice","committer_email":"alice@example.com","committer_date":"2024-01-15T10:30:00Z","message":"","additions":42,"deletions":7,"files_changed":3}
{"type":"commit_parent","sha":"abc...","parent_sha":"ghi..."}
{"type":"commit_file","commit":"abc...","path_current":"src/main.go","path_previous":"src/main.go","status":"M","old_hash":"111...","new_hash":"222...","old_size":1024,"new_size":1087,"additions":10,"deletions":3}
{"type":"dev","dev_id":"sha256hash...","name":"Alice","email":"alice@example.com"}
```

### Resume

Extraction is resumable. State is saved to a file (default `git_state`) at every checkpoint:

```bash
# First run (interrupted or completed)
gitcortex extract --repo /path/to/repo --output data.jsonl

# Resume from where it left off
gitcortex extract --repo /path/to/repo --output data.jsonl
```

The checkpoint interval is controlled by `--batch-size` (default 1000 commits).

### Stats

```bash
# All stats at once (table format)
gitcortex stats --input data.jsonl

# Individual stat
gitcortex stats --input data.jsonl --stat contributors --top 20

# Multi-repo: aggregate stats across repositories
gitcortex stats --input svc-auth.jsonl --input svc-payments.jsonl --input svc-gateway.jsonl

# Export as CSV or JSON
gitcortex stats --input data.jsonl --stat hotspots --format csv > hotspots.csv
gitcortex stats --input data.jsonl --format json > report.json

# Full dataset (no truncation) — useful for scripting
gitcortex stats --input data.jsonl --stat churn-risk --top 0 --format csv

# Activity by week
gitcortex stats --input data.jsonl --stat activity --granularity week

# Filter to recent period
gitcortex stats --since 7d                    # last 7 days
gitcortex stats --since 3m --stat contributors # last 3 months
gitcortex report --since 30d --output monthly.html

# Closed window (arbitrary start/end — e.g. past quarter)
gitcortex stats  --from 2026-01-01 --to 2026-03-31 --stat contributors
gitcortex report --from 2026-01-01 --to 2026-03-31 --output q1.html
gitcortex report --from 2025-06-01 --output post-release.html  # open-ended forward
gitcortex report --to 2024-12-31 --output pre-2025.html        # open-ended backward
```

Available stats:

| Stat | Description |
|------|-------------|
| `summary` | Total commits, devs, files, additions/deletions, merge count, averages, date range |
| `contributors` | Ranked by commit count with additions/deletions per developer |
| `hotspots` | Most frequently changed files with churn and unique developer count |
| `activity` | Commits and line changes bucketed by day, week, month, or year |
| `busfactor` | Files with lowest bus factor (fewest developers owning 80%+ of changes) |
| `coupling` | Files that frequently change together, revealing hidden architectural dependencies |
| `churn-risk` | Files ranked by recent churn, classified into `cold` / `active` / `active-core` / `silo` / `fading-silo` |
| `working-patterns` | Commit heatmap by hour and day of week |
| `dev-network` | Developer collaboration graph based on shared file ownership |
| `profile` | Per-developer report: scope, specialization index, contribution type, pace, collaboration, top files |
| `top-commits` | Largest commits ranked by lines changed (includes message if extracted with `--include-commit-messages`) |
| `pareto` | Concentration (80% threshold) across files, devs (two lenses: commits and churn), and directories |
| `structure` | Repo layout as a `tree(1)`-style view, dirs sorted by aggregate churn, capped by `--tree-depth` (default 3) |
| `extensions` | File extensions ranked by recent churn, with file count, unique devs, and first/last-seen — the historical lens on language distribution |

Output formats: `table` (default, human-readable), `csv` (single clean table per `--stat`, header row on line 1), `json` (unified object with all sections).

`--top 0` disables the truncation and returns every row — useful for driving downstream scripts. Prefer `--format json` piped into `jq` for reliable filtering:

```bash
gitcortex stats --input data.jsonl --stat churn-risk --top 0 --format json \
  | jq '.churn_risk[] | select(.Label == "fading-silo")'
```

CSV output also carries a stable header on line 1, but paths containing commas (font filenames, generated assets) are standard-quoted — a naive `awk -F','` will mis-split on those rows. For CSV pipelines use a proper parser (`csvkit`, `mlr`) or stick with the JSON path above.

See [`docs/METRICS.md`](docs/METRICS.md) for how each metric is calculated, including timezone handling (UTC for aggregation buckets, author-local for working patterns) and rename tracking (history merged across git-detected renames).

### Developer profile

Manager-facing report per developer showing scope, specialization, contribution type, pace, collaboration, and top files.

```bash
# All developers, ranked by commits
gitcortex stats --input data.jsonl --stat profile

# Single developer
gitcortex stats --input data.jsonl --stat profile --email alice@company.com

# JSON export
gitcortex stats --input data.jsonl --stat profile --format json
```

Each profile includes:
- **Scope**: top directories where the dev works (by unique files, %)
- **Specialization**: Herfindahl concentration over the dev's full directory distribution; 1 = all files in one dir (narrow specialist), approaches 0 for broad generalists. Labelled `broad generalist` / `balanced` / `focused specialist` / `narrow specialist`. *Measures file distribution on disk, not domain expertise — a security engineer who refactored auth across four dirs looks like a generalist even though they are a domain specialist. See METRICS.md for the caveat in full.*
- **Contribution**: growth (add >> del), balanced, or refactor (del >> add)
- **Pace**: commits per active day
- **Collaboration**: top devs sharing the same files (ranked by `shared_lines` = Σ min(linesA, linesB))
- **Weekend %**: off-hours work ratio
- **Top files**: most impacted files by churn
- **Top commits**: the dev's largest individual commits by lines changed (additions + deletions); surfaces vendored drops and bulk rewrites that can skew the totals

### Coupling analysis

File coupling detects files that co-change in the same commits, revealing architectural coupling invisible in the code structure. Based on Adam Tornhill's ["Your Code as a Crime Scene"](https://pragprog.com/titles/atcrime/your-code-as-a-crime-scene/) methodology.

```bash
gitcortex stats --input data.jsonl --stat coupling --top 20
gitcortex stats --input data.jsonl --stat coupling --coupling-min-changes 10 --coupling-max-files 30
```

```
FILE A                              FILE B                              CO-CHANGES  COUPLING  CHANGES A  CHANGES B
ApplicationDbContext.cs              ApplicationDbContextModelSnapshot.cs 54          61%       100        89
GuardianPortalControllerTests.cs    GuardianPortalController.cs          40          91%       44         61
IWorkspaceRepository.cs             WorkspaceRepository.cs               19          100%      19         29
```

- **Coupling %**: co-changes / min(changes A, changes B) — how tightly linked the pair is
- **100% coupling**: every time the less-active file changes, the other changes too

### Churn risk

Ranks files by recency-weighted churn and classifies each into an actionable label, so you can tell a healthy core module apart from a legacy bottleneck without eyeballing five columns.

```bash
gitcortex stats --input data.jsonl --stat churn-risk --top 15
gitcortex stats --input data.jsonl --stat churn-risk --churn-half-life 60   # faster decay
```

Real output:

```
PATH                                   LABEL                                       RECENT CHURN  BF   AGE    TREND
automated install/basic-install.sh     active (age P90, trend P87)                 115.3         15   4121d  0.00
.github/workflows/codeql-analysis.yml  active-core (age P30, trend P95)            66.2          2    1640d  0.26
advanced/Scripts/utils.sh              active-core (age P27, trend P94)            53.3          2    1523d  0.10
```

| Label | Meaning |
|-------|---------|
| `cold` | Low recent churn — ignore. |
| `active` | Shared ownership (bus factor ≥ 3). Healthy. |
| `active-core` | New code (younger than most of the repo), single author. Usually fine. |
| `silo` | Old + concentrated + stable/growing. Knowledge bottleneck — plan transfer. |
| `fading-silo` | **Urgent.** Old + concentrated + declining. A silo whose owner is drifting away. |

Sort order is **label priority** (fading-silo → silo → active-core → active → cold), then `recent_churn` descending within the same label. The label answers "is this activity a problem?" and leads the table so the actionable classifications surface at the top — without this, a mature repo's `--top 20` would be dominated by unremarkable active files and the flagged risks would scroll off. The composite `risk_score` field (`recent_churn / bus_factor`) is still emitted for CI gate back-compat.

**The `(age PXX, trend PYY)` suffix** reports where the file sits in this repo's distribution: `age P90` = older than 90% of tracked files, `trend P08` = declining more sharply than 92%. Classification thresholds are not absolute — they adapt to each dataset (P75 age and P25 trend, with a fallback to fixed constants for repos under 8 files). A `fading-silo` with `(age P76, trend P24)` barely qualifies; one at `(age P98, trend P03)` is the real alarm. Distance from the boundary is now visible instead of hidden. See `docs/METRICS.md` for the adaptive-thresholds section.

`--churn-half-life` controls how fast old changes lose weight (default 90 days = changes lose half their weight every 90 days).

The HTML report precedes the Churn Risk table with a colored distribution strip — `48 fading-silo · 1 silo · 2,330 active-core · 1,404 active · 4,585 cold` — counted over the full classified set. The truncated table below shows only the top N by label priority, so a reader glancing at "all 20 rows are fading-silo" can still tell whether the repo has 20 legacy files or 20,000 before drawing a conclusion. To inspect the full list, use `--top 0 --format json` from the CLI and filter with `jq`.

### Working patterns

Commit distribution heatmap by hour and day of week. Reveals timezones, overwork patterns, and deploy habits.

```bash
gitcortex stats --input data.jsonl --stat working-patterns
gitcortex stats --input data.jsonl --stat working-patterns --format csv > patterns.csv
```

```
HOUR  Mon Tue Wed Thu Fri Sat Sun
09:00 1   1   3   .   .   .   .
10:00 7   4   2   2   1   6   1
11:00 10  13  3   1   2   14  7
...
19:00 35  15  7   10  12  16  13
22:00 26  9   .   1   13  9   8
```

### Developer network

Collaboration graph where edges connect developers who modify the same files. Weight reflects overlap percentage.

```bash
gitcortex stats --input data.jsonl --stat dev-network --top 20
gitcortex stats --input data.jsonl --stat dev-network --network-min-files 10
gitcortex stats --input data.jsonl --stat dev-network --format csv > network.csv
```

```
DEV A                          DEV B            SHARED FILES  WEIGHT
alice@company.com              bob@company.com  142           34.5%
carol@company.com              alice@company.com 87           21.2%
```

### Multi-repo

Aggregate stats across multiple repositories. File paths are automatically prefixed with the filename to avoid collisions.

```bash
# Extract each repo
gitcortex extract --repo ./svc-auth --output auth.jsonl
gitcortex extract --repo ./svc-payments --output payments.jsonl

# Aggregate stats
gitcortex stats --input auth.jsonl --input payments.jsonl
gitcortex stats --input auth.jsonl --input payments.jsonl --stat coupling --top 20
```

Paths appear as `auth:src/main.go` and `payments:src/main.go`. Contributors are deduped by email across repos — the same developer contributing to both repos is counted once.

For workspaces containing many repos (an engineer's `~/work`, a platform team's service folder), `gitcortex scan` discovers every `.git` under one or more roots and extracts them in parallel — see below.

### Scan: discover and aggregate every repo under a root

Walk one or more directories, find every git repository (working trees and bare clones both detected), extract them in parallel, and optionally render HTML. Two output modes:

- `--report-dir <dir>` — one standalone HTML per repo plus an `index.html` landing page linking them. Each per-repo report is equivalent to running `gitcortex report` against that repo alone; no metric mixing across unrelated codebases.
- `--report <file> --email <address>` — a **single** consolidated profile report for one developer across every scanned repo. The only cross-repo aggregation in the feature, because "where did this person spend their time?" is the only question that genuinely benefits from pooling signal across projects.

There is no third mode. Cross-repo consolidation at the team/codebase level inflates hotspots, bus factor, and coupling with noise from unrelated codebases; if that's what you want, inspect `manifest.json` or run `gitcortex report` per JSONL.

```bash
# Discover and extract every repo under ~/work (JSONLs + manifest, no HTML)
gitcortex scan --root ~/work --output ./scan-out

# Per-repo HTML reports + index landing page
gitcortex scan --root ~/work --output ./scan-out --report-dir ./reports
# opens ./reports/index.html → click through to each repo

# Personal cross-repo profile: only MY commits, consolidated into one HTML
gitcortex scan --root ~/work --output ./scan-out \
  --report ./me.html --email me@company.com --since 1y \
  --include-commit-messages

# Multiple roots, higher parallelism, pre-set ignore patterns
gitcortex scan --root ~/work --root ~/personal --root ~/oss \
  --parallel 8 --max-depth 4 \
  --output ./scan-out --report-dir ./reports
```

The scan output directory holds:

| file | purpose |
|---|---|
| `<slug>.jsonl` | per-repo JSONL, one per discovered repo |
| `<slug>.state` | resume checkpoint (safe to re-run scan to continue) |
| `manifest.json` | discovery results, per-repo status (ok/failed/pending), timing |

Each repo's slug is derived from its directory basename; colliding basenames get a short SHA-1 suffix (the suffix lengthens automatically on the rare truncation collision, so `<slug>.state` is stable across runs).

**Filtering discovery with `.gitcortex-ignore`.** Create a gitignore-style file at the scan root:

```
# skip heavy clones we don't want in the report
node_modules
chromium.git
linux.git

# skip vendored repos except the one we own
vendor/
!vendor/in-house-fork
```

Directory rules, globs, `**/foo`, and `!path` negations all work. Globbed negations like `!vendor*/keep` are honored — discovery descends into any dir where a negation rule could match a descendant. If `--ignore-file` is not set, scan looks for `.gitcortex-ignore` in the first `--root`.

**Consolidated profile report.** When `scan --email me@company.com --report path.html` runs against a multi-repo dataset, the profile report renders a *Per-Repository Breakdown* section: commits, churn, files, active days, and share-of-total — all filtered to that developer's contributions (files count reflects only files the dev touched). This is the one report that legitimately aggregates across repos; team-level views live in `--report-dir` (one HTML per repo, never mixed).

**Flags worth knowing:**

- `--parallel N` — repos extracted concurrently (default 4). Git is I/O-bound, so values past NumCPU give diminishing returns.
- `--max-depth N` — stop descending past N levels. Useful when a root contains a monorepo with deeply nested internal repos you don't want enumerated.
- `--extract-ignore <glob>` (repeatable) — forwarded to each per-repo `extract --ignore`, e.g. `--extract-ignore 'package-lock.json' --extract-ignore 'dist/*'`.
- `--from / --to / --since` — time window applied to the consolidated report (same semantics as `report`).
- `--churn-half-life`, `--coupling-max-files`, `--coupling-min-changes`, `--network-min-files` — pass tuning to the consolidated report identical to `gitcortex report`.

Partial failures are non-fatal: the manifest records which repos failed, and the report is built from whichever JSONLs completed. `Ctrl+C` aborts both the discovery walk and any in-flight extracts; re-running picks up from each repo's state file.

### Diff: compare time periods

Compare stats between two time periods, or filter to a single period.

```bash
# Compare Q1 vs Q2
gitcortex diff --input data.jsonl \
  --from 2024-01-01 --to 2024-03-31 \
  --vs-from 2024-04-01 --vs-to 2024-06-30

# Filter to a single month (runs all stats for that period)
gitcortex diff --input data.jsonl --from 2024-03-01 --to 2024-03-31

# JSON export
gitcortex diff --input data.jsonl \
  --from 2024-01-01 --to 2024-06-30 \
  --vs-from 2024-07-01 --vs-to 2024-12-31 \
  --format json > comparison.json
```

```
=== Summary: 2024-01-01 to 2024-03-31 vs 2024-04-01 to 2024-06-30 ===
Commits                        812  →       945  (+133)
Additions                   45420  →     62830  (+17410)
Deletions                   12300  →     18900  (+6600)
Files touched                  320  →       410  (+90)
Merge commits                   45  →        38  (-7)
```

### HTML report

Generate a self-contained HTML dashboard with all stats visualized. Pure HTML+CSS, zero external dependencies, opens in any browser.

```bash
gitcortex report --input data.jsonl --output report.html
gitcortex report --input data.jsonl --output report.html --top 30

# Per-developer profile report (shareable with managers)
gitcortex report --input data.jsonl --email alice@company.com --output alice.html
```

Includes: summary cards, activity heatmap (with table toggle), top contributors, file hotspots, churn risk (with full-dataset label distribution strip above the truncated table), bus factor, file coupling, working patterns heatmap, top commits, developer network, and developer profiles. A collapsible glossary at the top defines the terms (bus factor, churn, fading-silo, specialization, etc.) for readers who are not already familiar. Typical size: 50-500KB depending on number of contributors.

When the input is multi-repo (from `gitcortex scan` or multiple `--input` files) AND `--email` is set, the profile report renders a *Per-Repository Breakdown* with commit/churn/files/active-days per repo, filtered to that developer's contributions. The team-view report intentionally omits this section — per-repo aggregates on a consolidated dataset reduce to raw git-history distribution, which is more usefully inspected via `manifest.json` or `stats --input X.jsonl` per repo.

> The HTML activity heatmap is always monthly (year × 12 months grid). For day/week/year buckets, use `gitcortex stats --stat activity --granularity <unit>`.

### CI: quality gates for pipelines

Run automated checks and fail the build when thresholds are exceeded.

```bash
# Fail if any file has bus factor of 1
gitcortex ci --input data.jsonl --fail-on-busfactor 1

# Fail if any file has churn risk >= 500 (legacy composite: recent_churn / bus_factor)
gitcortex ci --input data.jsonl --fail-on-churn-risk 500

# Both rules, GitHub Actions format
gitcortex ci --input data.jsonl \
  --fail-on-busfactor 1 \
  --fail-on-churn-risk 500 \
  --format github-actions
```

Output formats: `text` (default), `github-actions` (annotations), `gitlab` (Code Quality JSON), `json`.

Exit code 1 when violations are found, 0 when clean.

> `--fail-on-churn-risk` evaluates the legacy `risk_score = recent_churn / bus_factor` field, not the new label classification surfaced by `stats --stat churn-risk`. The two can disagree — a file might have `risk_score` below the threshold yet still classify as `fading-silo`. Use the stat command for triage; use the CI gate as a coarse threshold alarm.

## Architecture

```
cmd/gitcortex/main.go          CLI entry point (cobra)
internal/
  model/model.go               JSONL output types
  git/
    stream.go                  Single git log streaming parser
    catfile.go                 Long-running cat-file blob size resolver
    commands.go                Utility functions (branch detection, SHA validation)
    parse.go                   Shared types (RawEntry, NumstatEntry)
    discard.go                 Malformed entry tracking
  extract/extract.go           Extraction orchestration, state, JSONL writing
  scan/
    scan.go                    Multi-repo orchestration (worker pool over extract)
    discovery.go               Directory walk, bare-repo detection, slug uniqueness
    ignore.go                  Gitignore-style matcher with negation support
  stats/
    reader.go                  Streaming JSONL aggregator (single-pass, multi-JSONL)
    stats.go                   Stat computations (9 stats)
    repo_breakdown.go          Per-repository aggregate (scan consolidated report)
    format.go                  Table/CSV/JSON output formatting
```

### Extraction pipeline

Two long-running git processes for the entire extraction, regardless of repository size:

```
git log --raw --numstat -M --- single stream ---- parse ---- emit JSONL
                                                    |
git cat-file --batch-check -- long-running ---- resolve blob sizes
```

### Stats pipeline

Single-pass streaming aggregation. The JSONL file is read once, line by line, aggregating into compact maps. Raw records are never stored — only pre-computed aggregation state is kept in memory.

```
JSONL file ---- line by line ----> aggregate ----> lean Dataset ----> stat functions
                (no raw storage)    commits: SHA → {email, date, add, del}
                                    files:   path → {commits, devs, churn}
                                    coupling: computed on-the-fly
```
