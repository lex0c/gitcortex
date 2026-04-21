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
| File Touches | Sum of per-file commit counts. **A single commit touching N files in this directory contributes N to this number — this is NOT distinct commits.** |
| Churn | Sum of additions + deletions for all files |
| Files | Number of unique files in the directory |
| Devs | Unique authors who touched any file in the directory |
| Bus Factor | Minimum devs covering 80% of lines changed (see Bus Factor) |

**How to interpret**: Gives a module-level health view. Watch the `File Touches` name — it inflates relative to what people intuitively call "commits" when commits are large.

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

> **Caveat — weighted by lines, not commits.** A dev with one big 10k-line commit outweighs 100 small commits. If familiarity matters more than volume to you, treat bus factor as an upper bound on knowledge loss risk, not a direct measurement.

## Coupling

File pairs that frequently change in the same commit.

**Calculation**:
1. For each commit with 2-N files (skipping single-file commits and commits with >50 files by default), generate all file pairs
2. Exclude pairs from **mechanical-refactor commits**: a commit touching ≥ `refactorMinFiles` (10) with mean per-file churn < `refactorMaxChurnPerFile` (5 lines) is treated as a global rename / format / lint fix, and its pairs are skipped. The files' individual change counts (`changes_A`, `changes_B`) still include these commits — only the pair accumulation is suppressed.
3. Count co-occurrences per pair
4. Coupling % = co-changes / min(changes_A, changes_B) × 100

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

> **Caveat — co-change is not causation.** Two files changing in the same commit proves they were touched by the same unit of work, not that one depends on the other. The refactor filter catches the most blatant false positives (global renames, format passes) but not all — a genuinely large feature touching many related files can still leak pair counts. Treat high coupling % as a hypothesis worth investigating, not a proof of architectural dependency.
>
> **Caveat — the refactor filter uses mean churn.** A commit mixing many mechanical renames (low churn each) with a few substantive edits (high churn each) can have mean > 5 and escape the filter. Example: 12 renames of 1 line + 3 real edits of 100 lines → mean ≈ 21, filter does not fire, and the 12 rename-participating files generate ~66 spurious pairs. Per-file weighting would fix this but requires restructuring pair generation; it is an acknowledged limitation rather than a planned change.
>
> **Caveat — rename commits below `refactorMinFiles` leak.** A commit renaming 8 files (say, a small module rename) has zero churn per file but only 8 files, so the filter does not fire. Pairs are generated between old paths and then re-keyed onto the canonical (new) paths by the rename tracker. Each such pair has `CoChanges = 1`, so any sensible `--coupling-min-changes` threshold (≥ 2) filters them out of display. Readers inspecting raw data may still see the residual pairs.
>
> **Caveat — `--coupling-max-files` below the refactor threshold disables the filter.** The refactor filter only runs for commits with `≥ refactorMinFiles` (10) files, but the existing `--coupling-max-files` gate runs first. Setting `--coupling-max-files` below 10 discards those commits outright (no pairs at all), so the refactor filter becomes dead code. This is correct layering but worth knowing if you tune the flag aggressively.

## Churn Risk

Files ranked by recency-weighted churn, **classified into actionable labels** so the reader can judge whether the activity is a problem or just ongoing work.

### Ranking

Sort order: **label priority** first, then `recent_churn` descending within the same label, then lower `bus_factor` first, then path ascending. Label priority runs `legacy-hotspot` → `silo` → `active-core` → `active` → `cold`, so the named actionable classifications always lead the table. Sorting by `recent_churn` alone used to bury `legacy-hotspot` files behind very active code (declining trend is part of the classification, so recent churn is low by definition) — a user running `--top 20` on a mature repo would see unremarkable active files and zero flagged risks.

`recent_churn` uses exponential decay:
```
weight = e^(-λ × days_since_change)
recent_churn = Σ (additions + deletions) × weight
λ = ln(2) / half_life_days
```

Default half-life: 90 days (changes lose half their weight every 90 days).

### Classification labels

Ranking alone conflates "core being actively developed by one author" (expected)
with "legacy module everyone forgot" (urgent). The label separates them by
adding age and trend dimensions.

**Rules are evaluated in order; the first match wins.** Conditions on later
rows implicitly assume the earlier rows didn't match.

