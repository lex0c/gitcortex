package stats

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Thresholds for classification and profile categorization. Exposed as named
// constants so the values are discoverable and consistent across the package.
// See docs/METRICS.md for rationale.
const (
	// Churn Risk classification
	classifyColdChurnRatio    = 0.5  // recent_churn ≤ 0.5 × median → cold
	classifyActiveBusFactor   = 3    // bf ≥ 3 → active (shared)
	classifyOldAgeDays        = 180  // fallback "old" threshold when dataset < classifyMinSample files; adaptive mode uses the P75 of file ages in the dataset
	classifyDecliningTrend    = 0.5  // fallback "declining" threshold under the same condition; adaptive mode uses the P25 of file trends
	classifyTrendWindowMonths = 3    // recent vs earlier split
	classifyMinSample         = 8    // below this, percentile estimates are noisy and we fall back to the absolute constants above
	// adaptiveDecliningTrendFloor keeps the P25-derived "declining"
	// threshold strictly positive. churnTrend clamps its output at 0
	// for files with earlier-only history (the strongest
	// legacy-hotspot signal). In mature repos where ≥25% of files are
	// dormant, P25 collapses to 0; without this floor, `trend < 0`
	// never fires and every dormant concentrated file is misrouted to
	// silo instead of legacy-hotspot — the exact signal the rule is
	// supposed to surface. Epsilon is small enough not to widen the
	// declining band past the fallback (0.5) even in the pathological
	// case, large enough that 0.0 ≠ threshold under float compare.
	adaptiveDecliningTrendFloor = 0.01

	// Developer profile contribution type (del/add ratio)
	contribRefactorRatio = 0.8 // ratio ≥ 0.8 → refactor
	contribBalancedRatio = 0.4 // 0.4 ≤ ratio < 0.8 → balanced; else growth

	// Coupling — mechanical refactor heuristic. A commit touching many
	// files with very low mean churn per file is almost always a rename,
	// format, or lint fix, not meaningful co-change. Pairs from such
	// commits are excluded to reduce false coupling. Denominators
	// (couplingFileChanges) are still counted so ChangesA/ChangesB
	// remain honest totals.
	refactorMinFiles = 10
	// Unit is line-churn = additions + deletions per file, not just
	// additions. Strict < threshold: a commit with mean exactly 5.0 is
	// NOT filtered.
	refactorMaxChurnPerFile = 5.0

	// Developer specialization labels, applied to DevProfile.Specialization
	// (Herfindahl over per-directory file distribution). Tuned so that
	// plausible repo shapes land in the expected band:
	//   uniform spread over 7+ dirs → broad generalist
	//   2-4 dirs with one somewhat dominant → balanced
	//   one dir clearly dominant (~60-85% of files) → focused specialist
	//   ≥ 85% of files in one dir → narrow specialist
	specBroadGeneralistMax = 0.15
	specBalancedMax        = 0.35
	specFocusedMax         = 0.7

	// Pct80Threshold is the classic 80/20 cutoff. Any question of the
	// form "what is the smallest subset that accounts for 80% of X?" uses
	// this (bus factor across files/dirs, Pareto across files/devs/dirs).
	Pct80Threshold = 0.8
)

type ContributorStat struct {
	Name       string
	Email      string
	Commits    int
	Additions  int64
	Deletions  int64
	FilesTouched int
	ActiveDays int
	FirstDate  string
	LastDate   string
}

type FileStat struct {
	Path       string
	Commits    int
	Additions  int64
	Deletions  int64
	Churn      int64
	UniqueDevs int
}

type ActivityBucket struct {
	Period    string
	Commits   int
	Additions int64
	Deletions int64
}

type Summary struct {
	TotalCommits    int
	TotalDevs       int
	TotalFiles      int
	TotalAdditions  int64
	TotalDeletions  int64
	MergeCommits    int
	AvgAdditions    float64
	AvgDeletions    float64
	AvgFilesChanged float64
	FirstCommitDate string
	LastCommitDate  string
}

type BusFactorResult struct {
	Path      string
	BusFactor int
	TopDevs   []string
}

type CouplingResult struct {
	FileA       string
	FileB       string
	CoChanges   int
	CouplingPct float64
	ChangesA    int
	ChangesB    int
}

type ChurnRiskResult struct {
	Path           string
	RecentChurn    float64
	BusFactor      int
	RiskScore      float64 // kept for CI gate compatibility; not used for ranking
	TotalChanges   int
	LastChangeDate string
	FirstChangeDate string
	AgeDays         int
	Trend           float64 // recent 3mo churn / earlier churn; 1 = flat, <0.5 declining, >1.5 growing
	Label           string  // "cold" | "active" | "active-core" | "silo" | "legacy-hotspot"
	// AgePercentile and TrendPercentile report where this file lands in the
	// per-dataset distribution (0-100). Nil when the fallback path ran
	// (dataset below classifyMinSample) so JSON consumers see the field
	// omitted rather than a `-1` sentinel. Surfacing these alongside the
	// label makes the distance from the classification boundary visible:
	// `legacy-hotspot (age P92, trend P08)` vs a file that barely crossed.
	AgePercentile   *int `json:"age_percentile,omitempty"`
	TrendPercentile *int `json:"trend_percentile,omitempty"`
}

