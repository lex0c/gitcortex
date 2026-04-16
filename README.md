# gitcortex

A fast CLI for extracting git repository metrics and generating statistics. Single binary, zero dependencies beyond git.

Extracts commit metadata, file changes, blob sizes, and developer info into JSONL. Generates stats like top contributors, file hotspots, bus factor, coupling analysis, churn risk, working patterns, and developer collaboration networks.

## Performance

Tested on real repositories:

| Repository | Commits | Extract time | Throughput |
|------------|---------|-------------|------------|
| Small project (829 commits) | 829 | 0.3s | ~2,700/s |
| **Linux kernel** | **1,438,443** | **11m 55s** | **~2,000/s** |

The extraction uses only **2 git processes** regardless of repository size (one `git log` stream, one `cat-file` for blob sizes), keeping memory usage at ~21MB for the application itself.

## Install

```bash
go install gitcortex/cmd/gitcortex@latest
```

Or build from source:

```bash
git clone https://github.com/your-org/gitcortex.git
cd gitcortex
go build -o gitcortex ./cmd/gitcortex/
```

Requires Git 2.31+ and Go 1.21+.

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
```

The default branch is auto-detected from `origin/HEAD`, falling back to `main`, `master`, or `HEAD`.

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

# Export as CSV or JSON
gitcortex stats --input data.jsonl --stat hotspots --format csv > hotspots.csv
gitcortex stats --input data.jsonl --format json > report.json

# Activity by week
gitcortex stats --input data.jsonl --stat activity --granularity week
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
| `churn-risk` | Files ranked by recency-weighted churn combined with bus factor |
| `working-patterns` | Commit heatmap by hour and day of week |
| `dev-network` | Developer collaboration graph based on shared file ownership |

Output formats: `table` (default, human-readable), `csv` (single clean table per `--stat`), `json` (unified object with all sections).

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

Ranks files by a risk score combining recency-weighted churn with bus factor. Recent changes weigh more (exponential decay), and files with fewer owners score higher.

```bash
gitcortex stats --input data.jsonl --stat churn-risk --top 15
gitcortex stats --input data.jsonl --stat churn-risk --churn-half-life 60   # faster decay
```

```
PATH                           RISK    RECENT CHURN  BUS FACTOR  TOTAL CHANGES  LAST CHANGE
src/Api/Controllers/Auth.cs    142.5   285.0         2           47             2024-03-28
src/Domain/Entities/User.cs    98.3    98.3          1           12             2024-03-25
```

`--churn-half-life` controls how fast old changes lose weight (default 90 days = changes lose half their weight every 90 days).

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

Memory usage for the Linux kernel (1.4M commits, 3M file records):

| | Raw storage | Streaming aggregation |
|--|-------------|----------------------|
| Commit data | 700 MB (1.4M structs) | 184 MB (lean index) |
| File data | 1,200 MB (3M structs) | 44 MB (172K aggregated) |
| **Total** | **~2.1 GB** | **~360 MB** |

## License

MIT
