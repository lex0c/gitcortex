package stats

import (
	"testing"
	"time"
)

func TestRepoBreakdown_GroupsByPrefix(t *testing.T) {
	ds := &Dataset{
		commits: map[string]*commitEntry{
			"a1": {email: "me@x.com", repo: "alpha", date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), add: 10, del: 0},
			"a2": {email: "me@x.com", repo: "alpha", date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC), add: 5, del: 2},
			"b1": {email: "you@x.com", repo: "beta", date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), add: 30, del: 0},
		},
		files: map[string]*fileEntry{
			"alpha:main.go":   {},
			"alpha:helper.go": {},
			"beta:app.py":     {},
		},
	}

	breakdown := RepoBreakdown(ds, "")
	if len(breakdown) != 2 {
		t.Fatalf("want 2 repos, got %d", len(breakdown))
	}
	// Sort is by commit count desc, so alpha first.
	if breakdown[0].Repo != "alpha" || breakdown[0].Commits != 2 {
		t.Errorf("alpha: got %+v", breakdown[0])
	}
	if breakdown[0].Files != 2 {
		t.Errorf("alpha files: got %d, want 2", breakdown[0].Files)
	}
	if breakdown[1].Repo != "beta" || breakdown[1].Commits != 1 {
		t.Errorf("beta: got %+v", breakdown[1])
	}
	// Pct totals — alpha 2/3, beta 1/3.
	if got := breakdown[0].PctOfTotalCommits; got < 66.6 || got > 66.7 {
		t.Errorf("alpha pct commits: got %.2f, want ~66.67", got)
	}
}

func TestRepoBreakdown_EmailFilter(t *testing.T) {
	ds := &Dataset{
		commits: map[string]*commitEntry{
			"a1": {email: "me@x.com", repo: "alpha", date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), add: 10},
			"a2": {email: "other@x.com", repo: "alpha", date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), add: 5},
			"b1": {email: "me@x.com", repo: "beta", date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), add: 30},
		},
		// Per-dev file counts: alpha has 2 files but me@ touched only 1;
		// beta has 1 file me@ touched. Other dev's exclusive file in
		// alpha must NOT be counted toward me@'s repo Files.
		files: map[string]*fileEntry{
			"alpha:mine.go":  {devCommits: map[string]int{"me@x.com": 1}},
			"alpha:other.go": {devCommits: map[string]int{"other@x.com": 1}},
			"beta:both.py":   {devCommits: map[string]int{"me@x.com": 1}},
		},
	}
	breakdown := RepoBreakdown(ds, "me@x.com")
	if len(breakdown) != 2 {
		t.Fatalf("want 2 repos for me@x.com, got %d", len(breakdown))
	}
	for _, b := range breakdown {
		if b.Commits != 1 {
			t.Errorf("repo %s: expected 1 commit (filtered), got %d", b.Repo, b.Commits)
		}
		if b.Files != 1 {
			t.Errorf("repo %s: expected 1 file (filtered), got %d", b.Repo, b.Files)
		}
	}
}

// A rename within a repo collapses two file paths onto one canonical
// path during finalizeDataset. The collapsed path keeps its `<repo>:`
// prefix, so RepoBreakdown should still attribute the file to the
// correct repo. Regression guard: without proper handling, the
// post-rename canonical key would either lose its prefix (and bucket
// into "(repo)") or duplicate-count.
func TestRepoBreakdown_SurvivesRename(t *testing.T) {
	ds := newDataset()
	ds.commits["c1"] = &commitEntry{
		email: "me@x.com", repo: "alpha",
		date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), add: 5,
	}
	ds.commits["c2"] = &commitEntry{
		email: "me@x.com", repo: "alpha",
		date: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC), add: 10,
	}
	ds.files["alpha:old.go"] = &fileEntry{
		devCommits: map[string]int{"me@x.com": 1},
	}
	ds.files["alpha:new.go"] = &fileEntry{
		devCommits: map[string]int{"me@x.com": 1},
	}
	ds.renameEdges = []renameEdge{{
		oldPath: "alpha:old.go", newPath: "alpha:new.go",
		commitDate: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
	}}
	applyRenames(ds)

	breakdown := RepoBreakdown(ds, "")
	if len(breakdown) != 1 || breakdown[0].Repo != "alpha" {
		t.Fatalf("want single alpha row, got %+v", breakdown)
	}
	// After rename collapse: 1 canonical file, both commits attributed.
	if breakdown[0].Files != 1 {
		t.Errorf("want 1 file (post-rename), got %d", breakdown[0].Files)
	}
	if breakdown[0].Commits != 2 {
		t.Errorf("want 2 commits, got %d", breakdown[0].Commits)
	}
}

func TestRepoBreakdown_SingleRepoFallback(t *testing.T) {
	// No prefix tag → all commits bucket under "(repo)".
	ds := &Dataset{
		commits: map[string]*commitEntry{
			"x1": {email: "me@x.com", date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), add: 5},
		},
		files: map[string]*fileEntry{
			"src/foo.go": {},
		},
	}
	breakdown := RepoBreakdown(ds, "")
	if len(breakdown) != 1 || breakdown[0].Repo != "(repo)" {
		t.Fatalf("want single (repo) row, got %+v", breakdown)
	}
}