type WorkingPattern struct {
	Hour    int
	Day     string
	Commits int
}

type DevEdge struct {
	DevA        string
	DevB        string
	SharedFiles int     // files where both devs contributed at least one line
	SharedLines int64   // Σ min(linesA, linesB) across shared files — measures real overlap
	Weight      float64 // shared_files / max(files_A, files_B) * 100 (legacy)
}

// herfindahl returns the Herfindahl–Hirschman concentration index of a
// sample of non-negative values: Σ (pᵢ)² where pᵢ = valueᵢ / Σ value.
//
// Unlike Gini (which measures inequality between buckets and so returns 0
// for both "100% in 1 bucket" and "evenly across N buckets"), Herfindahl
// distinguishes these cases:
//   100% in 1 bucket → 1 (maximal concentration / specialization)
//   evenly across N buckets → 1/N (approaches 0 as N grows)
// This matches the specialization semantics needed here: a developer
// working in a single directory is maximally specialized, a developer
// spread across many directories is a generalist.
//
// Returns 0 for empty input or zero-sum input; returns 1 for a single
// non-zero bucket. Returns full float64 precision — callers that need
// to display the value should round at format time (the CLI and HTML
// templates use %.2f). Rounding inside this function caused quantization-
// induced label misclassification at band boundaries: a true value of
// 0.1496 would round to 0.150 and flip from "broad generalist" to
// "balanced".
func herfindahl(values []int) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum int64
	for _, v := range values {
		if v < 0 {
			v = 0
		}
		sum += int64(v)
	}
	if sum == 0 {
		return 0
	}
	total := float64(sum)
	var h float64
	for _, v := range values {
		if v <= 0 {
			continue
		}
		p := float64(v) / total
		h += p * p
	}
	return h
}

type StatsFlags struct {
	CouplingMinChanges int
	NetworkMinFiles    int
}

// --- Stats from pre-aggregated Dataset ---

func ComputeSummary(ds *Dataset) Summary {
	s := Summary{
		TotalCommits:   ds.CommitCount,
		TotalDevs:      ds.DevCount,
		TotalFiles:     ds.UniqueFileCount,
		TotalAdditions: ds.TotalAdditions,
		TotalDeletions: ds.TotalDeletions,
		MergeCommits:   ds.MergeCount,
	}

	if s.TotalCommits > 0 {
		s.AvgAdditions = float64(ds.TotalAdditions) / float64(s.TotalCommits)
		s.AvgDeletions = float64(ds.TotalDeletions) / float64(s.TotalCommits)
		s.AvgFilesChanged = float64(ds.TotalFilesChanged) / float64(s.TotalCommits)
	}

	if !ds.Earliest.IsZero() {
		s.FirstCommitDate = ds.Earliest.UTC().Format("2006-01-02")
	}
	if !ds.Latest.IsZero() {
		s.LastCommitDate = ds.Latest.UTC().Format("2006-01-02")
	}

	return s
}

func TopContributors(ds *Dataset, n int) []ContributorStat {
	result := make([]ContributorStat, 0, len(ds.contributors))
	for _, cs := range ds.contributors {
		result = append(result, *cs)
	}

	// Deterministic ordering under ties: commits desc, then email asc.
	sort.Slice(result, func(i, j int) bool {
		if result[i].Commits != result[j].Commits {
			return result[i].Commits > result[j].Commits
		}
		return result[i].Email < result[j].Email
	})

	if n > 0 && n < len(result) {
		result = result[:n]
	}
	return result
}

func FileHotspots(ds *Dataset, n int) []FileStat {
	result := make([]FileStat, 0, len(ds.files))
	for path, fe := range ds.files {
		result = append(result, FileStat{
			Path:       path,
			Commits:    fe.commits,
			Additions:  fe.additions,
			Deletions:  fe.deletions,
			Churn:      fe.additions + fe.deletions,
			UniqueDevs: len(fe.devLines),
		})
	}

	// Deterministic ordering under ties: commits desc, then path asc.
	sort.Slice(result, func(i, j int) bool {
		if result[i].Commits != result[j].Commits {
			return result[i].Commits > result[j].Commits
		}
		return result[i].Path < result[j].Path
	})

	if n > 0 && n < len(result) {
		result = result[:n]
	}
	return result
}

type DirStat struct {
	Dir        string
	// FileTouches is the sum of per-file commit counts across files in this
	// directory. A single commit touching N files in the directory contributes
	// N to this number — it is NOT distinct commits. Named accordingly to
	// avoid the prior "Commits" misnomer.
	FileTouches int
	Churn       int64
	Files       int
	UniqueDevs  int
	BusFactor   int
}