| # | Label | Rule | Action |
|---|-------|------|--------|
| 1 | **cold** | `recent_churn ≤ 0.5 × median(recent_churn)` | Ignore. |
| 2 | **active** | `bus_factor ≥ 3` | Healthy, shared. |
| 3 | **active-core** | `bus_factor ≤ 2` and `age < oldAgeThreshold` | New code, single author is expected. |
| 4 | **legacy-hotspot** | `bus_factor ≤ 2`, `age ≥ oldAgeThreshold`, and `trend < decliningTrendThreshold` | **Urgent.** Old + concentrated + declining. |
| 5 | **silo** | default (everything the rules above didn't catch) | Knowledge bottleneck — plan transfer. |

Where:
- `age = days between firstChange and latest commit in dataset`
- `trend = churn_last_3_months / churn_earlier`. Edge cases: empty history returns 1 (no signal); recent-only history returns 2 (grew from nothing); earlier-only history returns 0 (declined to nothing — the strongest `legacy-hotspot` signal); short-span datasets whose entire window fits inside the trend window return 1 to avoid false "growing" reports

### Adaptive thresholds (per-dataset calibration)

`oldAgeThreshold` and `decliningTrendThreshold` are not fixed constants: they are derived from the dataset's own distribution each run. With at least `classifyMinSample` (8) files present:
- `oldAgeThreshold` = **P75** of file ages in this dataset
- `decliningTrendThreshold` = **P25** of file trends in this dataset, clamped to at least `adaptiveDecliningTrendFloor` (0.01). The floor matters on mature repos where ≥25% of files are dormant (trend=0 via the earlier-only path): P25 would otherwise collapse to 0 and the strict `trend < threshold` check would never fire, silently misclassifying every dormant concentrated file as `silo` instead of `legacy-hotspot`. The floor keeps the threshold strictly positive so the trend=0 signal — the strongest legacy-hotspot alarm — still reaches the rule.

This makes "old" mean "older than 75% of tracked files in this repo" instead of an absolute 180 days. A 4-year-old file in a 12-year-old codebase was previously tagged `legacy-hotspot` even though it was newer than most of the repo — now the same file lands in `active-core`. Below the sample threshold, the absolute fallbacks `classifyOldAgeDays` and `classifyDecliningTrend` apply so tiny repos still produce labels.

Each `ChurnRiskResult` also exposes `AgePercentile` and `TrendPercentile` (0-100) showing where the file sits in the distribution. The fields are nil (omitted from JSON, empty in CSV) when the fallback path ran. The CLI and HTML surface these alongside the label — `legacy-hotspot (age P92, trend P08)` tells you the file is both old and sharply declining relative to peers; `legacy-hotspot (age P76, trend P24)` barely qualifies. Distance from the classification boundary is now readable, not hidden.

> **Degenerate trend distribution.** When every file's entire history fits inside the trend window (e.g. a repo with <3 months of commits), `churnTrend` returns the flat-signal sentinel `1.0` for all of them. The adaptive P25 then lands on `1.0` too, and the `trend < P25` predicate matches nobody — no file reaches `legacy-hotspot` through the trend check. Old + concentrated files fall through to `silo` instead. This is mathematically correct (there's no variation to classify on) but can surprise readers of short-lived repos. Pinned by `TestChurnRiskAdaptiveDegenerateTrendDistribution` so future refactors don't silently flip it.

> **Sensitivity note.** Files touched a single time long ago and never again correctly route to `legacy-hotspot` via the earlier-only trend=0 path. On large mature repos this pattern is the common case, not the exception — e.g. validation on a kubernetes snapshot classified ~29k files this way. If the label distribution looks heavy on `legacy-hotspot` for a long-lived codebase, that is usually diagnosing real dormant code, not a bug.

### Additional columns

| Column | Meaning |
|--------|---------|
| `risk_score` | `recent_churn / bus_factor` — legacy composite. Still consumed by `gitcortex ci --fail-on-churn-risk N`. Not used for ranking. May diverge from the label (a file can have low `risk_score` but be classified `legacy-hotspot`, and vice versa). |
| `first_change`, `last_change` | Bounds of the file's activity in the dataset (UTC) |
| `age_days` | `latest - first_change` in days |
| `trend` | Ratio described above |

### How to interpret

- **legacy-hotspot** is the alarm — investigate first.
- **silo** suggests pairing / documentation work, not panic.
- **active-core** is usually fine, but watch for `bus_factor=1` + growing.
- **active** with growing trend may indicate a healthy shared module or a collision of too many cooks.

## Working Patterns

Commit distribution by day of week and hour.

- Uses author date (not committer date)
- Hours are in the author's **local** timezone as recorded by git — this metric describes human work rhythm, not UTC instants
- Displayed as a 7×24 heatmap

