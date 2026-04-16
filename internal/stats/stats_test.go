package stats

import (
	"strings"
	"testing"
	"time"
)

func makeDataset() *Dataset {
	ds := &Dataset{
		CommitCount:       4,
		DevCount:          2,
		UniqueFileCount:   3,
		TotalAdditions:    100,
		TotalDeletions:    30,
		TotalFilesChanged: 8,
		MergeCount:        1,
		Earliest:          time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC),
		Latest:            time.Date(2024, 3, 15, 14, 0, 0, 0, time.UTC),
		commits: map[string]*commitEntry{
			"sha1": {email: "alice@test.com", date: time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC), add: 40, del: 5, files: 2},
			"sha2": {email: "bob@test.com", date: time.Date(2024, 2, 20, 15, 30, 0, 0, time.UTC), add: 20, del: 10, files: 3},
			"sha3": {email: "alice@test.com", date: time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC), add: 30, del: 10, files: 2},
			"sha4": {email: "alice@test.com", date: time.Date(2024, 3, 15, 14, 0, 0, 0, time.UTC), add: 10, del: 5, files: 1},
		},
		parentCounts: map[string]int{"sha1": 2},
		contributors: map[string]*ContributorStat{
			"alice@test.com": {Name: "Alice", Email: "alice@test.com", Commits: 3, Additions: 80, Deletions: 20,
				FilesTouched: 2, ActiveDays: 3, FirstDate: "2024-01-10", LastDate: "2024-03-15"},
			"bob@test.com": {Name: "Bob", Email: "bob@test.com", Commits: 1, Additions: 20, Deletions: 10,
				FilesTouched: 1, ActiveDays: 1, FirstDate: "2024-02-20", LastDate: "2024-02-20"},
		},
		files: map[string]*fileEntry{
			"main.go": {commits: 3, additions: 60, deletions: 15, devLines: map[string]int64{
				"alice@test.com": 50, "bob@test.com": 25,
			}, recentChurn: 100.0, lastChange: time.Date(2024, 3, 15, 14, 0, 0, 0, time.UTC)},
			"util.go": {commits: 2, additions: 30, deletions: 10, devLines: map[string]int64{
				"alice@test.com": 40,
			}, recentChurn: 50.0, lastChange: time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)},
			"readme.md": {commits: 1, additions: 10, deletions: 5, devLines: map[string]int64{
				"bob@test.com": 15,
			}, recentChurn: 10.0, lastChange: time.Date(2024, 2, 20, 15, 30, 0, 0, time.UTC)},
		},
		workGrid: [7][24]int{},
		couplingPairs: map[filePair]int{
			{a: "main.go", b: "util.go"}: 8,
		},
		couplingFileChanges: map[string]int{
			"main.go": 10, "util.go": 8,
		},
	}

	// Working patterns: Wednesday 10h, Wednesday 15h, Thursday 9h, Friday 14h
	ds.workGrid[2][10] = 1  // Wed 10
	ds.workGrid[2][15] = 1  // Wed 15
	ds.workGrid[3][9] = 1   // Thu 9
	ds.workGrid[4][14] = 1  // Fri 14

	return ds
}

func TestComputeSummary(t *testing.T) {
	ds := makeDataset()
	s := ComputeSummary(ds)

	if s.TotalCommits != 4 {
		t.Errorf("TotalCommits = %d", s.TotalCommits)
	}
	if s.TotalDevs != 2 {
		t.Errorf("TotalDevs = %d", s.TotalDevs)
	}
	if s.TotalFiles != 3 {
		t.Errorf("TotalFiles = %d", s.TotalFiles)
	}
	if s.MergeCommits != 1 {
		t.Errorf("MergeCommits = %d", s.MergeCommits)
	}
	if s.AvgFilesChanged != 2.0 {
		t.Errorf("AvgFilesChanged = %f", s.AvgFilesChanged)
	}
	if s.FirstCommitDate != "2024-01-10" {
		t.Errorf("FirstCommitDate = %q", s.FirstCommitDate)
	}
	if s.LastCommitDate != "2024-03-15" {
		t.Errorf("LastCommitDate = %q", s.LastCommitDate)
	}
}

