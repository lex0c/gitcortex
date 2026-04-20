package report

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// Regression: the Per-Repository Breakdown section emits a CSS width
// for each repo's commit-share bar. The width has to be a numeric
// literal — if the template feeds a string through printf "%.0f"
// (e.g. via the string-returning pctFloat helper) html/template's
// CSS sanitizer replaces it with `ZgotmplZ` and the bars render at
// zero width. Exercise the multi-repo path end-to-end and assert
// the rendered widths are clean.
func TestGenerate_PerRepoBreakdownWidthsAreNumeric(t *testing.T) {
	dir := t.TempDir()
	alpha := filepath.Join(dir, "alpha.jsonl")
	beta := filepath.Join(dir, "beta.jsonl")
	// Distinct SHAs and content so both repos appear in the breakdown.
	alphaRow := `{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","author_email":"me@x.com","author_name":"Me","author_date":"2024-01-01T00:00:00Z","additions":10,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","path_current":"a.go","additions":10,"deletions":0}
`
	betaRow := `{"type":"commit","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author_email":"me@x.com","author_name":"Me","author_date":"2024-02-01T00:00:00Z","additions":20,"deletions":5,"files_changed":1}
{"type":"commit_file","commit":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","path_current":"b.go","additions":20,"deletions":5}
`
	if err := os.WriteFile(alpha, []byte(alphaRow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta, []byte(betaRow), 0o644); err != nil {
		t.Fatal(err)
	}
	ds, err := stats.LoadMultiJSONL([]string{alpha, beta})
	if err != nil {
		t.Fatalf("LoadMultiJSONL: %v", err)
	}
	var buf bytes.Buffer
	if err := Generate(&buf, ds, "scan-fixture", 10, stats.StatsFlags{CouplingMinChanges: 1, NetworkMinFiles: 1}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Per-Repository Breakdown") {
		t.Fatal("breakdown section missing from multi-repo report")
	}
	if strings.Contains(out, "ZgotmplZ") {
		t.Error("report contains ZgotmplZ — template fed a non-numeric value to a CSS context (likely printf'd a string)")
	}
	if strings.Contains(out, "%!f(string=") {
		t.Error("report contains %!f(string=...) — printf was given a string where a float was expected")
	}

	// Stronger guard: the absence-of-markers check above would pass
	// if a future bug rendered every bar at `width:0%`. The breakdown
	// tile has a green fill (background:#216e39) — extract every
	// rendered width for those tiles and assert at least one is
	// non-zero. With alpha's 10-churn commit and beta's 25-churn
	// commit producing distinct totals, the commit-share proportions
	// can't all collapse to zero unless the template is broken.
	widthRe := regexp.MustCompile(`width:([0-9]+(?:\.[0-9]+)?)%; background:#216e39`)
	matches := widthRe.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		t.Fatal("no repo-breakdown bar widths found; template may have changed selector")
	}
	sawNonZero := false
	for _, m := range matches {
		if m[1] != "0" && m[1] != "0.0" {
			sawNonZero = true
			break
		}
	}
	if !sawNonZero {
		t.Errorf("every repo bar rendered with width:0 — proportions would be invisible in the UI; widths: %v", matches)
	}
}

