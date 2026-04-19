package stats

import (
	"bytes"
	"fmt"
	"os"
	"sort"
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

// makeTiesDataset builds a dataset where every primary sort key ties, so
// only tiebreakers determine ordering. Used to guard against non-determinism
// regressions across every sort function in the package.
func makeTiesDataset() *Dataset {
	t1 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	ds := &Dataset{
		CommitCount: 6,
		Earliest:    t1,
		Latest:      t1,
		commits: map[string]*commitEntry{
			"sha1": {email: "dev-a@x", date: t1, add: 10, del: 5, files: 1},
			"sha2": {email: "dev-b@x", date: t1, add: 10, del: 5, files: 1},
			"sha3": {email: "dev-c@x", date: t1, add: 10, del: 5, files: 1},
			"sha4": {email: "dev-d@x", date: t1, add: 10, del: 5, files: 1},
			"sha5": {email: "dev-e@x", date: t1, add: 10, del: 5, files: 1},
			"sha6": {email: "dev-f@x", date: t1, add: 10, del: 5, files: 1},
		},
		contributors: map[string]*ContributorStat{
			"dev-a@x": {Email: "dev-a@x", Name: "A", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5},
			"dev-b@x": {Email: "dev-b@x", Name: "B", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5},
			"dev-c@x": {Email: "dev-c@x", Name: "C", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5},
			"dev-d@x": {Email: "dev-d@x", Name: "D", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5},
			"dev-e@x": {Email: "dev-e@x", Name: "E", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5},
			"dev-f@x": {Email: "dev-f@x", Name: "F", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5},
		},
		files: map[string]*fileEntry{
			"a/one.go": {commits: 1, additions: 10, deletions: 5, devLines: map[string]int64{"dev-a@x": 15}, monthChurn: map[string]int64{"2024-01": 15}, firstChange: t1, lastChange: t1},
			"a/two.go": {commits: 1, additions: 10, deletions: 5, devLines: map[string]int64{"dev-b@x": 15}, monthChurn: map[string]int64{"2024-01": 15}, firstChange: t1, lastChange: t1},
			"b/one.go": {commits: 1, additions: 10, deletions: 5, devLines: map[string]int64{"dev-c@x": 15}, monthChurn: map[string]int64{"2024-01": 15}, firstChange: t1, lastChange: t1},
			"b/two.go": {commits: 1, additions: 10, deletions: 5, devLines: map[string]int64{"dev-d@x": 15}, monthChurn: map[string]int64{"2024-01": 15}, firstChange: t1, lastChange: t1},
			"c/one.go": {commits: 1, additions: 10, deletions: 5, devLines: map[string]int64{"dev-e@x": 15}, monthChurn: map[string]int64{"2024-01": 15}, firstChange: t1, lastChange: t1},
			"c/two.go": {commits: 1, additions: 10, deletions: 5, devLines: map[string]int64{"dev-f@x": 15}, monthChurn: map[string]int64{"2024-01": 15}, firstChange: t1, lastChange: t1},
		},
		couplingPairs:       map[filePair]int{},
		couplingFileChanges: map[string]int{},
	}
	return ds
}

// TestAllSortsDeterministicUnderTies ensures every sort function in the
// package produces identical ordering across runs when the primary sort key
// ties. Without tiebreakers, Go map iteration order + unstable sort.Slice
// cause the CLI and the HTML report to show different top-N entries. This
// test guards every function that ranks results.
func TestAllSortsDeterministicUnderTies(t *testing.T) {
	ds := makeTiesDataset()

	identify := func(v interface{}) []string {
		switch r := v.(type) {
		case []BusFactorResult:
			out := make([]string, len(r))
			for i, e := range r {
				out[i] = e.Path
			}
			return out
		case []FileStat:
			out := make([]string, len(r))
			for i, e := range r {
				out[i] = e.Path
			}
			return out
		case []ContributorStat:
			out := make([]string, len(r))
			for i, e := range r {
				out[i] = e.Email
			}
			return out
		case []DirStat:
			out := make([]string, len(r))
			for i, e := range r {
				out[i] = e.Dir
			}
			return out
		case []BigCommit:
			out := make([]string, len(r))
			for i, e := range r {
				out[i] = e.SHA
			}
			return out
		case []DevProfile:
			out := make([]string, len(r))
			for i, e := range r {
				out[i] = e.Email
			}
			return out
		}
		t.Fatalf("unknown type: %T", v)
		return nil
	}

	cases := []struct {
		name string
		run  func() []string
	}{
		{"BusFactor", func() []string { return identify(BusFactor(ds, 0)) }},
		{"FileHotspots", func() []string { return identify(FileHotspots(ds, 0)) }},
		{"TopContributors", func() []string { return identify(TopContributors(ds, 0)) }},
		{"DirectoryStats", func() []string { return identify(DirectoryStats(ds, 0)) }},
		{"TopCommits", func() []string { return identify(TopCommits(ds, 0)) }},
		{"DevProfiles", func() []string { return identify(DevProfiles(ds, "", 0)) }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			first := c.run()
			if len(first) < 2 {
				t.Fatalf("fixture produced too few entries (%d) — need ties to exercise tiebreakers", len(first))
			}
			for i := 0; i < 20; i++ {
				got := c.run()
				for j := range got {
					if j >= len(first) || got[j] != first[j] {
						t.Fatalf("iteration %d: non-deterministic at index %d (got %q, first run had %q)",
							i, j, got[j], first[j])
					}
				}
			}
		})
	}
}

func TestDevProfilesCollaboratorsSharedLines(t *testing.T) {
	// Alice co-edits 2 files with each of Bob and Carol. Alice has 500
	// lines in each file. Bob's overlap is trivial (1 line per file);
	// Carol wrote substantive lines (500 per file). SharedFiles ties at
	// 2 for both collaborators — only SharedLines can distinguish.
	// Expected overlap: Bob = min(500,1)*2 = 2; Carol = min(500,500)*2 = 1000.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"a.go": {devLines: map[string]int64{"alice@x": 500, "bob@x": 1, "carol@x": 500}},
			"b.go": {devLines: map[string]int64{"alice@x": 500, "bob@x": 1, "carol@x": 500}},
		},
		contributors: map[string]*ContributorStat{
			"alice@x": {Email: "alice@x", Name: "A", Commits: 1, ActiveDays: 1, FilesTouched: 2},
			"bob@x":   {Email: "bob@x", Name: "B", Commits: 1, ActiveDays: 1, FilesTouched: 2},
			"carol@x": {Email: "carol@x", Name: "C", Commits: 1, ActiveDays: 1, FilesTouched: 2},
		},
	}
	profiles := DevProfiles(ds, "alice@x", 0)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	collabs := profiles[0].Collaborators
	if len(collabs) != 2 {
		t.Fatalf("collabs = %d, want 2", len(collabs))
	}
	// SharedFiles ties at 2; SharedLines distinguishes Carol (1000) from Bob (2).
	if collabs[0].Email != "carol@x" || collabs[0].SharedLines != 1000 {
		t.Errorf("top collaborator = {%s, lines=%d}, want {carol@x, 1000}",
			collabs[0].Email, collabs[0].SharedLines)
	}
	if collabs[1].Email != "bob@x" || collabs[1].SharedLines != 2 {
		t.Errorf("second collaborator = {%s, lines=%d}, want {bob@x, 2}",
			collabs[1].Email, collabs[1].SharedLines)
	}
	// SharedFiles must still be populated for both.
	for _, c := range collabs {
		if c.SharedFiles != 2 {
			t.Errorf("%s SharedFiles = %d, want 2", c.Email, c.SharedFiles)
		}
	}
}

func TestFileCouplingTertiaryTiebreaker(t *testing.T) {
	// Two pairs with identical CoChanges AND identical CouplingPct must
	// still order deterministically by file name.
	ds := &Dataset{
		couplingPairs: map[filePair]int{
			{a: "m/x.go", b: "m/y.go"}: 5,
			{a: "a/x.go", b: "a/y.go"}: 5,
		},
		couplingFileChanges: map[string]int{
			"m/x.go": 10, "m/y.go": 10,
			"a/x.go": 10, "a/y.go": 10,
		},
	}
	// CouplingPct ties at 50% for both. CoChanges ties at 5.
	first := FileCoupling(ds, 0, 1)
	for i := 0; i < 20; i++ {
		got := FileCoupling(ds, 0, 1)
		if got[0].FileA != first[0].FileA {
			t.Fatalf("iter %d non-deterministic: %q vs %q", i, got[0].FileA, first[0].FileA)
		}
	}
	if first[0].FileA != "a/x.go" {
		t.Errorf("top FileA = %q, want a/x.go (path asc)", first[0].FileA)
	}
}

func TestDeveloperNetworkTertiaryTiebreaker(t *testing.T) {
	// Two dev pairs with identical SharedLines AND SharedFiles must order
	// by (DevA, DevB) asc.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"f1.go": {devLines: map[string]int64{"m@x": 10, "n@x": 10}},
			"f2.go": {devLines: map[string]int64{"a@x": 10, "b@x": 10}},
		},
	}
	// Both pairs: SharedFiles=1, SharedLines=10.
	first := DeveloperNetwork(ds, 0, 1)
	for i := 0; i < 20; i++ {
		got := DeveloperNetwork(ds, 0, 1)
		if got[0].DevA != first[0].DevA {
			t.Fatalf("iter %d non-deterministic: %q vs %q", i, got[0].DevA, first[0].DevA)
		}
	}
	if first[0].DevA != "a@x" || first[0].DevB != "b@x" {
		t.Errorf("top pair = {%s,%s}, want {a@x,b@x} (pair asc)", first[0].DevA, first[0].DevB)
	}
}

func TestChurnRiskTertiaryTiebreaker(t *testing.T) {
	// Two files with identical RecentChurn AND BusFactor must order by path asc.
	t1 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	ds := &Dataset{
		Earliest: t1,
		Latest:   t1,
		files: map[string]*fileEntry{
			"z.go": {commits: 1, recentChurn: 100, devLines: map[string]int64{"a@x": 50}, monthChurn: map[string]int64{"2024-01": 100}, firstChange: t1, lastChange: t1},
			"a.go": {commits: 1, recentChurn: 100, devLines: map[string]int64{"a@x": 50}, monthChurn: map[string]int64{"2024-01": 100}, firstChange: t1, lastChange: t1},
			"m.go": {commits: 1, recentChurn: 100, devLines: map[string]int64{"a@x": 50}, monthChurn: map[string]int64{"2024-01": 100}, firstChange: t1, lastChange: t1},
		},
	}
	first := ChurnRisk(ds, 0)
	for i := 0; i < 20; i++ {
		got := ChurnRisk(ds, 0)
		for j := range got {
			if got[j].Path != first[j].Path {
				t.Fatalf("iter %d [%d]: %q vs %q", i, j, got[j].Path, first[j].Path)
			}
		}
	}
	// All three tie on RecentChurn (100) and BusFactor (1). Expect a, m, z.
	want := []string{"a.go", "m.go", "z.go"}
	for i, p := range want {
		if first[i].Path != p {
			t.Errorf("[%d] = %q, want %q (tertiary path-asc tiebreak)", i, first[i].Path, p)
		}
	}
}

func TestDevProfilesInnerSortsDeterministic(t *testing.T) {
	// Profile-internal slices (topFiles, scope, collaborators) are assembled
	// via map iteration. Before the tiebreaker fix they could land in any
	// order on ties, producing different top-N per run. This test forces
	// multiple internal ties and asserts stability across 20 invocations.
	t1 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	ds := &Dataset{
		CommitCount: 4,
		Earliest:    t1,
		Latest:      t1,
		commits: map[string]*commitEntry{
			"sha1": {email: "alice@x", date: t1, add: 10, del: 5, files: 4},
			"sha2": {email: "bob@x", date: t1, add: 10, del: 5, files: 1},
			"sha3": {email: "carol@x", date: t1, add: 10, del: 5, files: 1},
			"sha4": {email: "dan@x", date: t1, add: 10, del: 5, files: 1},
		},
		contributors: map[string]*ContributorStat{
			"alice@x": {Email: "alice@x", Name: "A", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5, FilesTouched: 4},
			"bob@x":   {Email: "bob@x", Name: "B", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5, FilesTouched: 1},
			"carol@x": {Email: "carol@x", Name: "C", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5, FilesTouched: 1},
			"dan@x":   {Email: "dan@x", Name: "D", Commits: 1, ActiveDays: 1, Additions: 10, Deletions: 5, FilesTouched: 1},
		},
		// Alice touches 4 files across 3 dirs; each of bob/carol/dan overlaps
		// with Alice on exactly one file (equal collaborator strength).
		files: map[string]*fileEntry{
			"a/one.go": {commits: 2, devLines: map[string]int64{"alice@x": 15, "bob@x": 15}, devCommits: map[string]int{"alice@x": 1, "bob@x": 1}},
			"a/two.go": {commits: 2, devLines: map[string]int64{"alice@x": 15, "carol@x": 15}, devCommits: map[string]int{"alice@x": 1, "carol@x": 1}},
			"b/one.go": {commits: 2, devLines: map[string]int64{"alice@x": 15, "dan@x": 15}, devCommits: map[string]int{"alice@x": 1, "dan@x": 1}},
			"c/one.go": {commits: 1, devLines: map[string]int64{"alice@x": 15}, devCommits: map[string]int{"alice@x": 1}},
		},
	}

	// Run DevProfiles(ds, "alice@x", 0) 20 times; all three inner slices must
	// be identical across iterations.
	first := DevProfiles(ds, "alice@x", 0)
	if len(first) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(first))
	}
	for i := 0; i < 20; i++ {
		next := DevProfiles(ds, "alice@x", 0)[0]
		for k, f := range next.TopFiles {
			if f.Path != first[0].TopFiles[k].Path {
				t.Fatalf("TopFiles iter %d: [%d] got %q, first %q", i, k, f.Path, first[0].TopFiles[k].Path)
			}
		}
		for k, s := range next.Scope {
			if s.Dir != first[0].Scope[k].Dir {
				t.Fatalf("Scope iter %d: [%d] got %q, first %q", i, k, s.Dir, first[0].Scope[k].Dir)
			}
		}
		for k, c := range next.Collaborators {
			if c.Email != first[0].Collaborators[k].Email {
				t.Fatalf("Collaborators iter %d: [%d] got %q, first %q", i, k, c.Email, first[0].Collaborators[k].Email)
			}
		}
	}

	// Verify the tiebreakers produced the expected asc order.
	// TopFiles all tied at churn=15; path asc expected.
	wantFiles := []string{"a/one.go", "a/two.go", "b/one.go", "c/one.go"}
	for i, want := range wantFiles {
		if first[0].TopFiles[i].Path != want {
			t.Errorf("TopFiles[%d] = %q, want %q (path-asc tiebreak)", i, first[0].TopFiles[i].Path, want)
		}
	}
	// Scope: dirs "a" (2 files), "b" (1), "c" (1) — dirs "b" and "c" tie at
	// 1 file and must appear in dir-asc order.
	if first[0].Scope[1].Dir != "b" || first[0].Scope[2].Dir != "c" {
		t.Errorf("Scope dir-asc tiebreak failed: got %+v", first[0].Scope)
	}
	// Collaborators: bob, carol, dan all share exactly 1 file with alice;
	// email-asc order.
	wantCollab := []string{"bob@x", "carol@x", "dan@x"}
	for i, want := range wantCollab {
		if first[0].Collaborators[i].Email != want {
			t.Errorf("Collaborators[%d] = %q, want %q", i, first[0].Collaborators[i].Email, want)
		}
	}
}

