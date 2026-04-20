package report

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestHumanizeAgoAt(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		lastDate  string
		wantLabel string
		wantBucket string
	}{
		// Boundary on day 0 — "today" reads cleaner than "0d ago".
		{"same day", "2026-04-20", "today", "fresh"},
		{"one day", "2026-04-19", "1d ago", "fresh"},
		{"29 days", "2026-03-22", "29d ago", "fresh"},
		// Transition from days to months at 30.
		{"30 days exact", "2026-03-21", "1mo ago", "fresh"},
		{"two months", "2026-02-15", "2mo ago", "stable"},
		{"eleven months", "2025-05-25", "11mo ago", "stable"},
		// 365-day boundary: still "stable" at exactly one year,
		// "stale" the day after. 2025-04-20 is 365 days before now.
		{"one year exact (stable boundary)", "2025-04-20", "1y ago", "stable"},
		{"one year + one day (stale)", "2025-04-19", "1y ago", "stale"},
		{"two years stale", "2024-04-10", "2y ago", "stale"},
		// Parse failure yields empty.
		{"bad input", "not-a-date", "", ""},
		// Future dates (clock skew) yield empty — we don't label "in 3d"
		// on an index that exists to surface recency of PAST commits.
		{"future date", "2026-05-01", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			label, bucket := humanizeAgoAt(c.lastDate, now)
			if label != c.wantLabel {
				t.Errorf("label: got %q, want %q", label, c.wantLabel)
			}
			if bucket != c.wantBucket {
				t.Errorf("bucket: got %q, want %q", bucket, c.wantBucket)
			}
		})
	}
}

// End-to-end check that the recency chip renders for an ok entry
// and is absent for failed entries (which have no dates). Confirms
// the CSS bucket class is reachable by the template.
func TestGenerateScanIndex_RecencyChipRenders(t *testing.T) {
	data := ScanIndexData{
		GeneratedAt: "2026-04-20 12:00",
		TotalRepos:  2,
		OKRepos:     1,
		FailedRepos: 1,
		MaxCommits:  10,
		Repos: []ScanIndexEntry{
			{
				Slug:            "alive",
				Path:            "/work/alive",
				Status:          "ok",
				Commits:         10,
				FirstCommitDate: "2024-01-01",
				LastCommitDate:  "2026-04-18",
				LastCommitAgo:   "2d ago",
				RecencyBucket:   "fresh",
				ReportHref:      "alive.html",
			},
			{
				Slug:   "broken",
				Path:   "/work/broken",
				Status: "failed",
				Error:  "boom",
			},
		},
	}
	var buf bytes.Buffer
	if err := GenerateScanIndex(&buf, data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `class="recency fresh"`) {
		t.Error("fresh recency chip missing from ok entry")
	}
	if !strings.Contains(out, `>2d ago<`) {
		t.Error("recency label text missing")
	}
	// Failed entry has no dates block, so no recency chip should
	// render for it — a weak but useful guard against a template
	// restructure leaking the chip into the failure render.
	if strings.Count(out, `class="recency`) != 1 {
		t.Errorf("expected exactly one recency chip (ok entry only); got %d", strings.Count(out, `class="recency`))
	}
}