func DirectoryStats(ds *Dataset, n int) []DirStat {
	type dirAcc struct {
		fileTouches int
		churn       int64
		files       int
		devs        map[string]int64
	}

	dirs := make(map[string]*dirAcc)
	for path, fe := range ds.files {
		dir := "."
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			dir = path[:idx]
		}
		d, ok := dirs[dir]
		if !ok {
			d = &dirAcc{devs: make(map[string]int64)}
			dirs[dir] = d
		}
		d.files++
		d.fileTouches += fe.commits
		d.churn += fe.additions + fe.deletions
		for email, lines := range fe.devLines {
			d.devs[email] += lines
		}
	}

	var result []DirStat
	for dir, d := range dirs {
		// Bus factor: devs covering 80% of lines
		type dl struct {
			lines int64
		}
		var totalLines int64
		devSlice := make([]dl, 0, len(d.devs))
		for _, lines := range d.devs {
			devSlice = append(devSlice, dl{lines})
			totalLines += lines
		}
		sort.Slice(devSlice, func(i, j int) bool { return devSlice[i].lines > devSlice[j].lines })
		bf := 0
		var cum int64
		threshold := float64(totalLines) * Pct80Threshold
		for _, dv := range devSlice {
			cum += dv.lines
			bf++
			if float64(cum) >= threshold {
				break
			}
		}
		if bf == 0 {
			bf = len(d.devs)
		}

		result = append(result, DirStat{
			Dir:         dir,
			FileTouches: d.fileTouches,
			Churn:       d.churn,
			Files:       d.files,
			UniqueDevs:  len(d.devs),
			BusFactor:   bf,
		})
	}

	// Deterministic ordering under ties: file touches desc, then dir asc.
	sort.Slice(result, func(i, j int) bool {
		if result[i].FileTouches != result[j].FileTouches {
			return result[i].FileTouches > result[j].FileTouches
		}
		return result[i].Dir < result[j].Dir
	})
	if n > 0 && n < len(result) {
		result = result[:n]
	}
	return result
}

func ActivityOverTime(ds *Dataset, granularity string) []ActivityBucket {
	buckets := make(map[string]*ActivityBucket)
	var order []string

	for _, cm := range ds.commits {
		if cm.date.IsZero() {
			continue
		}

		// Bucket in UTC so the same commit can't fall into different periods
		// depending on the author's local timezone.
		d := cm.date.UTC()
		var key string
		switch granularity {
		case "day":
			key = d.Format("2006-01-02")
		case "week":
			y, w := d.ISOWeek()
			key = fmt.Sprintf("%04d-W%02d", y, w)
		case "year":
			key = d.Format("2006")
		default:
			key = d.Format("2006-01")
		}

		b, ok := buckets[key]
		if !ok {
			b = &ActivityBucket{Period: key}
			buckets[key] = b
			order = append(order, key)
		}
		b.Commits++
		b.Additions += cm.add
		b.Deletions += cm.del
	}

	sort.Strings(order)
	result := make([]ActivityBucket, len(order))
	for i, key := range order {
		result[i] = *buckets[key]
	}
	return result
}

func BusFactor(ds *Dataset, n int) []BusFactorResult {
	type devLines struct {
		email string
		lines int64
	}

	var result []BusFactorResult

	for path, fe := range ds.files {
		if len(fe.devLines) == 0 {
			continue
		}

		devs := make([]devLines, 0, len(fe.devLines))
		var totalLines int64
		for email, lines := range fe.devLines {
			devs = append(devs, devLines{email: email, lines: lines})
			totalLines += lines
		}

		sort.Slice(devs, func(i, j int) bool {
			if devs[i].lines != devs[j].lines {
				return devs[i].lines > devs[j].lines
			}
			return devs[i].email < devs[j].email
		})

		threshold := float64(totalLines) * Pct80Threshold
		var cumulative int64
		busFactor := 0
		var topDevs []string
		for _, d := range devs {
			cumulative += d.lines
			busFactor++
			topDevs = append(topDevs, d.email)
			if float64(cumulative) >= threshold {
				break
			}
		}

		result = append(result, BusFactorResult{
			Path:      path,
			BusFactor: busFactor,
			TopDevs:   topDevs,
		})
	}

	// Deterministic ordering under ties: bus factor asc, then path asc.
	// Ties on bf=1 are universal in real repos; without a tiebreaker the
	// top-N varies between invocations (map iteration order is random).
	sort.Slice(result, func(i, j int) bool {
		if result[i].BusFactor != result[j].BusFactor {
			return result[i].BusFactor < result[j].BusFactor
		}
		return result[i].Path < result[j].Path
	})

	if n > 0 && n < len(result) {
		result = result[:n]
	}
	return result
}