**How to interpret**: Reveals team timezones, work-life patterns, and off-hours work. Consistent late-night commits may indicate overwork.

> The per-developer working grid in `profile` uses the same local-timezone semantics.

## Developer Network

Collaboration graph based on shared file ownership.

**Calculation**:
1. For each file, collect each author's line contribution
2. For each pair of authors who share files:
   - `shared_files` = count of files both touched (any amount)
   - `shared_lines` = `Σ min(lines_A, lines_B)` across shared files
   - `weight` = `shared_files / max(files_A, files_B) × 100` (legacy)

Sort is by `shared_lines` descending (tiebreak by `shared_files`).

**How to interpret**:
- `shared_lines` is the honest signal. Alice editing 1 line and Bob editing 200 on the same file share only 1 line of real collaboration — `shared_lines` reflects this, `shared_files` doesn't.
- Strong `shared_lines` = deep co-ownership. Low `shared_lines` with high `shared_files` = trivial touches (one-line fixes, format commits).
- Isolated nodes may signal silos.

## Profile

Per-developer report combining multiple metrics.

| Field | Calculation |
|-------|------------|
| Commits | Count of commits by this author |
| Lines changed | additions + deletions across all commits |
| Files touched | Unique files modified (from `contribFiles` accumulator) |
| Active days | Unique dates with at least one commit |
| Pace | commits / active_days (smooths bursts — a dev with 100 commits on 2 days and silence for 28 shows pace=50, which reads as a steady rate but isn't) |
| Weekend % | commits on Saturday+Sunday / total commits × 100 |
| Scope | Top 5 directories by unique file count, as % of the dev's **authored** files — i.e. files where the dev added or removed at least one line. Pure renames (file appears in the dev's change set with zero line changes) are excluded from both numerator and denominator so the visible Pct values sum to 100% (modulo the top-5 truncation). Same denominator is used for Extensions and for the Herfindahl specialization index, keeping the three consistent. |
| Extensions | Top 5 file extensions the dev touched, sorted by **files desc** (tiebreak churn desc, then ext asc) so the displayed `Pct` is monotonic with the sort order and HTML bar widths read correctly. `Pct` is `Files / authored * 100` where `authored` is the count of files the dev added or removed at least one line on — same denominator as Scope, so Pcts sum to 100% modulo top-5 truncation. The raw dev-attributable `Churn` (sum of `devLines[email]` across bucket files) is kept on the struct for JSON consumers who want a churn-ranked view. Answers the "language/skill fingerprint" question (`.go` + `.yaml` → backend+infra; `.tsx` + `.ts` + `.css` → frontend). **Attribution caveat:** bucket is derived from the file's canonical (post-rename) path — a dev who worked on `foo.js` pre-migration still shows up under `.ts` if it was later renamed; per-era per-dev attribution would need `byExt` to carry a dev dimension, which isn't tracked. |
| Specialization | Herfindahl index over the **full** per-directory file-count distribution: Σ pᵢ² where pᵢ is the share of the dev's files in directory i. 1 = all files in one directory (narrow specialist); 1/N for a uniform spread across N directories; approaches 0 as the distribution widens. Computed before the top-5 Scope truncation so it reflects actual breadth. Labels (see `specBroadGeneralistMax`, `specBalancedMax`, `specFocusedMax` constants): `< 0.15` broad generalist, `< 0.35` balanced, `< 0.7` focused specialist, `≥ 0.7` narrow specialist. Herfindahl, not Gini, because Gini would collapse "1 file in 1 dir" and "1 file in each of 5 dirs" to the same value (both have zero inequality among buckets), which misses the specialization distinction. **Measures file distribution, not domain expertise** — see caveat below. **Display vs raw:** CLI and HTML show the value rounded to 3 decimals (`%.3f`) for readability; JSON output preserves the full float64. Band classification runs against the raw float, so a value like 0.149 lands in `broad generalist` even though %.2f would have rounded it to `0.15`. JSON consumers that reproduce the banding must use the raw value, not a rounded version. |
| Contribution type | Based on del/add ratio: growth (<0.4), balanced (0.4-0.8), refactor (>0.8) |
| Collaborators | Top 5 devs sharing code with this dev. Ranked by `shared_lines` (Σ min(linesA, linesB) across shared files), tiebreak `shared_files`, then email. Same `shared_lines` semantics as the Developer Network metric — discounts trivial one-line touches so "collaborator" reflects real overlap. |

