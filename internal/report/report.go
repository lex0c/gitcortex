package report

import (
	"fmt"
	"html/template"
	"io"
	"math"
	"sort"
	"time"

	"github.com/lex0c/gitcortex/internal/stats"
)

type ReportData struct {
	RepoName     string
	Summary      stats.Summary
	Contributors []stats.ContributorStat
	Hotspots     []stats.FileStat
	Directories  []stats.DirStat
	ActivityRaw    []stats.ActivityBucket
	ActivityYears  []string
	ActivityGrid   [][]ActivityCell // [year][month 0-11]
	MaxActivityCommits int
	BusFactor    []stats.BusFactorResult
	Coupling     []stats.CouplingResult
	ChurnRisk    []stats.ChurnRiskResult
	Patterns     []stats.WorkingPattern
	TopCommits   []stats.BigCommit
	DevNetwork   []stats.DevEdge
	Profiles     []stats.DevProfile
	GeneratedAt    string
	Pareto         ParetoData
	PatternGrid    [7][24]int
	MaxPattern     int
}

type ParetoData struct {
	FilesPct80Churn  float64 // % of files that account for 80% of churn
	DevsPct80Commits float64 // % of devs that account for 80% of commits
	DevsPct80Churn   float64 // % of devs that account for 80% of churn (see METRICS.md — complements commits)
	DirsPct80Churn   float64 // % of dirs that account for 80% of churn
	TopChurnFiles    int
	TotalFiles       int
	TopCommitDevs    int
	TopChurnDevs     int
	TotalDevs        int
	TopChurnDirs     int
	TotalDirs        int
}

func ComputePareto(ds *stats.Dataset) ParetoData {
	p := ParetoData{}

	// Files: % of files for 80% of churn (FileHotspots returns sorted by commits, re-sort by churn)
	hotspots := stats.FileHotspots(ds, 0)
	sort.Slice(hotspots, func(i, j int) bool { return hotspots[i].Churn > hotspots[j].Churn })
	var totalChurn int64
	for _, h := range hotspots {
		totalChurn += h.Churn
	}
	p.TotalFiles = len(hotspots)
	// Guard: when totalChurn is zero (merges-only dataset, or all empty
	// commits), skip the loop entirely. Without this, the first iteration
	// trips on `cum >= 0` and leaves TopChurnFiles = 1 for empty signal.
	if totalChurn > 0 {
		threshold := float64(totalChurn) * 0.8
		var cum int64
		for _, h := range hotspots {
			cum += h.Churn
			p.TopChurnFiles++
			if float64(cum) >= threshold {
				break
			}
		}
		if p.TotalFiles > 0 {
			p.FilesPct80Churn = math.Round(float64(p.TopChurnFiles) / float64(p.TotalFiles) * 1000) / 10
		}
	}

	// Devs: two complementary lenses.
	// - 80% of commits: rewards frequent committers (bots, squash-off teams).
	// - 80% of churn:   rewards volume of lines written/removed.
	// Divergence between the two is informative (bot author vs feature author).
	contribs := stats.TopContributors(ds, 0)
	p.TotalDevs = len(contribs)

	var totalCommits int
	for _, c := range contribs {
		totalCommits += c.Commits
	}
	// Guard: when the aggregate is zero, the 80% threshold is zero and the
	// first iteration trips it, producing TopX=1 for an empty signal. Skip.
	if totalCommits > 0 {
		commitThreshold := float64(totalCommits) * 0.8
		var cumCommits int
		for _, c := range contribs {
			cumCommits += c.Commits
			p.TopCommitDevs++
			if float64(cumCommits) >= commitThreshold {
				break
			}
		}
		if p.TotalDevs > 0 {
			p.DevsPct80Commits = math.Round(float64(p.TopCommitDevs) / float64(p.TotalDevs) * 1000) / 10
		}
	}

	// Dev churn ranking: re-sort contribs by lines changed, apply same 80%
	// cumulative cutoff. Tiebreaker on email asc for determinism. The copy
	// preserves the commits-ordered `contribs` slice in case of future reuse.
	byChurn := make([]stats.ContributorStat, len(contribs))
	copy(byChurn, contribs)
	sort.Slice(byChurn, func(i, j int) bool {
		li := byChurn[i].Additions + byChurn[i].Deletions
		lj := byChurn[j].Additions + byChurn[j].Deletions
		if li != lj {
			return li > lj
		}
		return byChurn[i].Email < byChurn[j].Email
	})
	var totalDevChurn int64
	for _, c := range byChurn {
		totalDevChurn += c.Additions + c.Deletions
	}
	// Same zero-aggregate guard as above. Without it, zero-churn datasets
	// (e.g., all empty commits) would report 1 dev as the 80% owner.
	if totalDevChurn > 0 {
		devChurnThreshold := float64(totalDevChurn) * 0.8
		var cumDevChurn int64
		for _, c := range byChurn {
			cumDevChurn += c.Additions + c.Deletions
			p.TopChurnDevs++
			if float64(cumDevChurn) >= devChurnThreshold {
				break
			}
		}
		if p.TotalDevs > 0 {
			p.DevsPct80Churn = math.Round(float64(p.TopChurnDevs) / float64(p.TotalDevs) * 1000) / 10
		}
	}

	// Dirs: % of dirs for 80% of churn
	dirs := stats.DirectoryStats(ds, 0)
	var totalDirChurn int64
	for _, d := range dirs {
		totalDirChurn += d.Churn
	}
	p.TotalDirs = len(dirs)
	// Same zero-churn guard as files.
	if totalDirChurn > 0 {
		dirThreshold := float64(totalDirChurn) * 0.8
		var cumDirChurn int64
		for _, d := range dirs {
			cumDirChurn += d.Churn
			p.TopChurnDirs++
			if float64(cumDirChurn) >= dirThreshold {
				break
			}
		}
		if p.TotalDirs > 0 {
			p.DirsPct80Churn = math.Round(float64(p.TopChurnDirs) / float64(p.TotalDirs) * 1000) / 10
		}
	}

	return p
}