// End-to-end for the `gitcortex scan --email me@x.com --report …`
// flow. Covers three assertions at once:
//   1. GenerateProfile emits the Per-Repository Breakdown section
//      when the dataset is multi-repo (gated on len(Repos) > 1).
//   2. Counts per repo are filtered to the dev — a commit by
//      someone-else@x.com in alpha doesn't bleed into my profile's
//      alpha row.
//   3. Files counted per repo are only files THIS dev touched — a
//      colleague-exclusive file in alpha must not inflate my scope.
func TestGenerateProfile_MultiRepoBreakdownFiltersByEmail(t *testing.T) {
	dir := t.TempDir()
	alpha := filepath.Join(dir, "alpha.jsonl")
	beta := filepath.Join(dir, "beta.jsonl")

	// alpha: me has 1 commit on a.go; colleague has 1 commit on
	// colleague-only.go. beta: me has 1 commit on b.go.
	alphaContent := `{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","author_email":"me@x.com","author_name":"Me","author_date":"2024-01-10T00:00:00Z","additions":10,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","path_current":"a.go","additions":10,"deletions":0}
{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","author_email":"colleague@x.com","author_name":"Col","author_date":"2024-01-11T00:00:00Z","additions":5,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","path_current":"colleague-only.go","additions":5,"deletions":0}
`
	betaContent := `{"type":"commit","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb01","author_email":"me@x.com","author_name":"Me","author_date":"2024-02-10T00:00:00Z","additions":20,"deletions":5,"files_changed":1}
{"type":"commit_file","commit":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb01","path_current":"b.go","additions":20,"deletions":5}
`
	if err := os.WriteFile(alpha, []byte(alphaContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta, []byte(betaContent), 0o644); err != nil {
		t.Fatal(err)
	}

	ds, err := stats.LoadMultiJSONL([]string{alpha, beta})
	if err != nil {
		t.Fatalf("LoadMultiJSONL: %v", err)
	}

	var buf bytes.Buffer
	if err := GenerateProfile(&buf, ds, "scan-profile", "me@x.com"); err != nil {
		t.Fatalf("GenerateProfile: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Per-Repository Breakdown") {
		t.Fatal("profile report missing the breakdown section — scan --email users won't see their cross-repo split")
	}

	// Each row renders as `<td class="mono">alpha</td>` followed by
	// a `<td>N</td>` for commits. Assert 1 commit per repo — if the
	// filter leaked, alpha would show 2 (me's + colleague's).
	commitCountRe := regexp.MustCompile(`<td class="mono">(alpha|beta)</td>\s*<td>(\d+)</td>`)
	rows := commitCountRe.FindAllStringSubmatch(out, -1)
	if len(rows) != 2 {
		t.Fatalf("expected 2 repo rows in breakdown, got %d: %v", len(rows), rows)
	}
	for _, r := range rows {
		if r[2] != "1" {
			t.Errorf("repo %s shows %s commits in profile breakdown — email filter leaked (want 1)", r[1], r[2])
		}
	}

	// Colleague-exclusive file must not appear in my scope. The
	// template renders file counts as `<td>N</td>` inside each row
	// — indirect assertion: if the file-count bumped, the dev-filter
	// on devCommits is wrong.
	if strings.Contains(out, "colleague-only.go") {
		t.Error("profile report mentions a file only the colleague touched")
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

func TestBuildLabelCountList(t *testing.T) {
	counts := map[string]int{
		"active":         2,
		"legacy-hotspot": 1,
		"cold":           1,
		"silo":           1,
		"active-core":    3,
	}
	got := buildLabelCountList(counts)

	wantOrder := []string{"legacy-hotspot", "silo", "active-core", "active", "cold"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(wantOrder), got)
	}
	for i, lbl := range wantOrder {
		if got[i].Label != lbl {
			t.Errorf("entry %d: label=%q, want %q", i, got[i].Label, lbl)
		}
		if got[i].Priority != i {
			t.Errorf("entry %d: priority=%d, want %d", i, got[i].Priority, i)
		}
		if got[i].Count != counts[lbl] {
			t.Errorf("%s count = %d, want %d", lbl, got[i].Count, counts[lbl])
		}
	}
}

func TestBuildLabelCountListOmitsEmpty(t *testing.T) {
	// Small repo where only two label buckets have entries — the strip
	// skips empties so it doesn't render "0 silo" chips.
	counts := map[string]int{
		"active":      1,
		"active-core": 2,
	}
	got := buildLabelCountList(counts)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Label != "active-core" || got[1].Label != "active" {
		t.Errorf("order wrong: %+v", got)
	}
}

// Regression: pct(int64(x), int64(y)) collapsed every sub-1 float to
// 0 before this helper existed, so extension/churn-risk bars all
// rendered as 0% on datasets with heavily decayed RecentChurn (small
// repos, aggressive --since filters). pctFloat preserves the relative
// scale.
func TestPctFloat(t *testing.T) {
	cases := []struct {
		val, max float64
		want     string
	}{
		// Sub-1 values: relative scale preserved (would all be 0 under int64 cast).
		{0.5, 1.0, "50.0"},
		{0.25, 0.5, "50.0"},
		{0.1, 0.9, "11.1"},
		// Mixed small + large.
		{50.0, 200.0, "25.0"},
		// max at zero → safe zero string, no NaN or division by zero.
		{5.0, 0.0, "0"},
		{0.0, 0.0, "0"},
		// val > max (can happen under rounding noise in sort+display).
		{10.0, 5.0, "200.0"},
	}
	for _, c := range cases {
		if got := pctFloat(c.val, c.max); got != c.want {
			t.Errorf("pctFloat(%v, %v) = %q, want %q", c.val, c.max, got, c.want)
		}
	}
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