func TestComputeSummaryEmpty(t *testing.T) {
	ds := &Dataset{commits: map[string]*commitEntry{}}
	s := ComputeSummary(ds)
	if s.TotalCommits != 0 || s.AvgAdditions != 0 {
		t.Errorf("empty summary = %+v", s)
	}
}

func TestTopContributors(t *testing.T) {
	ds := makeDataset()

	result := TopContributors(ds, 10)
	if len(result) != 2 {
		t.Fatalf("len = %d", len(result))
	}
	if result[0].Email != "alice@test.com" || result[0].Commits != 3 {
		t.Errorf("result[0] = %+v", result[0])
	}
	if result[1].Email != "bob@test.com" || result[1].Commits != 1 {
		t.Errorf("result[1] = %+v", result[1])
	}

	// Top 1
	result = TopContributors(ds, 1)
	if len(result) != 1 {
		t.Errorf("top 1 len = %d", len(result))
	}
}

func TestFileHotspots(t *testing.T) {
	ds := makeDataset()
	result := FileHotspots(ds, 2)

	if len(result) != 2 {
		t.Fatalf("len = %d", len(result))
	}
	if result[0].Path != "main.go" || result[0].Commits != 3 {
		t.Errorf("result[0] = %+v", result[0])
	}
	if result[0].Churn != 75 {
		t.Errorf("churn = %d, want 75", result[0].Churn)
	}
	if result[0].UniqueDevs != 2 {
		t.Errorf("devs = %d, want 2", result[0].UniqueDevs)
	}
}

func TestActivityOverTime(t *testing.T) {
	ds := makeDataset()

	result := ActivityOverTime(ds, "month")
	if len(result) != 3 {
		t.Fatalf("len = %d, want 3", len(result))
	}
	if result[0].Period != "2024-01" || result[0].Commits != 1 {
		t.Errorf("result[0] = %+v", result[0])
	}
	if result[2].Period != "2024-03" || result[2].Commits != 2 {
		t.Errorf("result[2] = %+v", result[2])
	}

	// Year granularity
	result = ActivityOverTime(ds, "year")
	if len(result) != 1 || result[0].Period != "2024" {
		t.Errorf("year result = %+v", result)
	}
}

func TestBusFactor(t *testing.T) {
	ds := makeDataset()
	result := BusFactor(ds, 10)

	// readme.md has 1 dev (bus factor 1), util.go has 1 dev, main.go has 2
	if len(result) < 2 {
		t.Fatalf("len = %d", len(result))
	}
	// Sorted by bus factor ascending (lowest = highest risk)
	if result[0].BusFactor != 1 {
		t.Errorf("result[0].BusFactor = %d, want 1", result[0].BusFactor)
	}
}

func TestBusFactorSingleDev(t *testing.T) {
	ds := &Dataset{
		files: map[string]*fileEntry{
			"solo.go": {commits: 5, devLines: map[string]int64{"dev@x.com": 100}},
		},
	}
	result := BusFactor(ds, 10)
	if len(result) != 1 || result[0].BusFactor != 1 {
		t.Errorf("single dev result = %+v", result)
	}
}

func TestFileCoupling(t *testing.T) {
	ds := makeDataset()
	result := FileCoupling(ds, 10, 0, 1)

	if len(result) != 1 {
		t.Fatalf("len = %d", len(result))
	}
	if result[0].CoChanges != 8 {
		t.Errorf("CoChanges = %d, want 8", result[0].CoChanges)
	}
	// coupling% = 8 / min(10, 8) = 8/8 = 100%
	if result[0].CouplingPct != 100 {
		t.Errorf("CouplingPct = %.1f, want 100", result[0].CouplingPct)
	}
}

func TestFileCouplingMinThreshold(t *testing.T) {
	ds := makeDataset()
	result := FileCoupling(ds, 10, 0, 20) // min 20 co-changes
	if len(result) != 0 {
		t.Errorf("expected empty with high threshold, got %d", len(result))
	}
}

func TestChurnRisk(t *testing.T) {
	ds := makeDataset()
	result := ChurnRisk(ds, 10, 90)

	if len(result) != 3 {
		t.Fatalf("len = %d", len(result))
	}
	// main.go: recentChurn=100, 2 devs → risk=50
	// util.go: recentChurn=50, 1 dev → risk=50
	// readme.md: recentChurn=10, 1 dev → risk=10
	// Sorted by risk descending
	if result[0].RiskScore <= result[len(result)-1].RiskScore {
		t.Errorf("not sorted descending: first=%f, last=%f", result[0].RiskScore, result[len(result)-1].RiskScore)
	}
}

