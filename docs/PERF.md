# Performance

How `gitcortex extract` scales across repositories. All measurements
below were taken on NVMe SSD with the v2.3.0 binary (LRU blob cache
enabled) and `--batch-size 1000` (default).

## Extract benchmarks

Seven repositories spanning four orders of magnitude in commit count,
extracted end-to-end (git log stream, blob size resolution, JSONL
emission, checkpointing). Smaller repos run without `--ignore`
filters; Chromium runs with a monorepo filter set applied (see the
Chromium section for details).

| Repository | Commits | Bare size | Extract | Stats (JSON) | Report (HTML) | JSONL |
|---|---|---|---|---|---|---|
| [gitcortex](https://github.com/lex0c/gitcortex) (self) | 189 | 620 KB | **0.05s** | 0.012s | 0.022s | 803 lines / 245 KB |
| [Pi-hole](https://github.com/pi-hole/pi-hole) | 7,077 | 10 MB | **0.99s** | 0.19s | 0.27s | 23k lines / 6.5 MB |
| [Praat](https://github.com/praat/praat) | 10,221 | 490 MB | **24.0s** | 1.02s | 1.07s | 95k lines / 30 MB |
| [WordPress](https://github.com/WordPress/WordPress) | 52,466 | 629 MB | **46.3s** | 2.90s | 3.03s | 298k lines / 96 MB |
| [Kubernetes](https://github.com/kubernetes/kubernetes) | 137,016 | 1.3 GB | **2m 4.1s** | 12.4s | 15.5s | 943k lines / 314 MB |
| [Linux kernel](https://github.com/torvalds/linux) | 1,438,634 | 6.3 GB | **13m 26.9s** | 1m 7.7s | 1m 9.8s | 6.1M lines / 1.9 GB |
| [Chromium](https://chromium.googlesource.com/chromium/src) † | 1,738,421 | 61 GB | **1h 55m 52s** | OOM ‡ | OOM ‡ | 12.3M lines / 4.4 GB |

† Chromium was extracted with `--ignore 'third_party/*' --ignore 'out/*'
--ignore 'node_modules/*' --ignore '*.min.js' --ignore '*.min.css'
--ignore 'package-lock.json' --ignore 'yarn.lock' --ignore '*.pb.go'
--ignore '*_generated.*'`. Without filters the JSONL would be
substantially larger and the extract slower.

‡ `stats` and `report` on Chromium's 4.4 GB JSONL exceed the memory
available on a 15 GB machine (~6 GB of free RAM after OS/browser use).
The resident working set for analysis at this scale is dominated by
per-file accumulators (notably the `monthChurn` map used for trend
classification) that scale as O(files × months_active). Reducing this
is tracked as future work — see the "Memory limits" section below.

## Throughput: records/second, not commits/second

`commits/second` is a leaky metric because commits vary wildly in
size: a typical one-file commit is hundreds of times cheaper than a
3,000-file import commit. A stabler metric is **JSONL records emitted
per second** — normalizing by actual work rather than commit count:

| Repository | Records/sec (avg) |
|---|---|
| gitcortex (self) | ~18,000 † |
| Pi-hole | ~23,000 |
| Kubernetes | ~7,600 |
| Linux kernel | ~7,500 |
| WordPress | ~6,400 |
| Praat | ~3,900 |
| Chromium | ~1,775 |

† gitcortex's number is noisy: 803 records in 45 ms is too short a
sample to characterize sustained throughput reliably. Included here
because it's useful to see the tool exercising itself — the dogfood
benchmark.

Small repos benefit from the entire working set fitting in OS page
cache. Linux (6 GB) and Kubernetes (1.3 GB) mostly fit. Chromium
(61 GB bare) exceeds most workstations' available cache, so
`cat-file --batch-check` lookups land on SSD more often than not —
hence the 4× drop in records/sec vs. Linux.

## What drives extract time

Extract is an I/O-bound pipeline with three stages:

1. **`git log --raw --numstat`** streams commit history newest-first.
   Sequential read of packfiles, cheap on SSD (typically 200+ MB/s
   reading rate from the filesystem).
2. **`cat-file --batch-check`** resolves blob sizes. For each unique
   hash in each commit, gitcortex writes a hash to stdin and reads
   back a `<hash> blob <size>` line. Each lookup triggers a small
   random read into the packfile index plus the object header.
3. **JSONL emission** is buffered writes, negligible relative to
   the two above.

CPU usage stays between 5% and 10% across all runs — the process
blocks on the `cat-file` pipe the vast majority of wall time. The
LRU blob cache (v2.3.0) removes redundant pipe round-trips when the
same hash appears across consecutive commits, which is the common
case: a file unchanged across N commits would otherwise be queried
N times.

## Chromium rate trajectory

Smaller repos extract at near-constant throughput. Chromium's rate
varies 6× during a single run because history contains both
small-commit epochs (modern development: a handful of files per
commit) and import-heavy epochs (2013-era Blink fork, V8/WebKit2/Skia
vendor integrations: thousands of files per commit).

Sampled from the run's checkpoint log:

| Elapsed | Offset | % done | Window rate (cps) |
|---|---|---|---|
| 3:16 | 58k | 3% | ~296 |
| 11:09 | 198k | 11% | ~260 |
| 34:10 | 542k | 31% | ~226 |
| 1:02:41 | 941k | 54% | ~250 |
| 1:15 | 1,175k | 68% | **~400** (peak) |
| 1:25 | 1,459k | 84% | **65-88** (trough — Blink imports) |
| 1:40 | 1,570k | 90% | ~130 |
| 1:55 | 1,731k | 99.6% | ~200 |

The trough at 84% is `git log` walking through commits from roughly
2010-2013. In that era, a single entry can emit hundreds of blob-hash
lookups and tens of KB of JSONL output. The commits/second metric
crashes even though the per-record throughput stays comparable to
the baseline — the unit "commit" temporarily weighs 20-50× more than
its modern counterpart.

## LRU blob cache (v2.3.0)

The v2.3.0 resolver adds a 50,000-entry LRU of `hash → blob size`.
Git content-addresses blobs, so `hash → size` is a pure function,
making the cache provably safe — extract output is byte-identical
with or without it, only faster.

Measured impact on WordPress (52k commits, warm packfiles, SSD):
**50.0s → 46.3s wall time (-7.4%)**. The cache removes pipe
round-trips for blobs that persist across consecutive commits
(the common case: most files change rarely).

Memory cost: ~7 MB for the 50k-entry cache regardless of repository
size.

## Memory limits

Extract streams the commit history and keeps a small buffer in RAM
(peak ~25 MB on Chromium). The bottleneck for memory is **analysis**:
`stats` and `report` build an in-memory `Dataset` with per-file and
per-dev accumulators that scale with the number of classified files
and the active span of each.

Post-v2.3.0 optimizations reduce two of the hot spots:

- **ChurnRiskLabelCounts avoids materializing full result structs**
  for the HTML chip strip. Earlier versions called
  `stats.ChurnRisk(ds, 0)` to get per-label counts, which held one
  ~200-byte struct per classified file in memory. On Linux-class
  repos this was hundreds of MB of transient allocation.
- **DevProfiles respects the `--top` cap when invoked by the HTML
  report**. Without this, the report built full per-dev maps
  (files, collaborators, work grid, monthly activity) for every
  contributor — 38k on Linux, pushing RSS past 6 GB and triggering
  the kernel OOM-killer silently. Capping at top-N before building
  those structures keeps the heavy work proportional to the output.

Together these changes made the Linux report finish cleanly in
~1m 10s on a machine where it previously died at 0 bytes. **Chromium
remains out of reach** for `stats` and `report` on a 15 GB machine.
The dominant remaining hog is `fileEntry.monthChurn` — a per-month
activity map on every file, used only to compute the trend dimension
of the Churn Risk classification. Scaling `O(files × months_active)`,
it reaches several GB on a 1.7M-file repo with a 15+ year history.

Cutting this further would require either:
- Computing trend lazily (per file, on demand) without storing all
  month buckets up-front;
- Switching `monthChurn` to a compact fixed-size array (e.g., 12
  quarterly buckets) with lower resolution;
- Or a different trend formulation that doesn't require per-month
  granularity at all.

None of these are trivial and all change classification semantics
slightly. For now, the practical cap on `stats`/`report` is roughly
the Linux-scale repo — ~1.5M commits, a few GB of JSONL, a few
hundred MB of `Dataset` in memory. Chromium is the exception.

## Practical guidance

- **Filter aggressively with `--ignore`.** Vendor directories, build
  outputs, and generated paths are both the biggest source of noise
  in stats and the biggest chunk of extract time. gitcortex skips
  them at emit time, so each `--ignore` saves `cat-file` round-trips
  and JSONL bytes.
- **Extract is resumable.** State is checkpointed every
  `--batch-size` commits (default 1000). If a run is interrupted,
  rerunning with the same flags continues from the last checkpoint
  — important on multi-hour runs like Chromium.
- **Memory stays low.** The resolver cache uses ~7 MB; the commit
  stream has no unbounded buffers. Even Chromium extract peaks
  around 25 MB RSS.
- **Plan capacity by records/second, not commits/second.** The
  commits/second metric is dominated by repository content: import-
  heavy histories artificially depress it even when the underlying
  throughput is unchanged.
- **All numbers are SSD.** Extract is I/O-bound; the comparisons
  above assume NVMe-class storage. Running on a different class of
  device would produce different absolute numbers; relative
  behavior across repos should be similar.
