# gitcortex Runbook

Operational guide for extracting git metrics and generating stats at any scale.

## Quick start

```bash
# Build
make build

# Extract + stats in one go
./gitcortex extract --repo /path/to/repo
./gitcortex stats

# Run tests
make check
```

## Extract

### Basic extraction

```bash
./gitcortex extract --repo /path/to/repo
```

Produces `git_data.jsonl` and `git_state` in the current directory.

### Branch selection

The default branch is auto-detected (`origin/HEAD` > `main` > `master` > `HEAD`). Override with:

```bash
./gitcortex extract --repo /path/to/repo --branch develop
```

### Normalize identities with .mailmap

Unify developer identities using the repo's `.mailmap` file:

```bash
./gitcortex extract --repo /path/to/repo --mailmap
```

Without `--mailmap`, the same person with different emails appears as separate contributors, splitting their stats. With it, git normalizes names and emails before extraction.

Requires a `.mailmap` file in the repo root or `~/.mailmap`. See `man gitmailmap` for format details.

### Include commit messages

Messages are excluded by default to save space. Enable with:

```bash
./gitcortex extract --repo /path/to/repo --include-commit-messages
```

### Custom output paths

```bash
./gitcortex extract --repo /path/to/repo --output /data/project.jsonl --state-file /data/project.state
```

### Large repositories

For repos with 100K+ commits, increase the checkpoint interval to reduce state file writes:

```bash
./gitcortex extract --repo /path/to/linux --batch-size 50000
```

Expected performance:

| Repo size | Time | Memory |
|-----------|------|--------|
| 1K commits | <1s | ~15 MB |
| 10K commits | ~5s | ~20 MB |
| 100K commits | ~1 min | ~20 MB |
| 1M+ commits | ~12 min | ~20 MB |

### Resume after interruption

If extraction is interrupted (Ctrl+C, disk full, crash), just re-run the same command. It resumes from the last checkpoint:

```bash
# Interrupted at 500K commits
./gitcortex extract --repo /path/to/repo --output data.jsonl

# Resume — appends to data.jsonl from where it stopped
./gitcortex extract --repo /path/to/repo --output data.jsonl
```

The state file tracks progress. To start fresh, delete both files:

```bash
rm git_data.jsonl git_state
```

### Manual resume from a specific point

```bash
# Resume after a known commit SHA
./gitcortex extract --repo /path/to/repo --start-sha abc123...

# Resume from a numeric offset (legacy)
./gitcortex extract --repo /path/to/repo --start-offset 50000
```

### Bare repositories

Works with bare repos (no working tree):

```bash
git clone --bare https://github.com/org/repo.git /tmp/repo.git
./gitcortex extract --repo /tmp/repo.git --branch master
```

## Stats

### All stats at once

```bash
./gitcortex stats --input data.jsonl
```

Section headers go to stderr, data to stdout. To capture only data:

```bash
./gitcortex stats --input data.jsonl 2>/dev/null
```

### Individual stats

```bash
./gitcortex stats --input data.jsonl --stat summary
./gitcortex stats --input data.jsonl --stat contributors --top 20
./gitcortex stats --input data.jsonl --stat hotspots --top 30
./gitcortex stats --input data.jsonl --stat activity --granularity week
./gitcortex stats --input data.jsonl --stat busfactor --top 20
./gitcortex stats --input data.jsonl --stat coupling --top 20
./gitcortex stats --input data.jsonl --stat churn-risk --top 20
./gitcortex stats --input data.jsonl --stat working-patterns
./gitcortex stats --input data.jsonl --stat dev-network --top 20
./gitcortex stats --input data.jsonl --stat profile
./gitcortex stats --input data.jsonl --stat profile --email alice@company.com
```

### Output formats

```bash
# Human-readable table (default)
./gitcortex stats --input data.jsonl --format table

# CSV — pipe to file or other tools
./gitcortex stats --input data.jsonl --stat contributors --format csv > contributors.csv

# JSON — single unified object
./gitcortex stats --input data.jsonl --format json > report.json
```

### Multi-repo

Aggregate stats across repositories:

```bash
./gitcortex stats --input svc-a.jsonl --input svc-b.jsonl --input svc-c.jsonl
```

File paths are prefixed automatically (`svc-a:src/main.go`). Contributors are deduped by email across repos.

### Tuning stats parameters

**Coupling analysis:**

```bash
# Skip commits with >30 files (stricter noise filter)
./gitcortex stats --stat coupling --coupling-max-files 30

# Require at least 10 co-changes (fewer, higher-confidence results)
./gitcortex stats --stat coupling --coupling-min-changes 10
```

**Churn risk:**

```bash
# Faster decay — 30-day half-life (focus on very recent changes)
./gitcortex stats --stat churn-risk --churn-half-life 30

# Slower decay — 180-day half-life (6-month window)
./gitcortex stats --stat churn-risk --churn-half-life 180
```

**Developer network:**

```bash
# Only show strong connections (10+ shared files)
./gitcortex stats --stat dev-network --network-min-files 10
```

## Diff

### Compare two time periods

```bash
./gitcortex diff --input data.jsonl \
  --from 2024-01-01 --to 2024-06-30 \
  --vs-from 2024-07-01 --vs-to 2024-12-31
```

Shows side-by-side delta for summary, contributors, and hotspots.

### Filter to a single period

```bash
# All stats for March 2024 only
./gitcortex diff --input data.jsonl --from 2024-03-01 --to 2024-03-31
```

### Export comparison as JSON

```bash
./gitcortex diff --input data.jsonl \
  --from 2024-01-01 --to 2024-03-31 \
  --vs-from 2024-04-01 --vs-to 2024-06-30 \
  --format json > q1_vs_q2.json
```

