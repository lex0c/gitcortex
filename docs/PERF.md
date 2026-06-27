# Performance

How `gitcortex` scales across repositories. All measurements below were
taken on NVMe SSD with a **development build following v2.11.0** and
`--batch-size 1000` (default), on a 12-core machine. The full pipeline
(extract → stats → report) was timed on each repo in isolation, one repo
at a time, so the numbers are not skewed by concurrent load.

Two changes since v2.11.0 reshape these tables:

- **Blob-size resolution is now opt-in** (`--blob-sizes`). The default
  extract no longer runs `git cat-file --batch-check`, and the JSONL no
  longer carries `old_size`/`new_size` — so files are a few percent
  smaller. Extract wall time is unchanged within run-to-run variance (the
  `git log` stream dominates, not the blob lookup — see "What drives
  extract time").
- **`stats` and `report` parallelize.** Their independent metric passes
  now run concurrently across cores, and the load path parses each JSONL
  line once instead of twice. Together these cut `stats`/`report` wall
  time ~30–49% versus v2.11.0 on this machine.

`extract` is I/O-bound and carries roughly ±10% run-to-run variance;
treat its figures as directional. `stats`/`report` also include the
test-to-source ratio section introduced in v2.11.0.

## Extract benchmarks

Six repositories spanning four orders of magnitude in commit count,
extracted end-to-end (git log stream, JSONL emission, checkpointing;
blob-size resolution off by default) then analyzed (`stats --format
json`, `report`). None use `--ignore` filters. Chromium is carried over
from the v2.3.0 run (the repo was not available for this round) for the
extreme-scale / OOM reference; its filtered extract used the `--ignore`
set in footnote †.

| Repository | Commits | Bare size | Extract | Stats (JSON) | Report (HTML) | JSONL |
|---|---|---|---|---|---|---|
| [gitcortex](https://github.com/lex0c/gitcortex) (self) | 189 | 620 KB | **0.1s** | 0.01s | 0.02s | 803 lines / 231 KB |
| [Pi-hole](https://github.com/pi-hole/pi-hole) | 7,077 | 9.8 MB | **0.9s** | 0.11s | 0.14s | 23k lines / 6.2 MB |
| [Praat](https://github.com/praat/praat) | 10,221 | 490 MB | **20.1s** | 0.57s | 0.64s | 95k lines / 27 MB |
| [WordPress](https://github.com/WordPress/WordPress) | 52,466 | 629 MB | **40.3s** | 1.58s | 1.76s | 298k lines / 90 MB |
| [Kubernetes](https://github.com/kubernetes/kubernetes) | 137,016 | 1.3 GB | **1m 50.6s** | 8.07s | 8.47s | 943k lines / 295 MB |
| [Linux kernel](https://github.com/torvalds/linux) | 1,438,634 | 6.3 GB | **12m 45.6s** | 47.6s | 48.8s | 6.1M lines / 1.74 GB |
| [Chromium](https://chromium.googlesource.com/chromium/src) †◇ | 1,738,421 | 61 GB | **1h 55m 52s** | OOM ‡ | OOM ‡ | 12.3M lines / 4.4 GB |

◇ Chromium figures are from the v2.3.0 run (with blob sizes on, the
default at the time), not re-measured this round.

**`stats`/`report` vs v2.11.0** (same machine, same repos) — the
parallelization + single-parse load:

| Repository | Stats v2.11.0 → now | Report v2.11.0 → now |
|---|---|---|
| Pi-hole | 0.21s → **0.11s** (−48%) | 0.23s → **0.14s** (−39%) |
| Praat | 1.12s → **0.57s** (−49%) | 1.24s → **0.64s** (−48%) |
| WordPress | 2.96s → **1.58s** (−47%) | 3.23s → **1.76s** (−46%) |
| Kubernetes | 11.1s → **8.07s** (−27%) | 12.0s → **8.47s** (−29%) |
| Linux | 1m 23.7s → **47.6s** (−43%) | 1m 29.2s → **48.8s** (−45%) |

The win is largest on mid-size repos where compute dominates; on
Kubernetes and Linux the single-threaded JSONL load (one big file) is a
growing share of the total and Amdahl-caps the gain — parallelizing that
load is the next lever. Extract rows are within ±10% run-to-run variance
of v2.11.0 (most came out faster here, Linux slower — all noise; the
default no longer does any blob lookup, so no extract work was added).

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
| Kubernetes | ~8,530 |
| Linux kernel | ~7,940 |
| WordPress | ~7,400 |
| Praat | ~4,720 |
| Chromium | ~1,775 ◇ |
| gitcortex (self) | noisy † |

† gitcortex's number is noisy: 803 records in ~0.1s is too short a
sample to characterize sustained throughput reliably. Included in the
extract table because it's useful to see the tool exercising itself —
the dogfood benchmark. ◇ Chromium carried from v2.3.0.

Small repos benefit from the entire working set fitting in OS page
cache. Linux (6 GB) and Kubernetes (1.3 GB) mostly fit. The carried-over
Chromium figure (61 GB bare, measured before blob sizes went opt-in)
exceeds most workstations' available cache, so its `cat-file
--batch-check` lookups landed on SSD more often than not — part of the 4×
drop in records/sec vs. Linux. The current default skips that lookup
entirely, so a re-measured Chromium would close some of that gap; the
`git log` stream over 61 GB of packfiles remains the floor.

## What drives extract time

Extract is dominated by a single stage: the **`git log -M --raw
--numstat`** stream. Git computes a rename-detected diff for every commit
server-side and streams the result; gitcortex parses it newest-first and
emits JSONL. The packfile reads are sequential and cheap on SSD (200+
MB/s), but git's own diff + rename-detection work over the full history
is the real cost — it shows up as *git's* CPU while gitcortex sits at 5–10%
blocking on the log pipe. **JSONL emission** is buffered writes,
negligible by comparison.

Blob-size resolution via **`git cat-file --batch-check`** (opt-in since
the post-v2.11.0 build) is a *minor* component, not the bottleneck.
Measured on WordPress, same machine, back to back:

| WordPress extract | wall time | JSONL |
|---|---|---|
| default (cat-file off) | 44.2s | 90 MB |
| `--blob-sizes` (cat-file on) | 42.9s | 96 MB |

The two are within run-to-run variance — the cat-file run was even
slightly *faster* (warmer cache, ran second). An earlier version of this
doc claimed extract "blocks on the cat-file pipe the vast majority of
wall time"; that did not hold up. Disabling cat-file did not materially
change wall time, because the LRU blob cache (below) already removed
almost all of its pipe round-trips. With the default now skipping the
lookup outright, the JSONL is a few percent smaller and one subprocess is
gone — but extract speed is set by `git log`, not blob resolution.

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

Relevant only with `--blob-sizes` (since the post-v2.11.0 build the
resolver is off by default). When enabled, the resolver carries a
50,000-entry LRU of `hash → blob size`. Git content-addresses blobs, so
`hash → size` is a pure function, making the cache provably safe —
extract output is byte-identical with or without it, only faster.

Measured impact back when blob resolution was the default — WordPress
(52k commits, warm packfiles, SSD): **50.0s → 46.3s wall time (-7.4%)**.
The cache removes pipe round-trips for blobs that persist across
consecutive commits (the common case: most files change rarely). This is
also *why* disabling cat-file outright now saves so little: the cache had
already eliminated most of its cost.

Memory cost: ~7 MB for the 50k-entry cache, and only when `--blob-sizes`
is passed.

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
- **(post-v2.11.0) Single-parse JSONL load** — each line is type-detected
  from its prefix and unmarshalled once instead of twice, roughly halving
  the load phase's transient allocations and CPU. This is a throughput
  win, not a peak-RSS one: peak is still set by the retained `Dataset`.
- **(post-v2.11.0) Parallel `stats`/`report` compute** — the independent
  metric passes run concurrently. They were already all held in memory at
  once before rendering, so concurrency does not raise peak RSS; Chromium
  still OOMs for the same reason (`monthChurn`), just no sooner.

Together these changes made the Linux report finish cleanly (~49s now,
~1m 29s on v2.11.0) on a machine where it previously died at 0 bytes.
**Chromium remains out of reach** for `stats` and `report` on a 15 GB
machine.
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
  outputs, and generated paths are both the biggest source of noise in
  stats and a real chunk of extract work (git still diffs them) and JSONL
  bytes. gitcortex skips them at emit time, so each `--ignore` shrinks the
  output and the downstream `stats`/`report` load.
- **Leave `--blob-sizes` off unless you need it.** It's off by default;
  enabling it adds a `cat-file` subprocess and `old_size`/`new_size` to
  every file record. No gitcortex stat reads those — turn it on only for
  an external consumer of the JSONL.
- **`stats`/`report` use all your cores.** The metric passes run
  concurrently, so more cores = faster analysis; the single-threaded
  JSONL load is the part that doesn't scale with cores. `stats --format
  json` is the leanest path when you only need aggregate data.
- **Extract is resumable.** State is checkpointed every
  `--batch-size` commits (default 1000). If a run is interrupted,
  rerunning with the same flags continues from the last checkpoint
  — important on multi-hour runs like Chromium.
- **Memory stays low.** The commit stream has no unbounded buffers (the
  ~7 MB resolver cache only exists with `--blob-sizes`). Even Chromium
  extract peaks around 25 MB RSS.
- **Plan capacity by records/second, not commits/second.** The
  commits/second metric is dominated by repository content: import-
  heavy histories artificially depress it even when the underlying
  throughput is unchanged.
- **All numbers are SSD.** Extract is I/O-bound; the comparisons
  above assume NVMe-class storage. Running on a different class of
  device would produce different absolute numbers; relative
  behavior across repos should be similar.