type ActivityCell struct {
	Commits   int
	Additions int64
	Deletions int64
	Ratio     float64
	HasData   bool
}

func buildActivityGrid(raw []stats.ActivityBucket) ([]string, [][]ActivityCell, int) {
	// Parse periods into year+month, build grid
	type key struct{ year, month int }
	cells := make(map[key]*ActivityCell)
	yearSet := make(map[int]bool)
	maxCommits := 0

	for _, a := range raw {
		if len(a.Period) < 7 {
			continue
		}
		var y, m int
		fmt.Sscanf(a.Period, "%d-%d", &y, &m)
		if y == 0 || m == 0 {
			continue
		}
		yearSet[y] = true
		ratio := 0.0
		if a.Additions > 0 {
			ratio = float64(a.Deletions) / float64(a.Additions)
		}
		cells[key{y, m - 1}] = &ActivityCell{
			Commits: a.Commits, Additions: a.Additions, Deletions: a.Deletions,
			Ratio: ratio, HasData: true,
		}
		if a.Commits > maxCommits {
			maxCommits = a.Commits
		}
	}

	// Sort years
	years := make([]int, 0, len(yearSet))
	for y := range yearSet {
		years = append(years, y)
	}
	sort.Ints(years)

	yearLabels := make([]string, len(years))
	grid := make([][]ActivityCell, len(years))
	for i, y := range years {
		yearLabels[i] = fmt.Sprintf("%d", y)
		row := make([]ActivityCell, 12)
		for m := 0; m < 12; m++ {
			if c, ok := cells[key{y, m}]; ok {
				row[m] = *c
			}
		}
		grid[i] = row
	}

	return yearLabels, grid, maxCommits
}

