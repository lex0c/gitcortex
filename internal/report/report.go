package report

import (
	"fmt"
	"html/template"
	"io"
	"strings"

	"gitcortex/internal/stats"
)

type ReportData struct {
	Summary      stats.Summary
	Contributors []stats.ContributorStat
	Ranking      []stats.RankedContributor
	Hotspots     []stats.FileStat
	Activity     []stats.ActivityBucket
	BusFactor    []stats.BusFactorResult
	Coupling     []stats.CouplingResult
	ChurnRisk    []stats.ChurnRiskResult
	Patterns     []stats.WorkingPattern
	TopCommits   []stats.BigCommit
	DevNetwork   []stats.DevEdge
	Profiles     []stats.DevProfile
	PatternGrid  [7][24]int
	MaxPattern   int
}

func Generate(w io.Writer, ds *stats.Dataset, topN int, sf stats.StatsFlags) error {
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

	data := ReportData{
		Summary:      stats.ComputeSummary(ds),
		Contributors: stats.TopContributors(ds, topN),
		Ranking:      stats.ContributorRanking(ds, topN),
		Hotspots:     stats.FileHotspots(ds, topN),
		Activity:     stats.ActivityOverTime(ds, "month"),
		BusFactor:    stats.BusFactor(ds, topN),
		Coupling:     stats.FileCoupling(ds, topN, sf.CouplingMaxFiles, sf.CouplingMinChanges),
		ChurnRisk:    stats.ChurnRisk(ds, topN, sf.ChurnHalfLife),
		Patterns:     patterns,
		TopCommits:   stats.TopCommits(ds, topN),
		DevNetwork:   stats.DeveloperNetwork(ds, topN, sf.NetworkMinFiles),
		Profiles:     stats.DevProfiles(ds, ""),
		PatternGrid:  grid,
		MaxPattern:   maxP,
	}

	return tmpl.Execute(w, data)
}

func pct(val, max int64) float64 {
	if max == 0 {
		return 0
	}
	return float64(val) / float64(max) * 100
}

func pctInt(val, max int) float64 {
	if max == 0 {
		return 0
	}
	return float64(val) / float64(max) * 100
}

func heatColor(val, max int) string {
	if max == 0 || val == 0 {
		return "#f0f0f0"
	}
	intensity := float64(val) / float64(max)
	g := int(255 * (1 - intensity*0.8))
	return fmt.Sprintf("#%02x%02x%02x", 50, g, 80)
}

func shortPath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen+3:]
}

func joinDevs(devs []string) string {
	if len(devs) <= 3 {
		return strings.Join(devs, ", ")
	}
	return strings.Join(devs[:3], ", ") + fmt.Sprintf(" +%d", len(devs)-3)
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

var funcMap = template.FuncMap{
	"pct":       pct,
	"pctInt":    pctInt,
	"heatColor": heatColor,
	"shortPath": shortPath,
	"joinDevs":  joinDevs,
	"seq":       seq,
	"list":      list,
	"int64":     toInt64,
}

var tmpl = template.Must(template.New("report").Funcs(funcMap).Parse(reportHTML))