## Top Commits

Commits ranked by total lines changed (additions + deletions).

- Message is included if extracted with `--include-commit-messages`, truncated to 80 characters
- Useful for identifying large imports, generated code, or risky big-bang changes

## Pareto (Concentration)

How asymmetric is the work distribution.

**Calculation**: For each dimension, sort by the metric descending and count how many items are needed to reach 80% of the total.

| Dimension | Sort key | Why |
|-----------|----------|-----|
| Files | Churn (additions + deletions) | — |
| Devs (commits) | Commit count | Rewards frequent committers. Inflated by bots and squash-off workflows. |
| Devs (churn) | additions + deletions | Rewards volume of lines written/removed. Inflated by generated-file authors and verbose coders. |
| Directories | Churn (additions + deletions) | — |

Two dev lenses are surfaced because commit count alone is a flawed proxy for contribution: a squash-merge counts as one commit while a rebase-and-merge counts as many; bots routinely dominate commit leaderboards despite writing little code. Rather than replace one bias with another, gitcortex shows both and lets the divergence be the signal.

**Reading the divergence**:
- Aligned counts (e.g. 17 ≈ 17) → consistent contributor base; both lenses agree.
- Commits ≫ churn (e.g. 267 vs 132 on kubernetes) → bots or squash workflows inflate commit counts. The smaller list is closer to "who actually wrote code".
- Churn ≫ commits → single heavy-feature authors who commit rarely but write volumes.

**Judgment thresholds**:
- ≤10%: extremely concentrated (plus "key-person dependence" on the Devs-by-commits card)
- ≤25%: moderately concentrated
- \>25%: well distributed
- total == 0 (no commits, or no churn for the churn lens): no data (neutral marker)

**How to interpret**: "20 files concentrate 80% of all churn" describes where change lands — it can indicate a healthy core module under active development, or a bottleneck if combined with low bus factor. Cross-reference with the Churn Risk section before drawing conclusions.

## Extensions

File extensions aggregated from `ds.files`, ranked by **recent churn** (decay-weighted — see "Recent churn" below). The historical lens is the point: `cloc`/`tokei` answer "what languages exist on disk"; this answers "which extensions is the team spending effort on right now".

**Extraction policy** (`extractExtension`):
- Last path segment (after the final `/`).
- Multi-dot names report the final segment: `foo.tar.gz` → `.gz`, `.eslintrc.json` → `.json`.
- Single-dot dotfiles keep their full name: `.gitignore` → `.gitignore`, `.env` → `.env`. Merging these into "(none)" would erase a meaningful group.
- No-dot names collapse into the `(none)` bucket: `Makefile`, `LICENSE`, `bin/run`.
- Extensions lowercased so `.PNG` and `.png` aggregate.

**Per-bucket fields**:
- `files` — distinct file lineages that ever held this extension. A file renamed across extensions (foo.js → foo.ts) counts once in each bucket; totals across buckets can therefore exceed the dataset's file count in migration-heavy repos.
- `churn` — lifetime additions + deletions attributed to this extension specifically. A foo.js → foo.ts migration with 1000 lines of pre-rename churn and 500 post-rename does **not** collapse all 1500 onto `.ts`; `.js` keeps its 1000 and `.ts` gets 500. The attribution comes from capturing the path's extension at each change before `applyRenames` merges the lineage.
- `recent_churn` — same per-era semantics, decay-weighted (same half-life as other stats, set at load time). Leads the sort so a dormant extension with high lifetime churn won't displace an active one.
- `unique_devs` — distinct emails that touched any file that ever held this extension. **Over-counts across migrations**: a dev who only worked on `foo.js` pre-migration still appears under `.ts` if that file was migrated. Splitting devs per era would need per-commit dev tracking that `fileEntry` does not retain. Read this as "people with context on files that at some point were this extension" rather than "active contributors in this extension".
- `first_seen` / `last_seen` — min/max within the bucket's era, UTC date. For the `.js` bucket in a TypeScript migration, `last_seen` is the migration cutoff, not today's date.

**Reading signals**:
- `.yaml` recent churn high + unique_devs low → config owned by one person; schedule handoff before they leave.
- `.md` recent churn high → docs-heavy phase (release prep?) or churn-heavy README thrash.
- Cross-read with Directories: `.yaml` concentrated in one dir is config-as-code; `.yaml` spread across many dirs is config sprawl.