## CI

### GitHub Actions

```yaml
- name: Extract git metrics
  run: gitcortex extract --repo . --output data.jsonl

- name: Check quality gates
  run: gitcortex ci --input data.jsonl --fail-on-busfactor 1 --fail-on-churn-risk 500 --format github-actions
```

### GitLab CI

```yaml
code_quality:
  script:
    - gitcortex extract --repo . --output data.jsonl
    - gitcortex ci --input data.jsonl --fail-on-busfactor 1 --format gitlab > gl-code-quality-report.json
  artifacts:
    reports:
      codequality: gl-code-quality-report.json
```

### Custom thresholds

```bash
# Only flag bus factor (ignore churn)
./gitcortex ci --input data.jsonl --fail-on-busfactor 1

# Only flag churn risk
./gitcortex ci --input data.jsonl --fail-on-churn-risk 500

# Both rules, stricter churn decay
./gitcortex ci --input data.jsonl --fail-on-busfactor 1 --fail-on-churn-risk 300 --churn-half-life 60
```

## Recipes

### Weekly team report

```bash
#!/bin/bash
REPO=/path/to/repo
DATA=/data/team.jsonl

./gitcortex extract --repo "$REPO" --output "$DATA" --state-file /data/team.state
./gitcortex stats --input "$DATA" --stat summary
./gitcortex stats --input "$DATA" --stat contributors --top 10
./gitcortex stats --input "$DATA" --stat hotspots --top 10
```

### Find architectural coupling

```bash
./gitcortex stats --input data.jsonl --stat coupling --top 30 --coupling-min-changes 5
```

Look for:
- **Controller + Test at 90%+**: healthy, tests co-evolve with code
- **Interface + Implementation at 100%**: expected for clean architecture
- **Unrelated modules at 50%+**: hidden dependency, refactoring opportunity
- **Config + Multiple modules**: shared config file is a bottleneck

### Identify bus factor risk

```bash
./gitcortex stats --input data.jsonl --stat busfactor --top 20
```

Files with bus factor 1 and high churn are the biggest risk. Cross-reference with:

```bash
./gitcortex stats --input data.jsonl --stat churn-risk --top 20
```

Files appearing in both lists need knowledge transfer.

### Quarterly review

```bash
./gitcortex diff --input data.jsonl \
  --from 2024-01-01 --to 2024-03-31 \
  --vs-from 2024-04-01 --vs-to 2024-06-30 \
  --top 15
```

Check:
- Did commit velocity change?
- Did new hotspots emerge?
- Did contributor distribution shift?

### Multi-repo analysis

Extract each repo separately, then analyze:

```bash
for repo in /repos/*/; do
  name=$(basename "$repo")
  ./gitcortex extract --repo "$repo" --output "/data/${name}.jsonl" --state-file "/data/${name}.state"
  echo "=== $name ===" >> /data/summary.txt
  ./gitcortex stats --input "/data/${name}.jsonl" --stat summary >> /data/summary.txt 2>/dev/null
done
```

### Export to spreadsheet

```bash
./gitcortex stats --input data.jsonl --stat contributors --format csv > contributors.csv
./gitcortex stats --input data.jsonl --stat hotspots --format csv > hotspots.csv
./gitcortex stats --input data.jsonl --stat coupling --format csv > coupling.csv
```

## Troubleshooting

### "rev-list --count: exit status 128"

The branch doesn't exist. Check available branches:

```bash
git -C /path/to/repo branch -a
```

Override with `--branch`:

```bash
./gitcortex extract --repo /path/to/repo --branch master
```

### Extraction hangs

If extraction appears stuck, check if `git log` is processing a very large commit (e.g., a vendor directory import). The `--debug` flag shows per-commit progress:

```bash
./gitcortex extract --repo /path/to/repo --debug
```

### Disk full during extraction

Resume after freeing space. The state file tracks progress, and the output file is appended to:

```bash
# After freeing space, just re-run
./gitcortex extract --repo /path/to/repo --output data.jsonl
```

### Stats show fewer files than expected for coupling

Coupling excludes:
- Single-file commits (no coupling information)
- Commits with >50 files (bulk operations, configurable via `--coupling-max-files`)
- Merge commits (no file records in extraction)

This is by design — these filters remove noise from the coupling analysis.

### State file is corrupted

Delete it and re-extract:

```bash
rm git_state
./gitcortex extract --repo /path/to/repo
```

## JSONL schema reference

Each line is a JSON object with a `type` field:

### commit

```json
{
  "type": "commit",
  "sha": "40-char hex",
  "tree": "40-char hex",
  "parents": ["sha1", "sha2"],
  "author_name": "string",
  "author_email": "string",
  "author_date": "RFC3339",
  "committer_name": "string",
  "committer_email": "string",
  "committer_date": "RFC3339",
  "message": "string (empty unless --include-commit-messages)",
  "additions": 0,
  "deletions": 0,
  "files_changed": 0
}
```

### commit_parent

```json
{
  "type": "commit_parent",
  "sha": "commit SHA",
  "parent_sha": "parent SHA"
}
```

### commit_file

```json
{
  "type": "commit_file",
  "commit": "commit SHA",
  "path_current": "current file path (empty for deletes)",
  "path_previous": "previous file path",
  "status": "M|A|D|R100|C075",
  "old_hash": "blob hash",
  "new_hash": "blob hash",
  "old_size": 0,
  "new_size": 0,
  "additions": 0,
  "deletions": 0
}
```

### dev

```json
{
  "type": "dev",
  "dev_id": "SHA256 of lowercase email",
  "name": "string",
  "email": "string"
}
```

## Requirements

- Git 2.31+
- Go 1.21+ (build only)