func FileCoupling(ds *Dataset, n, minCoChanges int) []CouplingResult {
	var results []CouplingResult

	for p, count := range ds.couplingPairs {
		if count < minCoChanges {
			continue
		}

		ca := ds.couplingFileChanges[p.a]
		cb := ds.couplingFileChanges[p.b]
		denom := ca
		if cb < denom {
			denom = cb
		}

		pct := 0.0
		if denom > 0 {
			pct = float64(count) / float64(denom) * 100
		}

		results = append(results, CouplingResult{
			FileA:       p.a,
			FileB:       p.b,
			CoChanges:   count,
			CouplingPct: pct,
			ChangesA:    ca,
			ChangesB:    cb,
		})
	}

	// Co-changes desc, coupling % desc, then path-asc final tiebreak.
	sort.Slice(results, func(i, j int) bool {
		if results[i].CoChanges != results[j].CoChanges {
			return results[i].CoChanges > results[j].CoChanges
		}
		if results[i].CouplingPct != results[j].CouplingPct {
			return results[i].CouplingPct > results[j].CouplingPct
		}
		if results[i].FileA != results[j].FileA {
			return results[i].FileA < results[j].FileA
		}
		return results[i].FileB < results[j].FileB
	})

	if n > 0 && n < len(results) {
		results = results[:n]
	}
	return results
}

// churnTrend compares churn from the last 3 months (relative to latest) to
// churn from earlier months. Returns 1 when there isn't enough signal to tell.
//
// Uses string comparison on "YYYY-MM" keys so the classification is stable
// regardless of the day-of-month of the dataset's latest commit.
//
// When the dataset span is shorter than the trend window (e.g. under a tight
// --since filter), returns 1 — without this, every file appears in the
// "recent" bucket only, which would falsely return 2 (growing from nothing)
// for the entire dataset.
//
// A file with a single month of churn is still a valid signal: if that month
// falls before the cutoff, the file's activity ended in the past (declining
// to zero → 0); if it falls at or after the cutoff, the file has only recent
// activity (growing from nothing → 2). Only an entirely empty map returns
// neutral.
func churnTrend(monthChurn map[string]int64, earliest, latest time.Time) float64 {
	if len(monthChurn) == 0 || latest.IsZero() {
		return 1
	}
	cutoffKey := latest.UTC().AddDate(0, -classifyTrendWindowMonths, 0).Format("2006-01")

	// Dataset too narrow: the trend window extends before the earliest commit,
	// so nothing can fall into the "earlier" bucket. No meaningful signal.
	if !earliest.IsZero() && earliest.UTC().Format("2006-01") >= cutoffKey {
		return 1
	}

	var recent, earlier int64
	for month, v := range monthChurn {
		if month < cutoffKey {
			earlier += v
		} else {
			recent += v
		}
	}
	if earlier == 0 {
		if recent == 0 {
			return 1
		}
		return 2 // recent-only: growing from nothing
	}
	if recent == 0 {
		return 0 // earlier-only: declined to nothing (single-month-in-past case)
	}
	return float64(recent) / float64(earlier)
}

// classifyBands holds the per-dataset calibrated thresholds used by
// classifyFile. When the dataset has fewer than classifyMinSample files,
// percentile estimates would be noisy, so defaultBands() returns the
// absolute fallback constants and nil sorted slices (callers that want
// to surface a percentile rank should check via HasPercentiles()).
type classifyBands struct {
	OldAgeDays     int
	DecliningTrend float64
	sortedAges     []int     // ascending; nil in fallback mode
	sortedTrends   []float64 // ascending; nil in fallback mode
}

func defaultBands() classifyBands {
	return classifyBands{
		OldAgeDays:     classifyOldAgeDays,
		DecliningTrend: classifyDecliningTrend,
	}
}

// HasPercentiles reports whether the bands carry the population data
// needed to answer agePercentile/trendPercentile queries.
func (b classifyBands) HasPercentiles() bool {
	return b.sortedAges != nil && b.sortedTrends != nil
}

// computeBands gathers ages and trends across the dataset and returns
// the P75 age / P25 trend as calibrated thresholds. Falls back to the
// absolute constants when the sample is too small.
func computeBands(ages []int, trends []float64) classifyBands {
	if len(ages) < classifyMinSample || len(trends) < classifyMinSample {
		return defaultBands()
	}
	sortedAges := make([]int, len(ages))
	copy(sortedAges, ages)
	sort.Ints(sortedAges)
	sortedTrends := make([]float64, len(trends))
	copy(sortedTrends, trends)
	sort.Float64s(sortedTrends)

	declining := percentileFloat(sortedTrends, 25)
	if declining < adaptiveDecliningTrendFloor {
		declining = adaptiveDecliningTrendFloor
	}
	return classifyBands{
		OldAgeDays:     percentileInt(sortedAges, 75),
		DecliningTrend: declining,
		sortedAges:     sortedAges,
		sortedTrends:   sortedTrends,
	}
}