**What it does not do**: no language-family grouping (`.js`+`.ts`+`.tsx` stay distinct). Aggregate downstream if you need "frontend vs backend"; the tool does not prescribe the taxonomy. Generated-file buckets (`.lock`, `.pb.go`, `.min.js`) will dominate unless filtered via `--ignore` at extract time — the suspect-paths warning flags these.

## Repo Structure

A `tree(1)`-style view of the repository's directory layout, built from paths seen in history (`FileHotspots`), not from the filesystem at HEAD. Deleted files are included — the view answers "what shaped the codebase", not "what is present today".

**Aggregation**:
- File nodes: `Commits` and `Churn` are the per-file values.
- Directory nodes: `Churn` and `Files` sum over all descendants; `Commits` is intentionally left at zero. Per-file commit counts do not sum to a distinct commit count — one commit that touches three files would add to three children. `Files` is the distinct descendant count.

**Ordering**: within each level, directories come first (architectural shape reads top-down), then files. Ties are broken by churn descending, then name ascending.

**Truncation**: the CLI caps depth at `--tree-depth` (default 3, 0 = unlimited). The HTML report additionally caps children at 50 per directory to keep the page under ~1MB on kernel-scale repos; the tail is collapsed into a `… N more hidden (ranked by churn)` counter.

**When to use**: before drilling into hotspots or churn-risk, skim the structure to locate the modules those files live in. The tree is navigational context; ranked tables are where judgment happens.

## Per-Repository Breakdown

Cross-repo aggregation scoped to a single developer. Renders in the HTML profile report (`report --email me@x.com` or `scan --email me@x.com --report …`) when the dataset was loaded from more than one JSONL. The metric is deliberately profile-only: on a team-view consolidated report, per-repo aggregates reduce to raw git-history distribution and are better inspected via `manifest.json` or by running `stats --input X.jsonl` per-repo. Filtered by a developer's email, the same numbers answer a genuinely different question — *"where did this person spend their time?"* — which is what the section surfaces.

One row per repo:

| column | meaning |
|---|---|
| `Commits` | author-dated commit count in this repo |
| `% Commits` | share of total commits in the dataset |
| `Churn` | additions + deletions attributed to this repo |
| `% Churn` | share of total churn |
| `Files` | distinct files the developer touched in this repo (always email-filtered, since the section only renders on profile reports) |
| `Active days` | distinct UTC author-dates |
| `Devs` | unique author emails in this repo |
| `First → Last` | earliest and latest author-date |

**How the repo label is derived**: `LoadMultiJSONL` prefixes every path in the dataset with `<filename-stem>:` (so `WordPress.git.jsonl` contributes paths like `WordPress.git:wp-includes/foo.php`). The breakdown groups by that prefix. If only a single JSONL is loaded, no prefix is emitted and the breakdown collapses to a single `(repo)` row the HTML report hides.

**Divergence between `% Commits` and `% Churn` is informative**: a repo dominating churn while holding modest commit share often signals large-content work (docs, data), while the reverse points to small-diff high-frequency repos (config, manifests).

**SHA collisions across repos** (forks, mirrors, cherry-picks between sibling projects) are preserved here — the breakdown tracks commits per repo via a dedicated slice populated at ingest, not the SHA-keyed commit map. Other file-level metrics (bus factor, coupling, dev network) still key by SHA and will collapse collided commits onto one record; if exact attribution matters for those, scan and aggregate the sibling repos separately.

## Data Flow

```
git log --raw --numstat    ─── stream ──→ JSONL ──→ streaming load ──→ Dataset ──→ stats
git cat-file --batch-check ─── sizes  ─↗
```

- Extraction reads git metadata only (never source code)
- Commit messages excluded by default
- Stats computed from pre-aggregated maps (not raw records)
- All processing is 100% local

## Behavior and caveats

### Thresholds

Every classification boundary is a named constant in `internal/stats/stats.go`. Values below are the defaults; there is no runtime flag to override them yet — changing a threshold requires editing the source and rebuilding.

