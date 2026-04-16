# gitcortex

A fast CLI for extracting git repository metrics and generating statistics. Single binary, zero dependencies beyond git.

Extracts commit metadata, file changes, blob sizes, and developer info into JSONL. Generates stats like top contributors, file hotspots, bus factor, and activity over time.

## Performance

Tested on real repositories:

| Repository | Commits | Extract time | Throughput |
|------------|---------|-------------|------------|
| Small project (829 commits) | 829 | 0.5s | ~1,600/s |
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
# Summary of all stats (table format)
gitcortex stats --input data.jsonl

# Top 20 contributors
gitcortex stats --input data.jsonl --stat contributors --top 20

# File hotspots as CSV
gitcortex stats --input data.jsonl --stat hotspots --format csv > hotspots.csv

# Full report as JSON
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

Output formats: `table` (default, human-readable), `csv` (single clean table per `--stat`), `json` (unified object with all sections).

### Coupling analysis

File coupling detects files that co-change in the same commits, revealing architectural coupling invisible in the code structure. Based on Adam Tornhill's ["Your Code as a Crime Scene"](https://pragprog.com/titles/atcrime/your-code-as-a-crime-scene/) methodology.

```bash
# Top 20 coupled file pairs
gitcortex stats --input data.jsonl --stat coupling --top 20

# Stricter: at least 10 co-changes, skip commits with 30+ files
gitcortex stats --input data.jsonl --stat coupling --coupling-min-changes 10 --coupling-max-files 30

# Export for visualization
gitcortex stats --input data.jsonl --stat coupling --format csv > coupling.csv
```

Example output:

```
FILE A                                           FILE B                                           CO-CHANGES  COUPLING  CHANGES A  CHANGES B
ApplicationDbContext.cs                           ApplicationDbContextModelSnapshot.cs              54          61%       100        89
GuardianPortalControllerTests.cs                 GuardianPortalController.cs                       40          91%       44         61
IWorkspaceRepository.cs                          WorkspaceRepository.cs                            19          100%      19         29
```

What the columns mean:
- **Co-changes**: number of commits where both files changed together
- **Coupling %**: co-changes / min(changes A, changes B) — how tightly linked the pair is
- **100% coupling**: every time the less-active file changes, the other changes too

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
    reader.go                  JSONL dataset loader
    stats.go                   Stat computations
    format.go                  Table/CSV/JSON output formatting
```

The extraction pipeline:

```
git log --raw --numstat -M ─── single stream ──── parse ──── emit JSONL
                                                    │
git cat-file --batch-check ── long-running ──── resolve blob sizes
```

Two git processes for the entire extraction, regardless of repository size.

## License

MIT
