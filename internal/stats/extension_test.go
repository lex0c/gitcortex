package stats

import (
	"fmt"
	"strings"
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

// DevProfile extensions: verifies that a dev's extension footprint is
// aggregated from the files they touched, sorted churn-desc, with Pct
// equal to files/FilesTouched*100. Uses a hand-built dataset so the
// expected distribution is deterministic.
func TestDevProfileExtensions(t *testing.T) {
	ds := &Dataset{
		Latest: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		contributors: map[string]*ContributorStat{
			"alice@x": {
				Name: "Alice", Email: "alice@x",
				Commits: 4, FilesTouched: 4, ActiveDays: 2,
				FirstDate: "2024-01-01", LastDate: "2024-05-01",
			},
		},
		files: map[string]*fileEntry{
			"cmd/main.go":      {devLines: map[string]int64{"alice@x": 100}, devCommits: map[string]int{"alice@x": 2}, additions: 80, deletions: 20},
			"internal/util.go": {devLines: map[string]int64{"alice@x": 60}, devCommits: map[string]int{"alice@x": 1}, additions: 50, deletions: 10},
			"deploy/prod.yaml": {devLines: map[string]int64{"alice@x": 20}, devCommits: map[string]int{"alice@x": 1}, additions: 15, deletions: 5},
			"Makefile":         {devLines: map[string]int64{"alice@x": 5}, devCommits: map[string]int{"alice@x": 1}, additions: 5, deletions: 0},
		},
		commits:   map[string]*commitEntry{},
		workGrid:  [7][24]int{},
	}

	profiles := DevProfiles(ds, "alice@x", 0)
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1", len(profiles))
	}
	p := profiles[0]

	if len(p.Extensions) != 3 {
		t.Fatalf("alice.Extensions len = %d, want 3 (.go, .yaml, (none))", len(p.Extensions))
	}
	// .go dominates (160 churn across 2 files), .yaml next (20), (none)
	// last (5). Pct based on files/FilesTouched = files/4.
	if p.Extensions[0].Ext != ".go" || p.Extensions[0].Files != 2 || p.Extensions[0].Churn != 160 {
		t.Errorf("[0] = %+v, want {.go, 2, 160}", p.Extensions[0])
	}
	if p.Extensions[0].Pct != 50.0 {
		t.Errorf(".go pct = %.1f, want 50.0 (2/4)", p.Extensions[0].Pct)
	}
	if p.Extensions[1].Ext != ".yaml" || p.Extensions[1].Files != 1 {
		t.Errorf("[1] = %+v, want .yaml/1", p.Extensions[1])
	}
	if p.Extensions[2].Ext != "(none)" {
		t.Errorf("[2] = %+v, want (none)", p.Extensions[2])
	}
}

