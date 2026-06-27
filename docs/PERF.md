# Performance

How `gitcortex` scales across repositories. All measurements below were
taken on NVMe SSD with the **v2.11.0** binary (LRU blob cache enabled)
and `--batch-size 1000` (default). The full pipeline (extract → stats →
report) was timed on each repo in isolation, one repo at a time, so the
numbers are not skewed by concurrent load. `stats`/`report` since v2.11.0
also compute the test-to-source ratio section, so they do slightly more
work than the earlier (v2.3.0) figures these tables replace.

## Extract benchmarks

Six repositories spanning four orders of magnitude in commit count,
extracted end-to-end (git log stream, blob size resolution, JSONL
emission, checkpointing) then analyzed (`stats --format json`, `report`).
None use `--ignore` filters. Chromium is carried over from the v2.3.0
run (the repo was not available for this round) for the extreme-scale /
OOM reference; its filtered extract used the `--ignore` set in footnote †.

| Repository | Commits | Bare size | Extract | Stats (JSON) | Report (HTML) | JSONL |
|---|---|---|---|---|---|---|
| [gitcortex](https://github.com/lex0c/gitcortex) (self) | 189 | 620 KB | **0.1s** | 0.01s | 0.02s | 803 lines / 244 KB |
| [Pi-hole](https://github.com/pi-hole/pi-hole) | 7,077 | 9.8 MB | **0.9s** | 0.21s | 0.23s | 23k lines / 6.4 MB |
| [Praat](https://github.com/praat/praat) | 10,221 | 490 MB | **23.8s** | 1.12s | 1.24s | 95k lines / 29 MB |
| [WordPress](https://github.com/WordPress/WordPress) | 52,466 | 629 MB | **46.4s** | 2.96s | 3.23s | 298k lines / 96 MB |
| [Kubernetes](https://github.com/kubernetes/kubernetes) | 137,016 | 1.3 GB | **1m 58.1s** | 11.1s | 12.0s | 943k lines / 313 MB |
| [Linux kernel](https://github.com/torvalds/linux) | 1,438,634 | 6.3 GB | **11m 34.3s** | 1m 23.7s | 1m 29.2s | 6.1M lines / 1.9 GB |
| [Chromium](https://chromium.googlesource.com/chromium/src) †◇ | 1,738,421 | 61 GB | **1h 55m 52s** | OOM ‡ | OOM ‡ | 12.3M lines / 4.4 GB |

◇ Chromium figures are from the v2.3.0 run, not re-measured this round.

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
| Pi-hole | ~25,600 |
| Linux kernel | ~8,750 |
| Kubernetes | ~7,990 |
| WordPress | ~6,425 |
| Praat | ~3,990 |
| Chromium | ~1,775 ◇ |
| gitcortex (self) | noisy † |

† gitcortex's number is noisy: 803 records in ~0.1s is too short a
sample to characterize sustained throughput reliably. Included in the
extract table because it's useful to see the tool exercising itself —
the dogfood benchmark. ◇ Chromium carried from v2.3.0.

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

Post-v2.3.0 optimizations reduce several hot spots:

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
- **(v2.11.0) Per-commit decay caching at ingest** — the recency
  weight (`exp(-λ·days)`) and month key are computed once per commit
  and reused across its files, instead of once per file change, cutting
  `math.Exp` + `time.Format` calls in the hottest load loop.
- **(v2.11.0) Commit messages truncated at ingest** to a small bound
  (only the first line is ever displayed), so `--include-commit-messages`
  no longer retains full multi-line bodies for every commit.
- **(v2.11.0) The test-stats per-era map (`byPath`) is built only at
  the rename merge, not per file at ingest** — so the unrenamed majority
  (and runs that never read test stats) allocate nothing extra. The
  test-to-source section itself adds modest CPU to `stats`/`report`.

Together these changes made the Linux report finish cleanly (~1m 29s on
v2.11.0) on a machine where it previously died at 0 bytes. **Chromium
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
