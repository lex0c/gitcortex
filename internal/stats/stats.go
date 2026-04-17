package stats

import (
	"fmt"
	"math"
	"sort"
	"strings"
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
	RiskScore      float64
	TotalChanges   int
	LastChangeDate string
}

type WorkingPattern struct {
	Hour    int
	Day     string
	Commits int
}

type DevEdge struct {
	DevA        string
	DevB        string
	SharedFiles int
	Weight      float64
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
		s.FirstCommitDate = ds.Earliest.Format("2006-01-02")
	}
	if !ds.Latest.IsZero() {
		s.LastCommitDate = ds.Latest.Format("2006-01-02")
	}

	return s
}

func TopContributors(ds *Dataset, n int) []ContributorStat {
	result := make([]ContributorStat, 0, len(ds.contributors))
	for _, cs := range ds.contributors {
		result = append(result, *cs)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Commits > result[j].Commits
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

	sort.Slice(result, func(i, j int) bool {
		return result[i].Commits > result[j].Commits
	})

	if n > 0 && n < len(result) {
		result = result[:n]
	}
	return result
}

type DirStat struct {
	Dir        string
	Commits    int
	Churn      int64
	Files      int
	UniqueDevs int
	BusFactor  int
}

func DirectoryStats(ds *Dataset, n int) []DirStat {
	type dirAcc struct {
		commits int
		churn   int64
		files   int
		devs    map[string]int64
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
		d.commits += fe.commits
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
			Dir:        dir,
			Commits:    d.commits,
			Churn:      d.churn,
			Files:      d.files,
			UniqueDevs: len(d.devs),
			BusFactor:  bf,
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Commits > result[j].Commits })
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

		var key string
		switch granularity {
		case "day":
			key = cm.date.Format("2006-01-02")
		case "week":
			y, w := cm.date.ISOWeek()
			key = fmt.Sprintf("%04d-W%02d", y, w)
		case "year":
			key = cm.date.Format("2006")
		default:
			key = cm.date.Format("2006-01")
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

	sort.Slice(result, func(i, j int) bool {
		return result[i].BusFactor < result[j].BusFactor
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

func ChurnRisk(ds *Dataset, n int) []ChurnRiskResult {
	var results []ChurnRiskResult

	for path, fe := range ds.files {
		bf := len(fe.devLines)
		if bf < 1 {
			bf = 1
		}

		risk := fe.recentChurn / float64(bf)

		lastDate := ""
		if !fe.lastChange.IsZero() {
			lastDate = fe.lastChange.Format("2006-01-02")
		}

		results = append(results, ChurnRiskResult{
			Path:           path,
			RecentChurn:    math.Round(fe.recentChurn*10) / 10,
			BusFactor:      len(fe.devLines),
			RiskScore:      math.Round(risk*10) / 10,
			TotalChanges:   fe.commits,
			LastChangeDate: lastDate,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].RiskScore > results[j].RiskScore
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
			Date:         cm.date.Format("2006-01-02"),
			Message:      msg,
			Additions:    cm.add,
			Deletions:    cm.del,
			LinesChanged: cm.add + cm.del,
			FilesChanged: cm.files,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].LinesChanged > result[j].LinesChanged
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
			devFiles[email][path] = &fileAcc{commits: fe.commits, churn: lines}
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
		di := dayIdx[cm.date.Weekday()]
		devGrid[cm.email][di][cm.date.Hour()]++

		month := cm.date.Format("2006-01")
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
		if contribRatio >= 0.8 {
			contribType = "refactor"
		} else if contribRatio >= 0.4 {
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

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Commits > profiles[j].Commits
	})

	return profiles
}

func DeveloperNetwork(ds *Dataset, n, minSharedFiles int) []DevEdge {
	type devPair struct{ a, b string }
	pairFiles := make(map[devPair]int)
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
				pairFiles[devPair{a, b}]++
			}
		}
	}

	var results []DevEdge
	for p, shared := range pairFiles {
		if shared < minSharedFiles {
			continue
		}
		maxFiles := devFileCount[p.a]
		if devFileCount[p.b] > maxFiles {
			maxFiles = devFileCount[p.b]
		}
		weight := 0.0
		if maxFiles > 0 {
			weight = float64(shared) / float64(maxFiles) * 100
		}
		results = append(results, DevEdge{
			DevA:        p.a,
			DevB:        p.b,
			SharedFiles: shared,
			Weight:      math.Round(weight*10) / 10,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].SharedFiles > results[j].SharedFiles
	})

	if n > 0 && n < len(results) {
		results = results[:n]
	}
	return results
}