// percentileInt returns the p-th percentile of a sorted int slice using
// the nearest-rank method. Assumes len(sorted) >= 1 (callers guard via
// classifyMinSample).
func percentileInt(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * (len(sorted) - 1)) / 100
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func percentileFloat(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * (len(sorted) - 1)) / 100
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// rankInt returns the percentile rank (0-100) of v within a sorted
// slice — i.e. the percentage of entries strictly below v. Ties count
// as "at" rather than "below", so the rank of the minimum is 0 and the
// rank of a value present in the slice reports how many are strictly
// less.
func rankInt(sorted []int, v int) int {
	// binary search for first index i where sorted[i] >= v
	lo, hi := 0, len(sorted)
	for lo < hi {
		mid := (lo + hi) / 2
		if sorted[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo * 100 / len(sorted)
}

func rankFloat(sorted []float64, v float64) int {
	lo, hi := 0, len(sorted)
	for lo < hi {
		mid := (lo + hi) / 2
		if sorted[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo * 100 / len(sorted)
}

// classifyFile assigns an actionable label based on churn, ownership, age,
// and trend. Thresholds come from bands, which are either dataset-derived
// percentiles or the fallback absolute constants.
func classifyFile(recentChurn, lowChurn float64, bf, ageDays int, trend float64, bands classifyBands) string {
	if recentChurn <= lowChurn {
		return "cold"
	}
	if bf >= classifyActiveBusFactor {
		return "active" // shared, healthy
	}
	// Concentrated ownership (bf 1-2) with meaningful churn.
	if ageDays < bands.OldAgeDays {
		return "active-core" // new code, single author is expected
	}
	if trend < bands.DecliningTrend {
		return "legacy-hotspot" // old + concentrated + declining → urgent
	}
	return "silo" // old + concentrated + stable/growing → knowledge bottleneck
}

func ChurnRisk(ds *Dataset, n int) []ChurnRiskResult {
	// Compute median recentChurn as the "cold" threshold.
	churns := make([]float64, 0, len(ds.files))
	for _, fe := range ds.files {
		if fe.recentChurn > 0 {
			churns = append(churns, fe.recentChurn)
		}
	}
	sort.Float64s(churns)
	lowChurn := 0.0
	if len(churns) > 0 {
		median := churns[len(churns)/2]
		lowChurn = median * classifyColdChurnRatio
	}

	// Pass 1: gather per-file age and trend so bands can be calibrated
	// from the whole-dataset distribution before classification.
	type perFile struct {
		path    string
		fe      *fileEntry
		ageDays int
		trend   float64
	}
	items := make([]perFile, 0, len(ds.files))
	var ages []int
	var trends []float64
	for path, fe := range ds.files {
		ageDays := 0
		if !fe.firstChange.IsZero() && !ds.Latest.IsZero() {
			ageDays = int(ds.Latest.Sub(fe.firstChange).Hours() / 24)
		}
		trend := churnTrend(fe.monthChurn, ds.Earliest, ds.Latest)
		items = append(items, perFile{path: path, fe: fe, ageDays: ageDays, trend: trend})
		ages = append(ages, ageDays)
		trends = append(trends, trend)
	}

	bands := computeBands(ages, trends)

	var results []ChurnRiskResult

	for _, it := range items {
		path, fe := it.path, it.fe
		// Compute bus factor (80% threshold), same as BusFactor stat.
		type dl struct{ lines int64 }
		devs := make([]dl, 0, len(fe.devLines))
		var totalLines int64
		for _, lines := range fe.devLines {
			devs = append(devs, dl{lines})
			totalLines += lines
		}
		sort.Slice(devs, func(i, j int) bool { return devs[i].lines > devs[j].lines })

		bf := 0
		var cum int64
		threshold := float64(totalLines) * Pct80Threshold
		for _, d := range devs {
			cum += d.lines
			bf++
			if float64(cum) >= threshold {
				break
			}
		}
		if bf < 1 {
			bf = 1
		}

		risk := fe.recentChurn / float64(bf)

		lastDate, firstDate := "", ""
		if !fe.lastChange.IsZero() {
			lastDate = fe.lastChange.UTC().Format("2006-01-02")
		}
		if !fe.firstChange.IsZero() {
			firstDate = fe.firstChange.UTC().Format("2006-01-02")
		}

		label := classifyFile(fe.recentChurn, lowChurn, bf, it.ageDays, it.trend, bands)

		var agePct, trendPct *int
		if bands.HasPercentiles() {
			a := rankInt(bands.sortedAges, it.ageDays)
			tr := rankFloat(bands.sortedTrends, it.trend)
			agePct = &a
			trendPct = &tr
		}

		results = append(results, ChurnRiskResult{
			Path:            path,
			RecentChurn:     math.Round(fe.recentChurn*10) / 10,
			BusFactor:       bf,
			RiskScore:       math.Round(risk*10) / 10,
			TotalChanges:    fe.commits,
			LastChangeDate:  lastDate,
			FirstChangeDate: firstDate,
			AgeDays:         it.ageDays,
			Trend:           math.Round(it.trend*100) / 100,
			Label:           label,
			AgePercentile:   agePct,
			TrendPercentile: trendPct,
		})
	}

	// Primary sort: recent churn descending (attention = where the activity is).
	// Tiebreak: lower bus factor first (more concentrated = more exposed).
	// Final tiebreak on path asc for determinism when integer bus_factor ties.
	sort.Slice(results, func(i, j int) bool {
		if results[i].RecentChurn != results[j].RecentChurn {
			return results[i].RecentChurn > results[j].RecentChurn
		}
		if results[i].BusFactor != results[j].BusFactor {
			return results[i].BusFactor < results[j].BusFactor
		}
		return results[i].Path < results[j].Path
	})

	if n > 0 && n < len(results) {
		results = results[:n]
	}
	return results
}

func WorkingPatterns(ds *Dataset) []WorkingPattern {
	days := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	var results []WorkingPattern

	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			if ds.workGrid[d][h] > 0 {
				results = append(results, WorkingPattern{
					Hour:    h,
					Day:     days[d],
					Commits: ds.workGrid[d][h],
				})
			}
		}
	}
	return results
}

// --- Top Commits ---

type BigCommit struct {
	SHA          string
	AuthorName   string
	AuthorEmail  string
	Date         string
	Message      string
	Additions    int64
	Deletions    int64
	LinesChanged int64
	FilesChanged int
}

func TopCommits(ds *Dataset, n int) []BigCommit {
	result := make([]BigCommit, 0, len(ds.commits))
	for sha, cm := range ds.commits {
		msg := cm.message
		if len(msg) > 80 {
			msg = msg[:77] + "..."
		}
		authorName := cm.email
		if cs, ok := ds.contributors[cm.email]; ok {
			authorName = cs.Name
		}
		result = append(result, BigCommit{
			SHA:          sha,
			AuthorName:   authorName,
			AuthorEmail:  cm.email,
			Date:         cm.date.UTC().Format("2006-01-02"),
			Message:      msg,
			Additions:    cm.add,
			Deletions:    cm.del,
			LinesChanged: cm.add + cm.del,
			FilesChanged: cm.files,
		})
	}

	// Deterministic ordering under ties: lines desc, then SHA asc.
	sort.Slice(result, func(i, j int) bool {
		if result[i].LinesChanged != result[j].LinesChanged {
			return result[i].LinesChanged > result[j].LinesChanged
		}
		return result[i].SHA < result[j].SHA
	})

	if n > 0 && n < len(result) {
		result = result[:n]
	}
	return result
}

// --- Dev Profile ---

type DevProfile struct {
	Name            string
	Email           string
	Commits         int
	Additions       int64
	Deletions       int64
	LinesChanged    int64
	FilesTouched    int
	ActiveDays      int
	FirstDate       string
	LastDate        string
	TopFiles        []DevFileContrib
	Scope           []DirScope
	Specialization  float64 // Gini over dir file-count distribution: 0 = broad generalist, 1 = single-dir specialist
	ContribRatio    float64 // del/add — 0=growth, ~1=rewrite, >1=cleanup
	ContribType     string  // "growth", "balanced", "refactor"
	Pace            float64 // commits per active day
	Collaborators   []DevCollaborator
	MonthlyActivity []ActivityBucket
	WorkGrid        [7][24]int
	WeekendPct      float64
}

type DirScope struct {
	Dir     string
	Files   int
	Pct     float64
}

type DevCollaborator struct {
	Email       string
	SharedFiles int   // files where both devs contributed at least one line
	SharedLines int64 // Σ min(linesA, linesB) across shared files — mirrors DeveloperNetwork
}

type DevFileContrib struct {
	Path    string
	Commits int
	Churn   int64
}

// DevProfiles returns a profile for each developer (or a specific one if filterEmail is set).
func DevProfiles(ds *Dataset, filterEmail string) []DevProfile {
	// Per-dev file contributions: count commits per file from devLines
	type fileAcc struct {
		commits int
		churn   int64
	}
	devFiles := make(map[string]map[string]*fileAcc)
	for path, fe := range ds.files {
		for email, lines := range fe.devLines {
			if filterEmail != "" && email != filterEmail {
				continue
			}
			if devFiles[email] == nil {
				devFiles[email] = make(map[string]*fileAcc)
			}
			devFiles[email][path] = &fileAcc{commits: fe.devCommits[email], churn: lines}
		}
	}

	// Per-dev collaborator pairs pre-computed in one pass over ds.files.
	// Previously computed inside the per-dev loop below as O(D × F × D_avg),
	// which is O(1.2e9) on kubernetes. Pre-computing is O(F × D_per_file²)
	// = O(7e5), roughly three orders of magnitude faster.
	//
	// Trade-off: peak memory holds one acc entry per ordered dev-pair that
	// shares at least one file (~50 MB on kubernetes, ~150 MB on linux).
	// Acceptable for modern machines; if it matters, a map[devPair]*collabAcc
	// keyed by sorted pair would halve memory at the cost of a read-time
	// sort step.
	type collabAcc struct {
		files int
		lines int64
	}
	allCollabs := make(map[string]map[string]*collabAcc)
	for _, fe := range ds.files {
		if len(fe.devLines) < 2 {
			continue
		}
		devs := make([]string, 0, len(fe.devLines))
		for email := range fe.devLines {
			devs = append(devs, email)
		}
		// Undirected pair accumulation: write to both a→b and b→a so each
		// dev's map contains everyone they share the file with.
		for i := 0; i < len(devs); i++ {
			a := devs[i]
			la := fe.devLines[a]
			for j := i + 1; j < len(devs); j++ {
				b := devs[j]
				lb := fe.devLines[b]
				overlap := la
				if lb < overlap {
					overlap = lb
				}
				if allCollabs[a] == nil {
					allCollabs[a] = make(map[string]*collabAcc)
				}
				accA := allCollabs[a][b]
				if accA == nil {
					accA = &collabAcc{}
					allCollabs[a][b] = accA
				}
				accA.files++
				accA.lines += overlap
				if allCollabs[b] == nil {
					allCollabs[b] = make(map[string]*collabAcc)
				}
				accB := allCollabs[b][a]
				if accB == nil {
					accB = &collabAcc{}
					allCollabs[b][a] = accB
				}
				accB.files++
				accB.lines += overlap
			}
		}
	}

	// Per-dev work grid + monthly activity
	devGrid := make(map[string]*[7][24]int)
	devMonthly := make(map[string]map[string]*ActivityBucket)
	dayIdx := [7]int{6, 0, 1, 2, 3, 4, 5} // Sunday=6, Monday=0, ...

	for _, cm := range ds.commits {
		if filterEmail != "" && cm.email != filterEmail {
			continue
		}
		if cm.date.IsZero() {
			continue
		}

		if devGrid[cm.email] == nil {
			devGrid[cm.email] = &[7][24]int{}
		}
		// devGrid uses local TZ on purpose — it describes the author's
		// work rhythm (when *they* were typing), not UTC instants.
		di := dayIdx[cm.date.Weekday()]
		devGrid[cm.email][di][cm.date.Hour()]++

		// Monthly bucket uses UTC for stable grouping.
		month := cm.date.UTC().Format("2006-01")
		if devMonthly[cm.email] == nil {
			devMonthly[cm.email] = make(map[string]*ActivityBucket)
		}
		b, ok := devMonthly[cm.email][month]
		if !ok {
			b = &ActivityBucket{Period: month}
			devMonthly[cm.email][month] = b
		}
		b.Commits++
		b.Additions += cm.add
		b.Deletions += cm.del
	}

	var profiles []DevProfile
	for email, cs := range ds.contributors {
		if filterEmail != "" && email != filterEmail {
			continue
		}

		var topFiles []DevFileContrib
		if files, ok := devFiles[email]; ok {
			for path, fa := range files {
				topFiles = append(topFiles, DevFileContrib{Path: path, Commits: fa.commits, Churn: fa.churn})
			}
			// Deterministic: churn desc, then path asc. Without the path
			// tiebreaker, devs with several files tied on churn would get
			// different top-N across runs.
			sort.Slice(topFiles, func(i, j int) bool {
				if topFiles[i].Churn != topFiles[j].Churn {
					return topFiles[i].Churn > topFiles[j].Churn
				}
				return topFiles[i].Path < topFiles[j].Path
			})
			if len(topFiles) > 10 {
				topFiles = topFiles[:10]
			}
		}

		var monthly []ActivityBucket
		if months, ok := devMonthly[email]; ok {
			var order []string
			for k := range months {
				order = append(order, k)
			}
			sort.Strings(order)
			for _, k := range order {
				monthly = append(monthly, *months[k])
			}
		}

		var grid [7][24]int
		var total, weekend int
		if g, ok := devGrid[email]; ok {
			grid = *g
			for d := 0; d < 7; d++ {
				for h := 0; h < 24; h++ {
					total += grid[d][h]
					if d == 5 || d == 6 {
						weekend += grid[d][h]
					}
				}
			}
		}
		wpct := 0.0
		if total > 0 {
			wpct = math.Round(float64(weekend)/float64(total)*1000) / 10
		}

		// Scope: top directories by file count. Root-level files (no "/"
		// in path) collapse into "." so they form a single bucket instead
		// of each filename becoming its own pseudo-directory. Matches the
		// convention in DirectoryStats and keeps Specialization honest —
		// otherwise a dev who only touches README, Makefile, go.mod, etc.
		// appears as a broad generalist across N pseudo-dirs instead of
		// a narrow specialist on the repo root.
		dirCount := make(map[string]int)
		if files, ok := devFiles[email]; ok {
			for path := range files {
				dir := "."
				if idx := strings.LastIndex(path, "/"); idx >= 0 {
					dir = path[:idx]
				}
				dirCount[dir]++
			}
		}
		var scope []DirScope
		for dir, count := range dirCount {
			pct := 0.0
			if cs.FilesTouched > 0 {
				pct = math.Round(float64(count)/float64(cs.FilesTouched)*1000) / 10
			}
			scope = append(scope, DirScope{Dir: dir, Files: count, Pct: pct})
		}
		// Deterministic: file count desc, then dir asc.
		sort.Slice(scope, func(i, j int) bool {
			if scope[i].Files != scope[j].Files {
				return scope[i].Files > scope[j].Files
			}
			return scope[i].Dir < scope[j].Dir
		})
		// Specialization index: Herfindahl over the FULL per-directory
		// file-count distribution (before truncation to top 5). 1.0 = all
		// files in one directory (narrow specialist); ~0 = spread across
		// many dirs (broad generalist). See herfindahl() for why this
		// captures concentration rather than inequality.
		specValues := make([]int, 0, len(dirCount))
		for _, count := range dirCount {
			specValues = append(specValues, count)
		}
		specialization := herfindahl(specValues)
		if len(scope) > 5 {
			scope = scope[:5]
		}

		// Contribution type
		contribRatio := 0.0
		contribType := "growth"
		if cs.Additions > 0 {
			contribRatio = math.Round(float64(cs.Deletions)/float64(cs.Additions)*100) / 100
		}
		if contribRatio >= contribRefactorRatio {
			contribType = "refactor"
		} else if contribRatio >= contribBalancedRatio {
			contribType = "balanced"
		}

		// Pace
		pace := 0.0
		if cs.ActiveDays > 0 {
			pace = math.Round(float64(cs.Commits)/float64(cs.ActiveDays)*10) / 10
		}

		// Collaborators: looked up from the pre-computed allCollabs map
		// (built once before this loop). SharedLines uses the min-per-file
		// overlap (same semantics as DeveloperNetwork).
		var collabs []DevCollaborator
		if m, ok := allCollabs[email]; ok {
			for e, acc := range m {
				collabs = append(collabs, DevCollaborator{Email: e, SharedFiles: acc.files, SharedLines: acc.lines})
			}
		}
		// Deterministic: shared-lines desc, shared-files desc, email asc.
		sort.Slice(collabs, func(i, j int) bool {
			if collabs[i].SharedLines != collabs[j].SharedLines {
				return collabs[i].SharedLines > collabs[j].SharedLines
			}
			if collabs[i].SharedFiles != collabs[j].SharedFiles {
				return collabs[i].SharedFiles > collabs[j].SharedFiles
			}
			return collabs[i].Email < collabs[j].Email
		})
		if len(collabs) > 5 {
			collabs = collabs[:5]
		}

		profiles = append(profiles, DevProfile{
			Name: cs.Name, Email: cs.Email,
			Commits: cs.Commits, Additions: cs.Additions, Deletions: cs.Deletions,
			LinesChanged: cs.Additions + cs.Deletions, FilesTouched: cs.FilesTouched,
			ActiveDays: cs.ActiveDays, FirstDate: cs.FirstDate, LastDate: cs.LastDate,
			TopFiles: topFiles, Scope: scope, Specialization: specialization,
			ContribRatio: contribRatio, ContribType: contribType,
			Pace: pace, Collaborators: collabs,
			MonthlyActivity: monthly, WorkGrid: grid, WeekendPct: wpct,
		})
	}

	// Deterministic ordering under ties: commits desc, then email asc.
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Commits != profiles[j].Commits {
			return profiles[i].Commits > profiles[j].Commits
		}
		return profiles[i].Email < profiles[j].Email
	})

	return profiles
}

