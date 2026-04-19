package report

import (
	"fmt"
	"html/template"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lex0c/gitcortex/internal/stats"
)

// Pareto concentration label boundaries, in % of the universe needed to
// cover 80% of activity. Shared by the HTML report and the CLI so both
// surfaces classify and color concentration consistently.
const (
	paretoExtremelyConcentratedMax = 10.0 // ≤10% of items → extremely concentrated (🔴)
	paretoModeratelyConcentratedMax = 25.0 // ≤25% → moderately concentrated (🟡); above → well distributed (🟢)
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

	// Label distribution for the Churn Risk section — counted over the
	// full classified set so the reader can tell "top 20, all legacy-
	// hotspot" from "there are 48 legacy-hotspots in total". Populated
	// alongside ChurnRisk in Generate().
	ChurnRiskLabelCounts []LabelCount

	// Structure holds a pruned repo-structure tree rendered as a
	// collapsible architecture view. Truncated to htmlTreeDepth levels
	// so mature repos (linux-scale) don't blow up the HTML. nil when
	// the dataset has no files.
	Structure *TreeNode
}

// htmlTreeDepth caps the repo-structure tree baked into the HTML report.
// Three levels resolves top-level modules and their immediate children,
// enough to read the architecture at a glance without drowning the page
// on kernel-scale repos. CLI users can override via --tree-depth.
const htmlTreeDepth = 3

// LabelCount pairs a Churn Risk label with its total count and sort
// priority, so the template can render chips in the same label order
// used by the table below.
type LabelCount struct {
	Label    string
	Count    int
	Priority int
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

	// Precomputed human labels and emoji markers. Both CLI and HTML read
	// from these so the two surfaces never drift on thresholds or wording.
	FilesLabel        string
	FilesMarker       string
	DevsCommitsLabel  string
	DevsCommitsMarker string
	DevsChurnLabel    string
	DevsChurnMarker   string
	DirsLabel         string
	DirsMarker        string
}

// concentrationLabel classifies a Pareto percentage (items holding 80% of
// activity) into a textual band and matching emoji marker. topCount is the
// raw count used as a zero-guard: empty signals map to "no data" / ⚪
// regardless of the pct value (which would otherwise be 0 and trip ≤10).
func concentrationLabel(pct float64, topCount int) (label, marker string) {
	if topCount == 0 {
		return "no data", "⚪"
	}
	if pct <= paretoExtremelyConcentratedMax {
		return "extremely concentrated", "🔴"
	}
	if pct <= paretoModeratelyConcentratedMax {
		return "moderately concentrated", "🟡"
	}
	return "well distributed", "🟢"
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
		threshold := float64(totalChurn) * stats.Pct80Threshold
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
		commitThreshold := float64(totalCommits) * stats.Pct80Threshold
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
		devChurnThreshold := float64(totalDevChurn) * stats.Pct80Threshold
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
		dirThreshold := float64(totalDirChurn) * stats.Pct80Threshold
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

	p.FilesLabel, p.FilesMarker = concentrationLabel(p.FilesPct80Churn, p.TopChurnFiles)
	p.DevsCommitsLabel, p.DevsCommitsMarker = concentrationLabel(p.DevsPct80Commits, p.TopCommitDevs)
	if p.TopCommitDevs > 0 && p.DevsPct80Commits <= paretoExtremelyConcentratedMax {
		p.DevsCommitsLabel += ", key-person dependence"
	}
	p.DevsChurnLabel, p.DevsChurnMarker = concentrationLabel(p.DevsPct80Churn, p.TopChurnDevs)
	p.DirsLabel, p.DirsMarker = concentrationLabel(p.DirsPct80Churn, p.TopChurnDirs)

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

	// Compute label distribution for the Churn Risk chip strip without
	// materializing a full result slice. The display table still takes
	// the truncated ChurnRisk(ds, topN) call below — only the chip
	// counts needed the whole-dataset view, and we can get those from
	// a dedicated counter that never builds per-file structs.
	labelCountsMap := stats.ChurnRiskLabelCounts(ds)
	labelCounts := buildLabelCountList(labelCountsMap)

	data := ReportData{
		GeneratedAt:          now,
		RepoName:             repoName,
		Summary:              stats.ComputeSummary(ds),
		Contributors:         stats.TopContributors(ds, topN),
		Hotspots:             stats.FileHotspots(ds, topN),
		Directories:          stats.DirectoryStats(ds, topN),
		ActivityRaw:          actRaw,
		ActivityYears:        actYears,
		ActivityGrid:         actGrid,
		MaxActivityCommits:   maxActCommits,
		BusFactor:            stats.BusFactor(ds, topN),
		Coupling:             stats.FileCoupling(ds, topN, sf.CouplingMinChanges),
		ChurnRisk:            stats.ChurnRisk(ds, topN),
		ChurnRiskLabelCounts: labelCounts,
		Patterns:             patterns,
		TopCommits:           stats.TopCommits(ds, topN),
		DevNetwork:           stats.DeveloperNetwork(ds, topN, sf.NetworkMinFiles),
		Profiles:             stats.DevProfiles(ds, "", topN),
		Pareto:               ComputePareto(ds),
		PatternGrid:          grid,
		MaxPattern:           maxP,
		Structure:            BuildRepoTree(stats.FileHotspots(ds, 0), htmlTreeDepth),
	}

	return tmpl.Execute(w, data)
}

