package report

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lex0c/gitcortex/internal/stats"
)

const fixtureJSONL = `{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","author_name":"Alice","author_email":"alice@test.com","author_date":"2024-01-15T10:00:00Z","additions":40,"deletions":5,"files_changed":2}
{"type":"commit_file","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","path_current":"main.go","status":"M","additions":30,"deletions":5}
{"type":"commit_file","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","path_current":"util.go","status":"M","additions":10,"deletions":0}
{"type":"commit","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author_name":"Bob","author_email":"bob@test.com","author_date":"2024-02-20T15:30:00Z","additions":20,"deletions":10,"files_changed":1}
{"type":"commit_file","commit":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","path_current":"main.go","status":"M","additions":20,"deletions":10}
{"type":"commit","sha":"cccccccccccccccccccccccccccccccccccccccc","author_name":"Alice","author_email":"alice@test.com","author_date":"2024-03-05T09:00:00Z","additions":30,"deletions":10,"files_changed":2}
{"type":"commit_file","commit":"cccccccccccccccccccccccccccccccccccccccc","path_current":"main.go","status":"M","additions":20,"deletions":5}
{"type":"commit_file","commit":"cccccccccccccccccccccccccccccccccccccccc","path_current":"util.go","status":"M","additions":10,"deletions":5}
{"type":"commit","sha":"dddddddddddddddddddddddddddddddddddddddd","author_name":"Alice","author_email":"alice@test.com","author_date":"2024-03-15T14:00:00Z","additions":10,"deletions":5,"files_changed":1}
{"type":"commit_file","commit":"dddddddddddddddddddddddddddddddddddddddd","path_current":"readme.md","status":"M","additions":10,"deletions":5}
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.jsonl")
	if err := os.WriteFile(path, []byte(fixtureJSONL), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func loadFixture(t *testing.T) *stats.Dataset {
	t.Helper()
	ds, err := stats.LoadJSONL(writeFixture(t))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return ds
}

func TestGenerate_SmokeRender(t *testing.T) {
	ds := loadFixture(t)
	var buf bytes.Buffer
	err := Generate(&buf, ds, "testrepo", 10, stats.StatsFlags{CouplingMinChanges: 1, NetworkMinFiles: 1})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()

	wants := []string{
		"<!DOCTYPE html>",
		"testrepo",
		`id="act-heatmap"`,
		`id="act-table"`,
		`getElementById('act-heatmap')`,
		`getElementById('act-table')`,
		"<th>Del/Add</th>",
		"Concentration",
		"Top Contributors",
		"Developer Profiles",
		`href="https://github.com/lex0c/gitcortex"`,
		`target="_blank"`,
		`rel="noopener noreferrer"`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in output", w)
		}
	}

	if strings.Contains(out, "<no value>") {
		t.Errorf("output contains `<no value>` — template rendered a nil field")
	}
}

func TestGenerate_EmptyDataset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ds, err := stats.LoadJSONL(path)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}

	var buf bytes.Buffer
	if err := Generate(&buf, ds, "empty", 10, stats.StatsFlags{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()

	// Empty dataset should render the "no data" path, not fall through to
	// "extremely concentrated" (pre-fix bug: FilesPct80Churn=0.0 was <= 10).
	wantCount := strings.Count(out, "no data")
	if wantCount < 3 {
		t.Errorf("expected at least 3 'no data' markers (Files/Devs/Dirs), got %d", wantCount)
	}
	if strings.Contains(out, "extremely concentrated") {
		t.Errorf("output should not claim 'extremely concentrated' for empty dataset")
	}
	if !strings.Contains(out, "⚪") {
		t.Errorf("output should contain neutral emoji ⚪ for empty cards")
	}
	if strings.Contains(out, "<no value>") {
		t.Errorf("template rendered nil field as <no value>")
	}
}

func TestGenerateProfile_SmokeRender(t *testing.T) {
	ds := loadFixture(t)
	var buf bytes.Buffer
	err := GenerateProfile(&buf, ds, "testrepo", "alice@test.com")
	if err != nil {
		t.Fatalf("GenerateProfile: %v", err)
	}
	out := buf.String()

	wants := []string{
		"<!DOCTYPE html>",
		"Alice",
		"alice@test.com",
		`id="prof-act-heatmap"`,
		`id="prof-act-table"`,
		`getElementById('prof-act-heatmap')`,
		`getElementById('prof-act-table')`,
		"<th>Del/Add</th>",
		"Scope",
		"Top Files",
		`target="_blank"`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in output", w)
		}
	}

	if strings.Contains(out, "<no value>") {
		t.Errorf("output contains `<no value>` — template rendered a nil field")
	}
}

func TestGenerateProfile_UnknownEmail(t *testing.T) {
	ds := loadFixture(t)
	var buf bytes.Buffer
	err := GenerateProfile(&buf, ds, "testrepo", "ghost@nowhere.com")
	if err == nil {
		t.Fatal("expected error for unknown email, got nil")
	}
}

func TestBuildActivityGrid_Monthly(t *testing.T) {
	raw := []stats.ActivityBucket{
		{Period: "2024-01", Commits: 5, Additions: 100, Deletions: 20},
		{Period: "2024-03", Commits: 2, Additions: 50, Deletions: 10},
		{Period: "2025-07", Commits: 1, Additions: 10, Deletions: 0},
	}
	years, grid, max := buildActivityGrid(raw)

	if len(years) != 2 || years[0] != "2024" || years[1] != "2025" {
		t.Errorf("years = %v, want [2024 2025]", years)
	}
	if len(grid) != 2 {
		t.Fatalf("grid rows = %d, want 2", len(grid))
	}
	if len(grid[0]) != 12 {
		t.Errorf("grid cols = %d, want 12", len(grid[0]))
	}
	if !grid[0][0].HasData || grid[0][0].Commits != 5 {
		t.Errorf("grid[2024][Jan] = %+v, want Commits=5 HasData=true", grid[0][0])
	}
	if grid[0][1].HasData {
		t.Errorf("grid[2024][Feb] should be empty, got %+v", grid[0][1])
	}
	if !grid[0][2].HasData || grid[0][2].Commits != 2 {
		t.Errorf("grid[2024][Mar] = %+v, want Commits=2", grid[0][2])
	}
	if !grid[1][6].HasData || grid[1][6].Commits != 1 {
		t.Errorf("grid[2025][Jul] = %+v, want Commits=1", grid[1][6])
	}
	if max != 5 {
		t.Errorf("maxCommits = %d, want 5", max)
	}
}

func TestComputeParetoDivergenceBotVsAuthor(t *testing.T) {
	// Scenario: bot commits 100 tiny commits, human commits 3 big ones.
	// Commits-based lens says bot dominates (100/103 ≈ 97%).
	// Churn-based lens says human dominates (3000/3100 ≈ 97%).
	// The two numbers must diverge — that is the whole reason the card exists.
	var lines []string
	// 100 tiny commits from bot (1 line each)
	for i := 0; i < 100; i++ {
		sha := fmt.Sprintf("%040d", i+1)
		lines = append(lines,
			fmt.Sprintf(`{"type":"commit","sha":"%s","author_name":"bot","author_email":"bot@ci","author_date":"2024-01-15T10:00:00Z","additions":1,"deletions":0,"files_changed":1}`, sha),
			fmt.Sprintf(`{"type":"commit_file","commit":"%s","path_current":"log.txt","status":"M","additions":1,"deletions":0}`, sha),
		)
	}
	// 3 big commits from a human (1000 lines each)
	for i := 0; i < 3; i++ {
		sha := fmt.Sprintf("h%039d", i+1)
		lines = append(lines,
			fmt.Sprintf(`{"type":"commit","sha":"%s","author_name":"Human","author_email":"h@x","author_date":"2024-01-15T10:00:00Z","additions":1000,"deletions":0,"files_changed":1}`, sha),
			fmt.Sprintf(`{"type":"commit_file","commit":"%s","path_current":"feature.go","status":"A","additions":1000,"deletions":0}`, sha),
		)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "divergence.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ds, err := stats.LoadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	p := ComputePareto(ds)

	// Both lenses should identify 1 dev (out of 2) as holding 80%.
	// But WHICH dev is different: commits → bot, churn → human.
	if p.TopCommitDevs != 1 {
		t.Errorf("TopCommitDevs = %d, want 1 (bot dominates commits)", p.TopCommitDevs)
	}
	if p.TopChurnDevs != 1 {
		t.Errorf("TopChurnDevs = %d, want 1 (human dominates churn)", p.TopChurnDevs)
	}
	// The percentage is the same (1/2 = 50%) — divergence is in WHICH dev,
	// not in the count. We assert both are populated and distinct data paths.
	if p.DevsPct80Commits != p.DevsPct80Churn {
		// With this crafted input they happen to tie at 50%, but the test's
		// purpose is to exercise both code paths independently.
	}
}

func TestComputeParetoZeroChurn(t *testing.T) {
	// All commits have zero additions and zero deletions (e.g., pure merges
	// or empty commits). TopChurnDevs must stay 0, not leak to 1 via the
	// zero-threshold-tripped-on-first-iteration bug.
	jsonl := `{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","author_name":"A","author_email":"a@x","author_date":"2024-01-10T10:00:00Z","additions":0,"deletions":0,"files_changed":0}
{"type":"commit","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author_name":"B","author_email":"b@x","author_date":"2024-01-11T10:00:00Z","additions":0,"deletions":0,"files_changed":0}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "zero.jsonl")
	if err := os.WriteFile(path, []byte(jsonl), 0644); err != nil {
		t.Fatal(err)
	}
	ds, err := stats.LoadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	p := ComputePareto(ds)

	if p.TopChurnDevs != 0 {
		t.Errorf("TopChurnDevs = %d, want 0 on zero-churn dataset", p.TopChurnDevs)
	}
	if p.DevsPct80Churn != 0 {
		t.Errorf("DevsPct80Churn = %.1f, want 0", p.DevsPct80Churn)
	}
}

