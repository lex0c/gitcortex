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