| Constant | Default | Controls |
|----------|---------|----------|
| `classifyColdChurnRatio` | `0.5` | A file is `cold` when `recent_churn ≤ ratio × median(recent_churn)`. |
| `classifyActiveBusFactor` | `3` | A file is `active` (shared, healthy) when `bus_factor ≥ this`. |
| `classifyOldAgeDays` | `180` | **Fallback only** (dataset < `classifyMinSample` files). Adaptive path uses P75 of the dataset's own age distribution. |
| `classifyDecliningTrend` | `0.5` | **Fallback only**. Adaptive path uses P25 of the dataset's own trend distribution. |
| `classifyMinSample` | `8` | Below this many files, percentile estimates are too noisy to trust and the two thresholds above revert to absolutes. |
| `adaptiveDecliningTrendFloor` | `0.01` | Minimum value for the adaptive `decliningTrendThreshold`. Prevents P25 from collapsing to 0 on mature repos where dormant files dominate, which would hide every legacy-hotspot. |
| `suspectWarningMinChurnRatio` | `0.10` | Vendor/generated path warning fires only when matched paths together exceed this fraction of total repo churn — prevents a single incidental `.lock` file from triggering noise. |
| `classifyTrendWindowMonths` | `3` | Window (months, relative to latest commit) for the recent vs earlier split in `trend`. |
| `contribRefactorRatio` | `0.8` | `del/add ≥ this` → dev profile `contribType = refactor`. |
| `contribBalancedRatio` | `0.4` | `0.4 ≤ del/add < 0.8` → `balanced`; below 0.4 → `growth`. |
| `refactorMinFiles` | `10` | Minimum files for a commit to be a mechanical-refactor candidate (coupling filter). |
| `refactorMaxChurnPerFile` | `5.0` | Mean churn per file below this in a candidate commit → treated as refactor; its pairs are excluded from coupling. |
| `specBroadGeneralistMax` | `0.15` | Specialization Herfindahl `< 0.15` → `broad generalist` label in dev profile. |
| `specBalancedMax` | `0.35` | `0.15 ≤ H < 0.35` → `balanced`. |
| `specFocusedMax` | `0.7` | `0.35 ≤ H < 0.7` → `focused specialist`; `H ≥ 0.7` → `narrow specialist`. |
| `Pct80Threshold` | `0.8` | Classic 80/20 cutoff. Shared by bus factor (fewest devs covering 80% of lines) and Pareto (subset producing 80% of churn/commits). |
| `paretoExtremelyConcentratedMax` | `10.0` | Pareto cards with `≤10%` of items holding 80% of activity are labelled *extremely concentrated* (🔴). Defined in `internal/report/report.go`. |
| `paretoModeratelyConcentratedMax` | `25.0` | `≤25%` → *moderately concentrated* (🟡); above → *well distributed* (🟢). Labels/markers are precomputed in `ComputePareto` so the HTML template and CLI both consume the same strings. |

### Reproducibility

Every ranking function has an explicit tiebreaker so the same input produces the same output across runs and between the CLI (`stats --format json`) and the HTML report. Without this, ties on integer keys (ubiquitous — e.g. many files with `bus_factor = 1`) would let Go's randomized map iteration produce a different top-N each time.

| Stat | Primary key (desc unless noted) | Tiebreaker |
|------|---------------------------------|------------|
| `summary` | — | N/A (scalar) |
| `contributors` | commits | email asc |
| `hotspots` | commits | path asc |
| `directories` | file_touches | dir asc |
| `busfactor` | bus_factor (asc) | path asc |
| `coupling` | co_changes | coupling_pct |
| `churn-risk` | label priority (legacy-hotspot → silo → active-core → active → cold) | recent_churn desc, then bus_factor asc |
| `top-commits` | lines_changed | sha asc |
| `dev-network` | shared_lines | shared_files |
| `profile` | commits | email asc |

A third-level tiebreaker on path/sha/email asc is applied where primary and secondary can both tie (`churn-risk`, `coupling`, `dev-network`) so ordering is stable even with exact equality on the first two keys. Inside each profile, the `TopFiles`, `Scope`, and `Collaborators` sub-lists are also sorted with explicit tiebreakers (path / dir / email asc) so their internal ordering is deterministic too.

Inside `busfactor`, the per-file `TopDevs` list is sorted by lines desc with an email asc tiebreaker. Without it, binary assets and small files where two devs contribute equal lines (e.g. `.gif`, `.png`, one-line configs) produced a different `TopDevs` email order on every run.

### Vendor/generated path warning

When `stats` loads a dataset in table format, it scans for paths matching a conservative list of vendor/generated heuristics: `vendor/`, `node_modules/`, `dist/`, `build/`, `third_party/`, `*.min.js`, `*.min.css`, `*.lock`, language-specific lockfiles (`package-lock.json`, `go.sum`, `Cargo.lock`, `poetry.lock`, `yarn.lock`, `pnpm-lock.yaml`), and common generated extensions (`*.pb.go`, `*_pb2.py`, `*.generated.*`).

