# gitcortex

**Repository behavior analyzer.** Reads git history — commits, authors, dates, file paths, line counts — and surfaces signals about how people and processes interact with a codebase: hotspots, bus factor, coupling, churn risk, working patterns, collaboration networks. It analyzes the behavior recorded in git, not the source code itself — every metric is derived from who touched what, when, and with whom.

That distinction matters in practice: a beautifully written library touched by one maintainer can classify as `silo`; a pile of spaghetti that the whole team works on daily will read as `active`. The tool complements code review and static analysis, it doesn't replace them — it surfaces the human and process layer over the repo.

Under the hood it extracts commit metadata, file changes, blob sizes, and developer info into JSONL, then derives the stats above from that stream.

## Performance

Benchmarked on open-source repositories. `extract` reads bare clones; `stats` and `report` read the resulting JSONL. Measurements taken with a pre-built binary on a single machine (not a controlled lab benchmark; directional, not absolute).

| Repository | Commits | Devs | Extract | Stats (JSON) | Report (HTML) | JSONL size |
|------------|---------|------|---------|-------------|--------------|------------|
| [Pi-hole](https://github.com/pi-hole/pi-hole) | 7,077 | 281 | 1.5s | 0.18s | 0.24s | 23K lines / 6.5 MB |
| [Praat](https://github.com/praat/praat) | 10,221 | 19 | 25s | 0.96s | 0.95s | 95K lines / 30 MB |
| [WordPress](https://github.com/WordPress/WordPress) | 52,466 | 131 | 47s | 2.9s | 2.8s | 298K lines / 96 MB |
| [Kubernetes](https://github.com/kubernetes/kubernetes) | 137,016 | 5,295 | 2m 4s | 11.7s | 14s | 943K lines / 314 MB |
| [Linux kernel](https://github.com/torvalds/linux) | 1,438,634 | 38,832 | 12m 57s | 1m 15s | 1m 53s | 6M lines / 1.9 GB |

`extract`, `stats`, and `report` scale roughly linearly with dataset size. The per-dev collaborator map in `report` is pre-computed in a single pass over files (O(F × D_per_file²)); on the kubernetes snapshot that adds ~2 seconds over `stats`, on linux ~40 seconds. A previous implementation computed this nested inside the per-dev loop (O(D × F × D_per_file)) and was 6× slower on kubernetes and 11× slower on linux. If you only need the aggregate data, `stats --format json` is always the fastest path; reach for `report` when you actually want the HTML dashboard.

See [`docs/PERF.md`](docs/PERF.md) for extended benchmarks, including gitcortex extracting itself (189 commits, 45 ms) and Chromium at scale (1.7M commits, ~2 hours) — plus an analysis of what drives extract throughput on large monorepos.

## Vendor and generated code

**This is the biggest practical distortion in every stat.** Line-count metrics treat a 50k-line `generated.pb.go` the same as a 50k-line hand-written module. Lock files like `package-lock.json` regenerate with every dependency bump. Vendored dependencies inflate churn whenever they're updated. OpenAPI specs, minified JS, `bindata.go`-style embeds — all common, all inflate churn and bus factor without reflecting real human contribution.

Run gitcortex on kubernetes without filtering and the top legacy-hotspots are `vendor/golang.org/x/tools/…/manifest.go`, `api/openapi-spec/v3/…v1alpha3_openapi.json`, and `staging/…/generated.pb.go` — technically correct per the data, practically useless for decision-making.

Mitigate with `--ignore` glob patterns at extract time. Files matched are dropped from the JSONL entirely, so **every downstream stat** (hotspots, churn-risk, bus factor, coupling, dev-network, profiles) reflects only hand-authored code:

```bash
# Typical starter set
gitcortex extract --repo . \
  --ignore "vendor/*" \
  --ignore "node_modules/*" \
  --ignore "dist/*" \
  --ignore "build/*" \
  --ignore "*.min.js" \
  --ignore "*.min.css" \
  --ignore "package-lock.json" \
  --ignore "yarn.lock" \
  --ignore "Cargo.lock" \
  --ignore "go.sum" \
  --ignore "poetry.lock" \
  --ignore "*.pb.go" \
  --ignore "*_generated.go"
```

Patterns match against the file path as emitted by `git log --raw` (forward-slash, repo-relative). Directory patterns like `vendor/*` are **repo-root prefixes** — they exclude everything under `vendor/` at the top of the tree, but **not** nested occurrences like `pkg/vendor/foo.go` or `services/auth/vendor/bar.go`. For those you need explicit entries such as `--ignore "pkg/vendor/*"`. File-name patterns like `*.pb.go` and `package-lock.json` match at any depth via extract's basename match, so one entry covers every occurrence.

Start permissive, run `gitcortex stats --stat hotspots --top 20` and `--stat churn-risk --top 20`, and add `--ignore` entries for whatever generated file type dominates the output. Re-extract until the top list represents real changes worth understanding.

**You don't need to get this right on the first try.** When `stats` runs on an un-filtered dataset and likely vendor/generated paths account for ≥10% of repo churn, it prints a warning to stderr with the matched buckets and a copy-pasteable `--ignore` invocation. The warning enumerates the exact nested prefixes it found (e.g. `wp-includes/js/dist/*`, `services/auth/vendor/*`), so monorepos and subproject-heavy layouts get the specific entries they need without guessing. Running the suggestion and re-extracting is the fastest path from raw repo to usable stats.

> Both commit-level (`Summary.TotalAdditions/Deletions`) and file-level aggregations recompute from the filtered set, so all totals stay consistent after `--ignore` — the extract step recalculates commit additions/deletions as the sum of non-ignored file records before writing them to JSONL.

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
| `churn-risk` | Files ranked by recent churn, classified into `cold` / `active` / `active-core` / `silo` / `legacy-hotspot` |
| `working-patterns` | Commit heatmap by hour and day of week |
| `dev-network` | Developer collaboration graph based on shared file ownership |
| `profile` | Per-developer report: scope, specialization index, contribution type, pace, collaboration, top files |
| `top-commits` | Largest commits ranked by lines changed (includes message if extracted with `--include-commit-messages`) |
| `pareto` | Concentration (80% threshold) across files, devs (two lenses: commits and churn), and directories |

Output formats: `table` (default, human-readable), `csv` (single clean table per `--stat`, header row on line 1), `json` (unified object with all sections).

`--top 0` disables the truncation and returns every row — useful for driving downstream scripts. Prefer `--format json` piped into `jq` for reliable filtering:

```bash
gitcortex stats --input data.jsonl --stat churn-risk --top 0 --format json \
  | jq '.churn_risk[] | select(.Label == "legacy-hotspot")'
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
| `legacy-hotspot` | **Urgent.** Old + concentrated + declining. Deprecated paths still being touched. |

Sort order is **label priority** (legacy-hotspot → silo → active-core → active → cold), then `recent_churn` descending within the same label. The label answers "is this activity a problem?" and leads the table so the actionable classifications surface at the top — without this, a mature repo's `--top 20` would be dominated by unremarkable active files and the flagged risks would scroll off. The composite `risk_score` field (`recent_churn / bus_factor`) is still emitted for CI gate back-compat.

**The `(age PXX, trend PYY)` suffix** reports where the file sits in this repo's distribution: `age P90` = older than 90% of tracked files, `trend P08` = declining more sharply than 92%. Classification thresholds are not absolute — they adapt to each dataset (P75 age and P25 trend, with a fallback to fixed constants for repos under 8 files). A `legacy-hotspot` with `(age P76, trend P24)` barely qualifies; one at `(age P98, trend P03)` is the real alarm. Distance from the boundary is now visible instead of hidden. See `docs/METRICS.md` for the adaptive-thresholds section.

`--churn-half-life` controls how fast old changes lose weight (default 90 days = changes lose half their weight every 90 days).

The HTML report precedes the Churn Risk table with a colored distribution strip — `48 legacy-hotspot · 1 silo · 2,330 active-core · 1,404 active · 4,585 cold` — counted over the full classified set. The truncated table below shows only the top N by label priority, so a reader glancing at "all 20 rows are legacy-hotspot" can still tell whether the repo has 20 legacy files or 20,000 before drawing a conclusion. To inspect the full list, use `--top 0 --format json` from the CLI and filter with `jq`.

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

Includes: summary cards, activity heatmap (with table toggle), top contributors, file hotspots, churn risk (with full-dataset label distribution strip above the truncated table), bus factor, file coupling, working patterns heatmap, top commits, developer network, and developer profiles. A collapsible glossary at the top defines the terms (bus factor, churn, legacy-hotspot, specialization, etc.) for readers who are not already familiar. Typical size: 50-500KB depending on number of contributors.

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

> `--fail-on-churn-risk` evaluates the legacy `risk_score = recent_churn / bus_factor` field, not the new label classification surfaced by `stats --stat churn-risk`. The two can disagree — a file might have `risk_score` below the threshold yet still classify as `legacy-hotspot`. Use the stat command for triage; use the CI gate as a coarse threshold alarm.

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
  stats/
    reader.go                  Streaming JSONL aggregator (single-pass)
    stats.go                   Stat computations (9 stats)
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
