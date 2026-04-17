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
	classifyOldAgeDays        = 180  // age ≥ 180d is "old"
	classifyDecliningTrend    = 0.5  // trend < 0.5 = declining
	classifyTrendWindowMonths = 3    // recent vs earlier split

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
		threshold := float64(totalLines) * 0.8
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
			return devs[i].lines > devs[j].lines
		})

		threshold := float64(totalLines) * 0.8
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

	sort.Slice(results, func(i, j int) bool {
		if results[i].CoChanges != results[j].CoChanges {
			return results[i].CoChanges > results[j].CoChanges
		}
		return results[i].CouplingPct > results[j].CouplingPct
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
func churnTrend(monthChurn map[string]int64, latest time.Time) float64 {
	if len(monthChurn) < 2 || latest.IsZero() {
		return 1
	}
	cutoffKey := latest.UTC().AddDate(0, -classifyTrendWindowMonths, 0).Format("2006-01")
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
		return 2 // growing from nothing
	}
	return float64(recent) / float64(earlier)
}

// classifyFile assigns an actionable label based on churn, ownership, age,
// and trend. Thresholds are package constants (classify*).
func classifyFile(recentChurn, lowChurn float64, bf, ageDays int, trend float64) string {
	if recentChurn <= lowChurn {
		return "cold"
	}
	if bf >= classifyActiveBusFactor {
		return "active" // shared, healthy
	}
	// Concentrated ownership (bf 1-2) with meaningful churn.
	if ageDays < classifyOldAgeDays {
		return "active-core" // new code, single author is expected
	}
	if trend < classifyDecliningTrend {
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

	var results []ChurnRiskResult

	for path, fe := range ds.files {
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
		threshold := float64(totalLines) * 0.8
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

		ageDays := 0
		if !fe.firstChange.IsZero() && !ds.Latest.IsZero() {
			ageDays = int(ds.Latest.Sub(fe.firstChange).Hours() / 24)
		}

		trend := churnTrend(fe.monthChurn, ds.Latest)
		label := classifyFile(fe.recentChurn, lowChurn, bf, ageDays, trend)

		results = append(results, ChurnRiskResult{
			Path:            path,
			RecentChurn:     math.Round(fe.recentChurn*10) / 10,
			BusFactor:       bf,
			RiskScore:       math.Round(risk*10) / 10,
			TotalChanges:    fe.commits,
			LastChangeDate:  lastDate,
			FirstChangeDate: firstDate,
			AgeDays:         ageDays,
			Trend:           math.Round(trend*100) / 100,
			Label:           label,
		})
	}

	// Primary sort: recent churn descending (attention = where the activity is).
	// Tiebreak: lower bus factor first (more concentrated = more exposed).
	sort.Slice(results, func(i, j int) bool {
		if results[i].RecentChurn != results[j].RecentChurn {
			return results[i].RecentChurn > results[j].RecentChurn
		}
		return results[i].BusFactor < results[j].BusFactor
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
	SharedFiles int
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
			sort.Slice(topFiles, func(i, j int) bool {
				return topFiles[i].Churn > topFiles[j].Churn
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

		// Scope: top directories by file count
		dirCount := make(map[string]int)
		if files, ok := devFiles[email]; ok {
			for path := range files {
				dir := path
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
		sort.Slice(scope, func(i, j int) bool { return scope[i].Files > scope[j].Files })
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

		// Collaborators: devs sharing files with this dev
		collabMap := make(map[string]int)
		for _, fe := range ds.files {
			if _, ok := fe.devLines[email]; !ok {
				continue
			}
			for otherEmail := range fe.devLines {
				if otherEmail != email {
					collabMap[otherEmail]++
				}
			}
		}
		var collabs []DevCollaborator
		for e, count := range collabMap {
			collabs = append(collabs, DevCollaborator{Email: e, SharedFiles: count})
		}
		sort.Slice(collabs, func(i, j int) bool { return collabs[i].SharedFiles > collabs[j].SharedFiles })
		if len(collabs) > 5 {
			collabs = collabs[:5]
		}

		profiles = append(profiles, DevProfile{
			Name: cs.Name, Email: cs.Email,
			Commits: cs.Commits, Additions: cs.Additions, Deletions: cs.Deletions,
			LinesChanged: cs.Additions + cs.Deletions, FilesTouched: cs.FilesTouched,
			ActiveDays: cs.ActiveDays, FirstDate: cs.FirstDate, LastDate: cs.LastDate,
			TopFiles: topFiles, Scope: scope,
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

	sort.Slice(results, func(i, j int) bool {
		if results[i].SharedLines != results[j].SharedLines {
			return results[i].SharedLines > results[j].SharedLines
		}
		return results[i].SharedFiles > results[j].SharedFiles
	})

	if n > 0 && n < len(results) {
		results = results[:n]
	}
	return results
}