func TestBusFactorOrderDeterministicUnderTies(t *testing.T) {
	// 6 files, all with bus factor = 1. Without a deterministic tiebreaker
	// the top-N varies between invocations because Go map iteration is
	// randomized. The CLI and HTML report call BusFactor separately and
	// must return the same ordering — this test guards against regression.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"z/last.go":  {commits: 1, devLines: map[string]int64{"a@x": 10}},
			"a/first.go": {commits: 1, devLines: map[string]int64{"b@x": 10}},
			"m/mid.go":   {commits: 1, devLines: map[string]int64{"c@x": 10}},
			"a/second.go": {commits: 1, devLines: map[string]int64{"d@x": 10}},
			"b/one.go":   {commits: 1, devLines: map[string]int64{"e@x": 10}},
			"b/two.go":   {commits: 1, devLines: map[string]int64{"f@x": 10}},
		},
	}
	first := BusFactor(ds, 3)
	// Run 20 times; order must be identical every time.
	for i := 0; i < 20; i++ {
		run := BusFactor(ds, 3)
		for j := range run {
			if run[j].Path != first[j].Path {
				t.Fatalf("iteration %d index %d: got %q, want %q",
					i, j, run[j].Path, first[j].Path)
			}
		}
	}
	// Expected ordering: all bf=1, so tiebreaker is path asc.
	want := []string{"a/first.go", "a/second.go", "b/one.go"}
	for i, p := range want {
		if first[i].Path != p {
			t.Errorf("[%d] got %q, want %q", i, first[i].Path, p)
		}
	}
}

func TestBusFactorTopDevsDeterministicUnderTies(t *testing.T) {
	// Two devs touching the same file with equal line counts. Without an
	// email tiebreaker inside the per-file sort, TopDevs order flips between
	// runs because the iteration that builds the slice comes from a random
	// map ordering. Small binary assets (.gif, .png, one-liner configs) in
	// real repos frequently hit this case.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"tied.bin": {commits: 2, devLines: map[string]int64{
				"zeta@x": 5,
				"alpha@x": 5,
			}},
		},
	}
	first := BusFactor(ds, 10)
	if len(first) != 1 {
		t.Fatalf("expected 1 result, got %d", len(first))
	}
	// Run 30 times; TopDevs ordering must stay identical.
	for i := 0; i < 30; i++ {
		run := BusFactor(ds, 10)
		if len(run[0].TopDevs) != len(first[0].TopDevs) {
			t.Fatalf("iter %d: TopDevs length drift: got %v, want %v",
				i, run[0].TopDevs, first[0].TopDevs)
		}
		for j, e := range run[0].TopDevs {
			if e != first[0].TopDevs[j] {
				t.Fatalf("iter %d idx %d: got %q, want %q (full: %v vs %v)",
					i, j, e, first[0].TopDevs[j], run[0].TopDevs, first[0].TopDevs)
			}
		}
	}
	// Expected tiebreaker: email asc → alpha before zeta.
	if first[0].TopDevs[0] != "alpha@x" {
		t.Errorf("TopDevs[0] = %q, want %q", first[0].TopDevs[0], "alpha@x")
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
	result := FileCoupling(ds, 10, 1)

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
	result := FileCoupling(ds, 10, 20) // min 20 co-changes
	if len(result) != 0 {
		t.Errorf("expected empty with high threshold, got %d", len(result))
	}
}

func TestChurnRisk(t *testing.T) {
	ds := makeDataset()
	result := ChurnRisk(ds, 10)

	if len(result) != 3 {
		t.Fatalf("len = %d", len(result))
	}
	// Within the same label bucket the secondary sort is RecentChurn
	// descending. The fixture's three files land in the same label, so
	// expected order is main.go (100) → util.go (50) → readme.md (10).
	for i := 1; i < len(result); i++ {
		if result[i-1].Label == result[i].Label &&
			result[i-1].RecentChurn < result[i].RecentChurn {
			t.Errorf("not sorted by RecentChurn desc within label at index %d: %.1f < %.1f",
				i, result[i-1].RecentChurn, result[i].RecentChurn)
		}
	}
	if result[0].Path != "main.go" {
		t.Errorf("top file = %q, want main.go", result[0].Path)
	}
}

func TestChurnRiskLabelPriority(t *testing.T) {
	// Labels must sort in this actionability order regardless of
	// RecentChurn — a legacy-hotspot with RC=50 outranks an active file
	// with RC=50000. See the sort comment in stats.go.
	want := []string{
		"legacy-hotspot", "silo", "active-core", "active", "cold",
	}
	for i := 1; i < len(want); i++ {
		if churnRiskLabelPriority(want[i-1]) >= churnRiskLabelPriority(want[i]) {
			t.Errorf("priority(%q)=%d must be less than priority(%q)=%d",
				want[i-1], churnRiskLabelPriority(want[i-1]),
				want[i], churnRiskLabelPriority(want[i]))
		}
	}
	// Unknown labels must sort after all known ones so the named risks
	// always lead the table even if a new classifier is added upstream.
	if churnRiskLabelPriority("future-label") <= churnRiskLabelPriority("cold") {
		t.Errorf("unknown label should sort after cold")
	}
}

func TestChurnRiskSortLegacyBeatsActiveDespiteHigherChurn(t *testing.T) {
	// A legacy-hotspot with low RecentChurn must rank above an active
	// file with huge RecentChurn — otherwise the top-N display hides the
	// classified risks behind unremarkable active code (the WordPress bug
	// that motivated the label-first sort).
	results := []ChurnRiskResult{
		{Path: "active.go", Label: "active", RecentChurn: 10000, BusFactor: 4},
		{Path: "legacy.go", Label: "legacy-hotspot", RecentChurn: 50, BusFactor: 1},
	}
	sort.Slice(results, func(i, j int) bool {
		pi, pj := churnRiskLabelPriority(results[i].Label), churnRiskLabelPriority(results[j].Label)
		if pi != pj {
			return pi < pj
		}
		if results[i].RecentChurn != results[j].RecentChurn {
			return results[i].RecentChurn > results[j].RecentChurn
		}
		return results[i].Path < results[j].Path
	})
	if results[0].Path != "legacy.go" {
		t.Errorf("legacy-hotspot must outrank active file, got top=%q", results[0].Path)
	}
}

func TestChurnRiskLabelCountsMatchesChurnRisk(t *testing.T) {
	// Invariant: the light-weight ChurnRiskLabelCounts must agree with
	// counting labels from the full ChurnRisk output. If someone later
	// edits classifyFile, computeBands, the bus-factor rule, or the
	// coldChurn threshold in only one of the two paths, this test
	// catches the drift.
	ds := makeDataset()

	lightCounts := ChurnRiskLabelCounts(ds)

	heavy := ChurnRisk(ds, 0)
	heavyCounts := make(map[string]int)
	for _, r := range heavy {
		heavyCounts[r.Label]++
	}

	// Every label present in either map must have matching counts.
	for label, want := range heavyCounts {
		if got := lightCounts[label]; got != want {
			t.Errorf("label %q: light=%d heavy=%d", label, got, want)
		}
	}
	for label, got := range lightCounts {
		if want := heavyCounts[label]; got != want {
			t.Errorf("label %q: light=%d heavy=%d (light-only)", label, got, want)
		}
	}
}

func TestClassifyFile(t *testing.T) {
	cases := []struct {
		name         string
		recentChurn  float64
		lowChurn     float64
		bf, ageDays  int
		trend        float64
		want         string
	}{
		{"cold: churn below threshold", 5, 50, 1, 365, 1.0, "cold"},
		{"active: shared ownership", 200, 50, 3, 365, 1.0, "active"},
		{"active-core: new code, single author", 200, 50, 1, 30, 1.0, "active-core"},
		{"silo: old + concentrated + stable", 200, 50, 2, 365, 1.0, "silo"},
		{"silo: old + concentrated + growing", 200, 50, 2, 365, 2.0, "silo"},
		{"legacy-hotspot: old + concentrated + declining", 200, 50, 1, 365, 0.3, "legacy-hotspot"},
		{"cold wins over everything when churn low", 10, 50, 1, 365, 0.1, "cold"},
	}
	// Use defaultBands so the old absolute constants (180d age, 0.5 trend)
	// still drive classification — this test covers the fallback path.
	bands := defaultBands()
	for _, c := range cases {
		got := classifyFile(c.recentChurn, c.lowChurn, c.bf, c.ageDays, c.trend, bands)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestComputeBandsFallsBackBelowMinSample(t *testing.T) {
	// Fewer than classifyMinSample files → percentiles are unreliable,
	// so bands must fall back to the absolute constants and report
	// HasPercentiles() == false.
	ages := []int{10, 200, 400}
	trends := []float64{0.2, 1.0, 2.0}
	b := computeBands(ages, trends)
	if b.OldAgeDays != classifyOldAgeDays {
		t.Errorf("fallback OldAgeDays = %d, want %d", b.OldAgeDays, classifyOldAgeDays)
	}
	if b.DecliningTrend != classifyDecliningTrend {
		t.Errorf("fallback DecliningTrend = %.2f, want %.2f", b.DecliningTrend, classifyDecliningTrend)
	}
	if b.HasPercentiles() {
		t.Errorf("HasPercentiles should be false when sample < classifyMinSample")
	}
}

func TestComputeBandsAdaptive(t *testing.T) {
	// With 10 files spanning a wide range, P75 of ages and P25 of trends
	// should replace the absolute constants. Nearest-rank indexing:
	//   idx(p, n) = ceil(p*n/100) - 1
	ages := []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 1000}
	// P75 over 10 items: ceil(750/100)-1 = 7 → sorted[7] = 80
	trends := []float64{0.1, 0.3, 0.5, 0.7, 0.9, 1.0, 1.1, 1.2, 1.5, 2.0}
	// P25 over 10 items: ceil(250/100)-1 = 2 → sorted[2] = 0.5
	b := computeBands(ages, trends)
	if !b.HasPercentiles() {
		t.Fatal("HasPercentiles should be true for 10-item sample")
	}
	if b.OldAgeDays != 80 {
		t.Errorf("adaptive OldAgeDays = %d, want 80 (P75 of 10 values, nearest-rank)", b.OldAgeDays)
	}
	if b.DecliningTrend != 0.5 {
		t.Errorf("adaptive DecliningTrend = %.2f, want 0.5 (P25 nearest-rank)", b.DecliningTrend)
	}
}

func TestNearestRankIndex(t *testing.T) {
	cases := []struct {
		p, n, want int
	}{
		// Exact percentile positions (no ceiling kicks in).
		{25, 4, 0},   // ceil(1.0)-1 = 0
		{50, 4, 1},   // ceil(2.0)-1 = 1
		{75, 4, 2},   // ceil(3.0)-1 = 2
		{100, 4, 3},  // ceil(4.0)-1 = 3

		// Ceiling-driven cases — the classic pitfall the previous
		// implementation missed.
		{75, 10, 7},  // ceil(7.5)-1 = 7 (old impl returned 6)
		{25, 10, 2},  // ceil(2.5)-1 = 2
		{25, 7, 1},   // ceil(1.75)-1 = 1
		{75, 11, 8},  // ceil(8.25)-1 = 8

		// Edges: p=0 clamps to 0, p=100 to n-1, n=1 collapses both.
		{0, 10, 0},
		{100, 10, 9},
		{50, 1, 0},
		{99, 1, 0},
	}
	for _, c := range cases {
		if got := nearestRankIndex(c.p, c.n); got != c.want {
			t.Errorf("nearestRankIndex(p=%d, n=%d) = %d, want %d", c.p, c.n, got, c.want)
		}
	}
}

func TestRankInt(t *testing.T) {
	sorted := []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	cases := []struct {
		v    int
		want int // percentile rank (% of values strictly below v)
	}{
		{5, 0},    // below min → 0/10 = 0%
		{10, 0},   // equal to min → 0 below
		{50, 40},  // 4 below → 40%
		{55, 50},  // 5 below → 50%
		{100, 90}, // 9 below → 90%
		{999, 100},
	}
	for _, c := range cases {
		if got := rankInt(sorted, c.v); got != c.want {
			t.Errorf("rankInt(%d) = %d, want %d", c.v, got, c.want)
		}
	}
}

func TestChurnRiskPercentilesPopulatedOnLargeDataset(t *testing.T) {
	// 10 files with varied ages and monthly churn histories so the
	// adaptive path fires and percentiles are populated in results.
	ds := &Dataset{
		Earliest: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Latest:   time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		files:    map[string]*fileEntry{},
	}
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("f%02d.go", i)
		firstChange := ds.Latest.AddDate(0, 0, -(30 + i*30))
		ds.files[path] = &fileEntry{
			commits:     5,
			additions:   int64(10 * (i + 1)),
			deletions:   int64(5 * (i + 1)),
			recentChurn: float64(10 * (i + 1)),
			firstChange: firstChange,
			lastChange:  ds.Latest,
			devLines:    map[string]int64{"a@x": int64(15 * (i + 1))},
			monthChurn:  map[string]int64{"2024-05": int64(10 * (i + 1))},
		}
	}
	results := ChurnRisk(ds, 0)
	if len(results) != 10 {
		t.Fatalf("got %d results, want 10", len(results))
	}
	for _, r := range results {
		if r.AgePercentile == nil || r.TrendPercentile == nil {
			t.Errorf("%s: percentiles should be populated on large dataset", r.Path)
			continue
		}
		if *r.AgePercentile < 0 || *r.AgePercentile > 100 {
			t.Errorf("%s: AgePercentile out of range: %d", r.Path, *r.AgePercentile)
		}
		if *r.TrendPercentile < 0 || *r.TrendPercentile > 100 {
			t.Errorf("%s: TrendPercentile out of range: %d", r.Path, *r.TrendPercentile)
		}
	}
}

