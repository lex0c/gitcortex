package stats

import (
	"fmt"
	"sort"
)

type ContributorStat struct {
	Name     string
	Email    string
	Commits  int
	Additions int64
	Deletions int64
}

type FileStat struct {
	Path       string
	Commits    int
	Additions  int64
	Deletions  int64
	Churn      int64
	UniqueDevs int
}

type ActivityBucket struct {
	Period    string
	Commits  int
	Additions int64
	Deletions int64
}

type Summary struct {
	TotalCommits    int
	TotalDevs       int
	TotalFiles      int
	TotalAdditions  int64
	TotalDeletions  int64
	MergeCommits    int
	AvgAdditions    float64
	AvgDeletions    float64
	AvgFilesChanged float64
	FirstCommitDate string
	LastCommitDate  string
}

type BusFactorResult struct {
	Path      string
	BusFactor int
	TopDevs   []string
}

func ComputeSummary(ds *Dataset) Summary {
	s := Summary{
		TotalCommits: len(ds.Commits),
		TotalDevs:    len(ds.Devs),
	}

	uniqueFiles := make(map[string]struct{})
	parentCount := make(map[string]int)
	for _, p := range ds.Parents {
		parentCount[p.SHA]++
	}

	var totalFiles int64
	for _, c := range ds.Commits {
		s.TotalAdditions += c.Additions
		s.TotalDeletions += c.Deletions
		totalFiles += int64(c.FilesChanged)
		if parentCount[c.SHA] > 1 {
			s.MergeCommits++
		}
	}

	for _, f := range ds.Files {
		if f.PathCurrent != "" {
			uniqueFiles[f.PathCurrent] = struct{}{}
		}
	}
	s.TotalFiles = len(uniqueFiles)

	if s.TotalCommits > 0 {
		s.AvgAdditions = float64(s.TotalAdditions) / float64(s.TotalCommits)
		s.AvgDeletions = float64(s.TotalDeletions) / float64(s.TotalCommits)
		s.AvgFilesChanged = float64(totalFiles) / float64(s.TotalCommits)
	}

	if len(ds.Commits) > 0 {
		s.FirstCommitDate = ds.Commits[0].AuthorDate
		s.LastCommitDate = ds.Commits[len(ds.Commits)-1].AuthorDate

		earliest := parseDate(ds.Commits[0].AuthorDate)
		latest := earliest
		for _, c := range ds.Commits[1:] {
			t := parseDate(c.AuthorDate)
			if t.IsZero() {
				continue
			}
			if t.Before(earliest) {
				earliest = t
			}
			if t.After(latest) {
				latest = t
			}
		}
		if !earliest.IsZero() {
			s.FirstCommitDate = earliest.Format("2006-01-02")
		}
		if !latest.IsZero() {
			s.LastCommitDate = latest.Format("2006-01-02")
		}
	}

	return s
}

func TopContributors(ds *Dataset, n int) []ContributorStat {
	byEmail := make(map[string]*ContributorStat)

	for _, c := range ds.Commits {
		key := c.AuthorEmail
		cs, ok := byEmail[key]
		if !ok {
			cs = &ContributorStat{Name: c.AuthorName, Email: c.AuthorEmail}
			byEmail[key] = cs
		}
		cs.Commits++
		cs.Additions += c.Additions
		cs.Deletions += c.Deletions
	}

	result := make([]ContributorStat, 0, len(byEmail))
	for _, cs := range byEmail {
		result = append(result, *cs)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Commits > result[j].Commits
	})

	if n > 0 && n < len(result) {
		result = result[:n]
	}

	return result
}

