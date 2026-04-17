# Metrics Reference

How each metric is calculated, what it means, and how to act on it.

## Summary

Basic counts aggregated from the JSONL dataset.

| Metric | Calculation | Notes |
|--------|------------|-------|
| Total commits | Count of `commit` records | Excludes merge commit file diffs (merges counted but no file records) |
| Total devs | Count of unique author emails | Committer-only emails excluded (e.g. Co-Authored-By) |
| Total files | Count of unique `path_current` values | Files that were deleted still count if they appeared |
| Additions/Deletions | Sum from commit records | Recalculated when `--ignore` is used |
| Merge commits | Commits with >1 parent | Detected from `commit_parent` records |

## Contributors

Ranked by commit count, grouped by author email.

- Same person with different emails appears as separate entries unless `--mailmap` is used during extraction
- Additions/deletions are per-author totals across all their commits

## Hotspots

Files ranked by number of commits that touched them.

| Column | Meaning |
|--------|---------|
| Commits | Number of commits that modified this file |
| Churn | additions + deletions across all commits |
| Devs | Unique author emails that modified this file |

**How to interpret**: High churn + few devs = knowledge silo. High churn + many devs = shared bottleneck, possible design issue.

## Directories

Same as hotspots but aggregated by directory path. Files in the repository root are grouped under `.`.

| Column | Meaning |
|--------|---------|
| Commits | Sum of commits across all files in the directory |
| Churn | Sum of additions + deletions for all files |
| Files | Number of unique files in the directory |
| Devs | Unique authors who touched any file in the directory |
| Bus Factor | Minimum devs covering 80% of lines changed (see Bus Factor) |

**How to interpret**: Gives a module-level health view for leadership meetings.

## Activity

Commits and line changes bucketed by time period.

- Granularity: `day`, `week`, `month` (default), `year`
- Additions and deletions are summed per period from commit records
- Periods with zero activity are omitted

**How to interpret**: Spikes may indicate releases or sprints. Sustained drops may signal attrition or project wind-down.

## Bus Factor

For each file/directory: the minimum number of developers whose contributions cover 80% of all lines changed.

**Calculation**:
1. For each file, collect all authors and their total lines changed (additions + deletions)
2. Sort authors by lines changed, descending
3. Sum from the top until cumulative sum reaches 80% of total
4. Count of authors needed = bus factor

**Bus factor 1** = one person owns 80%+ of the changes. If they leave, the knowledge is lost.

**How to interpret**: Files with bus factor 1 and high recent churn are the highest risk. Cross-reference with churn-risk.

## Coupling

File pairs that frequently change in the same commit.

**Calculation**:
1. For each commit with 2-N files (skipping single-file commits and commits with >50 files by default), generate all file pairs
2. Count co-occurrences per pair
3. Coupling % = co-changes / min(changes_A, changes_B) × 100

| Column | Meaning |
|--------|---------|
| Co-changes | Number of commits where both files changed |
| Coupling % | How tightly linked — 100% means every time the less-active file changes, the other changes too |
| Changes A/B | Individual change counts for context |

**How to interpret**:
- Test + code at 90%+: healthy (tests co-evolve)
- Interface + implementation at 100%: expected
- Unrelated modules at 50%+: hidden dependency, refactoring opportunity

Based on Adam Tornhill's ["Your Code as a Crime Scene"](https://pragprog.com/titles/atcrime/your-code-as-a-crime-scene/) methodology.

## Churn Risk

Files ranked by a risk score combining recent churn with bus factor.

**Calculation**:
```
risk_score = recent_churn / bus_factor
```

Where `recent_churn` uses exponential decay:
```
weight = e^(-λ × days_since_change)
recent_churn = Σ (additions + deletions) × weight
λ = ln(2) / half_life_days
```

Default half-life: 90 days (changes lose half their weight every 90 days).

**How to interpret**: High risk = lots of recent changes owned by few people. These files need knowledge transfer or pair programming.

## Working Patterns

Commit distribution by day of week and hour.

- Uses author date (not committer date)
- Hours are in the author's local timezone (as recorded by git)
- Displayed as a 7×24 heatmap

**How to interpret**: Reveals team timezones, work-life patterns, and off-hours work. Consistent late-night commits may indicate overwork.

## Developer Network

Collaboration graph based on shared file ownership.

**Calculation**:
1. For each file, collect the set of authors
2. For each pair of authors who share files, count shared files
3. Weight = shared_files / max(files_A, files_B) × 100

**How to interpret**: Strong connections indicate collaboration. Isolated nodes with no connections may signal silos. High weight between two devs means they work on the same parts of the codebase.

## Profile

Per-developer report combining multiple metrics.

| Field | Calculation |
|-------|------------|
| Commits | Count of commits by this author |
| Lines changed | additions + deletions across all commits |
| Files touched | Unique files modified (from `contribFiles` accumulator) |
| Active days | Unique dates with at least one commit |
| Pace | commits / active_days |
| Weekend % | commits on Saturday+Sunday / total commits × 100 |
| Scope | Top 5 directories by unique file count, as % of total files touched |
| Contribution type | Based on del/add ratio: growth (<0.4), balanced (0.4-0.8), refactor (>0.8) |
| Collaborators | Top 5 devs sharing the most files with this dev |

## Top Commits

Commits ranked by total lines changed (additions + deletions).

- Message is included if extracted with `--include-commit-messages`, truncated to 80 characters
- Useful for identifying large imports, generated code, or risky big-bang changes

## Pareto (Concentration)

How asymmetric is the work distribution.

**Calculation**: For each dimension (files, devs, directories), sort by the metric descending and count how many items are needed to reach 80% of the total.

| Metric | Dimension sorted by |
|--------|-------------------|
| Files | Churn (additions + deletions) |
| Devs | Commit count |
| Directories | Churn (additions + deletions) |

**Judgment thresholds**:
- ≤10%: extremely concentrated (high risk)
- ≤25%: moderately concentrated
- \>25%: well distributed

**How to interpret**: "20 files concentrate 80% of all churn" means changes are localized — good for focus, risky if those files have low bus factor.

## Data Flow

```
git log --raw --numstat    ─── stream ──→ JSONL ──→ streaming load ──→ Dataset ──→ stats
git cat-file --batch-check ─── sizes  ─↗
```

- Extraction reads git metadata only (never source code)
- Commit messages excluded by default
- Stats computed from pre-aggregated maps (not raw records)
- All processing is 100% local