func TestWorkingPatterns(t *testing.T) {
	ds := makeDataset()
	result := WorkingPatterns(ds)

	if len(result) != 4 {
		t.Fatalf("len = %d, want 4", len(result))
	}

	found := make(map[string]bool)
	for _, p := range result {
		found[p.Day] = true
	}
	if !found["Wed"] || !found["Thu"] || !found["Fri"] {
		t.Errorf("missing days: %v", result)
	}
}

func TestDeveloperNetwork(t *testing.T) {
	ds := makeDataset()
	result := DeveloperNetwork(ds, 10, 1)

	if len(result) != 1 {
		t.Fatalf("len = %d", len(result))
	}
	// alice and bob share main.go
	if result[0].SharedFiles != 1 {
		t.Errorf("SharedFiles = %d, want 1", result[0].SharedFiles)
	}
}

func TestDeveloperNetworkMinThreshold(t *testing.T) {
	ds := makeDataset()
	result := DeveloperNetwork(ds, 10, 100) // min 100 shared files
	if len(result) != 0 {
		t.Errorf("expected empty with high threshold, got %d", len(result))
	}
}

func TestContributorRanking(t *testing.T) {
	ds := makeDataset()
	result := ContributorRanking(ds, 10)

	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}

	// Alice is top: 3 commits (max), 100 lines (max), 2 files (max), 3 days (max) → score 100
	if result[0].Email != "alice@test.com" {
		t.Errorf("result[0].Email = %q, want alice", result[0].Email)
	}
	if result[0].Score != 100.0 {
		t.Errorf("alice score = %.1f, want 100.0", result[0].Score)
	}
	if result[0].Commits != 3 {
		t.Errorf("alice commits = %d, want 3", result[0].Commits)
	}
	if result[0].LinesChanged != 100 {
		t.Errorf("alice lines = %d, want 100", result[0].LinesChanged)
	}
	if result[0].FilesTouched != 2 {
		t.Errorf("alice files = %d, want 2", result[0].FilesTouched)
	}
	if result[0].ActiveDays != 3 {
		t.Errorf("alice days = %d, want 3", result[0].ActiveDays)
	}

	// Bob: 1/3 commits, 30/100 lines, 1/2 files, 1/3 days
	// = (0.333 + 0.3 + 0.5 + 0.333) / 4 * 100 = 36.7
	if result[1].Email != "bob@test.com" {
		t.Errorf("result[1].Email = %q, want bob", result[1].Email)
	}
	if result[1].Score < 36.0 || result[1].Score > 37.0 {
		t.Errorf("bob score = %.1f, want ~36.7", result[1].Score)
	}
}

func TestContributorRankingTopN(t *testing.T) {
	ds := makeDataset()
	result := ContributorRanking(ds, 1)
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].Email != "alice@test.com" {
		t.Errorf("top 1 = %q, want alice", result[0].Email)
	}
}

func TestContributorRankingEmpty(t *testing.T) {
	ds := &Dataset{contributors: map[string]*ContributorStat{}}
	result := ContributorRanking(ds, 10)
	if len(result) != 0 {
		t.Errorf("empty dataset ranking len = %d", len(result))
	}
}

func TestContributorRankingSingleDev(t *testing.T) {
	ds := &Dataset{
		contributors: map[string]*ContributorStat{
			"solo@x.com": {Name: "Solo", Email: "solo@x.com", Commits: 50, Additions: 1000, Deletions: 200,
				FilesTouched: 30, ActiveDays: 20, FirstDate: "2024-01-01", LastDate: "2024-03-01"},
		},
	}
	result := ContributorRanking(ds, 10)
	if len(result) != 1 {
		t.Fatalf("len = %d", len(result))
	}
	// Single dev is max in all dimensions → score 100
	if result[0].Score != 100.0 {
		t.Errorf("single dev score = %.1f, want 100.0", result[0].Score)
	}
}