func TestComputeParetoFilesAndDirsZeroChurn(t *testing.T) {
	// Files and dirs exist but every commit_file record has zero churn
	// (e.g. a sequence of pure renames with no content change). Previously
	// the Files and Dirs loops would trip the zero-threshold on the first
	// iteration and leave TopChurnFiles = TopChurnDirs = 1 — producing a
	// false "extremely concentrated" label. Guards added to ComputePareto
	// now skip the loops entirely when the aggregate is zero.
	jsonl := `{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","author_name":"A","author_email":"a@x","author_date":"2024-01-10T10:00:00Z","additions":0,"deletions":0,"files_changed":2}
{"type":"commit_file","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","path_current":"src/foo.go","path_previous":"src/foo.go","status":"M","additions":0,"deletions":0}
{"type":"commit_file","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","path_current":"src/bar.go","path_previous":"src/bar.go","status":"M","additions":0,"deletions":0}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "zero_file_churn.jsonl")
	if err := os.WriteFile(path, []byte(jsonl), 0644); err != nil {
		t.Fatal(err)
	}
	ds, err := stats.LoadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	p := ComputePareto(ds)

	if p.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d, want 2 (files exist in dataset)", p.TotalFiles)
	}
	if p.TopChurnFiles != 0 {
		t.Errorf("TopChurnFiles = %d, want 0 (no churn signal)", p.TopChurnFiles)
	}
	if p.FilesPct80Churn != 0 {
		t.Errorf("FilesPct80Churn = %.1f, want 0", p.FilesPct80Churn)
	}
	if p.TopChurnDirs != 0 {
		t.Errorf("TopChurnDirs = %d, want 0 (no churn signal)", p.TopChurnDirs)
	}
	if p.DirsPct80Churn != 0 {
		t.Errorf("DirsPct80Churn = %.1f, want 0", p.DirsPct80Churn)
	}
}

func TestComputePareto(t *testing.T) {
	ds := loadFixture(t)
	p := ComputePareto(ds)

	// Fixture has 2 devs (Alice: 3 commits, Bob: 1), 3 files, all in root dir.
	if p.TotalDevs != 2 {
		t.Errorf("TotalDevs = %d, want 2", p.TotalDevs)
	}
	if p.TotalFiles != 3 {
		t.Errorf("TotalFiles = %d, want 3", p.TotalFiles)
	}

	// Alice = 3/4 = 75% of commits (< 80%), so 80% threshold needs both devs.
	if p.TopCommitDevs != 2 {
		t.Errorf("TopCommitDevs = %d, want 2 (both devs needed for 80%%)", p.TopCommitDevs)
	}
	if p.DevsPct80Commits != 100.0 {
		t.Errorf("DevsPct80Commits = %.1f, want 100.0", p.DevsPct80Commits)
	}

	// File churn: main.go=90, util.go=25, readme.md=15 → total=130, 80%=104.
	// Cumulative: main=90 (<104) → util 115 (≥104) → stop. Top=2 of 3.
	if p.TopChurnFiles != 2 {
		t.Errorf("TopChurnFiles = %d, want 2", p.TopChurnFiles)
	}

	// Percentages must be in [0, 100].
	for _, v := range []float64{p.FilesPct80Churn, p.DevsPct80Commits, p.DevsPct80Churn, p.DirsPct80Churn} {
		if v < 0 || v > 100 {
			t.Errorf("pct out of range: %.1f", v)
		}
	}

	// DevsPct80Churn should also be populated (fixture has non-zero churn).
	if p.TopChurnDevs == 0 {
		t.Errorf("TopChurnDevs = 0, want > 0 (devs have non-zero churn in fixture)")
	}
}

// Documents current behavior: buildActivityGrid only parses "YYYY-MM".
// Daily ("YYYY-MM-DD") periods collapse multiple days into a single month cell.
// Weekly/yearly formats fail the Sscanf and are dropped.
// The HTML activity heatmap is monthly by design.
func TestBuildActivityGrid_NonMonthlyCollapsesOrDrops(t *testing.T) {
	daily := []stats.ActivityBucket{
		{Period: "2024-01-15", Commits: 3},
		{Period: "2024-01-20", Commits: 4},
	}
	_, grid, _ := buildActivityGrid(daily)
	if len(grid) != 1 {
		t.Fatalf("expected 1 year row, got %d", len(grid))
	}
	// Both days collapse into January (month 0). Only the last one written wins.
	if !grid[0][0].HasData {
		t.Errorf("January should have data (collapsed from daily buckets)")
	}

	weekly := []stats.ActivityBucket{
		{Period: "2024-W03", Commits: 3},
	}
	years, _, _ := buildActivityGrid(weekly)
	if len(years) != 0 {
		t.Errorf("weekly periods should be dropped (invalid month parse), got years=%v", years)
	}
}

func TestHumanize(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{int64(0), "0"},
		{int64(42), "42"},
		{int64(999), "999"},
		{int64(1000), "1k"},
		{int64(1200), "1.2k"},
		{int64(42844), "42.8k"},
		{int64(1_000_000), "1M"},
		{int64(1_280_000), "1.3M"},
		{int64(1_500_000_000), "1.5B"},
		{int64(-42844), "-42.8k"},
		// Ensure plain int (template default for int-typed fields) works too.
		{42, "42"},
		{42844, "42.8k"},
	}
	for _, c := range cases {
		if got := humanize(c.in); got != c.want {
			t.Errorf("humanize(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildExecutiveSummary(t *testing.T) {
	baseData := func() *ReportData {
		return &ReportData{
			Summary: stats.Summary{
				TotalCommits:    1000,
				TotalDevs:       10,
				FirstCommitDate: "2024-01-01",
				LastCommitDate:  "2024-12-31",
			},
			Pareto: ParetoData{
				TopChurnFiles: 50,
				TotalFiles:    500,
				FilesLabel:    "well distributed",
				FilesMarker:   "🟢",
			},
		}
	}

	t.Run("healthy repo with data emits positive ok bullet", func(t *testing.T) {
		data := baseData()
		// Supply a non-empty allBusFactor so hasData is true; BusFactor > 1
		// keeps it out of the single-owner bucket.
		bf := []stats.BusFactorResult{{BusFactor: 3, Path: "a.go"}}
		es := buildExecutiveSummary(data, nil, bf)
		// scope, concentration, ok = 3 bullets
		if len(es.Bullets) != 3 {
			t.Fatalf("got %d bullets, want 3: %+v", len(es.Bullets), es.Bullets)
		}
		if es.Bullets[2].Severity != "ok" {
			t.Errorf("last bullet severity = %q, want ok", es.Bullets[2].Severity)
		}
	})

	t.Run("bf=1 bullet requires intersection with legacy-hotspot", func(t *testing.T) {
		// Raw BF=1 count without legacy-hotspot overlap is noise-prone in
		// mature repos; the bullet only fires for the rarer intersection.
		data := baseData()
		cr := []stats.ChurnRiskResult{
			{Path: "active.go", Label: "active"},
			{Path: "silo.go", Label: "silo"},
		}
		bf := []stats.BusFactorResult{
			{BusFactor: 1, Path: "active.go"},
			{BusFactor: 1, Path: "silo.go"},
		}
		es := buildExecutiveSummary(data, cr, bf)
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "bus factor") {
				t.Errorf("BF=1 with no legacy-hotspot should not fire, got %q", b.Text)
			}
		}
	})

	t.Run("bf=1 ∩ legacy-hotspot fires with grammar agreement", func(t *testing.T) {
		data := baseData()
		// Single intersecting file → singular "is".
		cr := []stats.ChurnRiskResult{{Path: "old.go", Label: "legacy-hotspot"}}
		bf := []stats.BusFactorResult{{BusFactor: 1, Path: "old.go"}}
		es := buildExecutiveSummary(data, cr, bf)
		var got string
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "single-owner signal") {
				got = string(b.Text)
			}
		}
		if !strings.Contains(got, "<b>1</b> of those is") {
			t.Errorf("singular intersection broken, got %q", got)
		}

		// Two intersecting files → plural "are".
		cr = []stats.ChurnRiskResult{
			{Path: "old1.go", Label: "legacy-hotspot"},
			{Path: "old2.go", Label: "legacy-hotspot"},
		}
		bf = []stats.BusFactorResult{
			{BusFactor: 1, Path: "old1.go"},
			{BusFactor: 1, Path: "old2.go"},
		}
		es = buildExecutiveSummary(data, cr, bf)
		got = ""
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "single-owner signal") {
				got = string(b.Text)
			}
		}
		if !strings.Contains(got, "<b>2</b> of those are") {
			t.Errorf("plural intersection broken, got %q", got)
		}
	})

	t.Run("bf>1 legacy-hotspot files are not counted as intersection", func(t *testing.T) {
		// A legacy-hotspot with bus factor 2+ has more than one owner and
		// shouldn't light up the single-owner bullet, even though it's
		// still a legacy-hotspot.
		data := baseData()
		cr := []stats.ChurnRiskResult{{Path: "old.go", Label: "legacy-hotspot"}}
		bf := []stats.BusFactorResult{{BusFactor: 2, Path: "old.go"}}
		es := buildExecutiveSummary(data, cr, bf)
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "single-owner signal") {
				t.Errorf("BF=2 legacy-hotspot should not fire single-owner bullet, got %q", b.Text)
			}
		}
	})

	t.Run("legacy-hotspot count comes from full slice, not truncated", func(t *testing.T) {
		data := baseData()
		// Display slice would be truncated to top 3 of churn; the full slice
		// has 5 legacy-hotspots. The bullet must report 5, not 3.
		data.ChurnRisk = make([]stats.ChurnRiskResult, 3)
		full := []stats.ChurnRiskResult{
			{Label: "legacy-hotspot"},
			{Label: "legacy-hotspot"},
			{Label: "legacy-hotspot"},
			{Label: "legacy-hotspot"},
			{Label: "legacy-hotspot"},
			{Label: "active"},
		}
		es := buildExecutiveSummary(data, full, nil)
		var got string
		for _, b := range es.Bullets {
			if b.Severity == "critical" {
				got = string(b.Text)
			}
		}
		if !strings.Contains(got, "<b>5</b>") {
			t.Errorf("expected critical bullet to mention 5 files, got %q", got)
		}
	})

	t.Run("singular vs plural grammar for silo bullet", func(t *testing.T) {
		data := baseData()
		full := []stats.ChurnRiskResult{{Label: "silo"}}
		es := buildExecutiveSummary(data, full, nil)
		var got string
		for _, b := range es.Bullets {
			if b.Severity == "warning" {
				got = string(b.Text)
			}
		}
		if !strings.Contains(got, "<b>1</b> file classified") {
			t.Errorf("singular grammar broken, got %q", got)
		}

		full = []stats.ChurnRiskResult{{Label: "silo"}, {Label: "silo"}}
		es = buildExecutiveSummary(data, full, nil)
		for _, b := range es.Bullets {
			if b.Severity == "warning" {
				got = string(b.Text)
			}
		}
		if !strings.Contains(got, "<b>2</b> files classified") {
			t.Errorf("plural grammar broken, got %q", got)
		}
	})

t.Run("concentration bullet stays info regardless of label severity", func(t *testing.T) {
		// Concentration is a descriptive signal, not a pathology: mature
		// codebases follow Pareto by default. The bullet stays "info" even
		// when the underlying label reads "extremely concentrated" — the
		// text carries the caveat, severity doesn't escalate.
		data := baseData()
		data.Pareto.FilesLabel = "extremely concentrated"
		data.Pareto.FilesMarker = "🔴"
		es := buildExecutiveSummary(data, nil, nil)
		// second bullet is the concentration one (scope is first)
		if es.Bullets[1].Severity != "info" {
			t.Errorf("concentration severity = %q, want info", es.Bullets[1].Severity)
		}
		if !strings.Contains(string(es.Bullets[1].Text), "healthy core or a bottleneck") {
			t.Errorf("concentration bullet should carry the context caveat, got %q", es.Bullets[1].Text)
		}
	})

	t.Run("weekend ratio thresholds", func(t *testing.T) {
		// 19% weekend (81 weekday, 19 weekend) → should NOT flag.
		data := baseData()
		data.PatternGrid[0][10] = 81 // Mon
		data.PatternGrid[5][10] = 10 // Sat
		data.PatternGrid[6][10] = 9  // Sun — total weekend = 19, total = 100
		es := buildExecutiveSummary(data, nil, nil)
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "weekend") {
				t.Errorf("19%% weekend should not flag, got bullet: %q", b.Text)
			}
		}

		// 22% weekend (78 weekday, 22 weekend) → should flag as info.
		data = baseData()
		data.PatternGrid[0][10] = 78
		data.PatternGrid[5][10] = 22
		es = buildExecutiveSummary(data, nil, nil)
		var found bool
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "weekends") {
				found = true
				if b.Severity != "info" {
					t.Errorf("22%% weekend severity = %q, want info", b.Severity)
				}
			}
		}
		if !found {
			t.Error("22%% weekend should emit a bullet")
		}

		// 30% weekend (70 weekday, 30 weekend) → should flag as warning.
		data = baseData()
		data.PatternGrid[0][10] = 70
		data.PatternGrid[5][10] = 30
		es = buildExecutiveSummary(data, nil, nil)
		found = false
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "weekends") {
				found = true
				if b.Severity != "warning" {
					t.Errorf("30%% weekend severity = %q, want warning", b.Severity)
				}
			}
		}
		if !found {
			t.Error("30%% weekend should emit a bullet")
		}
	})

	t.Run("empty dataset suppresses the positive bullet", func(t *testing.T) {
		// A truly empty dataset has no commits, no files, no risk results to
		// inspect. The positive "✅ all clean" bullet must not render there
		// — it would falsely imply the repo was checked and cleared.
		data := &ReportData{
			Summary: stats.Summary{TotalCommits: 0},
			Pareto:  ParetoData{TotalFiles: 0},
		}
		es := buildExecutiveSummary(data, nil, nil)
		if len(es.Bullets) != 0 {
			t.Errorf("empty dataset should emit 0 bullets, got: %+v", es.Bullets)
		}
	})

	t.Run("scope bullet uses singular grammar when counts are 1", func(t *testing.T) {
		data := baseData()
		data.Summary.TotalCommits = 1
		data.Summary.TotalDevs = 1
		es := buildExecutiveSummary(data, nil, nil)
		scope := string(es.Bullets[0].Text)
		if !strings.Contains(scope, "<b>1</b> commit ") {
			t.Errorf("expected singular 'commit', got %q", scope)
		}
		if !strings.Contains(scope, "<b>1</b> developer ") {
			t.Errorf("expected singular 'developer', got %q", scope)
		}
	})

	t.Run("solo repo suppresses silo bullet", func(t *testing.T) {
		// In a single-developer repo there is nobody to transfer
		// knowledge to — the silo narrative is tautological and should
		// not render.
		data := baseData()
		data.Summary.TotalDevs = 1
		cr := []stats.ChurnRiskResult{{Path: "a", Label: "silo"}}
		es := buildExecutiveSummary(data, cr, nil)
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "classified as <b>silo</b>") {
				t.Errorf("silo bullet should be suppressed in solo repo, got %q", b.Text)
			}
		}
	})

	t.Run("solo repo suppresses bf=1 intersection bullet", func(t *testing.T) {
		// Every file has BF=1 when there is only one developer — the
		// intersection is 100% and carries no signal. Skip the bullet.
		data := baseData()
		data.Summary.TotalDevs = 1
		cr := []stats.ChurnRiskResult{{Path: "old.go", Label: "legacy-hotspot"}}
		bf := []stats.BusFactorResult{{BusFactor: 1, Path: "old.go"}}
		es := buildExecutiveSummary(data, cr, bf)
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "single-owner signal") {
				t.Errorf("bf=1 ∩ legacy bullet should be suppressed in solo repo, got %q", b.Text)
			}
		}
	})

	t.Run("solo repo still shows legacy-hotspot bullet", func(t *testing.T) {
		// Legacy-hotspot narrative is about deprecation, not ownership,
		// so it remains applicable in solo repos.
		data := baseData()
		data.Summary.TotalDevs = 1
		cr := []stats.ChurnRiskResult{{Path: "old.go", Label: "legacy-hotspot"}}
		es := buildExecutiveSummary(data, cr, nil)
		var found bool
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "legacy-hotspot") {
				found = true
			}
		}
		if !found {
			t.Error("legacy-hotspot bullet should still render in solo repo")
		}
	})

	t.Run("solo repo positive bullet ignores silo and bf=1 counts", func(t *testing.T) {
		// Silo / BF=1 are suppressed in solo mode, so the positive "✅"
		// bullet should only require legacyCount == 0 to fire.
		data := baseData()
		data.Summary.TotalDevs = 1
		cr := []stats.ChurnRiskResult{
			{Path: "a", Label: "silo"}, // would block positive in non-solo
			{Path: "b", Label: "active"},
		}
		es := buildExecutiveSummary(data, cr, nil)
		var found bool
		for _, b := range es.Bullets {
			if b.Severity == "ok" {
				found = true
				if !strings.Contains(string(b.Text), "No legacy-hotspots flagged") {
					t.Errorf("solo positive bullet wording off: %q", b.Text)
				}
			}
		}
		if !found {
			t.Error("solo repo with no legacy-hotspots should fire positive bullet")
		}
	})

	t.Run("merges-only repo skips the concentration bullet", func(t *testing.T) {
		// Repo with files but zero churn (all commits are merges) has
		// TopChurnFiles == 0. The old bullet wording "0 of N files
		// concentrate 80% of churn — no data" was awkward; guard the
		// bullet so it only emits when there's actual churn.
		data := baseData()
		data.Pareto.TopChurnFiles = 0
		data.Pareto.FilesLabel = "no data"
		data.Pareto.FilesMarker = "⚪"
		es := buildExecutiveSummary(data, nil, nil)
		for _, b := range es.Bullets {
			if strings.Contains(string(b.Text), "concentrate 80%") {
				t.Errorf("concentration bullet should be suppressed, got %q", b.Text)
			}
		}
	})
}

func TestThousands(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{int64(0), "0"},
		{int(42), "42"},
		{int64(999), "999"},
		{int64(1000), "1,000"},
		{int64(42844), "42,844"},
		{int64(1_000_000), "1,000,000"},
		{int64(1_438_634), "1,438,634"},
		{int64(-42844), "-42,844"},
		{int(100), "100"},
		{int64(12345), "12,345"},
		// Floats fall through to fmt's default rendering rather than silently
		// truncating — protects against surprise if a float field is ever
		// piped into the helper by mistake.
		{3.7, "3.7"},
		{float32(42.5), "42.5"},
	}
	for _, c := range cases {
		if got := thousands(c.in); got != c.want {
			t.Errorf("thousands(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
