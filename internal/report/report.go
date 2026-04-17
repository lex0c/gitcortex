package report

import (
	"fmt"
	"html/template"
	"io"
	"sort"

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
	PatternGrid    [7][24]int
	MaxPattern     int
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

	data := ReportData{
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
