package stats

import (
	"sort"
	"strings"
	"time"
)

// RepoStat aggregates commit-level activity for one repository in a
// multi-repo Dataset. Populated only when LoadMultiJSONL ran with more
// than one input file (single-repo loads have no path prefix to group by,
// so the breakdown would be a noop).
type RepoStat struct {
	Repo            string
	Commits         int
	Additions       int64
	Deletions       int64
	Churn           int64
	Files           int
	ActiveDays      int
	UniqueDevs      int
	FirstCommitDate string
	LastCommitDate  string
	// PctOfTotalCommits / PctOfTotalChurn are computed against the
	// dataset totals so the report can show "this repo was 38% of
	// your commits" without the template needing to do the math.
	PctOfTotalCommits float64
	PctOfTotalChurn   float64
}

// RepoBreakdown groups commits by their repo tag (set during ingest from
// the LoadMultiJSONL pathPrefix) and returns one row per repo, sorted by
// commit count descending. When emailFilter is non-empty, only commits
// authored by that email are counted — this is the "show my work across
// repos" lens used by the scan report.
//
// On single-repo datasets (no prefix tag, repo == ""), the function
// returns a single row labeled "(repo)" so callers can still render
// without a special case.
//
// Caveat: the underlying ds.commits map is keyed by SHA. If two repos
// share a commit SHA (cherry-pick, mirror, fork) the second one ingested
// overwrites the first, and the breakdown will report only the
// surviving repo. In practice this only matters for forked / mirrored
// repos under a single scan root — for those, scan and aggregate
// separately if exact attribution matters.
func RepoBreakdown(ds *Dataset, emailFilter string) []RepoStat {
	type acc struct {
		commits     int
		add, del    int64
		days        map[string]struct{}
		devs        map[string]struct{}
		first, last time.Time
	}

	repos := make(map[string]*acc)
	emailLower := strings.ToLower(strings.TrimSpace(emailFilter))

	for _, c := range ds.commits {
		if emailLower != "" && strings.ToLower(strings.TrimSpace(c.email)) != emailLower {
			continue
		}
		key := c.repo
		if key == "" {
			key = "(repo)"
		}
		a, ok := repos[key]
		if !ok {
			a = &acc{
				days: make(map[string]struct{}),
				devs: make(map[string]struct{}),
			}
			repos[key] = a
		}
		a.commits++
		a.add += c.add
		a.del += c.del
		if c.email != "" {
			a.devs[strings.ToLower(c.email)] = struct{}{}
		}
		if !c.date.IsZero() {
			a.days[c.date.UTC().Format("2006-01-02")] = struct{}{}
			if a.first.IsZero() || c.date.Before(a.first) {
				a.first = c.date
			}
			if c.date.After(a.last) {
				a.last = c.date
			}
		}
	}

	// File counts come from ds.files. Path prefix is `<repo>:<path>` so
	// the split key matches the commit-side `c.repo`. Files without a
	// prefix (single-repo loads) bucket under the same "(repo)" label.
	//
	// When emailFilter is set the count must reflect files THIS dev
	// touched, not the repo's total — the unfiltered count would
	// massively over-state per-dev scope (e.g. "I worked on 12k files
	// in monorepo X" when really the dev touched 30). devCommits
	// tracks per-file dev appearance and is the right denominator.
	fileCounts := make(map[string]int)
	for path, fe := range ds.files {
		if emailLower != "" {
			matched := false
			for devEmail := range fe.devCommits {
				if strings.ToLower(strings.TrimSpace(devEmail)) == emailLower {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		key := ""
		if i := strings.Index(path, ":"); i >= 0 {
			key = path[:i]
		}
		if key == "" {
			key = "(repo)"
		}
		fileCounts[key]++
	}

	// Totals computed AFTER the email filter: percentages are relative
	// to the filtered universe so a dev profile shows "60% of MY commits
	// happened in repo X", not "0.4% of the org's commits".
	var totalCommits int
	var totalChurn int64
	for _, a := range repos {
		totalCommits += a.commits
		totalChurn += a.add + a.del
	}

	out := make([]RepoStat, 0, len(repos))
	for repo, a := range repos {
		stat := RepoStat{
			Repo:       repo,
			Commits:    a.commits,
			Additions:  a.add,
			Deletions:  a.del,
			Churn:      a.add + a.del,
			Files:      fileCounts[repo],
			ActiveDays: len(a.days),
			UniqueDevs: len(a.devs),
		}
		if !a.first.IsZero() {
			stat.FirstCommitDate = a.first.UTC().Format("2006-01-02")
		}
		if !a.last.IsZero() {
			stat.LastCommitDate = a.last.UTC().Format("2006-01-02")
		}
		if totalCommits > 0 {
			stat.PctOfTotalCommits = float64(stat.Commits) / float64(totalCommits) * 100
		}
		if totalChurn > 0 {
			stat.PctOfTotalChurn = float64(stat.Churn) / float64(totalChurn) * 100
		}
		out = append(out, stat)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Commits != out[j].Commits {
			return out[i].Commits > out[j].Commits
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}