func TestStreamLoadContributorDetails(t *testing.T) {
	jsonl := `{"type":"commit","sha":"c1","tree":"t","parents":[],"author_name":"Alice","author_email":"alice@x.com","author_date":"2024-01-10T10:00:00Z","committer_name":"Alice","committer_email":"alice@x.com","committer_date":"2024-01-10T10:00:00Z","message":"","additions":10,"deletions":2,"files_changed":2}
{"type":"commit_file","commit":"c1","path_current":"a.go","path_previous":"a.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":5,"deletions":1}
{"type":"commit_file","commit":"c1","path_current":"b.go","path_previous":"b.go","status":"M","old_hash":"0","new_hash":"2","old_size":0,"new_size":0,"additions":5,"deletions":1}
{"type":"commit","sha":"c2","tree":"t","parents":[],"author_name":"Alice","author_email":"alice@x.com","author_date":"2024-01-15T14:00:00Z","committer_name":"Alice","committer_email":"alice@x.com","committer_date":"2024-01-15T14:00:00Z","message":"","additions":8,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"a.go","path_previous":"a.go","status":"M","old_hash":"0","new_hash":"3","old_size":0,"new_size":0,"additions":8,"deletions":0}
{"type":"commit","sha":"c3","tree":"t","parents":[],"author_name":"Bob","author_email":"bob@x.com","author_date":"2024-02-01T09:00:00Z","committer_name":"Bob","committer_email":"bob@x.com","committer_date":"2024-02-01T09:00:00Z","message":"","additions":20,"deletions":5,"files_changed":1}
{"type":"commit_file","commit":"c3","path_current":"c.go","path_previous":"c.go","status":"A","old_hash":"0","new_hash":"4","old_size":0,"new_size":0,"additions":20,"deletions":5}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	alice := ds.contributors["alice@x.com"]
	if alice == nil {
		t.Fatal("alice not found")
	}
	if alice.Commits != 2 {
		t.Errorf("alice commits = %d, want 2", alice.Commits)
	}
	if alice.FilesTouched != 2 {
		t.Errorf("alice files = %d, want 2 (a.go, b.go)", alice.FilesTouched)
	}
	if alice.ActiveDays != 2 {
		t.Errorf("alice active days = %d, want 2 (Jan 10, Jan 15)", alice.ActiveDays)
	}
	if alice.FirstDate != "2024-01-10" {
		t.Errorf("alice first = %q", alice.FirstDate)
	}
	if alice.LastDate != "2024-01-15" {
		t.Errorf("alice last = %q", alice.LastDate)
	}

	bob := ds.contributors["bob@x.com"]
	if bob == nil {
		t.Fatal("bob not found")
	}
	if bob.FilesTouched != 1 {
		t.Errorf("bob files = %d, want 1", bob.FilesTouched)
	}
	if bob.ActiveDays != 1 {
		t.Errorf("bob active days = %d, want 1", bob.ActiveDays)
	}

	// Verify ranking uses loaded data correctly
	ranked := ContributorRanking(ds, 10)
	if len(ranked) != 2 {
		t.Fatalf("ranked len = %d", len(ranked))
	}
	if ranked[0].Email != "alice@x.com" {
		t.Errorf("top ranked = %q, want alice", ranked[0].Email)
	}
}

func TestStreamLoadBasic(t *testing.T) {
	jsonl := `{"type":"commit","sha":"aaa","tree":"ttt","parents":[],"author_name":"Alice","author_email":"alice@test.com","author_date":"2024-06-15T10:00:00Z","committer_name":"Alice","committer_email":"alice@test.com","committer_date":"2024-06-15T10:00:00Z","message":"","additions":10,"deletions":2,"files_changed":1}
{"type":"commit_file","commit":"aaa","path_current":"main.go","path_previous":"main.go","status":"M","old_hash":"000","new_hash":"111","old_size":0,"new_size":100,"additions":10,"deletions":2}
{"type":"dev","dev_id":"d1","name":"Alice","email":"alice@test.com"}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	if ds.CommitCount != 1 {
		t.Errorf("CommitCount = %d", ds.CommitCount)
	}
	if ds.DevCount != 1 {
		t.Errorf("DevCount = %d", ds.DevCount)
	}
	if ds.UniqueFileCount != 1 {
		t.Errorf("UniqueFileCount = %d", ds.UniqueFileCount)
	}
	if ds.TotalAdditions != 10 {
		t.Errorf("TotalAdditions = %d", ds.TotalAdditions)
	}

	cs := ds.contributors["alice@test.com"]
	if cs == nil || cs.Commits != 1 {
		t.Errorf("contributor = %+v", cs)
	}

	fe := ds.files["main.go"]
	if fe == nil || fe.commits != 1 || fe.additions != 10 {
		t.Errorf("file = %+v", fe)
	}
}

