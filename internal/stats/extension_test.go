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