func DeveloperNetwork(ds *Dataset, n, minSharedFiles int) []DevEdge {
	type devPair struct{ a, b string }
	type pairAcc struct {
		files       int
		sharedLines int64
	}
	pairs := make(map[devPair]*pairAcc)
	devFileCount := make(map[string]int)

	for _, fe := range ds.files {
		devs := make([]string, 0, len(fe.devLines))
		for email := range fe.devLines {
			devs = append(devs, email)
		}
		for _, d := range devs {
			devFileCount[d]++
		}
		for i := 0; i < len(devs); i++ {
			for j := i + 1; j < len(devs); j++ {
				a, b := devs[i], devs[j]
				if a > b {
					a, b = b, a
				}
				acc, ok := pairs[devPair{a, b}]
				if !ok {
					acc = &pairAcc{}
					pairs[devPair{a, b}] = acc
				}
				acc.files++
				// Real overlap signal: min of each dev's line contribution to
				// the file. If Alice edited 1 line of README and Bob edited
				// 200, they share 1 line of real collaboration, not 200.
				la, lb := fe.devLines[a], fe.devLines[b]
				if la < lb {
					acc.sharedLines += la
				} else {
					acc.sharedLines += lb
				}
			}
		}
	}

	var results []DevEdge
	for p, acc := range pairs {
		if acc.files < minSharedFiles {
			continue
		}
		maxFiles := devFileCount[p.a]
		if devFileCount[p.b] > maxFiles {
			maxFiles = devFileCount[p.b]
		}
		weight := 0.0
		if maxFiles > 0 {
			weight = float64(acc.files) / float64(maxFiles) * 100
		}
		results = append(results, DevEdge{
			DevA:        p.a,
			DevB:        p.b,
			SharedFiles: acc.files,
			SharedLines: acc.sharedLines,
			Weight:      math.Round(weight*10) / 10,
		})
	}

	// Shared-lines desc, shared-files desc, then dev-pair-asc final tiebreak.
	sort.Slice(results, func(i, j int) bool {
		if results[i].SharedLines != results[j].SharedLines {
			return results[i].SharedLines > results[j].SharedLines
		}
		if results[i].SharedFiles != results[j].SharedFiles {
			return results[i].SharedFiles > results[j].SharedFiles
		}
		if results[i].DevA != results[j].DevA {
			return results[i].DevA < results[j].DevA
		}
		return results[i].DevB < results[j].DevB
	})

	if n > 0 && n < len(results) {
		results = results[:n]
	}
	return results
}