If the matched paths together account for at least `suspectWarningMinChurnRatio` (10%) of total repo churn, a warning is emitted to stderr listing the top-6 buckets with a copy-pasteable `extract --ignore` invocation. Below the floor, no warning — a single incidental `.lock` file in an otherwise clean repo stays silent.

Directory-segment heuristics (`vendor`, `node_modules`, `dist`, `build`, `third_party`) match the segment wherever it appears in the path, but `extract --ignore` treats a bare `dist/*` glob as a repo-root prefix. To avoid suggesting a fix that wouldn't actually remove the matched files, each bucket carries a `Suggestions` list of the specific parent prefixes it matched (e.g. `wp-includes/js/dist/*`, `services/auth/vendor/*`), and the warning emits every unique prefix so the copy-pasteable command covers every source of distortion. Suffix and basename patterns (`*.min.js`, `package-lock.json`, etc.) collapse to a single glob because extract's basename match already handles them at any depth.

The warning is advisory. Nothing is auto-filtered; the user decides whether to re-extract. Matches do not affect computed stats in that run. JSON/CSV output paths skip the warning since they're typically piped.

On multi-JSONL loads (e.g. `stats --input a.jsonl --input b.jsonl` or a `gitcortex scan` dataset), paths carry a `<repo>:` prefix internally. The suspect detector strips that prefix before matching and suggesting, so root-level `vendor/`, `package-lock.json`, `go.sum`, etc. in any individual repo are detected and the emitted `--ignore` globs are repo-relative (drop-in for `extract --ignore` and `scan --extract-ignore`). Same-shape findings across repos collapse to one suggestion (`dist/*` applies everywhere rather than being listed once per repo).

Statistical heuristics (very high churn-per-commit, single-author bulk updates) are deliberately out of scope — their false-positive rate on hand-authored code is higher than the path-based list and we'd rather stay quiet than cry wolf.

### `--mailmap` off by default

`extract` does not apply `.mailmap` unless you pass `--mailmap`. Without it, the same person with two emails (e.g. `alice@work.com` and `alice@personal.com`) splits into two contributors. Affected metrics: `contributors`, `bus factor`, `dev network`, `profiles`, churn-risk label (via bus factor).

Extract emits a warning when the repo has a `.mailmap` file but the flag was omitted. Enable it for any repo where identity matters:
```bash
gitcortex extract --repo . --mailmap
```

### Timezone handling

Two classes of metrics, different rules:

- **Cross-commit aggregation** (monthly/weekly/yearly buckets, active-day counts, trend calculation, display dates) uses **UTC**. Two commits at the same UTC instant land in the same bucket regardless of each author's local offset.
- **Human-rhythm metrics** (working patterns heatmap, per-dev work grid) use the **author's local timezone** as recorded by git. These describe *when the person was typing*, not instant-of-time.

Side effects worth knowing:
- A commit at `2024-03-31T23:00-05:00` (local) equals `2024-04-01T04:00Z` (UTC). It belongs to **April** in monthly activity but to **March 31** in the author's working grid.
- `active_days` is counted by UTC date. A developer who always commits near midnight local time may show slightly different day counts than a pure local-TZ count.
- `first_commit_date` / `last_commit_date` in the summary are UTC.
- **Working patterns trust the author's committed timezone.** If a dev has their laptop clock set wrong or a CI agent impersonates the author in UTC, the heatmap silently reflects that. There is no sanity check.

### Rename tracking

gitcortex uses `git log -M` so renames and moves (including cross-directory moves) are detected by git's similarity matcher. When a rename is detected, the historical entries for the old and new paths are **merged into a single canonical entry** (the newest path in the chain) during finalization.

Effects:
- `Total files touched` reflects canonical files, not distinct paths seen in history.
- `firstChange`, `monthChurn`, and `devLines` on the canonical entry span the **full history** including pre-rename commits.
- Rename chains (`A → B → C`) collapse to `C`.
- Files renamed to each other and later co-changing don't produce self-pairs in coupling.
- `FilesTouched` in developer profiles counts canonical paths, so one dev editing a file before and after a rename counts as **one** file.

