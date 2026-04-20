package stats

import (
	"testing"
	"time"
)

func TestExtractExtensionPolicy(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Happy path
		{"main.go", ".go"},
		{"src/pkg/util.go", ".go"},
		{"foo/bar/Baz.PNG", ".png"}, // lowercased
		// Multi-dot: last segment wins.
		{"archive.tar.gz", ".gz"},
		{"src/.eslintrc.json", ".json"}, // dotfile with a real ext splits on last
		// Single-dot dotfiles keep full name — ".gitignore" carries
		// meaning as a group; merging with "(none)" would confuse a
		// Makefile-only repo with a dotfile-only one.
		{".gitignore", ".gitignore"},
		{".env", ".env"},
		{"project/.env", ".env"},
		// Extensionless files.
		{"Makefile", "(none)"},
		{"LICENSE", "(none)"},
		{"bin/run", "(none)"},
		// Degenerate.
		{"", "(none)"},
		{"weird.", "(none)"},
		{".", "(none)"},    // single dot is not a dotfile, just noise
		{"..", "(none)"},   // two dots → trailing-dot rule collapses
		{"/", "(none)"},    // just a separator
		{"foo/", "(none)"}, // trailing slash, empty basename
		// Multi-input stem prefix. LoadMultiJSONL prepends "<stem>:"
		// to root-level paths, and the stem may legitimately contain
		// dots; without stripping, those dots would be mistaken for
		// extensions. Only reaches the basename for root-level files —
		// nested paths already discard the prefix via the slash split.
		{"repo.v1:Makefile", "(none)"},
		{"repo.v1:LICENSE", "(none)"},
		{"repo.v1:foo.go", ".go"},   // real ext still wins after prefix strip
		{"repo:Makefile", "(none)"}, // stem with no dots — same rule
		{"repo.v1:.gitignore", ".gitignore"}, // dotfile survives prefix
		{"repo.v1:src/foo.go", ".go"},        // nested path: slash strips prefix first
		{"repo.v1:", "(none)"},               // prefix with empty basename
	}
	for _, c := range cases {
		got := extractExtension(c.path)
		if got != c.want {
			t.Errorf("extractExtension(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestExtensionStatsAggregation(t *testing.T) {
	// Hand-built dataset so aggregation is inspectable: two .go files
	// with distinct devs, one .yaml shared by both, and a Makefile
	// (extensionless) owned by one dev. First/last dates differ so the
	// aggregator must track min/max across files, not last-seen only.
	ds := &Dataset{
		Latest: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		files: map[string]*fileEntry{
			"cmd/main.go": {
				additions: 100, deletions: 20, recentChurn: 80,
				devLines:    map[string]int64{"alice@x": 90, "bob@x": 30},
				firstChange: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				lastChange:  time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
			},
			"internal/util.go": {
				additions: 40, deletions: 10, recentChurn: 30,
				devLines:    map[string]int64{"alice@x": 50},
				firstChange: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
				lastChange:  time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
			},
			"deploy/prod.yaml": {
				additions: 20, deletions: 5, recentChurn: 15,
				devLines:    map[string]int64{"alice@x": 15, "bob@x": 10},
				firstChange: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
				lastChange:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			},
			"Makefile": {
				additions: 5, deletions: 1, recentChurn: 2,
				devLines:    map[string]int64{"bob@x": 6},
				firstChange: time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC),
				lastChange:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	result := ExtensionStats(ds, 0)
	if len(result) != 3 {
		t.Fatalf("got %d buckets, want 3 (.go, .yaml, (none))", len(result))
	}

	// Sort order: recent churn desc → .go (110) > .yaml (15) > (none) (2).
	if result[0].Ext != ".go" {
		t.Errorf("result[0] = %q, want .go", result[0].Ext)
	}
	if result[0].Files != 2 {
		t.Errorf(".go files = %d, want 2", result[0].Files)
	}
	if result[0].Churn != 170 { // 120 + 50
		t.Errorf(".go churn = %d, want 170", result[0].Churn)
	}
	if result[0].RecentChurn != 110 {
		t.Errorf(".go recentChurn = %.1f, want 110", result[0].RecentChurn)
	}
	if result[0].UniqueDevs != 2 { // alice, bob across both .go files
		t.Errorf(".go devs = %d, want 2", result[0].UniqueDevs)
	}
	if result[0].FirstSeen != "2024-01-01" {
		t.Errorf(".go firstSeen = %q, want 2024-01-01", result[0].FirstSeen)
	}
	if result[0].LastSeen != "2024-05-01" {
		t.Errorf(".go lastSeen = %q, want 2024-05-01 (max across .go files)", result[0].LastSeen)
	}

	// (none) bucket is last (lowest recent churn). Confirm Makefile
	// collapsed there and the FirstSeen predates the .go range — the
	// aggregator must take the earliest firstChange across bucket files.
	last := result[len(result)-1]
	if last.Ext != "(none)" {
		t.Errorf("last bucket = %q, want (none)", last.Ext)
	}
	if last.Files != 1 || last.FirstSeen != "2023-12-01" {
		t.Errorf("(none) bucket = %+v", last)
	}
}

// Regression: a file renamed across extensions (foo.js → foo.ts)
// collapses onto one canonical path after applyRenames. Bucketing on
// that canonical path alone would assign ALL historical churn to .ts
// and zero to .js — an ugly skew in migration-heavy repos. The fix
// uses fileEntry.byExt (populated at per-change time) to split the
// lineage back across both buckets.
func TestExtensionStatsHonorsPerEraSplit(t *testing.T) {
	ds := &Dataset{
		Latest: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		files: map[string]*fileEntry{
			// Simulates what reader.go produces post-applyRenames: one
			// canonical key ("foo.ts") with churn split across the two
			// extensions the file held during its lifetime.
			"foo.ts": {
				additions: 1200, deletions: 300,
				recentChurn: 900,
				devLines:    map[string]int64{"alice@x": 900, "bob@x": 600},
				byExt: map[string]*extContribution{
					".js": {
						churn:       1000,
						recentChurn: 200, // old era, decayed
						firstChange: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
						lastChange:  time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC),
					},
					".ts": {
						churn:       500,
						recentChurn: 700, // recent migration, less decay
						firstChange: time.Date(2024, 2, 16, 0, 0, 0, 0, time.UTC),
						lastChange:  time.Date(2024, 5, 30, 0, 0, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	result := ExtensionStats(ds, 0)
	if len(result) != 2 {
		t.Fatalf("got %d buckets, want 2 (.js + .ts)", len(result))
	}

	var js, ts *ExtensionStat
	for i := range result {
		switch result[i].Ext {
		case ".js":
			js = &result[i]
		case ".ts":
			ts = &result[i]
		}
	}
	if js == nil || ts == nil {
		t.Fatalf("missing .js or .ts bucket; result=%+v", result)
	}

	// Churn must reflect the per-era split, NOT be lumped into .ts.
	if js.Churn != 1000 || ts.Churn != 500 {
		t.Errorf("churn split wrong: .js=%d (want 1000), .ts=%d (want 500)", js.Churn, ts.Churn)
	}
	// RecentChurn similarly preserved per-era.
	if js.RecentChurn != 200 || ts.RecentChurn != 700 {
		t.Errorf("recent churn split wrong: .js=%.1f, .ts=%.1f", js.RecentChurn, ts.RecentChurn)
	}
	// One file lineage, but counts once per ext it held.
	if js.Files != 1 || ts.Files != 1 {
		t.Errorf("files per bucket: .js=%d, .ts=%d, want 1/1 (lineage counts in each bucket it held)", js.Files, ts.Files)
	}
	// Dates: each ext reports the range from its own era, not the whole
	// file's range. .js ended when the migration started; .ts starts
	// the day after.
	if js.LastSeen != "2024-02-15" {
		t.Errorf(".js LastSeen = %q, want 2024-02-15 (migration cutoff, not post-rename activity)", js.LastSeen)
	}
	if ts.FirstSeen != "2024-02-16" {
		t.Errorf(".ts FirstSeen = %q, want 2024-02-16 (post-rename era start)", ts.FirstSeen)
	}
}

// Regression: applyRenames calls mergeFileEntry when two fileEntries
// collapse onto the same canonical path (foo.js → foo.ts). If the
// byExt merge drops an entry, sums dates incorrectly, or clobbers an
// overlapping bucket, ExtensionStats silently loses per-era
// attribution and the TestExtensionStatsHonorsPerEraSplit consumer-
// side guard wouldn't catch it — that test hand-builds byExt and
// never exercises the merger.
func TestMergeFileEntryByExt(t *testing.T) {
	// dst covers two extensions; src covers one that overlaps (.js)
	// and one that's new to dst (.md). After merge: .js aggregates,
	// .md is transferred, .ts is untouched.
	dst := &fileEntry{
		devLines:   map[string]int64{"alice@x": 10},
		devCommits: map[string]int{"alice@x": 1},
		monthChurn: map[string]int64{},
		byExt: map[string]*extContribution{
			".js": {
				churn:       300,
				recentChurn: 100,
				firstChange: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				lastChange:  time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC),
			},
			".ts": {
				churn:       500,
				recentChurn: 400,
				firstChange: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				lastChange:  time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	src := &fileEntry{
		devLines:   map[string]int64{"bob@x": 20},
		devCommits: map[string]int{"bob@x": 2},
		monthChurn: map[string]int64{},
		byExt: map[string]*extContribution{
			".js": {
				// Earlier first + later last than dst — both bounds
				// should widen after merge.
				churn:       100,
				recentChurn: 50,
				firstChange: time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC),
				lastChange:  time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC),
			},
			".md": {
				churn:       40,
				recentChurn: 10,
				firstChange: time.Date(2023, 3, 1, 0, 0, 0, 0, time.UTC),
				lastChange:  time.Date(2023, 8, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	mergeFileEntry(dst, src)

	if len(dst.byExt) != 3 {
		t.Fatalf("dst.byExt size = %d, want 3 (.js .ts .md)", len(dst.byExt))
	}

	// Overlapping bucket: churn and recentChurn add; dates widen.
	js := dst.byExt[".js"]
	if js.churn != 400 {
		t.Errorf(".js churn = %d, want 400 (300+100)", js.churn)
	}
	if js.recentChurn != 150 {
		t.Errorf(".js recentChurn = %.1f, want 150 (100+50)", js.recentChurn)
	}
	if !js.firstChange.Equal(time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf(".js firstChange = %v, want 2022-06-01 (src's earlier date won)", js.firstChange)
	}
	if !js.lastChange.Equal(time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf(".js lastChange = %v, want 2023-12-01 (src's later date won)", js.lastChange)
	}

	// Non-overlapping bucket transferred from src: must be present
	// with src's values preserved verbatim.
	md := dst.byExt[".md"]
	if md == nil {
		t.Fatal(".md bucket missing after merge — src entry dropped")
	}
	if md.churn != 40 || md.recentChurn != 10 {
		t.Errorf(".md = %+v, want churn=40 recentChurn=10", md)
	}

	// dst-only bucket untouched.
	ts := dst.byExt[".ts"]
	if ts.churn != 500 || ts.recentChurn != 400 {
		t.Errorf(".ts = %+v, want churn=500 recentChurn=400 (dst value preserved)", ts)
	}
}

// mergeFileEntry must lazily create dst.byExt when dst started nil —
// covers the case where a long-lived file without extension history
// collides with a newly-seen one that carries a byExt map.
func TestMergeFileEntryByExtNilDst(t *testing.T) {
	dst := &fileEntry{
		devLines:   map[string]int64{},
		devCommits: map[string]int{},
		monthChurn: map[string]int64{},
	}
	src := &fileEntry{
		devLines:   map[string]int64{},
		devCommits: map[string]int{},
		monthChurn: map[string]int64{},
		byExt: map[string]*extContribution{
			".go": {churn: 10, recentChurn: 5},
		},
	}
	mergeFileEntry(dst, src)
	if dst.byExt == nil {
		t.Fatal("dst.byExt still nil after merging from non-nil src")
	}
	if got := dst.byExt[".go"]; got == nil || got.churn != 10 {
		t.Errorf("dst.byExt[.go] = %+v, want churn=10", got)
	}
}

// Inverse case: src has no byExt (e.g. legacy or hand-built). Merge
// must be a no-op on dst.byExt and not panic.
func TestMergeFileEntryByExtNilSrc(t *testing.T) {
	dst := &fileEntry{
		devLines:   map[string]int64{},
		devCommits: map[string]int{},
		monthChurn: map[string]int64{},
		byExt: map[string]*extContribution{
			".rs": {churn: 7},
		},
	}
	src := &fileEntry{
		devLines:   map[string]int64{},
		devCommits: map[string]int{},
		monthChurn: map[string]int64{},
	}
	mergeFileEntry(dst, src)
	if len(dst.byExt) != 1 || dst.byExt[".rs"].churn != 7 {
		t.Errorf("dst.byExt mutated by nil-src merge: %+v", dst.byExt)
	}
}

func TestExtensionStatsTopN(t *testing.T) {
	ds := &Dataset{
		files: map[string]*fileEntry{
			"a.go":  {recentChurn: 100, devLines: map[string]int64{"a": 1}},
			"b.py":  {recentChurn: 50, devLines: map[string]int64{"a": 1}},
			"c.rs":  {recentChurn: 10, devLines: map[string]int64{"a": 1}},
		},
	}
	result := ExtensionStats(ds, 2)
	if len(result) != 2 {
		t.Fatalf("top 2 len = %d", len(result))
	}
	if result[0].Ext != ".go" || result[1].Ext != ".py" {
		t.Errorf("top 2 = [%s, %s], want [.go, .py]", result[0].Ext, result[1].Ext)
	}
}