func Generate(w io.Writer, ds *stats.Dataset, repoName string, topN int, sf stats.StatsFlags) error {
	patterns := stats.WorkingPatterns(ds)
	var grid [7][24]int
	maxP := 0
	days := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	for _, p := range patterns {
		for d, name := range days {
			if name == p.Day {
				grid[d][p.Hour] = p.Commits
				if p.Commits > maxP {
					maxP = p.Commits
				}
			}
		}
	}

	actRaw := stats.ActivityOverTime(ds, "month")
	actYears, actGrid, maxActCommits := buildActivityGrid(actRaw)

	now := time.Now().Format("2006-01-02 15:04")

	data := ReportData{
		GeneratedAt:        now,
		RepoName:           repoName,
		Summary:            stats.ComputeSummary(ds),
		Contributors:       stats.TopContributors(ds, topN),
		Hotspots:           stats.FileHotspots(ds, topN),
		Directories:        stats.DirectoryStats(ds, topN),
		ActivityRaw:        actRaw,
		ActivityYears:      actYears,
		ActivityGrid:       actGrid,
		MaxActivityCommits: maxActCommits,
		BusFactor:          stats.BusFactor(ds, topN),
		Coupling:           stats.FileCoupling(ds, topN, sf.CouplingMinChanges),
		ChurnRisk:          stats.ChurnRisk(ds, topN),
		Patterns:           patterns,
		TopCommits:         stats.TopCommits(ds, topN),
		DevNetwork:         stats.DeveloperNetwork(ds, topN, sf.NetworkMinFiles),
		Profiles:           stats.DevProfiles(ds, ""),
		Pareto:             ComputePareto(ds),
		PatternGrid:        grid,
		MaxPattern:         maxP,
	}

	return tmpl.Execute(w, data)
}

func pct(val, max int64) string {
	if max == 0 {
		return "0"
	}
	return fmt.Sprintf("%.1f", float64(val)/float64(max)*100)
}

func pctInt(val, max int) string {
	if max == 0 {
		return "0"
	}
	return fmt.Sprintf("%.1f", float64(val)/float64(max)*100)
}

func heatColor(val, max int) string {
	if max == 0 || val == 0 {
		return "#f0f0f0"
	}
	intensity := float64(val) / float64(max)
	g := int(255 * (1 - intensity*0.8))
	return fmt.Sprintf("#%02x%02x%02x", 50, g, 80)
}

func seq(start, end int) []int {
	s := make([]int, end-start+1)
	for i := range s {
		s[i] = start + i
	}
	return s
}

func list(items ...string) []string {
	return items
}

func toInt64(v float64) int64 {
	return int64(v)
}

func plusInt(a, b int) int {
	return a + b
}

func pctRatio(del, add int64) float64 {
	if add == 0 {
		return 0
	}
	return float64(del) / float64(add)
}

func actColor(commits, max int) string {
	if max == 0 || commits == 0 {
		return "#ebedf0"
	}
	intensity := float64(commits) / float64(max)
	if intensity > 1 {
		intensity = 1
	}
	// GitHub-style green gradient
	if intensity < 0.25 {
		return "#9be9a8"
	} else if intensity < 0.5 {
		return "#40c463"
	} else if intensity < 0.75 {
		return "#30a14e"
	}
	return "#216e39"
}


var funcMap = template.FuncMap{
	"pct":       pct,
	"pctInt":    pctInt,
	"heatColor": heatColor,
	"joinDevs":  stats.JoinDevs,
	"seq":       seq,
	"list":      list,
	"int64":      toInt64,
	"actColor":   actColor,
	"pctRatio":   pctRatio,
	"plusInt":    plusInt,
}

var tmpl = template.Must(template.New("report").Funcs(funcMap).Parse(reportHTML))
var profileTmpl = template.Must(template.New("profile").Funcs(funcMap).Parse(profileHTML))

type ProfileReportData struct {
	GeneratedAt     string
	RepoName        string
	Profile         stats.DevProfile
	ActivityYears   []string
	ActivityGrid    [][]ActivityCell
	MaxActivityCommits int
	PatternGrid     [7][24]int
	MaxPattern      int
}

func GenerateProfile(w io.Writer, ds *stats.Dataset, repoName, email string) error {
	profiles := stats.DevProfiles(ds, email)
	if len(profiles) == 0 {
		return fmt.Errorf("developer %s not found", email)
	}
	p := profiles[0]

	// Build activity grid from this dev's monthly data
	actYears, actGrid, maxAct := buildActivityGrid(p.MonthlyActivity)

	// Pattern grid
	maxP := 0
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			if p.WorkGrid[d][h] > maxP {
				maxP = p.WorkGrid[d][h]
			}
		}
	}

	data := ProfileReportData{
		GeneratedAt:        time.Now().Format("2006-01-02 15:04"),
		RepoName:           repoName,
		Profile:            p,
		ActivityYears:      actYears,
		ActivityGrid:       actGrid,
		MaxActivityCommits: maxAct,
		PatternGrid:        p.WorkGrid,
		MaxPattern:         maxP,
	}

	return profileTmpl.Execute(w, data)
}