func TestChurnRiskAdaptiveDormantP25ZeroFlooring(t *testing.T) {
	// Mature-repo shape: half the files are dormant (trend=0 via the
	// earlier-only path), half are active. Without the declining-trend
	// floor, P25 collapses to 0 and `trend < 0` never fires — dormant
	// concentrated files would silently be misclassified as silo
	// instead of legacy-hotspot, hiding the strongest alarm.
	//
	// The floor guarantees the threshold is strictly positive so the
	// signal survives the adaptive-mode switch.
	latest := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	ds := &Dataset{
		// Wide span so "earlier" window exists for the active files.
		Earliest: latest.AddDate(-3, 0, 0),
		Latest:   latest,
		files:    map[string]*fileEntry{},
	}

	// 6 dormant files — single activity 2 years ago, nothing since.
	// churnTrend returns 0 for these (earlier-only bucket).
	for i := 0; i < 6; i++ {
		path := fmt.Sprintf("dormant/old_%02d.go", i)
		ds.files[path] = &fileEntry{
			commits:     3,
			additions:   200,
			deletions:   100,
			recentChurn: 200, // above cold threshold
			firstChange: latest.AddDate(-2, 0, 0),
			lastChange:  latest.AddDate(-2, 0, 0),
			devLines:    map[string]int64{"solo@x": 300}, // bf=1
			monthChurn:  map[string]int64{"2022-06": 300},
		}
	}
	// 6 active files — current activity, fresh history.
	for i := 0; i < 6; i++ {
		path := fmt.Sprintf("active/new_%02d.go", i)
		ds.files[path] = &fileEntry{
			commits:     3,
			additions:   200,
			deletions:   100,
			recentChurn: 200,
			firstChange: latest.AddDate(-2, 0, 0),
			lastChange:  latest,
			devLines:    map[string]int64{"a@x": 100, "b@x": 100, "c@x": 100}, // bf=3
			monthChurn:  map[string]int64{"2024-05": 300},
		}
	}

	results := ChurnRisk(ds, 0)
	var legacyCount int
	var dormantLabels []string
	for _, r := range results {
		if r.Label == "legacy-hotspot" {
			legacyCount++
		}
		if strings.HasPrefix(r.Path, "dormant/") {
			dormantLabels = append(dormantLabels, r.Label)
		}
	}
	if legacyCount == 0 {
		t.Errorf("expected dormant+concentrated files to be flagged legacy-hotspot; got 0 "+
			"(dormant labels: %v). P25 likely collapsed to 0 without the floor.", dormantLabels)
	}
	// Sanity: every dormant file (bf=1, old, trend=0) should be legacy-hotspot.
	for _, lbl := range dormantLabels {
		if lbl != "legacy-hotspot" {
			t.Errorf("dormant file got label %q, want legacy-hotspot", lbl)
		}
	}
}