func FileHotspots(ds *Dataset, n int) []FileStat {
	type fileAcc struct {
		commits   int
		additions int64
		deletions int64
		devs      map[string]struct{}
	}

	byPath := make(map[string]*fileAcc)

	commitAuthor := make(map[string]string)
	for _, c := range ds.Commits {
		commitAuthor[c.SHA] = c.AuthorEmail
	}

	for _, f := range ds.Files {
		path := f.PathCurrent
		if path == "" {
			path = f.PathPrevious
		}
		acc, ok := byPath[path]
		if !ok {
			acc = &fileAcc{devs: make(map[string]struct{})}
			byPath[path] = acc
		}
		acc.commits++
		acc.additions += f.Additions
		acc.deletions += f.Deletions
		if email, ok := commitAuthor[f.Commit]; ok {
			acc.devs[email] = struct{}{}
		}
	}

	result := make([]FileStat, 0, len(byPath))
	for path, acc := range byPath {
		result = append(result, FileStat{
			Path:       path,
			Commits:    acc.commits,
			Additions:  acc.additions,
			Deletions:  acc.deletions,
			Churn:      acc.additions + acc.deletions,
			UniqueDevs: len(acc.devs),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Commits > result[j].Commits
	})

	if n > 0 && n < len(result) {
		result = result[:n]
	}

	return result
}

func ActivityOverTime(ds *Dataset, granularity string) []ActivityBucket {
	format := "2006-01"
	switch granularity {
	case "day":
		format = "2006-01-02"
	case "week":
		format = "2006-W%V"
	case "year":
		format = "2006"
	}

	buckets := make(map[string]*ActivityBucket)
	var order []string

	for _, c := range ds.Commits {
		t := parseDate(c.AuthorDate)
		if t.IsZero() {
			continue
		}

		var key string
		if granularity == "week" {
			y, w := t.ISOWeek()
			key = fmt.Sprintf("%04d-W%02d", y, w)
		} else {
			key = t.Format(format)
		}

		b, ok := buckets[key]
		if !ok {
			b = &ActivityBucket{Period: key}
			buckets[key] = b
			order = append(order, key)
		}
		b.Commits++
		b.Additions += c.Additions
		b.Deletions += c.Deletions
	}

	sort.Strings(order)
	result := make([]ActivityBucket, len(order))
	for i, key := range order {
		result[i] = *buckets[key]
	}

	return result
}

func BusFactor(ds *Dataset, n int) []BusFactorResult {
	commitAuthor := make(map[string]string)
	for _, c := range ds.Commits {
		commitAuthor[c.SHA] = c.AuthorEmail
	}

	type devLines struct {
		email string
		lines int64
	}

	fileDevs := make(map[string]map[string]int64)

	for _, f := range ds.Files {
		path := f.PathCurrent
		if path == "" {
			continue
		}
		email, ok := commitAuthor[f.Commit]
		if !ok {
			continue
		}
		if fileDevs[path] == nil {
			fileDevs[path] = make(map[string]int64)
		}
		fileDevs[path][email] += f.Additions + f.Deletions
	}

	result := make([]BusFactorResult, 0, len(fileDevs))

	for path, devMap := range fileDevs {
		if len(devMap) == 0 {
			continue
		}

		devs := make([]devLines, 0, len(devMap))
		var totalLines int64
		for email, lines := range devMap {
			devs = append(devs, devLines{email: email, lines: lines})
			totalLines += lines
		}

		sort.Slice(devs, func(i, j int) bool {
			return devs[i].lines > devs[j].lines
		})

		threshold := float64(totalLines) * 0.8
		var cumulative int64
		busFactor := 0
		var topDevs []string
		for _, d := range devs {
			cumulative += d.lines
			busFactor++
			topDevs = append(topDevs, d.email)
			if float64(cumulative) >= threshold {
				break
			}
		}

		result = append(result, BusFactorResult{
			Path:      path,
			BusFactor: busFactor,
			TopDevs:   topDevs,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].BusFactor < result[j].BusFactor
	})

	if n > 0 && n < len(result) {
		result = result[:n]
	}

	return result
}

func fileStatusBreakdown(ds *Dataset) map[string]int {
	counts := make(map[string]int)
	for _, f := range ds.Files {
		status := normalizeStatus(f.Status)
		counts[status]++
	}
	return counts
}

func normalizeStatus(s string) string {
	if len(s) == 0 {
		return "unknown"
	}
	switch s[0] {
	case 'A':
		return "added"
	case 'M':
		return "modified"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default:
		return s
	}
}
