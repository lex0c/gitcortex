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
	ExecSummary    ExecutiveSummary
}

// ExecutiveSummary is a narrative header generated from the stats so a
// non-technical reader (manager, CTO) gets a triage view before drilling
// into the tables below. Every bullet is derived from fields already
// populated elsewhere in ReportData — this struct only snapshots them in a
// render-ready form.
type ExecutiveSummary struct {
	Bullets []SummaryBullet
}

type SummaryBullet struct {
	Emoji    string
	Text     template.HTML // pre-formatted with <b> emphasis on key numbers
	Severity string        // critical | warning | info | ok — drives left-border color
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

// buildExecutiveSummary builds the at-a-glance bullets shown at the top of
// the HTML report. allChurnRisk and allBusFactor must be the full,
// untruncated slices — passing data.ChurnRisk/data.BusFactor (which are
// capped by topN for display) would make the bullet counts drift with the
// user's --top flag instead of reflecting the repo's real state.
func buildExecutiveSummary(data *ReportData, allChurnRisk []stats.ChurnRiskResult, allBusFactor []stats.BusFactorResult) ExecutiveSummary {
	var bullets []SummaryBullet

	// Scope — always first, sets context for everything that follows.
	if data.Summary.TotalCommits > 0 {
		bullets = append(bullets, bullet("info", "📌",
			"<b>%s</b> commits from <b>%s</b> developers between <b>%s</b> and <b>%s</b>.",
			thousands(data.Summary.TotalCommits),
			thousands(data.Summary.TotalDevs),
			data.Summary.FirstCommitDate,
			data.Summary.LastCommitDate,
		))
	}

	// Files concentration — shown only when there's churn to concentrate.
	// Without the TopChurnFiles guard, merges-only repos would render a
	// nonsensical "0 of N files concentrate 80% of churn — no data" bullet.
	//
	// Severity stays at "info": concentration in mature codebases is the
	// baseline Pareto pattern, not a pathology. Whether a concentrated
	// core is healthy or a bottleneck depends on context the summary
	// can't know — the bullet describes the shape, the reader judges.
	if data.Pareto.TotalFiles > 0 && data.Pareto.TopChurnFiles > 0 {
		bullets = append(bullets, bullet("info", data.Pareto.FilesMarker,
			"<b>%s</b> of <b>%s</b> files concentrate 80%% of churn — <b>%s</b>. May be a healthy core or a bottleneck — depends on context.",
			thousands(data.Pareto.TopChurnFiles),
			thousands(data.Pareto.TotalFiles),
			data.Pareto.FilesLabel,
		))
	}

	// Count label buckets for the legacy/silo bullets. These operate on
	// the full untruncated slice so counts don't drift with display topN.
	var legacyCount, siloCount int
	for _, cr := range allChurnRisk {
		switch cr.Label {
		case "legacy-hotspot":
			legacyCount++
		case "silo":
			siloCount++
		}
	}
	if legacyCount > 0 {
		bullets = append(bullets, bullet("critical", "🔴",
			"<b>%s</b> %s classified as <b>legacy-hotspot</b> — old code with concentrated ownership and declining activity. Worth investigating whether they are deprecated paths still being touched or genuinely load-bearing code.",
			thousands(legacyCount), pluralFile(legacyCount),
		))
	}
	if siloCount > 0 {
		bullets = append(bullets, bullet("warning", "🟡",
			"<b>%s</b> %s classified as <b>silo</b> — old, concentrated, still active. Candidates for knowledge transfer if the owner is load-bearing.",
			thousands(siloCount), pluralFile(siloCount),
		))
	}

	// Bus-factor-1 ∩ legacy-hotspot: the narrow, high-signal intersection
	// where a file is simultaneously old, declining, and owned by exactly
	// one person. The raw BF=1 count is dominated by one-off commits in
	// mature repos (WordPress: 3k+, k8s: 29k+) and produced more noise
	// than signal. This intersection consistently points at code that
	// genuinely needs a human decision.
	bfOne := make(map[string]struct{}, len(allBusFactor))
	for _, bf := range allBusFactor {
		if bf.BusFactor == 1 {
			bfOne[bf.Path] = struct{}{}
		}
	}
	var bf1Legacy int
	for _, cr := range allChurnRisk {
		if cr.Label != "legacy-hotspot" {
			continue
		}
		if _, ok := bfOne[cr.Path]; ok {
			bf1Legacy++
		}
	}
	if bf1Legacy > 0 {
		bullets = append(bullets, bullet("critical", "🔴",
			"<b>%s</b> of those %s also <b>bus factor = 1</b> — old, declining, and owned by a single person. The strongest single-owner signal in this report.",
			thousands(bf1Legacy), map[bool]string{true: "is", false: "are"}[bf1Legacy == 1],
		))
	}

	// Weekend work ratio, repo-wide. Flag only when high enough to be
	// interesting — sub-20% is noise that depends on team timezone mix.
	var weekdayTotal, weekendTotal int
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			c := data.PatternGrid[d][h]
			if d == 5 || d == 6 { // PatternGrid rows: 0=Mon … 5=Sat, 6=Sun.
				weekendTotal += c
			} else {
				weekdayTotal += c
			}
		}
	}
	if total := weekdayTotal + weekendTotal; total > 0 {
		pct := float64(weekendTotal) / float64(total) * 100
		if pct >= 20 {
			sev := "info"
			if pct >= 30 {
				sev = "warning"
			}
			bullets = append(bullets, bullet(sev, "🗓️",
				"<b>%.0f%%</b> of commits land on weekends — possible overtime, globally distributed team, or off-hours release cadence.",
				pct,
			))
		}
	}

	// Positive signal when the narrow, high-signal buckets are clean —
	// and only when we actually have data to check. A truly empty JSONL
	// produces zero counts too; emitting "✅ all clean" there would
	// misleadingly imply the repo was inspected and cleared.
	hasData := len(allChurnRisk) > 0 || len(allBusFactor) > 0
	if hasData && legacyCount == 0 && siloCount == 0 && bf1Legacy == 0 {
		bullets = append(bullets, bullet("ok", "✅",
			"No legacy-hotspots, silos, or single-owner legacy files flagged.",
		))
	}

	return ExecutiveSummary{Bullets: bullets}
}

