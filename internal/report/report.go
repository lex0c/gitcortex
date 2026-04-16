package report

import (
	"fmt"
	"html/template"
	"io"

	"gitcortex/internal/stats"
)

type ReportData struct {
	RepoName     string
	Summary      stats.Summary
	Contributors []stats.ContributorStat
	Hotspots     []stats.FileStat
	Activity     []EnrichedActivity
	BusFactor    []stats.BusFactorResult
	Coupling     []stats.CouplingResult
	ChurnRisk    []stats.ChurnRiskResult
	Patterns     []stats.WorkingPattern
	TopCommits   []stats.BigCommit
	DevNetwork   []stats.DevEdge
	Profiles     []stats.DevProfile
	PatternGrid    [7][24]int
	MaxPattern     int
	MaxActivityLines int64
}

type EnrichedActivity struct {
	Period    string
	Commits   int
	Additions int64
	Deletions int64
	Ratio     string // del/add ratio formatted
	RatioClass string // "growth", "rewrite", "cleanup"
	Trend     string // "↑", "↓", "→"
	MovingAvg int    // 3-period moving average of commits
}

func enrichActivity(raw []stats.ActivityBucket) ([]EnrichedActivity, int64) {
	result := make([]EnrichedActivity, len(raw))
	var maxLines int64

	for i, a := range raw {
		total := a.Additions + a.Deletions
		if total > maxLines {
			maxLines = total
		}

		// Ratio
		ratio := 0.0
		ratioStr := "—"
		ratioClass := "growth"
		if a.Additions > 0 {
			ratio = float64(a.Deletions) / float64(a.Additions)
			ratioStr = fmt.Sprintf("%.2f", ratio)
		}
		if ratio >= 1.0 {
			ratioClass = "cleanup"
		} else if ratio >= 0.5 {
			ratioClass = "rewrite"
		}

		// Trend vs previous period
		trend := "→"
		if i > 0 {
			prev := raw[i-1].Commits
			if a.Commits > prev+prev/5 {
				trend = "↑"
			} else if a.Commits < prev-prev/5 {
				trend = "↓"
			}
		}

		// 3-period moving average
		ma := a.Commits
		count := 1
		if i >= 1 {
			ma += raw[i-1].Commits
			count++
		}
		if i >= 2 {
			ma += raw[i-2].Commits
			count++
		}
		ma = ma / count

		result[i] = EnrichedActivity{
			Period:     a.Period,
			Commits:    a.Commits,
			Additions:  a.Additions,
			Deletions:  a.Deletions,
			Ratio:      ratioStr,
			RatioClass: ratioClass,
			Trend:      trend,
			MovingAvg:  ma,
		}
	}

	return result, maxLines
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

	activityEnriched, maxActLines := enrichActivity(stats.ActivityOverTime(ds, "month"))

	data := ReportData{
		RepoName:     repoName,
		Summary:      stats.ComputeSummary(ds),
		Contributors: stats.TopContributors(ds, topN),
		Hotspots:     stats.FileHotspots(ds, topN),
		Activity:     activityEnriched,
		BusFactor:    stats.BusFactor(ds, topN),
		Coupling:     stats.FileCoupling(ds, topN, sf.CouplingMinChanges),
		ChurnRisk:    stats.ChurnRisk(ds, topN),
		Patterns:     patterns,
		TopCommits:   stats.TopCommits(ds, topN),
		DevNetwork:   stats.DeveloperNetwork(ds, topN, sf.NetworkMinFiles),
		Profiles:     stats.DevProfiles(ds, ""),
		PatternGrid:      grid,
		MaxPattern:       maxP,
		MaxActivityLines: maxActLines,
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

func shortPath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen+3:]
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

func plus(a, b int64) int64 {
	return a + b
}

var funcMap = template.FuncMap{
	"pct":       pct,
	"pctInt":    pctInt,
	"heatColor": heatColor,
	"shortPath": shortPath,
	"joinDevs":  stats.JoinDevs,
	"seq":       seq,
	"list":      list,
	"int64":     toInt64,
	"plus":      plus,
}

var tmpl = template.Must(template.New("report").Funcs(funcMap).Parse(reportHTML))