func TestStreamLoadDateFilter(t *testing.T) {
	jsonl := `{"type":"commit","sha":"old","tree":"t","parents":[],"author_name":"A","author_email":"a@x","author_date":"2024-01-01T00:00:00Z","committer_name":"A","committer_email":"a@x","committer_date":"2024-01-01T00:00:00Z","message":"","additions":5,"deletions":1,"files_changed":1}
{"type":"commit_file","commit":"old","path_current":"a.go","path_previous":"a.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":5,"deletions":1}
{"type":"commit","sha":"new","tree":"t","parents":[],"author_name":"B","author_email":"b@x","author_date":"2024-06-15T00:00:00Z","committer_name":"B","committer_email":"b@x","committer_date":"2024-06-15T00:00:00Z","message":"","additions":20,"deletions":3,"files_changed":2}
{"type":"commit_file","commit":"new","path_current":"b.go","path_previous":"b.go","status":"A","old_hash":"0","new_hash":"2","old_size":0,"new_size":0,"additions":20,"deletions":3}
`
	// Filter to June only
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{
		From: "2024-06-01", To: "2024-06-30",
		HalfLifeDays: 90, CoupMaxFiles: 50,
	})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	if ds.CommitCount != 1 {
		t.Errorf("CommitCount = %d, want 1 (filtered)", ds.CommitCount)
	}
	if ds.TotalAdditions != 20 {
		t.Errorf("TotalAdditions = %d, want 20", ds.TotalAdditions)
	}
	if _, ok := ds.files["a.go"]; ok {
		t.Error("a.go should be filtered out")
	}
}

func TestStreamLoadCoupling(t *testing.T) {
	jsonl := `{"type":"commit","sha":"c1","tree":"t","parents":[],"author_name":"A","author_email":"a@x","author_date":"2024-01-01T00:00:00Z","committer_name":"A","committer_email":"a@x","committer_date":"2024-01-01T00:00:00Z","message":"","additions":10,"deletions":0,"files_changed":2}
{"type":"commit_file","commit":"c1","path_current":"x.go","path_previous":"x.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":5,"deletions":0}
{"type":"commit_file","commit":"c1","path_current":"y.go","path_previous":"y.go","status":"M","old_hash":"0","new_hash":"2","old_size":0,"new_size":0,"additions":5,"deletions":0}
{"type":"commit","sha":"c2","tree":"t","parents":[],"author_name":"A","author_email":"a@x","author_date":"2024-01-02T00:00:00Z","committer_name":"A","committer_email":"a@x","committer_date":"2024-01-02T00:00:00Z","message":"","additions":8,"deletions":0,"files_changed":2}
{"type":"commit_file","commit":"c2","path_current":"x.go","path_previous":"x.go","status":"M","old_hash":"0","new_hash":"3","old_size":0,"new_size":0,"additions":4,"deletions":0}
{"type":"commit_file","commit":"c2","path_current":"y.go","path_previous":"y.go","status":"M","old_hash":"0","new_hash":"4","old_size":0,"new_size":0,"additions":4,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	pair := filePair{a: "x.go", b: "y.go"}
	if ds.couplingPairs[pair] != 2 {
		t.Errorf("coupling pair count = %d, want 2", ds.couplingPairs[pair])
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		in   string
		zero bool
	}{
		{"2024-01-15T10:30:00Z", false},
		{"2024-01-15T10:30:00+03:00", false},
		{"invalid", true},
		{"", true},
		{"2024-01-15", true}, // not RFC3339
	}

	for _, tt := range tests {
		got := parseDate(tt.in)
		if got.IsZero() != tt.zero {
			t.Errorf("parseDate(%q).IsZero() = %v, want %v", tt.in, got.IsZero(), tt.zero)
		}
	}
}