// bullet is a thin constructor that centralizes the `template.HTML` cast
// so the unsafe rendering happens in exactly one place. Every current
// caller passes only numeric formatters (thousands/humanize) and
// hardcoded labels — no repo-derived paths or developer identities. If a
// future caller needs to interpolate such values, escape them first.
func bullet(severity, emoji, format string, args ...interface{}) SummaryBullet {
	return SummaryBullet{
		Severity: severity,
		Emoji:    emoji,
		Text:     template.HTML(fmt.Sprintf(format, args...)),
	}
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

	// Compute unbounded ChurnRisk and BusFactor once. The executive summary
	// needs the full distribution (counts must not depend on display topN);
	// the HTML tables only need the top slice. Slicing at render time is
	// cheaper than running the computations twice.
	allChurnRisk := stats.ChurnRisk(ds, 0)
	allBusFactor := stats.BusFactor(ds, 0)
	displayChurnRisk := allChurnRisk
	displayBusFactor := allBusFactor
	if topN > 0 {
		if len(displayChurnRisk) > topN {
			displayChurnRisk = displayChurnRisk[:topN]
		}
		if len(displayBusFactor) > topN {
			displayBusFactor = displayBusFactor[:topN]
		}
	}

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
		BusFactor:          displayBusFactor,
		Coupling:           stats.FileCoupling(ds, topN, sf.CouplingMinChanges),
		ChurnRisk:          displayChurnRisk,
		Patterns:           patterns,
		TopCommits:         stats.TopCommits(ds, topN),
		DevNetwork:         stats.DeveloperNetwork(ds, topN, sf.NetworkMinFiles),
		Profiles:           stats.DevProfiles(ds, ""),
		Pareto:             ComputePareto(ds),
		PatternGrid:        grid,
		MaxPattern:         maxP,
	}
	data.ExecSummary = buildExecutiveSummary(&data, allChurnRisk, allBusFactor)

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

// pluralFile picks the right noun for a count. Used in the executive
// summary, where "1 files" reads as a bug to anyone paying attention.
func pluralFile(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
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