Limits:
- Git's rename detection defaults to ~50% similarity. A rename with heavy edits may not be detected, resulting in separate delete + add entries.
- Copies (`C*` status) are **not** merged — copied files legitimately live as two entries.
- If the rename commit falls outside a `--since` filter, the edge isn't captured and the old/new paths stay separate within the filtered window. The reuse heuristics (`oldPathCounts`, `isRenameTarget`, `maxEdgeDate`) are computed only over edges *inside* the filter window, so a rename that happened outside the window cannot participate in the signal — `wasRecreated` may be false even when the repo history contains a recreation, which can cause the single-edge reuse check to fire (or not fire) differently than it would on the full history.
- **Path reuse patterns.** gitcortex detects four distinct shapes where `oldPath` is ambiguous and handles each explicitly:

  1. **Multi-edge reuse (different lineages):** `A → B`, then later the name `A` is reused for an unrelated file that gets renamed to `D`. Two edges with `oldPath = A`, no intermediate edge pointing at `A`. gitcortex **refuses to migrate** either edge — A stays put with merged lineages, B and D keep only their own post-rename data. Detected by `oldPathCounts[A] > 1 && !isRenameTarget[A]`.

  2. **Rename-back chain (same lineage):** `A → B → A → C`. The repeated `A` is recreated by an explicit `B → A` edge, so the whole chain belongs to one lineage and collapses into `C`. Detected by `oldPathCounts[A] > 1 && isRenameTarget[A]`.

  3. **Single-edge reuse (different lineages, common in the wild):** `A → B` (one edge), then a new unrelated file is created at `A` and never renamed further. `oldPathCounts[A] = 1`, so the multi-edge heuristic does not fire. Detected **temporally**: `ds.files["A"].lastChange` is after `maxEdgeDate[A]` (the latest commitDate of any edge involving `A`, as source or target). Activity on `A` that continued past every rename involving `A` means a new lineage. Skip migration.

  4. **Chain + reuse (same temporal check, harder pattern):** `D → A → B` chain followed by a new file at `A`. `A` is both a rename target (`D → A`) and a rename source (`A → B`), so pattern #2 would classify it as rename-back. The temporal check correctly fires because `lastChange(A) > maxEdgeDate[A]` (= `A → B` commit date). Real-data impact: 136 such files in the kubernetes snapshot.

  **Known limitation:** the exotic mixed pattern `A → B` (lineage 1), then `C → A` (lineage 2 recreation-via-rename), then `A → D` (lineage 2 renamed) — here `wasRecreated[A] = true` via `C → A`, `maxEdgeDate[A]` = `A → D` commit date, and typical `lastChange(A)` is bounded by that. The heuristic treats it as chain and misattributes lineage 1's pre-rename commits to `D`. A full fix requires per-commit temporal segmentation of `ds.files["A"]`, a larger architectural change.

### `--since` filter + ChurnRisk age

`firstChange` is the first time a file appears **in the filtered dataset**, not in repo history. When you run `--since=30d`, a file created 4 years ago but touched yesterday gets `age_days ≈ 0` and classifies as `active-core` — even though it's genuinely legacy.

If you need the label to reflect true age, either extract without `--since` (then filter queries in post), or treat label output under a `--since` run as "what's happening in this window" rather than "what kind of file is this".

### Classification degenerate edge cases

- **Renames reverted (cycle A→B→A).** The resolver bails out of the cycle with the current path; it doesn't crash but the "canonical" is implementation-defined for cyclic inputs.
- **Repo with single file.** The median-based `cold` threshold degenerates (median is that file's churn); the single file is never classified `cold`.
- **All files with identical churn.** Median equals every value, `lowChurn = median × 0.5`, so nothing is `cold`. Everything falls into the bf/age/trend tree.

### Dev specialization measures distribution, not expertise

The `Specialization` number and its label (`broad generalist` … `narrow specialist`) describe **where the dev's files live on disk**, not their semantic area of expertise. The two diverge whenever the person's domain cuts across the directory structure rather than aligning with it:

- A security engineer who audited and refactored auth across `api/`, `web/`, `gateway/`, and `services/` touches four dirs. Herfindahl is low, the label says "broad generalist" — but the person is a domain specialist whose domain happens to be cross-cutting.
- A release engineer who maintains CI/CD config scattered across `.github/`, `docker/`, `scripts/`, and `deploy/` lands the same way.
- Conversely, a generalist who happened to do a big one-off refactor of a single module in the recent window looks like a "narrow specialist" for the snapshot.

The label is a shortcut for reading the Herfindahl value. Use it when directory structure aligns with domains (one dir per module); cross-reference with `TopFiles`, `Scope`, and `Collaborators` to confirm when the repo is organized along another axis (e.g. monorepo with service boundaries cutting across dirs, or a library where concerns are horizontal). The raw Herfindahl value is objective; the interpretation of the label is not.