// churnRiskLabelCounts aggregates the per-label totals for the Churn
// Risk distribution strip. Ordering matches the table below: legacy-
// hotspot first (most actionable), cold last. Labels with zero files
// are omitted so the strip doesn't show empty chips on small repos.
func buildLabelCountList(counts map[string]int) []LabelCount {
	order := []string{"legacy-hotspot", "silo", "active-core", "active", "cold"}
	var result []LabelCount
	for i, lbl := range order {
		if n := counts[lbl]; n > 0 {
			result = append(result, LabelCount{Label: lbl, Count: n, Priority: i})
		}
	}
	return result
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
	"int64":     toInt64,
	"actColor":  actColor,
	"pctRatio":  pctRatio,
	"plusInt":   plusInt,
	"derefInt":  derefInt,
	"humanize":  humanize,
	"thousands": thousands,
	"docRef":    docRef,
}

// docRef returns an anchor link to the Churn Risk / Bus Factor / etc.
// section of METRICS.md on the public repo. Centralized so the URL
// base (and security attributes) live in one place — if the repo ever
// moves or the style changes, the template callsites don't need to
// be touched. Callers pass literal anchor strings; no escaping is
// applied because the helper is not exposed to user-controlled data.
func docRef(anchor string) template.HTML {
	return template.HTML(fmt.Sprintf(
		`<a href="https://github.com/lex0c/gitcortex/blob/main/docs/METRICS.md#%s" target="_blank" rel="noopener noreferrer" style="color:#0969da;">docs</a>`,
		anchor,
	))
}

// asInt64 coerces common integer template values to int64. Templates pass a
// mix of int and int64 depending on the stats struct field — keeping the
// helpers polymorphic avoids sprinkling `(int64 .Field)` casts through the
// templates. Floats are intentionally rejected: silent truncation would
// turn `34.5` into "34" in a tooltip with no visible warning. Unknown and
// float inputs fall back to fmt's default rendering at the call sites,
// which preserves the value rather than hiding precision loss.
func asInt64(n interface{}) (int64, bool) {
	switch x := n.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		return int64(x), true
	case uint8:
		return int64(x), true
	case uint16:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		return int64(x), true
	}
	return 0, false
}

// humanize formats a count with a k/M/B suffix for compact display in
// narrow HTML surfaces (summary cards, badges). Trailing ".0" is dropped so
// round values read as "1k" rather than "1.0k". The exact integer should
// still be surfaced in a tooltip so no precision is lost.
//
// Edge cases worth knowing: the %.1f formatter uses round-half-to-even, so
// 1_250_000 → "1.2M" (not "1.3M"). Boundaries are strict powers of 1000, so
// 999_999 renders as "1000k" rather than promoting to "1M" — unlikely in
// practice but documented here to avoid surprise.
func humanize(n interface{}) string {
	v, ok := asInt64(n)
	if !ok {
		return fmt.Sprintf("%v", n)
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	if abs < 1000 {
		return fmt.Sprintf("%d", v)
	}
	var val float64
	var suffix string
	switch {
	case abs < 1_000_000:
		val = float64(v) / 1000
		suffix = "k"
	case abs < 1_000_000_000:
		val = float64(v) / 1_000_000
		suffix = "M"
	default:
		val = float64(v) / 1_000_000_000
		suffix = "B"
	}
	s := fmt.Sprintf("%.1f", val)
	s = strings.TrimSuffix(s, ".0")
	return s + suffix
}

// thousands formats an integer with comma thousand separators for use in
// table cells where comparison and exact value matter (42844 → "42,844").
// Negative numbers and zero pass through; non-numeric input falls back to
// fmt's default rendering.
func thousands(n interface{}) string {
	v, ok := asInt64(n)
	if !ok {
		return fmt.Sprintf("%v", n)
	}
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	// Build groups of 3 digits from the right.
	s := fmt.Sprintf("%d", v)
	var out strings.Builder
	if neg {
		out.WriteByte('-')
	}
	// Insert a comma before every group of 3 digits past the first.
	first := len(s) % 3
	if first == 0 {
		first = 3
	}
	out.WriteString(s[:first])
	for i := first; i < len(s); i += 3 {
		out.WriteByte(',')
		out.WriteString(s[i : i+3])
	}
	return out.String()
}

// derefInt returns the value behind an *int, or 0 if nil. Template-side
// helper for optional percentile fields on ChurnRiskResult: nil becomes a
// safe zero so `{{derefInt .AgePercentile}}` never panics or prints a
// pointer address (which is what %d on *int would do).
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
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
	profiles := stats.DevProfiles(ds, email, 0)
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