func TestClassifyFileFloorBoundary(t *testing.T) {
	// Strict-less-than check with the adaptive floor means files whose
	// trend is exactly at the floor are NOT declining; files strictly
	// below it are. This pins the comparison semantics so a future
	// refactor doesn't silently flip < to <=.
	bands := classifyBands{
		OldAgeDays:     100,
		DecliningTrend: adaptiveDecliningTrendFloor, // 0.01
	}
	// All cases share old age + concentrated ownership + non-cold churn
	// so the trend predicate drives the label.
	cases := []struct {
		name  string
		trend float64
		want  string
	}{
		{"trend 0 (dormant, earlier-only) → declining", 0.0, "legacy-hotspot"},
		{"trend 0.001 below floor → declining", 0.001, "legacy-hotspot"},
		{"trend exactly at floor 0.01 → NOT declining", 0.01, "silo"},
		{"trend 0.011 above floor → NOT declining", 0.011, "silo"},
		{"trend 1.0 flat → NOT declining", 1.0, "silo"},
	}
	for _, c := range cases {
		got := classifyFile(100, 50, 1, 200, c.trend, bands)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestChurnRiskAdaptiveDegenerateTrendDistribution(t *testing.T) {
	// Degenerate trend case: every file's history fits entirely inside the
	// trend window (earlier bucket is empty), so churnTrend returns the
	// sentinel 1.0 for all of them. The adaptive P25 then collapses onto
	// 1.0 and the "declining" check (`trend < 1.0`) matches nobody — no
	// file can reach legacy-hotspot via the trend predicate. Old +
	// concentrated files fall through to silo.
	//
	// This test pins that behavior so future refactors don't silently
	// flip it. The degenerate shape is a known limitation documented in
	// METRICS.md; the code is not a bug, but the outcome is surprising
	// enough that a regression test is worth the 30 lines.
	latest := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	ds := &Dataset{
		Earliest: latest.AddDate(0, -2, 0), // 2-month span — fits entirely in the 3-month trend window
		Latest:   latest,
		files:    map[string]*fileEntry{},
	}
	for i := 0; i < 12; i++ {
		path := fmt.Sprintf("old/file_%02d.go", i)
		ds.files[path] = &fileEntry{
			commits:     3,
			additions:   200,
			deletions:   100,
			recentChurn: 500, // well above cold threshold for all
			// Old enough (> 1 year) that they'd trip classifyOldAgeDays if we
			// were on the absolute path, AND above the adaptive P75 for this
			// dataset regardless of how ages are distributed.
			firstChange: latest.AddDate(-2, 0, -(i * 30)),
			lastChange:  latest,
			devLines:    map[string]int64{"solo@x": 300}, // bf=1 for everyone
			// Entire history fits inside the trend window → churnTrend
			// returns 1.0 for every file (its flat-signal sentinel).
			monthChurn: map[string]int64{"2024-05": 300},
		}
	}
	results := ChurnRisk(ds, 0)
	if len(results) != 12 {
		t.Fatalf("got %d results, want 12", len(results))
	}
	legacyCount, siloCount := 0, 0
	for _, r := range results {
		switch r.Label {
		case "legacy-hotspot":
			legacyCount++
		case "silo":
			siloCount++
		}
		// Trend of 1.0 means P25 of a constant-1 distribution is also 1,
		// so `trend < 1.0` never fires and no file is declining.
		if r.Label == "legacy-hotspot" {
			t.Errorf("%s: unexpected legacy-hotspot — trend distribution is degenerate (all 1.0), "+
				"no file should be classified as declining", r.Path)
		}
	}
	if siloCount == 0 {
		t.Errorf("expected at least one silo (old + concentrated + not declining), got legacy=%d silo=%d",
			legacyCount, siloCount)
	}
}

func TestChurnRiskPercentilesNotSetOnSmallDataset(t *testing.T) {
	// 3 files → below classifyMinSample. Percentile pointers should be nil
	// so JSON consumers see omitted fields, not a `-1` sentinel.
	ds := &Dataset{
		Earliest: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		Latest:   time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		files:    map[string]*fileEntry{},
	}
	for i, p := range []string{"a.go", "b.go", "c.go"} {
		ds.files[p] = &fileEntry{
			commits: 1, recentChurn: float64(10 * (i + 1)),
			firstChange: ds.Latest.AddDate(0, 0, -100),
			lastChange:  ds.Latest,
			devLines:    map[string]int64{"a@x": 10},
			monthChurn:  map[string]int64{"2024-05": 10},
		}
	}
	results := ChurnRisk(ds, 0)
	for _, r := range results {
		if r.AgePercentile != nil || r.TrendPercentile != nil {
			t.Errorf("%s: percentiles should be nil for small dataset, got age=%v trend=%v",
				r.Path, r.AgePercentile, r.TrendPercentile)
		}
	}
}

func TestLabelWithPercentile(t *testing.T) {
	p := func(v int) *int { return &v }
	cases := []struct {
		name, label         string
		age, trend          *int
		want                string
	}{
		{"both nil → bare label", "legacy-hotspot", nil, nil, "legacy-hotspot"},
		{"age nil → bare label", "silo", nil, p(10), "silo"},
		{"trend nil → bare label", "active", p(75), nil, "active"},
		{"both set → suffix", "legacy-hotspot", p(92), p(8), "legacy-hotspot (age P92, trend P8)"},
		{"zero values render", "active-core", p(0), p(0), "active-core (age P0, trend P0)"},
		{"three-digit value renders unpadded", "active", p(100), p(100), "active (age P100, trend P100)"},
	}
	for _, c := range cases {
		got := LabelWithPercentile(c.label, c.age, c.trend)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestActivityBucketUsesUTC(t *testing.T) {
	// Two commits at the same UTC instant but parsed with different TZs.
	// Both must fall into the same month bucket — without the UTC fix they
	// would split across months depending on the author's offset.
	jsonl := `{"type":"commit","sha":"a","author_name":"A","author_email":"a@x","author_date":"2024-03-31T23:00:00-05:00","additions":5,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"a","path_current":"x.go","additions":5,"deletions":0}
{"type":"commit","sha":"b","author_name":"B","author_email":"b@x","author_date":"2024-04-01T06:00:00+02:00","additions":3,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"b","path_current":"x.go","additions":3,"deletions":0}
`
	// Same UTC instant (2024-04-01T04:00Z) expressed in -05:00 and +02:00.
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	buckets := ActivityOverTime(ds, "month")
	if len(buckets) != 1 {
		t.Fatalf("got %d buckets, want 1 (same UTC instant): %+v", len(buckets), buckets)
	}
	if buckets[0].Period != "2024-04" {
		t.Errorf("bucket period = %q, want 2024-04", buckets[0].Period)
	}
}

func TestChurnTrend(t *testing.T) {
	latest := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	// "earliest" far before the trend window so the short-span guard does
	// not fire for these cases.
	earliestWide := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	// No data → neutral.
	if got := churnTrend(map[string]int64{}, earliestWide, latest); got != 1 {
		t.Errorf("empty → %.2f, want 1", got)
	}

	// Only recent activity (nothing earlier) → growing signal (2).
	recentOnly := map[string]int64{"2024-05": 100, "2024-06": 100}
	if got := churnTrend(recentOnly, earliestWide, latest); got != 2 {
		t.Errorf("recent-only → %.2f, want 2", got)
	}

	// Declining: old months dominate.
	declining := map[string]int64{
		"2024-01": 1000, "2024-02": 1000, // earlier
		"2024-05": 50, "2024-06": 50, // recent
	}
	if got := churnTrend(declining, earliestWide, latest); got >= 0.5 {
		t.Errorf("declining trend = %.2f, want < 0.5", got)
	}

	// Stability across day-of-month.
	data := map[string]int64{"2024-02": 100, "2024-03": 100, "2024-06": 100}
	early := churnTrend(data, earliestWide, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	late := churnTrend(data, earliestWide, time.Date(2024, 6, 30, 23, 0, 0, 0, time.UTC))
	if early != late {
		t.Errorf("trend depends on day-of-month: early=%.2f late=%.2f", early, late)
	}

	// Short-span guard: dataset whose entire span is inside the trend window.
	// earliest=2024-05, latest=2024-06, window=3 months → cutoff=2024-03.
	// earliestKey="2024-05" >= cutoffKey="2024-03" → no "earlier" bucket.
	// Previously returned 2 (falsely "growing from nothing") for every file;
	// now must return 1 (no signal).
	shortSpan := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	if got := churnTrend(recentOnly, shortSpan, latest); got != 1 {
		t.Errorf("short-span → %.2f, want 1 (no signal when window < trend window)", got)
	}

	// Single-month histories used to short-circuit to 1 via len(monthChurn)<2,
	// silencing the two strongest trend signals: earlier-only (declined to
	// nothing) and recent-only (grew from nothing). Both must now come
	// through so old concentrated files can be classified as legacy-hotspot.

	// Earlier-only: a single month well before the cutoff.
	earlierOnly := map[string]int64{"2023-05": 500}
	if got := churnTrend(earlierOnly, earliestWide, latest); got != 0 {
		t.Errorf("earlier-only single-month → %.2f, want 0 (declined to nothing)", got)
	}

	// Recent-only: a single month inside the trend window.
	recentSingle := map[string]int64{"2024-06": 100}
	if got := churnTrend(recentSingle, earliestWide, latest); got != 2 {
		t.Errorf("recent-only single-month → %.2f, want 2 (grew from nothing)", got)
	}

	// Zero-value entry (possible when a month only contains renames with
	// no content change: cf.Additions + cf.Deletions = 0). Both buckets
	// end up zero — defensive fallthrough must return 1, not 0 or 2.
	zeroOnly := map[string]int64{"2024-05": 0}
	if got := churnTrend(zeroOnly, earliestWide, latest); got != 1 {
		t.Errorf("zero-value single-month → %.2f, want 1 (no signal)", got)
	}
}

func TestChurnRiskLegacyHotspotFromSingleOldMonth(t *testing.T) {
	// Integration: file touched only in one old month, bf=1, age > 180.
	// Before the churnTrend fix (len<2 guard), this file returned
	// trend=1 (stable) and landed at label=silo. After the fix,
	// trend=0 (declined to nothing) routes it through the legacy-hotspot
	// branch. Pins the end-to-end wiring so a future regression in
	// churnTrend, classifyFile, or ChurnRisk can't silently send such
	// files back to silo.
	t1 := time.Date(2020, 1, 15, 10, 0, 0, 0, time.UTC)
	latest := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	ds := &Dataset{
		Earliest: t1,
		Latest:   latest,
		files: map[string]*fileEntry{
			"old.go": {
				commits:     1,
				recentChurn: 100, // kept high enough to clear the cold threshold
				devLines:    map[string]int64{"alice@x": 500},
				monthChurn:  map[string]int64{"2020-01": 500},
				firstChange: t1,
				lastChange:  t1,
			},
		},
	}
	results := ChurnRisk(ds, 0)
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Label != "legacy-hotspot" {
		t.Errorf("Label = %q, want legacy-hotspot (single-old-month + bf=1 + age>180)", r.Label)
	}
	if r.Trend != 0 {
		t.Errorf("Trend = %.2f, want 0 (earlier-only — the fix)", r.Trend)
	}
	if r.BusFactor != 1 {
		t.Errorf("BusFactor = %d, want 1", r.BusFactor)
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

func TestDeveloperNetworkSharedLinesWeighsRealOverlap(t *testing.T) {
	// Two files, both touched by Alice and Bob.
	// File 1: Alice=1 line, Bob=1000 lines → real overlap = 1 (the minimum)
	// File 2: Alice=500, Bob=500 → real overlap = 500
	// A raw "shared files" count shows 2 in both extremes; SharedLines
	// correctly reports 501, not 1001 or 2000.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"f1.go": {devLines: map[string]int64{"alice@x": 1, "bob@x": 1000}},
			"f2.go": {devLines: map[string]int64{"alice@x": 500, "bob@x": 500}},
		},
	}
	edges := DeveloperNetwork(ds, 10, 1)
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	e := edges[0]
	if e.SharedFiles != 2 {
		t.Errorf("SharedFiles = %d, want 2", e.SharedFiles)
	}
	if e.SharedLines != 501 {
		t.Errorf("SharedLines = %d, want 501 (min(1,1000)+min(500,500))", e.SharedLines)
	}
}

func TestDeveloperNetworkMinThreshold(t *testing.T) {
	ds := makeDataset()
	result := DeveloperNetwork(ds, 10, 100) // min 100 shared files
	if len(result) != 0 {
		t.Errorf("expected empty with high threshold, got %d", len(result))
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

func TestCouplingExcludesMechanicalRefactor(t *testing.T) {
	// A commit touching 10 files with ~1 line each (a rename or format pass)
	// would generate 45 coupling pairs and swamp the real signal. The heuristic
	// skips pair accumulation for such commits.
	var lines []string
	// c1: mechanical refactor — 10 files, 1 line each, mean churn = 1 < 5
	lines = append(lines, `{"type":"commit","sha":"c1","author_name":"A","author_email":"a@x","author_date":"2024-01-01T10:00:00Z","additions":10,"deletions":0,"files_changed":10}`)
	for i := 0; i < 10; i++ {
		lines = append(lines,
			fmt.Sprintf(`{"type":"commit_file","commit":"c1","path_current":"f%d.go","status":"M","additions":1,"deletions":0}`, i))
	}
	// c2: real multi-file change — 3 files, 100 lines each
	lines = append(lines, `{"type":"commit","sha":"c2","author_name":"A","author_email":"a@x","author_date":"2024-01-02T10:00:00Z","additions":300,"deletions":0,"files_changed":3}`)
	for i := 0; i < 3; i++ {
		lines = append(lines,
			fmt.Sprintf(`{"type":"commit_file","commit":"c2","path_current":"f%d.go","status":"M","additions":100,"deletions":0}`, i))
	}
	jsonl := strings.Join(lines, "\n") + "\n"

	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	// c1 must NOT contribute pairs, c2 must. For files f0..f2 the only pair
	// counts should come from c2 (3 pairs: {f0,f1}, {f0,f2}, {f1,f2}).
	for pair, count := range ds.couplingPairs {
		if count > 1 {
			t.Errorf("pair %v has count %d — mechanical refactor leaked into coupling", pair, count)
		}
	}
	p01 := filePair{a: "f0.go", b: "f1.go"}
	if ds.couplingPairs[p01] != 1 {
		t.Errorf("pair {f0,f1} count = %d, want 1 (only c2 contributes)", ds.couplingPairs[p01])
	}

	// But file-change denominators MUST still include c1 — those are honest
	// counts of how often each file appears, not a coupling signal.
	if ds.couplingFileChanges["f0.go"] != 2 {
		t.Errorf("f0.go changes = %d, want 2 (c1 + c2, even though c1 didn't contribute pairs)",
			ds.couplingFileChanges["f0.go"])
	}
	if ds.couplingFileChanges["f9.go"] != 1 {
		t.Errorf("f9.go changes = %d, want 1", ds.couplingFileChanges["f9.go"])
	}
}

func TestCouplingRefactorFilterBoundaries(t *testing.T) {
	// Exercise the threshold boundaries directly. The refactor filter uses
	// `len(unique) >= refactorMinFiles` (10) AND `meanChurn < refactorMax-
	// ChurnPerFile` (5.0, strict). These cases cover both dimensions plus
	// the pure-rename (zero churn) case that motivated the filter.
	cases := []struct {
		name         string
		files        int
		churnPerFile int64
		wantPairs    int // 0 = filtered; non-zero = pairs produced
	}{
		{"below minFiles: 9 files zero churn", 9, 0, 9 * 8 / 2},
		{"pure rename: 10 files zero churn", 10, 0, 0},
		{"below threshold: 10 files × 4 lines (mean 4.0)", 10, 4, 0},
		{"at threshold: 10 files × 5 lines (mean 5.0, strict <)", 10, 5, 10 * 9 / 2},
		{"above threshold: 10 files × 6 lines (mean 6.0)", 10, 6, 10 * 9 / 2},
		{"large substantive: 20 files × 50 lines", 20, 50, 20 * 19 / 2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			total := int64(c.files) * c.churnPerFile
			var lines []string
			lines = append(lines,
				fmt.Sprintf(`{"type":"commit","sha":"c1","author_name":"A","author_email":"a@x","author_date":"2024-01-01T10:00:00Z","additions":%d,"deletions":0,"files_changed":%d}`,
					total, c.files))
			for i := 0; i < c.files; i++ {
				lines = append(lines,
					fmt.Sprintf(`{"type":"commit_file","commit":"c1","path_current":"f%d.go","status":"M","additions":%d,"deletions":0}`,
						i, c.churnPerFile))
			}
			ds, err := streamLoad(strings.NewReader(strings.Join(lines, "\n")+"\n"),
				LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
			if err != nil {
				t.Fatalf("streamLoad: %v", err)
			}
			if got := len(ds.couplingPairs); got != c.wantPairs {
				t.Errorf("pairs = %d, want %d", got, c.wantPairs)
			}
			// Regardless of whether pairs were counted, each file's change
			// denominator must equal 1 (all participated in the single commit).
			for i := 0; i < c.files; i++ {
				path := fmt.Sprintf("f%d.go", i)
				if got := ds.couplingFileChanges[path]; got != 1 {
					t.Errorf("%s file changes = %d, want 1 (denominator always counted)", path, got)
				}
			}
		})
	}
}

func TestCouplingRenameBelowFilterThreshold(t *testing.T) {
	// Pins down current behavior at the intersection of two subsystems:
	// rename tracking and the refactor filter. A commit renaming 8 files
	// (below refactorMinFiles = 10) has zero churn per file, so mean < 5,
	// BUT the fileCount threshold isn't met — the filter does not fire.
	// Pairs between the old paths are generated during streaming, then
	// re-keyed to the canonical (new) paths in applyRenames.
	//
	// Each pair has CoChanges = 1, so a sensible --coupling-min-changes
	// threshold filters them out of user-facing output. But for callers
	// who inspect the raw ds.couplingPairs map, the pairs are present.
	// This test exists so any future tightening (lower refactorMinFiles,
	// or explicit skip-when-all-renames) is a conscious decision backed
	// by a failing test, not a silent behavior drift.
	var lines []string
	lines = append(lines, `{"type":"commit","sha":"c1","author_name":"A","author_email":"a@x","author_date":"2024-01-01T10:00:00Z","additions":0,"deletions":0,"files_changed":8}`)
	for i := 0; i < 8; i++ {
		lines = append(lines,
			fmt.Sprintf(`{"type":"commit_file","commit":"c1","path_current":"new%d.go","path_previous":"old%d.go","status":"R100","additions":0,"deletions":0}`, i, i))
	}
	ds, err := streamLoad(strings.NewReader(strings.Join(lines, "\n")+"\n"),
		LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	// 8 files, filter does not fire → C(8,2) = 28 pairs exist.
	if got := len(ds.couplingPairs); got != 28 {
		t.Errorf("pairs = %d, want 28 (current behavior — rename commits below refactorMinFiles leak)", got)
	}
	// Pairs must be keyed to the NEW paths (post-rename canonical), not old.
	for pair := range ds.couplingPairs {
		if !strings.HasPrefix(pair.a, "new") || !strings.HasPrefix(pair.b, "new") {
			t.Errorf("pair %v contains old-path key; applyRenames should have re-keyed", pair)
		}
	}
}

func TestCouplingKeepsNormalMultiFileCommits(t *testing.T) {
	// Counter-test: a commit touching 10 files with substantial churn per
	// file (40+ lines avg) must still contribute pairs — it's not a rename.
	var lines []string
	lines = append(lines, `{"type":"commit","sha":"c1","author_name":"A","author_email":"a@x","author_date":"2024-01-01T10:00:00Z","additions":500,"deletions":0,"files_changed":10}`)
	for i := 0; i < 10; i++ {
		lines = append(lines,
			fmt.Sprintf(`{"type":"commit_file","commit":"c1","path_current":"g%d.go","status":"M","additions":50,"deletions":0}`, i))
	}
	ds, err := streamLoad(strings.NewReader(strings.Join(lines, "\n")+"\n"),
		LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}
	// 10 choose 2 = 45 pairs
	if got := len(ds.couplingPairs); got != 45 {
		t.Errorf("coupling pairs = %d, want 45 (substantial per-file churn, should not trigger refactor filter)", got)
	}
}

func TestStreamLoadCouplingSingleFileCommits(t *testing.T) {
	// x.go and y.go co-change in c1, but x.go also changes alone in c2.
	// Coupling denominator for x.go should be 2 (both commits), not 1 (only multi-file).
	jsonl := `{"type":"commit","sha":"c1","tree":"t","parents":[],"author_name":"A","author_email":"a@x","author_date":"2024-01-01T00:00:00Z","committer_name":"A","committer_email":"a@x","committer_date":"2024-01-01T00:00:00Z","message":"","additions":10,"deletions":0,"files_changed":2}
{"type":"commit_file","commit":"c1","path_current":"x.go","path_previous":"x.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":5,"deletions":0}
{"type":"commit_file","commit":"c1","path_current":"y.go","path_previous":"y.go","status":"M","old_hash":"0","new_hash":"2","old_size":0,"new_size":0,"additions":5,"deletions":0}
{"type":"commit","sha":"c2","tree":"t","parents":[],"author_name":"A","author_email":"a@x","author_date":"2024-01-02T00:00:00Z","committer_name":"A","committer_email":"a@x","committer_date":"2024-01-02T00:00:00Z","message":"","additions":3,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"x.go","path_previous":"x.go","status":"M","old_hash":"0","new_hash":"3","old_size":0,"new_size":0,"additions":3,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	// x.go changed in 2 commits (c1 multi-file + c2 single-file)
	if ds.couplingFileChanges["x.go"] != 2 {
		t.Errorf("x.go changes = %d, want 2 (includes single-file commit)", ds.couplingFileChanges["x.go"])
	}
	// y.go changed in 1 commit
	if ds.couplingFileChanges["y.go"] != 1 {
		t.Errorf("y.go changes = %d, want 1", ds.couplingFileChanges["y.go"])
	}
	// co-changes = 1 (only c1)
	pair := filePair{a: "x.go", b: "y.go"}
	if ds.couplingPairs[pair] != 1 {
		t.Errorf("pair count = %d, want 1", ds.couplingPairs[pair])
	}
	// coupling % = 1 / min(2, 1) = 1/1 = 100%
	results := FileCoupling(ds, 10, 1)
	if len(results) != 1 {
		t.Fatalf("coupling results = %d", len(results))
	}
	if results[0].CouplingPct != 100 {
		t.Errorf("coupling %% = %.0f, want 100 (1/min(2,1))", results[0].CouplingPct)
	}
	// changes_A should be 2, not 1
	if results[0].ChangesA != 2 && results[0].ChangesB != 2 {
		t.Errorf("changes should include single-file commit: A=%d B=%d", results[0].ChangesA, results[0].ChangesB)
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

func TestLoadMultiJSONL(t *testing.T) {
	repoA := `{"type":"commit","sha":"a1","tree":"t","parents":[],"author_name":"Alice","author_email":"alice@x.com","author_date":"2024-01-10T10:00:00Z","committer_name":"Alice","committer_email":"alice@x.com","committer_date":"2024-01-10T10:00:00Z","message":"","additions":10,"deletions":2,"files_changed":1}
{"type":"commit_file","commit":"a1","path_current":"main.go","path_previous":"main.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":10,"deletions":2}
{"type":"dev","dev_id":"d1","name":"Alice","email":"alice@x.com"}
`
	repoB := `{"type":"commit","sha":"b1","tree":"t","parents":[],"author_name":"Bob","author_email":"bob@x.com","author_date":"2024-02-15T14:00:00Z","committer_name":"Bob","committer_email":"bob@x.com","committer_date":"2024-02-15T14:00:00Z","message":"","additions":20,"deletions":5,"files_changed":2}
{"type":"commit_file","commit":"b1","path_current":"main.go","path_previous":"main.go","status":"M","old_hash":"0","new_hash":"2","old_size":0,"new_size":0,"additions":15,"deletions":3}
{"type":"commit_file","commit":"b1","path_current":"util.go","path_previous":"util.go","status":"A","old_hash":"0","new_hash":"3","old_size":0,"new_size":0,"additions":5,"deletions":2}
{"type":"dev","dev_id":"d2","name":"Bob","email":"bob@x.com"}
`
	// Write temp files
	dirA := t.TempDir()
	dirB := t.TempDir()
	pathA := dirA + "/repo-a.jsonl"
	pathB := dirB + "/repo-b.jsonl"
	os.WriteFile(pathA, []byte(repoA), 0o644)
	os.WriteFile(pathB, []byte(repoB), 0o644)

	ds, err := LoadMultiJSONL([]string{pathA, pathB}, LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("LoadMultiJSONL: %v", err)
	}

	// Commits aggregate
	if ds.CommitCount != 2 {
		t.Errorf("CommitCount = %d, want 2", ds.CommitCount)
	}

	// Additions aggregate
	if ds.TotalAdditions != 30 {
		t.Errorf("TotalAdditions = %d, want 30", ds.TotalAdditions)
	}

	// Devs dedup across repos
	if ds.DevCount != 2 {
		t.Errorf("DevCount = %d, want 2", ds.DevCount)
	}

	// Paths should be prefixed
	if _, ok := ds.files["repo-a:main.go"]; !ok {
		t.Error("missing repo-a:main.go (expected prefix)")
	}
	if _, ok := ds.files["repo-b:main.go"]; !ok {
		t.Error("missing repo-b:main.go (expected prefix)")
	}
	if _, ok := ds.files["repo-b:util.go"]; !ok {
		t.Error("missing repo-b:util.go")
	}

	// No collision: repo-a:main.go and repo-b:main.go are separate
	if ds.UniqueFileCount != 3 {
		t.Errorf("UniqueFileCount = %d, want 3", ds.UniqueFileCount)
	}

	// Contributors from both repos
	if len(ds.contributors) != 2 {
		t.Errorf("contributors = %d, want 2", len(ds.contributors))
	}
}

func TestLoadMultiJSONLSharedDev(t *testing.T) {
	// Same dev in both repos — should be deduped
	repoA := `{"type":"commit","sha":"a1","tree":"t","parents":[],"author_name":"Alice","author_email":"alice@x.com","author_date":"2024-01-10T10:00:00Z","committer_name":"Alice","committer_email":"alice@x.com","committer_date":"2024-01-10T10:00:00Z","message":"","additions":5,"deletions":1,"files_changed":1}
{"type":"commit_file","commit":"a1","path_current":"f.go","path_previous":"f.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":5,"deletions":1}
{"type":"dev","dev_id":"d1","name":"Alice","email":"alice@x.com"}
`
	repoB := `{"type":"commit","sha":"b1","tree":"t","parents":[],"author_name":"Alice","author_email":"alice@x.com","author_date":"2024-02-01T10:00:00Z","committer_name":"Alice","committer_email":"alice@x.com","committer_date":"2024-02-01T10:00:00Z","message":"","additions":8,"deletions":2,"files_changed":1}
{"type":"commit_file","commit":"b1","path_current":"g.go","path_previous":"g.go","status":"A","old_hash":"0","new_hash":"2","old_size":0,"new_size":0,"additions":8,"deletions":2}
{"type":"dev","dev_id":"d1","name":"Alice","email":"alice@x.com"}
`
	dir := t.TempDir()
	pathA := dir + "/svc-a.jsonl"
	pathB := dir + "/svc-b.jsonl"
	os.WriteFile(pathA, []byte(repoA), 0o644)
	os.WriteFile(pathB, []byte(repoB), 0o644)

	ds, err := LoadMultiJSONL([]string{pathA, pathB}, LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("LoadMultiJSONL: %v", err)
	}

	// Dev dedup: same email across repos = 1 dev
	if ds.DevCount != 1 {
		t.Errorf("DevCount = %d, want 1 (deduped)", ds.DevCount)
	}

	// Commits aggregate across repos
	alice := ds.contributors["alice@x.com"]
	if alice == nil {
		t.Fatal("alice not found")
	}
	if alice.Commits != 2 {
		t.Errorf("alice commits = %d, want 2", alice.Commits)
	}
	if alice.Additions != 13 {
		t.Errorf("alice additions = %d, want 13", alice.Additions)
	}
	if alice.ActiveDays != 2 {
		t.Errorf("alice active days = %d, want 2", alice.ActiveDays)
	}
	if alice.FilesTouched != 2 {
		t.Errorf("alice files = %d, want 2 (svc-a:f.go + svc-b:g.go)", alice.FilesTouched)
	}
}

func TestLoadMultiJSONLSingleFile(t *testing.T) {
	// Single file should NOT prefix paths
	jsonl := `{"type":"commit","sha":"a1","tree":"t","parents":[],"author_name":"A","author_email":"a@x","author_date":"2024-01-01T00:00:00Z","committer_name":"A","committer_email":"a@x","committer_date":"2024-01-01T00:00:00Z","message":"","additions":5,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"a1","path_current":"main.go","path_previous":"main.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":5,"deletions":0}
`
	dir := t.TempDir()
	path := dir + "/data.jsonl"
	os.WriteFile(path, []byte(jsonl), 0o644)

	ds, err := LoadMultiJSONL([]string{path}, LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("LoadMultiJSONL: %v", err)
	}

	// Single file: no prefix
	if _, ok := ds.files["main.go"]; !ok {
		t.Error("single file should NOT have prefix, missing main.go")
	}
	if _, ok := ds.files["data:main.go"]; ok {
		t.Error("single file should NOT have prefix, found data:main.go")
	}
}

func TestTopCommits(t *testing.T) {
	ds := makeDataset()
	result := TopCommits(ds, 2)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	// Sorted by lines changed descending
	if result[0].LinesChanged < result[1].LinesChanged {
		t.Error("not sorted descending by lines changed")
	}
	// AuthorName should not be empty
	if result[0].AuthorName == "" {
		t.Error("AuthorName is empty")
	}
}

func TestTopCommitsWithMissingContributor(t *testing.T) {
	// Commit with email not in contributors — should not panic
	ds := &Dataset{
		commits: map[string]*commitEntry{
			"sha1": {email: "unknown@x.com", add: 100, del: 50, files: 3},
		},
		contributors: map[string]*ContributorStat{},
	}
	result := TopCommits(ds, 10)
	if len(result) != 1 {
		t.Fatalf("len = %d", len(result))
	}
	if result[0].AuthorName != "unknown@x.com" {
		t.Errorf("AuthorName = %q, want email fallback", result[0].AuthorName)
	}
}

func TestDevProfiles(t *testing.T) {
	ds := makeDataset()
	profiles := DevProfiles(ds, "", 0)
	if len(profiles) != 2 {
		t.Fatalf("len = %d, want 2", len(profiles))
	}
	// Sorted by commits descending
	if profiles[0].Commits < profiles[1].Commits {
		t.Error("not sorted by commits descending")
	}
	// Alice should have scope
	alice := profiles[0]
	if alice.Email != "alice@test.com" {
		t.Fatalf("first profile = %q, want alice", alice.Email)
	}
	if len(alice.Scope) == 0 {
		t.Error("alice scope is empty")
	}
	if alice.Pace <= 0 {
		t.Errorf("alice pace = %.1f, want > 0", alice.Pace)
	}
	if alice.ContribType == "" {
		t.Error("alice contrib type is empty")
	}
}

func TestDevProfilesFilterByEmail(t *testing.T) {
	ds := makeDataset()
	profiles := DevProfiles(ds, "bob@test.com", 0)
	if len(profiles) != 1 {
		t.Fatalf("len = %d, want 1", len(profiles))
	}
	if profiles[0].Email != "bob@test.com" {
		t.Errorf("email = %q", profiles[0].Email)
	}
}

func TestDevProfilesTopN(t *testing.T) {
	// Five devs with descending commit counts; verify n > 0 returns
	// exactly the top-N and excludes the rest.
	ds := makeDatasetWithManyDevs()

	got := DevProfiles(ds, "", 2)
	if len(got) != 2 {
		t.Fatalf("n=2: got %d profiles, want 2", len(got))
	}
	// Top-2 by commits must be dev5 (5 commits) and dev4 (4 commits).
	want := []string{"dev5@x", "dev4@x"}
	for i, email := range want {
		if got[i].Email != email {
			t.Errorf("profile[%d] = %q, want %q", i, got[i].Email, email)
		}
	}

	// Excluded devs must not appear.
	all := DevProfiles(ds, "", 0)
	if len(all) != 5 {
		t.Fatalf("n=0: got %d profiles, want 5 (all)", len(all))
	}

	// n greater than dev count behaves like n=0: return all.
	overflow := DevProfiles(ds, "", 100)
	if len(overflow) != 5 {
		t.Fatalf("n=100 (> total): got %d, want 5", len(overflow))
	}
}

func TestDevProfilesTopNPreservesOutsideCollaborators(t *testing.T) {
	// A top-N dev's collaborator list must still include non-target
	// devs they share files with. The early filter excludes non-target
	// devs from getting their own profile but must not strip them from
	// the target's collaborator map.
	ds := makeDatasetWithManyDevs()

	// dev5 is top-1 and shares "shared.go" with dev1 (non-target when n=1).
	got := DevProfiles(ds, "", 1)
	if len(got) != 1 || got[0].Email != "dev5@x" {
		t.Fatalf("expected dev5 as sole profile, got %+v", got)
	}
	var found bool
	for _, c := range got[0].Collaborators {
		if c.Email == "dev1@x" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dev5's collaborators must include non-target dev1; got %+v", got[0].Collaborators)
	}
}

// makeDatasetWithManyDevs builds a fixture with five developers of
// strictly decreasing commit counts: dev5=5, dev4=4, dev3=3, dev2=2,
// dev1=1. One shared file across dev5 and dev1 exercises the
// "non-target collaborator preserved" case.
func makeDatasetWithManyDevs() *Dataset {
	ds := &Dataset{
		Earliest:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Latest:       time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		commits:      map[string]*commitEntry{},
		contributors: map[string]*ContributorStat{},
		files:        map[string]*fileEntry{},
	}
	for i := 5; i >= 1; i-- {
		email := fmt.Sprintf("dev%d@x", i)
		ds.contributors[email] = &ContributorStat{
			Name: fmt.Sprintf("Dev %d", i), Email: email, Commits: i,
		}
		for c := 0; c < i; c++ {
			sha := fmt.Sprintf("s%d-%d", i, c)
			ds.commits[sha] = &commitEntry{
				email: email,
				date:  time.Date(2024, time.Month(1+i), 1+c, 10, 0, 0, 0, time.UTC),
				add:   10, del: 2, files: 1,
			}
		}
	}
	// Shared file between dev5 (target at n=1) and dev1 (non-target).
	ds.files["shared.go"] = &fileEntry{
		commits:   2,
		additions: 20, deletions: 4,
		devLines:   map[string]int64{"dev5@x": 50, "dev1@x": 30},
		devCommits: map[string]int{"dev5@x": 1, "dev1@x": 1},
		recentChurn: 20.0,
		lastChange:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	// A dev5-only file to give it some scope.
	ds.files["main.go"] = &fileEntry{
		commits:   1,
		additions: 10,
		devLines:   map[string]int64{"dev5@x": 10},
		devCommits: map[string]int{"dev5@x": 1},
		recentChurn: 10.0,
		lastChange:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	return ds
}

func TestDevProfilesContribType(t *testing.T) {
	tests := []struct {
		add  int64
		del  int64
		want string
	}{
		{100, 10, "growth"},   // ratio 0.1
		{100, 50, "balanced"}, // ratio 0.5
		{100, 90, "refactor"}, // ratio 0.9
		{0, 0, "growth"},     // zero additions
	}
	for _, tt := range tests {
		ds := &Dataset{
			commits: map[string]*commitEntry{
				"s1": {email: "a@x", add: tt.add, del: tt.del, files: 1},
			},
			contributors: map[string]*ContributorStat{
				"a@x": {Name: "A", Email: "a@x", Commits: 1, Additions: tt.add, Deletions: tt.del, ActiveDays: 1},
			},
			files: map[string]*fileEntry{},
		}
		profiles := DevProfiles(ds, "", 0)
		if len(profiles) == 0 {
			t.Fatal("no profiles")
		}
		if profiles[0].ContribType != tt.want {
			t.Errorf("add=%d del=%d: type=%q, want %q", tt.add, tt.del, profiles[0].ContribType, tt.want)
		}
	}
}

func TestRenameMergesHistory(t *testing.T) {
	// JSONL newest-first (as git log emits). Historical sequence:
	// 1) 2024-01 c1 creates old.go
	// 2) 2024-02 c2 edits old.go
	// 3) 2024-03 c3 renames old.go → new.go + edits
	// 4) 2024-04 c4 edits new.go
	jsonl := `{"type":"commit","sha":"c4","author_name":"A","author_email":"a@x","author_date":"2024-04-15T10:00:00Z","additions":5,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c4","path_current":"new.go","path_previous":"new.go","status":"M","additions":5,"deletions":0}
{"type":"commit","sha":"c3","author_name":"A","author_email":"a@x","author_date":"2024-03-15T10:00:00Z","additions":3,"deletions":2,"files_changed":1}
{"type":"commit_file","commit":"c3","path_current":"new.go","path_previous":"old.go","status":"R100","additions":3,"deletions":2}
{"type":"commit","sha":"c2","author_name":"A","author_email":"a@x","author_date":"2024-02-10T10:00:00Z","additions":8,"deletions":1,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"old.go","path_previous":"old.go","status":"M","additions":8,"deletions":1}
{"type":"commit","sha":"c1","author_name":"A","author_email":"a@x","author_date":"2024-01-05T10:00:00Z","additions":20,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c1","path_current":"old.go","path_previous":"old.go","status":"A","additions":20,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	if _, ok := ds.files["old.go"]; ok {
		t.Errorf("old.go should be merged into new.go, but still exists")
	}
	fe, ok := ds.files["new.go"]
	if !ok {
		t.Fatalf("new.go missing")
	}
	// All 4 commits merged into new.go.
	if fe.commits != 4 {
		t.Errorf("new.go commits = %d, want 4", fe.commits)
	}
	// firstChange must come from c1 (oldest), not c3 (the rename).
	if got := fe.firstChange.UTC().Format("2006-01-02"); got != "2024-01-05" {
		t.Errorf("firstChange = %q, want 2024-01-05 (pre-rename history preserved)", got)
	}
	if got := fe.lastChange.UTC().Format("2006-01-02"); got != "2024-04-15" {
		t.Errorf("lastChange = %q, want 2024-04-15", got)
	}
	// monthChurn must span all 4 months.
	if len(fe.monthChurn) != 4 {
		t.Errorf("monthChurn months = %d, want 4", len(fe.monthChurn))
	}
	if ds.UniqueFileCount != 1 {
		t.Errorf("UniqueFileCount = %d, want 1 (canonical)", ds.UniqueFileCount)
	}
}

func TestRenameChain(t *testing.T) {
	// A → B (in c2), B → C (in c3). Canonical = C.
	jsonl := `{"type":"commit","sha":"c3","author_name":"A","author_email":"a@x","author_date":"2024-03-10T10:00:00Z","additions":1,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c3","path_current":"C.go","path_previous":"B.go","status":"R100","additions":1,"deletions":0}
{"type":"commit","sha":"c2","author_name":"A","author_email":"a@x","author_date":"2024-02-10T10:00:00Z","additions":1,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"B.go","path_previous":"A.go","status":"R100","additions":1,"deletions":0}
{"type":"commit","sha":"c1","author_name":"A","author_email":"a@x","author_date":"2024-01-10T10:00:00Z","additions":10,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c1","path_current":"A.go","path_previous":"A.go","status":"A","additions":10,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}
	for _, orphan := range []string{"A.go", "B.go"} {
		if _, ok := ds.files[orphan]; ok {
			t.Errorf("%s should not exist after chain rename", orphan)
		}
	}
	fe, ok := ds.files["C.go"]
	if !ok {
		t.Fatal("C.go missing — chain rename not resolved")
	}
	if fe.commits != 3 {
		t.Errorf("C.go commits = %d, want 3 (A+B+C merged)", fe.commits)
	}
}

func TestRenameCollapsesCouplingSelfPair(t *testing.T) {
	// Two files A.go and B.go co-change in c1. Later c2 renames A→B (both
	// end up as B.go). The pair {A, B} must NOT survive as a self-pair.
	jsonl := `{"type":"commit","sha":"c2","author_name":"A","author_email":"a@x","author_date":"2024-02-10T10:00:00Z","additions":1,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"B.go","path_previous":"A.go","status":"R100","additions":1,"deletions":0}
{"type":"commit","sha":"c1","author_name":"A","author_email":"a@x","author_date":"2024-01-10T10:00:00Z","additions":10,"deletions":0,"files_changed":2}
{"type":"commit_file","commit":"c1","path_current":"A.go","path_previous":"A.go","status":"A","additions":5,"deletions":0}
{"type":"commit_file","commit":"c1","path_current":"B.go","path_previous":"B.go","status":"A","additions":5,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}
	for pair := range ds.couplingPairs {
		if pair.a == pair.b {
			t.Errorf("self-pair survived rename collapse: %+v", pair)
		}
	}
}

func TestRenameDevFilesTouchedUsesCanonical(t *testing.T) {
	// A single dev edits old.go, then renames it to new.go and edits again.
	// FilesTouched should be 1 (one canonical file), not 2.
	jsonl := `{"type":"commit","sha":"c3","author_name":"Alice","author_email":"alice@x","author_date":"2024-03-10T10:00:00Z","additions":2,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c3","path_current":"new.go","path_previous":"new.go","status":"M","additions":2,"deletions":0}
{"type":"commit","sha":"c2","author_name":"Alice","author_email":"alice@x","author_date":"2024-02-10T10:00:00Z","additions":1,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"new.go","path_previous":"old.go","status":"R100","additions":1,"deletions":0}
{"type":"commit","sha":"c1","author_name":"Alice","author_email":"alice@x","author_date":"2024-01-10T10:00:00Z","additions":5,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c1","path_current":"old.go","path_previous":"old.go","status":"A","additions":5,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}
	alice := ds.contributors["alice@x"]
	if alice == nil {
		t.Fatal("alice missing")
	}
	if alice.FilesTouched != 1 {
		t.Errorf("FilesTouched = %d, want 1 (old.go and new.go collapse to same canonical)", alice.FilesTouched)
	}
}

func TestRenameSkipsReusedOldPath(t *testing.T) {
	// Path-reuse scenario: A was renamed to B in old history, then the
	// name "A" was reused for an unrelated file, which was renamed to D.
	// JSONL is newest-first, so renameEdges hold {A→D} first and {A→B}
	// second. Both have oldPath="A" — this is the signal that lineages
	// cannot be disambiguated safely.
	//
	// Before the fix, the first edge won the dedup and applyRenames
	// migrated the merged ds.files["A"] (containing both lineages'
	// pre-rename data) into "D", polluting D with lineage-1 commits
	// that belong to B and stripping B of its pre-rename history.
	//
	// After the fix, applyRenames detects the duplicate oldPath and
	// refuses to migrate either edge. A stays where it is (still with
	// merged lineages, but no cross-contamination into B or D), and
	// B and D each keep only their own post-rename data. This is
	// underattribution, which is safer than active misattribution.
	ds := newDataset()
	ds.renameEdges = []renameEdge{
		{oldPath: "A", newPath: "D"},
		{oldPath: "A", newPath: "B"},
	}
	ds.files = map[string]*fileEntry{
		"A": {commits: 2, monthChurn: map[string]int64{}},
		"B": {commits: 1, monthChurn: map[string]int64{}},
		"D": {commits: 1, monthChurn: map[string]int64{}},
	}
	applyRenames(ds)

	a, ok := ds.files["A"]
	if !ok {
		t.Fatal("A should remain — path reuse must disable migration")
	}
	if a.commits != 2 {
		t.Errorf("A commits = %d, want 2 (unchanged)", a.commits)
	}
	if ds.files["B"].commits != 1 {
		t.Errorf("B commits = %d, want 1 (A must not have been merged in)", ds.files["B"].commits)
	}
	if ds.files["D"].commits != 1 {
		t.Errorf("D commits = %d, want 1 (A must not have been merged in)", ds.files["D"].commits)
	}
}

func TestRenameSkipsSingleEdgeReusedOldPath(t *testing.T) {
	// Path reuse with a SINGLE rename edge: A → B in old history, then
	// an unrelated file is created at "A" and never renamed further.
	// Only one edge involves A, so oldPathCounts[A] = 1 and the
	// multi-edge heuristic cannot detect reuse. The tell is temporal:
	// ds.files["A"].lastChange is AFTER the rename's commitDate, which
	// means activity on "A" continued past the rename — the new file.
	//
	// Without this check, direct[A] = B is applied and the new file's
	// history silently merges into B, corrupting B's stats.
	tRename := time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC)
	tNewFile := time.Date(2024, 5, 10, 0, 0, 0, 0, time.UTC)
	ds := newDataset()
	ds.renameEdges = []renameEdge{
		{oldPath: "A", newPath: "B", commitDate: tRename},
	}
	ds.files = map[string]*fileEntry{
		"A": {
			commits:    2, // 1 pre-rename (lineage 1) + 1 post-rename (lineage 2)
			lastChange: tNewFile,
			monthChurn: map[string]int64{},
		},
		"B": {
			commits:    1,
			lastChange: tRename,
			monthChurn: map[string]int64{},
		},
	}
	applyRenames(ds)

	if _, ok := ds.files["A"]; !ok {
		t.Error("A should remain — single-edge path reuse must skip migration")
	}
	if a, ok := ds.files["A"]; ok && a.commits != 2 {
		t.Errorf("A.commits = %d, want 2 (unchanged)", a.commits)
	}
	if ds.files["B"].commits != 1 {
		t.Errorf("B.commits = %d, want 1 (lineage 2 must not have leaked in)", ds.files["B"].commits)
	}
}

func TestRenameSingleEdgeReuseViaStream(t *testing.T) {
	// End-to-end equivalent of TestRenameSkipsSingleEdgeReusedOldPath,
	// exercising the full ingest path so fileEntry.lastChange and the
	// renameEdge.commitDate are populated naturally from commit_file
	// records. Covers the wiring between streamLoadInto and applyRenames.
	//
	// Timeline (encoded newest-first as git log emits):
	//   c6 2024-06-01: modify new A (lineage 2)
	//   c5 2024-05-01: new file created at A (lineage 2 starts)
	//   c4 2024-04-01: modify B
	//   c3 2024-03-15: rename A → B (ends lineage 1)
	//   c2 2024-02-10: modify A (lineage 1)
	//   c1 2024-01-05: create A (lineage 1 starts)
	jsonl := `{"type":"commit","sha":"c6","author_name":"X","author_email":"x@x","author_date":"2024-06-01T10:00:00Z","additions":30,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c6","path_current":"A.go","path_previous":"A.go","status":"M","additions":30,"deletions":0}
{"type":"commit","sha":"c5","author_name":"X","author_email":"x@x","author_date":"2024-05-01T10:00:00Z","additions":20,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c5","path_current":"A.go","path_previous":"A.go","status":"A","additions":20,"deletions":0}
{"type":"commit","sha":"c4","author_name":"Y","author_email":"y@x","author_date":"2024-04-01T10:00:00Z","additions":10,"deletions":2,"files_changed":1}
{"type":"commit_file","commit":"c4","path_current":"B.go","path_previous":"B.go","status":"M","additions":10,"deletions":2}
{"type":"commit","sha":"c3","author_name":"Y","author_email":"y@x","author_date":"2024-03-15T10:00:00Z","additions":0,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c3","path_current":"B.go","path_previous":"A.go","status":"R100","additions":0,"deletions":0}
{"type":"commit","sha":"c2","author_name":"Y","author_email":"y@x","author_date":"2024-02-10T10:00:00Z","additions":15,"deletions":3,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"A.go","path_previous":"A.go","status":"M","additions":15,"deletions":3}
{"type":"commit","sha":"c1","author_name":"Y","author_email":"y@x","author_date":"2024-01-05T10:00:00Z","additions":40,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c1","path_current":"A.go","path_previous":"A.go","status":"A","additions":40,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}
	a, ok := ds.files["A.go"]
	if !ok {
		t.Fatal("A.go missing — reuse detection should have skipped migration")
	}
	// A.go should contain c1 + c2 (lineage 1 pre-rename) + c5 + c6 (lineage
	// 2 post-reuse) = 4 commits. They are conflated at the path key level
	// (known limitation of non-temporal-segmented ingest), but at least
	// don't leak into B.
	if a.commits != 4 {
		t.Errorf("A.go commits = %d, want 4 (c1+c2+c5+c6 conflated at the reused path)", a.commits)
	}
	b, ok := ds.files["B.go"]
	if !ok {
		t.Fatal("B.go missing")
	}
	// B.go should have c3 (rename commit) + c4 (modify) = 2 commits.
	if b.commits != 2 {
		t.Errorf("B.go commits = %d, want 2 (c3+c4, no leak from A)", b.commits)
	}
}

func TestRenameChainPlusReuse(t *testing.T) {
	// Refinement catches a case that the pure wasRecreated guard missed:
	// D → A → B chain followed by a NEW unrelated file created at A.
	// A is both a rename target (via D→A) and a rename source (via
	// A→B), so wasRecreated[A] = true. Under the old single-edge
	// check the reuse detection was disabled here, letting the new A
	// lineage leak into B. Using maxEdgeDate (the latest edge touching
	// A), we see that lastChange(A) is AFTER the last A-involving
	// rename and correctly skip.
	tD_to_A := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	tA_to_B := time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC)
	tReuse := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	ds := newDataset()
	ds.renameEdges = []renameEdge{
		{oldPath: "A", newPath: "B", commitDate: tA_to_B},
		{oldPath: "D", newPath: "A", commitDate: tD_to_A},
	}
	ds.files = map[string]*fileEntry{
		"D": {commits: 1, lastChange: tD_to_A, monthChurn: map[string]int64{}},
		"A": {commits: 3, lastChange: tReuse, monthChurn: map[string]int64{}}, // post-reuse
		"B": {commits: 1, lastChange: tA_to_B, monthChurn: map[string]int64{}},
	}
	applyRenames(ds)

	// A must stay — reuse detected.
	if _, ok := ds.files["A"]; !ok {
		t.Fatal("A should remain — reuse detected by lastChange > maxEdgeDate")
	}
	// D still migrates through its own rename edge (D has no post-
	// rename activity). D → A because direct[A] was not set.
	if _, ok := ds.files["D"]; ok {
		t.Error("D should have been migrated (clean rename with no reuse on D)")
	}
	// B stays — A did not leak in.
	if b := ds.files["B"]; b.commits != 1 {
		t.Errorf("B.commits = %d, want 1 (A's reuse lineage must not leak into B)", b.commits)
	}
}

func TestRenameCleanRenameStillMigrates(t *testing.T) {
	// Counter-test: the single-edge reuse check must not fire for a
	// clean rename where all activity on oldPath predates the rename.
	// lastChange(A) ≤ commitDate(A→B) → migration proceeds, A merges
	// into B.
	tLastA := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	tRename := time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC)
	ds := newDataset()
	ds.renameEdges = []renameEdge{
		{oldPath: "A", newPath: "B", commitDate: tRename},
	}
	ds.files = map[string]*fileEntry{
		"A": {commits: 3, lastChange: tLastA, monthChurn: map[string]int64{}},
		"B": {commits: 1, lastChange: tRename, monthChurn: map[string]int64{}},
	}
	applyRenames(ds)

	if _, ok := ds.files["A"]; ok {
		t.Error("A should have been migrated into B (clean rename)")
	}
	if ds.files["B"].commits != 4 {
		t.Errorf("B.commits = %d, want 4 (A=3 + B=1 merged)", ds.files["B"].commits)
	}
}

func TestRenameBackThenRenameAgain(t *testing.T) {
	// Same-lineage chain with a rename-back: A → B → A → C. The repeated
	// "A" is NOT path reuse — the intermediate edge B → A recreates A
	// from B, so both A-segments belong to the same lineage and both
	// should collapse into C. Without the hasIncomingRename check this
	// would be mistaken for reuse and skipped, leaving stale B files
	// and an un-migrated A with merged-but-untouched history.
	ds := newDataset()
	ds.renameEdges = []renameEdge{
		{oldPath: "A", newPath: "C"}, // newest rename
		{oldPath: "B", newPath: "A"}, // middle: rename back
		{oldPath: "A", newPath: "B"}, // oldest rename
	}
	ds.files = map[string]*fileEntry{
		"A": {commits: 2, monthChurn: map[string]int64{}}, // pre-first + post-back, merged
		"B": {commits: 1, monthChurn: map[string]int64{}}, // mid-chain activity
		"C": {commits: 1, monthChurn: map[string]int64{}}, // post-final activity
	}
	applyRenames(ds)

	for _, orphan := range []string{"A", "B"} {
		if _, ok := ds.files[orphan]; ok {
			t.Errorf("%s should have been merged into C (rename-back chain, same lineage)", orphan)
		}
	}
	c, ok := ds.files["C"]
	if !ok {
		t.Fatal("C missing — chain should have collapsed into it")
	}
	if c.commits != 4 {
		t.Errorf("C.commits = %d, want 4 (A=2 + B=1 + C=1 merged)", c.commits)
	}
}

func TestRenameChainNotMistakenForReuse(t *testing.T) {
	// Guard against the fix being too aggressive: a genuine chain
	// A→B→C has DIFFERENT oldPaths (A once, B once) and must still
	// be collapsed into C as before.
	ds := newDataset()
	ds.renameEdges = []renameEdge{
		{oldPath: "B", newPath: "C"},
		{oldPath: "A", newPath: "B"},
	}
	ds.files = map[string]*fileEntry{
		"A": {commits: 1, monthChurn: map[string]int64{}},
		"B": {commits: 1, monthChurn: map[string]int64{}},
		"C": {commits: 1, monthChurn: map[string]int64{}},
	}
	applyRenames(ds)

	if _, ok := ds.files["A"]; ok {
		t.Error("A should have been merged — not a reuse case")
	}
	if _, ok := ds.files["B"]; ok {
		t.Error("B should have been merged — not a reuse case")
	}
	if ds.files["C"].commits != 3 {
		t.Errorf("C.commits = %d, want 3 (A+B+C chain)", ds.files["C"].commits)
	}
}

func TestRenameCycleDoesNotCrash(t *testing.T) {
	// Degenerate: A→B then B→A. Canonical resolution should bail out of
	// the cycle instead of infinite-looping, and — since migration is
	// ambiguous — both files survive with their own data rather than
	// one being silently merged into the other.
	jsonl := `{"type":"commit","sha":"c2","author_name":"A","author_email":"a@x","author_date":"2024-02-10T10:00:00Z","additions":1,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"A.go","path_previous":"B.go","status":"R100","additions":1,"deletions":0}
{"type":"commit","sha":"c1","author_name":"A","author_email":"a@x","author_date":"2024-01-10T10:00:00Z","additions":1,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c1","path_current":"B.go","path_previous":"A.go","status":"R100","additions":1,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}
	// Both files must survive the cycle. If the bail logic accidentally
	// routed one into the other, this would fail.
	if _, ok := ds.files["A.go"]; !ok {
		t.Error("A.go missing — cycle bail should leave both files intact")
	}
	if _, ok := ds.files["B.go"]; !ok {
		t.Error("B.go missing — cycle bail should leave both files intact")
	}
}

func TestRenameMixedLineageKnownLimitation(t *testing.T) {
	// Exotic mixed case documented in METRICS.md as a known limitation:
	//
	//   t1: A → B         (lineage 1 renames A away)
	//   t2: C → A         (lineage 2: a different file C takes the name A)
	//   t3: A → D         (lineage 2 renames away)
	//
	// Because the repeated oldPath "A" has an incoming edge (C → A), the
	// heuristic treats the two A-edges as a rename-back chain and migrates.
	// Lineage 1's pre-A→B commits end up misattributed to D.
	//
	// Pinning the current behavior here so that a future refactor cannot
	// silently change the outcome — any future fix (proper temporal
	// segmentation) should update or remove this test consciously.
	//
	// NOTE: fileEntry.lastChange is intentionally left zero. The single-
	// edge reuse check (`lastChange.After(maxEdgeDate)`) requires non-
	// zero timestamps to fire; leaving them unset exercises the mixed-
	// lineage "chain-like" path that the heuristic currently cannot
	// disambiguate. If a test update accidentally sets lastChange, this
	// fixture becomes a different scenario (chain+reuse) which IS
	// detected by the refined check and produces a different result.
	ds := newDataset()
	tNew := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	tMid := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	tOld := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ds.renameEdges = []renameEdge{
		{oldPath: "A", newPath: "D", commitDate: tNew}, // t3
		{oldPath: "C", newPath: "A", commitDate: tMid}, // t2
		{oldPath: "A", newPath: "B", commitDate: tOld}, // t1
	}
	ds.files = map[string]*fileEntry{
		"A": {commits: 2, monthChurn: map[string]int64{}}, // merged lineages (no lastChange on purpose)
		"B": {commits: 1, monthChurn: map[string]int64{}},
		"C": {commits: 1, monthChurn: map[string]int64{}},
		"D": {commits: 1, monthChurn: map[string]int64{}},
	}
	applyRenames(ds)

	// Current (imperfect) behavior: heuristic sees hasIncomingRename[A]=true
	// via C→A, so it proceeds with migration. Newest edge wins: A → D.
	// Both C and A collapse into D. B keeps only its post-rename data.
	if _, ok := ds.files["A"]; ok {
		t.Error("A should have been migrated (current behavior — chain-like)")
	}
	if _, ok := ds.files["C"]; ok {
		t.Error("C should have been migrated into D")
	}
	d, ok := ds.files["D"]
	if !ok {
		t.Fatal("D missing")
	}
	if d.commits != 4 {
		t.Errorf("D.commits = %d, want 4 (A=2 + C=1 + D=1 merged — lineage 1's pre-A→B data is misattributed here)", d.commits)
	}
	// B keeps only its post-rename data. Lineage 1's pre-rename history
	// that should live here is instead at D.
	if ds.files["B"].commits != 1 {
		t.Errorf("B.commits = %d, want 1 (lineage-1 pre-rename data leaked to D)", ds.files["B"].commits)
	}
}

func TestStreamLoadFullPipeline(t *testing.T) {
	jsonl := `{"type":"commit","sha":"c1","tree":"t","parents":["p1","p2"],"author_name":"Alice","author_email":"alice@x.com","author_date":"2024-03-15T10:00:00Z","committer_name":"Alice","committer_email":"alice@x.com","committer_date":"2024-03-15T10:00:00Z","message":"merge feature","additions":50,"deletions":10,"files_changed":3}
{"type":"commit_parent","sha":"c1","parent_sha":"p1"}
{"type":"commit_parent","sha":"c1","parent_sha":"p2"}
{"type":"commit_file","commit":"c1","path_current":"src/main.go","path_previous":"src/main.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":30,"deletions":5}
{"type":"commit_file","commit":"c1","path_current":"src/util.go","path_previous":"src/util.go","status":"M","old_hash":"0","new_hash":"2","old_size":0,"new_size":0,"additions":15,"deletions":3}
{"type":"commit_file","commit":"c1","path_current":"README.md","path_previous":"README.md","status":"M","old_hash":"0","new_hash":"3","old_size":0,"new_size":0,"additions":5,"deletions":2}
{"type":"commit","sha":"c2","tree":"t","parents":["c1"],"author_name":"Bob","author_email":"bob@x.com","author_date":"2024-03-16T14:00:00Z","committer_name":"Bob","committer_email":"bob@x.com","committer_date":"2024-03-16T14:00:00Z","message":"fix bug","additions":8,"deletions":2,"files_changed":1}
{"type":"commit_parent","sha":"c2","parent_sha":"c1"}
{"type":"commit_file","commit":"c2","path_current":"src/main.go","path_previous":"src/main.go","status":"M","old_hash":"0","new_hash":"4","old_size":0,"new_size":0,"additions":8,"deletions":2}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("streamLoad: %v", err)
	}

	// Summary
	s := ComputeSummary(ds)
	if s.TotalCommits != 2 {
		t.Errorf("commits = %d", s.TotalCommits)
	}
	if s.MergeCommits != 1 {
		t.Errorf("merges = %d, want 1 (c1 has 2 parents)", s.MergeCommits)
	}
	if s.TotalDevs != 2 {
		t.Errorf("devs = %d", s.TotalDevs)
	}

	// Top commits
	tc := TopCommits(ds, 1)
	if tc[0].SHA != "c1" {
		t.Errorf("top commit = %q, want c1 (60 lines vs 10)", tc[0].SHA)
	}
	if tc[0].Message != "merge feature" {
		t.Errorf("message = %q", tc[0].Message)
	}

	// Coupling: src/main.go + src/util.go co-change in c1
	coupling := FileCoupling(ds, 10, 1)
	if len(coupling) == 0 {
		t.Error("expected coupling between src/main.go and src/util.go")
	}

	// Dev profiles
	profiles := DevProfiles(ds, "alice@x.com", 0)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	if profiles[0].Commits != 1 {
		t.Errorf("alice commits = %d", profiles[0].Commits)
	}
	if len(profiles[0].Scope) == 0 {
		t.Error("alice scope empty")
	}
}

func TestHerfindahlHelper(t *testing.T) {
	cases := []struct {
		name string
		in   []int
		want float64
	}{
		{"empty", nil, 0},
		{"single", []int{5}, 1},                    // 1 bucket = fully concentrated
		{"single zero", []int{0}, 0},               // sum=0 short-circuits
		{"zeros only", []int{0, 0, 0}, 0},
		{"uniform 2", []int{5, 5}, 0.5},            // 0.25 + 0.25
		{"uniform 5", []int{1, 1, 1, 1, 1}, 0.2},   // 5 × (1/5)²
		{"70/30", []int{7, 3}, 0.58},               // 0.49 + 0.09
		{"90/10", []int{9, 1}, 0.82},               // 0.81 + 0.01
		{"100-in-one", []int{0, 0, 100}, 1},        // single non-zero bucket
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := herfindahl(c.in)
			diff := got - c.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.005 {
				t.Errorf("herfindahl(%v) = %.3f, want ≈ %.3f", c.in, got, c.want)
			}
		})
	}
}

func TestPrintProfilesSpecializationDisplayPrecision(t *testing.T) {
	// The Specialization display must show enough decimals that the
	// rendered number is self-consistent with the band label. At %.2f a
	// true value of 0.149 rounds to "0.15" and the shown label
	// "broad generalist" (correct: 0.149 < 0.15) appears to contradict
	// the displayed number (0.15 is NOT < 0.15). Using %.3f renders
	// "0.149" and the reader can verify the classification at a glance.
	p := DevProfile{
		Name: "N", Email: "n@x", Commits: 1, ActiveDays: 1,
		FirstDate: "2024-01-01", LastDate: "2024-01-01",
		Specialization: 0.149, // just under specBroadGeneralistMax
	}
	var buf bytes.Buffer
	f := NewFormatter(&buf, "table")
	if err := f.PrintProfiles([]DevProfile{p}); err != nil {
		t.Fatalf("PrintProfiles: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "0.149") {
		t.Errorf("output should contain %q to match the classification band (%%.3f), got:\n%s", "0.149", out)
	}
	if !strings.Contains(out, "broad generalist") {
		t.Errorf("output should contain label 'broad generalist' for H=0.149, got:\n%s", out)
	}
}

func TestHerfindahlPreservesPrecision(t *testing.T) {
	// herfindahl must return the full float64 value; rounding happens only
	// at display time. A prior version rounded to 3 decimals inside the
	// function, which would misclassify boundary cases (e.g. true 0.1496
	// rounding to 0.150 and flipping from "broad generalist" to "balanced").
	// Three uniform buckets produce H = 1/3 exactly; the stored value must
	// be the full-precision float, not a rounded approximation.
	h := herfindahl([]int{1, 1, 1})
	if h == 0.333 {
		t.Fatal("herfindahl returned 0.333 — the function is rounding internally again")
	}
	oneThird := 1.0 / 3.0
	if diff := h - oneThird; diff < -1e-12 || diff > 1e-12 {
		t.Errorf("herfindahl([1,1,1]) = %.18f, want 1/3 = %.18f", h, oneThird)
	}
}

func TestSpecLabelBandsBoundaryPrecision(t *testing.T) {
	// Thresholds are strict <, so a value exactly at the threshold lands in
	// the next band. Before the precision fix, internal rounding could move
	// a value JUST under a threshold (say 0.14999) up to 0.150, crossing
	// the band. Verify that values near boundaries classify by their true
	// precision, not by a rounded approximation.
	cases := []struct {
		h    float64
		want string
	}{
		{0.149999, "broad generalist"},  // just under specBroadGeneralistMax
		{0.150001, "balanced"},          // just over
		{0.349999, "balanced"},
		{0.350001, "focused specialist"},
		{0.699999, "focused specialist"},
		{0.700001, "narrow specialist"},
	}
	for _, c := range cases {
		if got := specLabel(c.h); got != c.want {
			t.Errorf("specLabel(%.6f) = %q, want %q (boundary precision)", c.h, got, c.want)
		}
	}
}

func TestSpecLabelBands(t *testing.T) {
	// Guard the four-band classification: boundaries are defined by the
	// specBroadGeneralistMax / specBalancedMax / specFocusedMax constants.
	// Constants drift without this test would silently change label output
	// in both CLI and HTML.
	cases := []struct {
		h    float64
		want string
	}{
		{0.0, "broad generalist"},
		{0.14, "broad generalist"},
		{specBroadGeneralistMax, "balanced"}, // boundary: < is strict
		{0.34, "balanced"},
		{specBalancedMax, "focused specialist"},
		{0.69, "focused specialist"},
		{specFocusedMax, "narrow specialist"},
		{1.0, "narrow specialist"},
	}
	for _, c := range cases {
		if got := specLabel(c.h); got != c.want {
			t.Errorf("specLabel(%.3f) = %q, want %q", c.h, got, c.want)
		}
	}
}

func TestDevProfilesSpecialization(t *testing.T) {
	// Three devs with deliberately distinct scope patterns:
	//   - narrow: 100% in one dir
	//   - focused: 70/30 across two dirs
	//   - broad: evenly spread across 5 dirs
	// Specialization (Gini over per-dir file counts) must rank them
	// narrow > focused > broad.
	t1 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	ds := &Dataset{
		Earliest: t1, Latest: t1,
		commits: map[string]*commitEntry{
			"c1": {email: "narrow@x", date: t1, add: 10, del: 0, files: 1},
			"c2": {email: "focused@x", date: t1, add: 10, del: 0, files: 1},
			"c3": {email: "broad@x", date: t1, add: 10, del: 0, files: 1},
		},
		contributors: map[string]*ContributorStat{
			"narrow@x":  {Email: "narrow@x", Name: "N", Commits: 1, ActiveDays: 1, FilesTouched: 5, Additions: 10},
			"focused@x": {Email: "focused@x", Name: "F", Commits: 1, ActiveDays: 1, FilesTouched: 10, Additions: 10},
			"broad@x":   {Email: "broad@x", Name: "B", Commits: 1, ActiveDays: 1, FilesTouched: 5, Additions: 10},
		},
		files: map[string]*fileEntry{},
	}
	// narrow@x: 5 files all in one dir
	for i := 0; i < 5; i++ {
		path := fmt.Sprintf("auth/f%d.go", i)
		ds.files[path] = &fileEntry{commits: 1, devLines: map[string]int64{"narrow@x": 10}, devCommits: map[string]int{"narrow@x": 1}, monthChurn: map[string]int64{}}
	}
	// focused@x: 7 in one dir, 3 in another
	for i := 0; i < 7; i++ {
		path := fmt.Sprintf("api/f%d.go", i)
		ds.files[path] = &fileEntry{commits: 1, devLines: map[string]int64{"focused@x": 10}, devCommits: map[string]int{"focused@x": 1}, monthChurn: map[string]int64{}}
	}
	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("web/f%d.go", i)
		ds.files[path] = &fileEntry{commits: 1, devLines: map[string]int64{"focused@x": 10}, devCommits: map[string]int{"focused@x": 1}, monthChurn: map[string]int64{}}
	}
	// broad@x: 1 file in each of 5 different dirs
	for i, d := range []string{"a", "b", "c", "d", "e"} {
		path := fmt.Sprintf("%s/f%d.go", d, i)
		ds.files[path] = &fileEntry{commits: 1, devLines: map[string]int64{"broad@x": 10}, devCommits: map[string]int{"broad@x": 1}, monthChurn: map[string]int64{}}
	}

	profiles := DevProfiles(ds, "", 0)
	get := func(email string) float64 {
		for _, p := range profiles {
			if p.Email == email {
				return p.Specialization
			}
		}
		t.Fatalf("missing profile %s", email)
		return 0
	}
	narrow := get("narrow@x")
	focused := get("focused@x")
	broad := get("broad@x")

	// Herfindahl semantics:
	//   narrow@x  (5 files all in 1 dir) → H = 1
	//   focused@x (7 in api, 3 in web)   → H = 0.49 + 0.09 = 0.58
	//   broad@x   (1 file in each of 5 dirs) → H = 5 × 0.04 = 0.2
	// Ordering: narrow > focused > broad. The old Gini collapsed narrow
	// and broad to 0 and was the reason this test was rewritten.
	if narrow != 1.0 {
		t.Errorf("narrow@x (1 dir) specialization = %.3f, want 1.0 (fully concentrated)", narrow)
	}
	if !(focused > 0.5 && focused < 0.65) {
		t.Errorf("focused@x (7+3 split) specialization = %.3f, want ~0.58", focused)
	}
	if !(broad > 0.15 && broad < 0.25) {
		t.Errorf("broad@x (5 dirs uniform) specialization = %.3f, want ~0.2", broad)
	}
	if !(narrow > focused && focused > broad) {
		t.Errorf("ordering failed: narrow=%.2f focused=%.2f broad=%.2f; want narrow > focused > broad",
			narrow, focused, broad)
	}
}

func TestDevProfilesSpecializationRootFilesBucket(t *testing.T) {
	// Bug reported in review: when a dev touches only repo-root files
	// (no slash in path), DevProfiles used to treat each filename as its
	// own "directory". A dev with README, Makefile, go.mod, LICENSE
	// ended up with 4 pseudo-dirs × 1 file → Herfindahl = 0.25
	// ("balanced") instead of 1.0 ("narrow specialist on the repo root").
	// Fix collapses root-level files into the "." bucket, matching the
	// convention in DirectoryStats.
	t1 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	ds := &Dataset{
		Earliest: t1, Latest: t1,
		commits: map[string]*commitEntry{"c1": {email: "root@x", date: t1, add: 10, del: 0, files: 4}},
		contributors: map[string]*ContributorStat{
			"root@x": {Email: "root@x", Name: "R", Commits: 1, ActiveDays: 1, FilesTouched: 4, Additions: 10},
		},
		files: map[string]*fileEntry{
			"README.md": {commits: 1, devLines: map[string]int64{"root@x": 5}, devCommits: map[string]int{"root@x": 1}, monthChurn: map[string]int64{}},
			"Makefile":  {commits: 1, devLines: map[string]int64{"root@x": 5}, devCommits: map[string]int{"root@x": 1}, monthChurn: map[string]int64{}},
			"go.mod":    {commits: 1, devLines: map[string]int64{"root@x": 5}, devCommits: map[string]int{"root@x": 1}, monthChurn: map[string]int64{}},
			"LICENSE":   {commits: 1, devLines: map[string]int64{"root@x": 5}, devCommits: map[string]int{"root@x": 1}, monthChurn: map[string]int64{}},
		},
	}
	profiles := DevProfiles(ds, "", 0)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	p := profiles[0]
	// All four files are at the root, so they must collapse into one
	// bucket named ".". Specialization must be 1.0 (narrow specialist).
	if p.Specialization != 1.0 {
		t.Errorf("root-only dev specialization = %.3f, want 1.0 (all four files collapse to one bucket)", p.Specialization)
	}
	if len(p.Scope) != 1 {
		t.Fatalf("Scope = %d entries, want 1 (the '.' bucket)", len(p.Scope))
	}
	if p.Scope[0].Dir != "." {
		t.Errorf("Scope[0].Dir = %q, want \".\"", p.Scope[0].Dir)
	}
	if p.Scope[0].Files != 4 {
		t.Errorf("Scope[0].Files = %d, want 4", p.Scope[0].Files)
	}
}

func TestDevProfilesSpecializationEdgeCases(t *testing.T) {
	t1 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	// Case 1: dev listed as contributor but no files touched.
	// dirCount empty → Herfindahl returns 0. Label falls through to
	// "broad generalist", which is semantically stretchy but consistent
	// with "no signal means no specialization".
	ds := &Dataset{
		Earliest: t1, Latest: t1,
		commits: map[string]*commitEntry{"c1": {email: "ghost@x", date: t1, add: 0, del: 0, files: 0}},
		contributors: map[string]*ContributorStat{
			"ghost@x": {Email: "ghost@x", Name: "G", Commits: 1, ActiveDays: 1, FilesTouched: 0},
		},
		files: map[string]*fileEntry{},
	}
	profiles := DevProfiles(ds, "", 0)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	if profiles[0].Specialization != 0 {
		t.Errorf("no-files dev specialization = %.2f, want 0", profiles[0].Specialization)
	}

	// Case 2: dev with 1 file in 1 dir. Should be maximally specialized.
	ds2 := &Dataset{
		Earliest: t1, Latest: t1,
		commits: map[string]*commitEntry{"c1": {email: "solo@x", date: t1, add: 10, del: 0, files: 1}},
		contributors: map[string]*ContributorStat{
			"solo@x": {Email: "solo@x", Name: "S", Commits: 1, ActiveDays: 1, FilesTouched: 1, Additions: 10},
		},
		files: map[string]*fileEntry{
			"auth/login.go": {commits: 1, devLines: map[string]int64{"solo@x": 10}, devCommits: map[string]int{"solo@x": 1}, monthChurn: map[string]int64{}},
		},
	}
	profiles = DevProfiles(ds2, "", 0)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	if profiles[0].Specialization != 1.0 {
		t.Errorf("single-file-single-dir specialization = %.2f, want 1.0 (fully concentrated — the canonical narrow specialist)", profiles[0].Specialization)
	}
}

// buildSyntheticLargeDataset creates a deterministic dataset shaped like a
// mid-size repo: thousands of devs, tens of thousands of files, with each
// file touched by a few devs. Used by BenchmarkDevProfiles* to exercise the
// allCollabs pre-compute path at a scale where the old O(D × F × D_avg)
// implementation was measurable.
func buildSyntheticLargeDataset(numDevs, numFiles, avgDevsPerFile, numCommits int) *Dataset {
	t1 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	ds := &Dataset{
		CommitCount:  numCommits,
		Earliest:     t1,
		Latest:       t1.AddDate(0, 6, 0),
		commits:      make(map[string]*commitEntry, numCommits),
		contributors: make(map[string]*ContributorStat, numDevs),
		files:        make(map[string]*fileEntry, numFiles),
	}
	for i := 0; i < numDevs; i++ {
		email := fmt.Sprintf("dev-%04d@x", i)
		ds.contributors[email] = &ContributorStat{
			Email: email, Name: email,
			Commits: numCommits / numDevs, ActiveDays: 1, FilesTouched: avgDevsPerFile,
			Additions: 100, Deletions: 10,
		}
	}
	for i := 0; i < numCommits; i++ {
		email := fmt.Sprintf("dev-%04d@x", i%numDevs)
		sha := fmt.Sprintf("c%06d", i)
		ds.commits[sha] = &commitEntry{
			email: email, date: t1.Add(time.Duration(i) * time.Hour),
			add: 10, del: 1, files: 1,
		}
	}
	// Deterministic dev assignment per file: stride through dev list.
	for i := 0; i < numFiles; i++ {
		path := fmt.Sprintf("src/pkg%03d/file%04d.go", i/100, i)
		fe := &fileEntry{
			commits:    avgDevsPerFile,
			additions:  100 * int64(avgDevsPerFile),
			deletions:  10 * int64(avgDevsPerFile),
			devLines:   make(map[string]int64, avgDevsPerFile),
			devCommits: make(map[string]int, avgDevsPerFile),
			monthChurn: map[string]int64{"2024-01": 100 * int64(avgDevsPerFile)},
			firstChange: t1, lastChange: t1,
		}
		for d := 0; d < avgDevsPerFile; d++ {
			email := fmt.Sprintf("dev-%04d@x", (i*7+d*11)%numDevs) // stride + prime for spread
			fe.devLines[email] = 100
			fe.devCommits[email] = 1
		}
		ds.files[path] = fe
	}
	return ds
}

// BenchmarkDevProfilesAll measures the full-dataset path (generate profiles
// for every dev). Before the allCollabs pre-compute, this was the hot path
// in `report` on large repos — kubernetes-scale took >80s even though stats
// on the same data took <12s. The pre-compute makes this roughly linear in
// F × D_per_file² instead of D × F × D_per_file.
func BenchmarkDevProfilesAll(b *testing.B) {
	ds := buildSyntheticLargeDataset(1000, 10000, 5, 30000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = DevProfiles(ds, "", 0)
	}
}

// BenchmarkDevProfilesFilterEmail measures the single-dev query path.
// The pre-compute does more work than strictly necessary for a single dev,
// but the absolute cost is bounded by F × D_per_file² and remains fast.
func BenchmarkDevProfilesFilterEmail(b *testing.B) {
	ds := buildSyntheticLargeDataset(1000, 10000, 5, 30000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = DevProfiles(ds, "dev-0042@x", 0)
	}
}