// Regression: sort MUST be files desc (not churn desc) so the
// displayed Pct — computed from files — is monotonic in both CLI and
// HTML bar widths. Here .py has MORE churn (one huge commit) but
// FEWER files than .go. Under the previous churn-first sort, .py
// would lead and the Pct column (25% for .py, 75% for .go) would
// decrease non-monotonically in a files-sorted visual. Under the
// corrected files-first sort, .go leads as it should.
func TestDevProfileExtensionsSortedByFiles(t *testing.T) {
	ds := &Dataset{
		contributors: map[string]*ContributorStat{
			"alice@x": {Email: "alice@x", Commits: 2, FilesTouched: 4, ActiveDays: 1},
		},
		files: map[string]*fileEntry{
			"a.go": {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
			"b.go": {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
			"c.go": {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
			// One .py file with way more dev-lines than each .go.
			"big.py": {devLines: map[string]int64{"alice@x": 500}, devCommits: map[string]int{"alice@x": 1}},
		},
		commits:  map[string]*commitEntry{},
		workGrid: [7][24]int{},
	}
	p := DevProfiles(ds, "alice@x", 0)[0]
	if p.Extensions[0].Ext != ".go" {
		t.Errorf("[0] = %q, want .go (3 files beats .py's 1 file under files-first sort)", p.Extensions[0].Ext)
	}
	if p.Extensions[0].Pct != 75.0 {
		t.Errorf(".go Pct = %.1f, want 75.0", p.Extensions[0].Pct)
	}
	if p.Extensions[1].Ext != ".py" || p.Extensions[1].Pct != 25.0 {
		t.Errorf("[1] = %+v, want .py @ 25%%", p.Extensions[1])
	}
	// The .py churn (500) is surfaced on the field for JSON consumers
	// even though it ranks second by file count.
	if p.Extensions[1].Churn != 500 {
		t.Errorf(".py Churn = %d, want 500 (raw value still exposed)", p.Extensions[1].Churn)
	}
}

// Edge case: a dev whose only touches are root-level extensionless
// files (Makefile, LICENSE) collapses into a single "(none)" bucket
// at 100% — no crash, no fallthrough.
func TestDevProfileExtensionsAllNone(t *testing.T) {
	ds := &Dataset{
		contributors: map[string]*ContributorStat{
			"ops@x": {Email: "ops@x", Commits: 2, FilesTouched: 2, ActiveDays: 1},
		},
		files: map[string]*fileEntry{
			"Makefile": {devLines: map[string]int64{"ops@x": 30}, devCommits: map[string]int{"ops@x": 1}},
			"LICENSE":  {devLines: map[string]int64{"ops@x": 5}, devCommits: map[string]int{"ops@x": 1}},
		},
		commits:  map[string]*commitEntry{},
		workGrid: [7][24]int{},
	}
	p := DevProfiles(ds, "ops@x", 0)[0]
	if len(p.Extensions) != 1 || p.Extensions[0].Ext != "(none)" {
		t.Fatalf("Extensions = %+v, want single (none) bucket", p.Extensions)
	}
	if p.Extensions[0].Pct != 100.0 {
		t.Errorf("(none) Pct = %.1f, want 100.0", p.Extensions[0].Pct)
	}
}

// Regression: Pct denominator is len(devFiles[email]), NOT
// cs.FilesTouched. FilesTouched includes pure-rename contributions
// (the dev appears in the file's contribFiles but adds zero lines),
// which would deflate all percentages because those files never make
// it into the Scope/Extension numerators. Under the corrected
// denominator the visible Pcts sum to 100 (modulo top-5 truncation).
// This test drives that divergence: FilesTouched = 5 but only 4
// files carry non-zero devLines, so the denominators differ.
func TestDevProfileExtensionsPctDenominatorIgnoresRenames(t *testing.T) {
	ds := &Dataset{
		contributors: map[string]*ContributorStat{
			// FilesTouched = 5 simulates the dev having appeared on a
			// 5th file via a pure rename (no add/del lines). The files
			// map below only has 4 entries with non-zero devLines.
			"alice@x": {Email: "alice@x", Commits: 5, FilesTouched: 5, ActiveDays: 1},
		},
		files: map[string]*fileEntry{
			"a.go": {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
			"b.go": {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
			"c.go": {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
			"d.py": {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
		},
		commits:  map[string]*commitEntry{},
		workGrid: [7][24]int{},
	}
	p := DevProfiles(ds, "alice@x", 0)[0]

	// Visible extensions: .go with 3/4 = 75%, .py with 1/4 = 25%.
	// Under the old (FilesTouched=5) denominator the sum would be
	// 3/5 + 1/5 = 80% — a silent 20% gap from the rename.
	var sum float64
	for _, e := range p.Extensions {
		sum += e.Pct
	}
	if sum != 100.0 {
		t.Errorf("Extensions Pct sum = %.1f, want 100.0 (denominator should be authored files, not FilesTouched)", sum)
	}

	// Spot-check individual values.
	var goPct, pyPct float64
	for _, e := range p.Extensions {
		if e.Ext == ".go" {
			goPct = e.Pct
		}
		if e.Ext == ".py" {
			pyPct = e.Pct
		}
	}
	if goPct != 75.0 {
		t.Errorf(".go Pct = %.1f, want 75.0 (3/4)", goPct)
	}
	if pyPct != 25.0 {
		t.Errorf(".py Pct = %.1f, want 25.0 (1/4)", pyPct)
	}

	// Same invariant on Scope: a.go/b.go/c.go all at root, d.py at
	// root → single bucket "." at 100%. Renames shouldn't count.
	var scopeSum float64
	for _, s := range p.Scope {
		scopeSum += s.Pct
	}
	if scopeSum != 100.0 {
		t.Errorf("Scope Pct sum = %.1f, want 100.0", scopeSum)
	}
}

// Edge case: a dev whose commits never touch any file (all commits
// had files_changed = 0, so no commit_file records reached fe.devLines).
// devFiles[email] is absent; Extensions must be nil — both HTML
// templates guard on truthiness so a nil slice renders as nothing.
func TestDevProfileExtensionsEmpty(t *testing.T) {
	ds := &Dataset{
		contributors: map[string]*ContributorStat{
			"ghost@x": {Email: "ghost@x", Commits: 1, FilesTouched: 0, ActiveDays: 1},
		},
		files:    map[string]*fileEntry{},
		commits:  map[string]*commitEntry{},
		workGrid: [7][24]int{},
	}
	p := DevProfiles(ds, "ghost@x", 0)[0]
	if len(p.Extensions) != 0 {
		t.Errorf("Extensions = %+v, want empty", p.Extensions)
	}
}

// Regression: once a dev has >5 buckets the top-5 truncation is the
// ONLY way Pct sum can drop below 100. Lock that invariant — a
// silent change to the cap size or the sort would surface here.
func TestDevProfileExtensionsTruncationSum(t *testing.T) {
	// 6 files, all single-ext, each 1 file → all 6 buckets carry the
	// same Files=1. Top-5 sort keeps 5 (tiebroken by churn desc then
	// ext asc); the 6th drops off, contributing its ~16.7% to the
	// gap. Math: 5 × round(1/6 × 1000)/10 = 5 × 16.7 = 83.5%.
	ds := &Dataset{
		contributors: map[string]*ContributorStat{
			"alice@x": {Email: "alice@x", Commits: 6, FilesTouched: 6, ActiveDays: 1},
		},
		files: map[string]*fileEntry{
			"a.go":  {devLines: map[string]int64{"alice@x": 60}, devCommits: map[string]int{"alice@x": 1}},
			"b.py":  {devLines: map[string]int64{"alice@x": 50}, devCommits: map[string]int{"alice@x": 1}},
			"c.rs":  {devLines: map[string]int64{"alice@x": 40}, devCommits: map[string]int{"alice@x": 1}},
			"d.ts":  {devLines: map[string]int64{"alice@x": 30}, devCommits: map[string]int{"alice@x": 1}},
			"e.md":  {devLines: map[string]int64{"alice@x": 20}, devCommits: map[string]int{"alice@x": 1}},
			"f.sh":  {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
		},
		commits:  map[string]*commitEntry{},
		workGrid: [7][24]int{},
	}
	p := DevProfiles(ds, "alice@x", 0)[0]
	if len(p.Extensions) != 5 {
		t.Fatalf("Extensions len = %d, want 5 (truncated from 6)", len(p.Extensions))
	}
	var sum float64
	for _, e := range p.Extensions {
		sum += e.Pct
	}
	// Must be strictly <100 (truncated) but close (~83.5 for this
	// fixture). Wide tolerance — the exact value is rounding-sensitive.
	if sum >= 100.0 {
		t.Errorf("truncated Extensions sum = %.1f, want strictly < 100", sum)
	}
	if sum < 80.0 || sum > 86.0 {
		t.Errorf("truncated Extensions sum = %.1f, want ~83.5 (5/6 buckets × 16.7%%)", sum)
	}
}

// Regression at the INGEST level: a pure rename (commit_file with
// additions=0 && deletions=0) used to create a zero-valued entry in
// fe.devLines, which then made len(devFiles[email]) count that file
// as "authored" by the renaming dev. The 50/50 symptom: Alice edits
// one .go file (5 lines) and separately renames one .md file (0
// lines); under the broken ingest she shows up with `.go (50%)` +
// `.md (50%)` in the Extensions fingerprint even though she never
// wrote a single line in .md. The fix skips the zero-line write
// site so devLines stays the "lines this dev contributed" map.
// devCommits is intentionally still bumped — that map preserves the
// "dev appeared on this file" signal for any caller that wants it.
func TestDevProfilePureRenamesNotAuthored(t *testing.T) {
	jsonl := `{"type":"commit","sha":"c1","tree":"t","parents":[],"author_name":"Alice","author_email":"alice@x","author_date":"2024-01-10T10:00:00Z","committer_name":"Alice","committer_email":"alice@x","committer_date":"2024-01-10T10:00:00Z","additions":5,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c1","path_current":"src/main.go","path_previous":"src/main.go","status":"M","old_hash":"0","new_hash":"1","old_size":0,"new_size":0,"additions":5,"deletions":0}
{"type":"commit","sha":"c2","tree":"t","parents":[],"author_name":"Alice","author_email":"alice@x","author_date":"2024-01-12T10:00:00Z","committer_name":"Alice","committer_email":"alice@x","committer_date":"2024-01-12T10:00:00Z","additions":0,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"c2","path_current":"docs/renamed.md","path_previous":"docs/old.md","status":"R100","old_hash":"0","new_hash":"2","old_size":0,"new_size":0,"additions":0,"deletions":0}
`
	ds, err := streamLoad(strings.NewReader(jsonl), LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := DevProfiles(ds, "alice@x", 0)[0]

	// Only the .go edit counts as authored. The .md pure-rename must
	// not show up in Extensions or Scope.
	if len(p.Extensions) != 1 || p.Extensions[0].Ext != ".go" {
		t.Errorf("Extensions = %+v, want single .go bucket", p.Extensions)
	}
	if p.Extensions[0].Pct != 100.0 {
		t.Errorf(".go Pct = %.1f, want 100.0 (rename should not inflate denominator)", p.Extensions[0].Pct)
	}
	if len(p.Scope) != 1 || p.Scope[0].Dir != "src" {
		t.Errorf("Scope = %+v, want single src/ bucket", p.Scope)
	}
	if p.Scope[0].Pct != 100.0 {
		t.Errorf("src/ Pct = %.1f, want 100.0", p.Scope[0].Pct)
	}

	// Cross-check downstream stats that also consume fe.devLines:
	// UniqueDevs on the renamed file must be 0 now (no one authored
	// lines on it) — before the fix it would have been 1.
	hotspots := FileHotspots(ds, 0)
	for _, h := range hotspots {
		if h.Path == "docs/renamed.md" && h.UniqueDevs != 0 {
			t.Errorf("pure-renamed file unique devs = %d, want 0 (no line authors)", h.UniqueDevs)
		}
	}
}

// Regression: when truncation drops buckets, the count goes into
// ScopeHidden/ExtensionsHidden so renderers can surface "+N more"
// next to the visible list. Silent when no truncation (the whole
// point is to appear only when the Pct-sum <100% case makes readers
// suspect a bug).
func TestDevProfileHiddenCounters(t *testing.T) {
	// Build a dev with 7 extensions and 6 dirs so both counters hit.
	ds := &Dataset{
		contributors: map[string]*ContributorStat{
			"alice@x": {Email: "alice@x", Commits: 7, FilesTouched: 7, ActiveDays: 1},
		},
		files: map[string]*fileEntry{
			"d1/a.go":  {devLines: map[string]int64{"alice@x": 70}, devCommits: map[string]int{"alice@x": 1}},
			"d2/b.py":  {devLines: map[string]int64{"alice@x": 60}, devCommits: map[string]int{"alice@x": 1}},
			"d3/c.rs":  {devLines: map[string]int64{"alice@x": 50}, devCommits: map[string]int{"alice@x": 1}},
			"d4/d.ts":  {devLines: map[string]int64{"alice@x": 40}, devCommits: map[string]int{"alice@x": 1}},
			"d5/e.md":  {devLines: map[string]int64{"alice@x": 30}, devCommits: map[string]int{"alice@x": 1}},
			"d6/f.sh":  {devLines: map[string]int64{"alice@x": 20}, devCommits: map[string]int{"alice@x": 1}},
			"d6/g.yml": {devLines: map[string]int64{"alice@x": 10}, devCommits: map[string]int{"alice@x": 1}},
		},
		commits:  map[string]*commitEntry{},
		workGrid: [7][24]int{},
	}
	p := DevProfiles(ds, "alice@x", 0)[0]

	// 6 dirs → 5 visible, 1 hidden.
	if p.ScopeHidden != 1 {
		t.Errorf("ScopeHidden = %d, want 1", p.ScopeHidden)
	}
	// 7 extensions → 5 visible, 2 hidden.
	if p.ExtensionsHidden != 2 {
		t.Errorf("ExtensionsHidden = %d, want 2", p.ExtensionsHidden)
	}
}

// Completes the Hidden-counter family: TopFiles truncates at 10,
// Collaborators at 5. Both used to drop buckets silently — the "+N
// more" surfaced for Scope/Extensions had no counterpart here, so a
// dev with 25 touched files or 12 frequent collaborators looked like
// they had exactly 10 / 5. Build a dev with 12 files and 7
// collaborators; assert the counters report the true drop count.
func TestDevProfileHiddenCountersTopFilesAndCollaborators(t *testing.T) {
	files := map[string]*fileEntry{}
	// 12 files authored by alice → TopFilesHidden = 2.
	for i := 0; i < 12; i++ {
		path := fmt.Sprintf("dir%d/file%d.go", i, i)
		files[path] = &fileEntry{
			devLines:   map[string]int64{"alice@x": int64(100 - i*5)},
			devCommits: map[string]int{"alice@x": 1},
		}
	}
	// Seed 6 shared files between alice and each of 6 other devs →
	// alice has 6 collaborators total; top-5 truncation gives
	// CollaboratorsHidden = 1.
	for i := 0; i < 6; i++ {
		path := fmt.Sprintf("shared/collab%d.go", i)
		collab := fmt.Sprintf("bob%d@x", i)
		files[path] = &fileEntry{
			devLines:   map[string]int64{"alice@x": 50, collab: 50},
			devCommits: map[string]int{"alice@x": 1, collab: 1},
		}
	}
	contribs := map[string]*ContributorStat{
		"alice@x": {Email: "alice@x", Commits: 18, FilesTouched: 18, ActiveDays: 1},
	}
	for i := 0; i < 6; i++ {
		contribs[fmt.Sprintf("bob%d@x", i)] = &ContributorStat{
			Email: fmt.Sprintf("bob%d@x", i), Commits: 1, FilesTouched: 1, ActiveDays: 1,
		}
	}
	ds := &Dataset{
		contributors: contribs,
		files:        files,
		commits:      map[string]*commitEntry{},
		workGrid:     [7][24]int{},
	}
	p := DevProfiles(ds, "alice@x", 0)[0]
	if len(p.TopFiles) != 10 {
		t.Fatalf("TopFiles len = %d, want 10 (truncated from 18)", len(p.TopFiles))
	}
	if p.TopFilesHidden != 8 {
		t.Errorf("TopFilesHidden = %d, want 8 (18 - 10)", p.TopFilesHidden)
	}
	if len(p.Collaborators) != 5 {
		t.Fatalf("Collaborators len = %d, want 5 (truncated from 6)", len(p.Collaborators))
	}
	if p.CollaboratorsHidden != 1 {
		t.Errorf("CollaboratorsHidden = %d, want 1 (6 - 5)", p.CollaboratorsHidden)
	}
}

// Silent when nothing to hide — the counters must be zero so the
// renderers don't emit "+0 more" (noise) for the common case.
func TestDevProfileHiddenCountersZeroWhenFits(t *testing.T) {
	ds := &Dataset{
		contributors: map[string]*ContributorStat{
			"bob@x": {Email: "bob@x", Commits: 3, FilesTouched: 3, ActiveDays: 1},
		},
		files: map[string]*fileEntry{
			"src/a.go":  {devLines: map[string]int64{"bob@x": 10}, devCommits: map[string]int{"bob@x": 1}},
			"src/b.go":  {devLines: map[string]int64{"bob@x": 10}, devCommits: map[string]int{"bob@x": 1}},
			"docs/x.md": {devLines: map[string]int64{"bob@x": 5}, devCommits: map[string]int{"bob@x": 1}},
		},
		commits:  map[string]*commitEntry{},
		workGrid: [7][24]int{},
	}
	p := DevProfiles(ds, "bob@x", 0)[0]
	if p.ScopeHidden != 0 || p.ExtensionsHidden != 0 {
		t.Errorf("Hidden counters: Scope=%d Ext=%d, want 0/0 (dev has ≤5 buckets each)",
			p.ScopeHidden, p.ExtensionsHidden)
	}
	// TopFiles cap is 10, Collaborators cap is 5 — bob has 3 files
	// and zero collaborators, so both must stay at 0.
	if p.TopFilesHidden != 0 || p.CollaboratorsHidden != 0 {
		t.Errorf("Hidden counters: TopFiles=%d Collab=%d, want 0/0",
			p.TopFilesHidden, p.CollaboratorsHidden)
	}
}

// Truncate to top-5 when a dev's extension set is larger. Under the
// files-first sort, ties on file count (all 1 each here) fall through
// to churn desc, so the top 5 by churn still win.
func TestDevProfileExtensionsTopFive(t *testing.T) {
	ds := &Dataset{
		Latest: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		contributors: map[string]*ContributorStat{
			"alice@x": {Email: "alice@x", Commits: 7, FilesTouched: 7, ActiveDays: 1},
		},
		files: map[string]*fileEntry{
			"a.go":  {devLines: map[string]int64{"alice@x": 100}, devCommits: map[string]int{"alice@x": 1}},
			"a.py":  {devLines: map[string]int64{"alice@x": 80}, devCommits: map[string]int{"alice@x": 1}},
			"a.rs":  {devLines: map[string]int64{"alice@x": 60}, devCommits: map[string]int{"alice@x": 1}},
			"a.ts":  {devLines: map[string]int64{"alice@x": 40}, devCommits: map[string]int{"alice@x": 1}},
			"a.md":  {devLines: map[string]int64{"alice@x": 20}, devCommits: map[string]int{"alice@x": 1}},
			"a.sh":  {devLines: map[string]int64{"alice@x": 5}, devCommits: map[string]int{"alice@x": 1}},
			"a.yml": {devLines: map[string]int64{"alice@x": 3}, devCommits: map[string]int{"alice@x": 1}},
		},
		commits:  map[string]*commitEntry{},
		workGrid: [7][24]int{},
	}
	p := DevProfiles(ds, "alice@x", 0)[0]
	if len(p.Extensions) != 5 {
		t.Fatalf("Extensions len = %d, want top-5 truncation", len(p.Extensions))
	}
	// Top 5 by churn: .go .py .rs .ts .md. .sh and .yml excluded.
	for i, want := range []string{".go", ".py", ".rs", ".ts", ".md"} {
		if p.Extensions[i].Ext != want {
			t.Errorf("[%d] = %q, want %q", i, p.Extensions[i].Ext, want)
		}
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
