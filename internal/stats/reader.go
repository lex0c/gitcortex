package stats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitcortex/internal/model"
)

type commitEntry struct {
	email string
	date  time.Time
	add   int64
	del   int64
	files int
}

type fileEntry struct {
	commits     int
	additions   int64
	deletions   int64
	devLines    map[string]int64
	recentChurn float64
	lastChange  time.Time
}

type filePair struct{ a, b string }

type Dataset struct {
	// Summary
	CommitCount       int
	DevCount          int
	UniqueFileCount   int
	TotalAdditions    int64
	TotalDeletions    int64
	TotalFilesChanged int64
	MergeCount        int
	Earliest          time.Time
	Latest            time.Time

	// Indexed data (lean)
	commits      map[string]*commitEntry
	parentCounts map[string]int
	contributors map[string]*ContributorStat
	files        map[string]*fileEntry
	workGrid     [7][24]int

	// Coupling (pre-aggregated during load)
	couplingPairs       map[filePair]int
	couplingFileChanges map[string]int

	// Contributor detail accumulators (internal, used to finalize ContributorStat)
	contribDays  map[string]map[string]struct{} // email → set of active dates
	contribFiles map[string]map[string]struct{} // email → set of file paths
	contribFirst map[string]time.Time           // email → earliest date
	contribLast  map[string]time.Time           // email → latest date
}

type LoadOptions struct {
	From, To     string
	HalfLifeDays int
	CoupMaxFiles int
}

func LoadJSONL(path string, opts ...LoadOptions) (*Dataset, error) {
	return LoadMultiJSONL([]string{path}, opts...)
}

func LoadMultiJSONL(paths []string, opts ...LoadOptions) (*Dataset, error) {
	opt := LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50}
	if len(opts) > 0 {
		opt = opts[0]
	}

	ds := newDataset()

	for _, path := range paths {
		prefix := ""
		if len(paths) > 1 {
			base := filepath.Base(path)
			prefix = strings.TrimSuffix(base, filepath.Ext(base)) + ":"
		}

		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}

		if err := streamLoadInto(ds, f, opt, prefix); err != nil {
			f.Close()
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		f.Close()
	}

	finalizeDataset(ds)
	return ds, nil
}

func streamLoad(r io.Reader, opt LoadOptions) (*Dataset, error) {
	ds := newDataset()
	if err := streamLoadInto(ds, r, opt, ""); err != nil {
		return nil, err
	}
	finalizeDataset(ds)
	return ds, nil
}

func newDataset() *Dataset {
	return &Dataset{
		commits:             make(map[string]*commitEntry),
		parentCounts:        make(map[string]int),
		contributors:        make(map[string]*ContributorStat),
		files:               make(map[string]*fileEntry),
		couplingPairs:       make(map[filePair]int),
		couplingFileChanges: make(map[string]int),
		contribDays:         make(map[string]map[string]struct{}),
		contribFiles:        make(map[string]map[string]struct{}),
		contribFirst:        make(map[string]time.Time),
		contribLast:         make(map[string]time.Time),
	}
}

func streamLoadInto(ds *Dataset, r io.Reader, opt LoadOptions, pathPrefix string) error {
	devSeen := make(map[string]struct{})
	uniqueFiles := make(map[string]struct{})
	commitInRange := make(map[string]bool)

	var fromTime, toTime time.Time
	if opt.From != "" {
		fromTime = parseDate(opt.From + "T00:00:00Z")
	}
	if opt.To != "" {
		toTime = parseDate(opt.To + "T23:59:59Z")
	}
	hasFilter := !fromTime.IsZero() || !toTime.IsZero()

	now := time.Now()
	lambda := math.Ln2 / float64(opt.HalfLifeDays)

	// Coupling streaming state
	var coupCurrentSHA string
	var coupCurrentFiles []string

	dayIndex := map[time.Weekday]int{
		time.Monday: 0, time.Tuesday: 1, time.Wednesday: 2,
		time.Thursday: 3, time.Friday: 4, time.Saturday: 5, time.Sunday: 6,
	}

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 1<<20)
	scanner.Buffer(buf, 10<<20)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			return fmt.Errorf("line %d: parse type: %w", lineNum, err)
		}

		switch peek.Type {
		case model.CommitType:
			var c model.CommitInfo
			if err := json.Unmarshal(line, &c); err != nil {
				return fmt.Errorf("line %d: parse commit: %w", lineNum, err)
			}

			t := parseDate(c.AuthorDate)

			if hasFilter {
				inRange := true
				if !fromTime.IsZero() && !t.IsZero() && t.Before(fromTime) {
					inRange = false
				}
				if !toTime.IsZero() && !t.IsZero() && t.After(toTime) {
					inRange = false
				}
				commitInRange[c.SHA] = inRange
				if !inRange {
					continue
				}
			}

			ds.CommitCount++
			ds.TotalAdditions += c.Additions
			ds.TotalDeletions += c.Deletions
			ds.TotalFilesChanged += int64(c.FilesChanged)

			ds.commits[c.SHA] = &commitEntry{
				email: c.AuthorEmail,
				date:  t,
				add:   c.Additions,
				del:   c.Deletions,
				files: c.FilesChanged,
			}

			// Contributors
			cs, ok := ds.contributors[c.AuthorEmail]
			if !ok {
				cs = &ContributorStat{Name: c.AuthorName, Email: c.AuthorEmail}
				ds.contributors[c.AuthorEmail] = cs
			}
			cs.Commits++
			cs.Additions += c.Additions

			// Contributor detail: active days, first/last date
			if !t.IsZero() {
				dayKey := t.Format("2006-01-02")
				if ds.contribDays[c.AuthorEmail] == nil {
					ds.contribDays[c.AuthorEmail] = make(map[string]struct{})
				}
				ds.contribDays[c.AuthorEmail][dayKey] = struct{}{}

				if first, ok := ds.contribFirst[c.AuthorEmail]; !ok || t.Before(first) {
					ds.contribFirst[c.AuthorEmail] = t
				}
				if last, ok := ds.contribLast[c.AuthorEmail]; !ok || t.After(last) {
					ds.contribLast[c.AuthorEmail] = t
				}
			}
			cs.Deletions += c.Deletions

			// Dates
			if !t.IsZero() {
				if ds.Earliest.IsZero() || t.Before(ds.Earliest) {
					ds.Earliest = t
				}
				if ds.Latest.IsZero() || t.After(ds.Latest) {
					ds.Latest = t
				}
				ds.workGrid[dayIndex[t.Weekday()]][t.Hour()]++
			}

		case model.CommitParentType:
			var cp model.CommitParentInfo
			if err := json.Unmarshal(line, &cp); err != nil {
				return fmt.Errorf("line %d: parse parent: %w", lineNum, err)
			}
			if hasFilter && !commitInRange[cp.SHA] {
				continue
			}
			ds.parentCounts[cp.SHA]++

		case model.CommitFileType:
			var cf model.CommitFileInfo
			if err := json.Unmarshal(line, &cf); err != nil {
				return fmt.Errorf("line %d: parse file: %w", lineNum, err)
			}
			if hasFilter && !commitInRange[cf.Commit] {
				continue
			}

			path := cf.PathCurrent
			if path == "" {
				path = cf.PathPrevious
			}
			if path == "" {
				continue
			}
			path = pathPrefix + path

			uniqueFiles[path] = struct{}{}

			// File aggregation (hotspots + busfactor + churn-risk)
			fe, ok := ds.files[path]
			if !ok {
				fe = &fileEntry{devLines: make(map[string]int64)}
				ds.files[path] = fe
			}
			fe.commits++
			fe.additions += cf.Additions
			fe.deletions += cf.Deletions

			cm := ds.commits[cf.Commit]
			if cm != nil {
				fe.devLines[cm.email] += cf.Additions + cf.Deletions

				// Contributor files touched
				if ds.contribFiles[cm.email] == nil {
					ds.contribFiles[cm.email] = make(map[string]struct{})
				}
				ds.contribFiles[cm.email][path] = struct{}{}

				if !cm.date.IsZero() {
					days := now.Sub(cm.date).Hours() / 24
					weight := math.Exp(-lambda * days)
					fe.recentChurn += float64(cf.Additions+cf.Deletions) * weight
					if cm.date.After(fe.lastChange) {
						fe.lastChange = cm.date
					}
				}
			}

			// Coupling: streaming pair computation
			if cf.Commit != coupCurrentSHA {
				flushCoupling(ds, coupCurrentFiles, opt.CoupMaxFiles)
				coupCurrentSHA = cf.Commit
				coupCurrentFiles = coupCurrentFiles[:0]
			}
			coupCurrentFiles = append(coupCurrentFiles, path)

		case model.DevType:
			var d model.DevInfo
			if err := json.Unmarshal(line, &d); err != nil {
				return fmt.Errorf("line %d: parse dev: %w", lineNum, err)
			}
			if _, seen := devSeen[d.DevID]; !seen {
				devSeen[d.DevID] = struct{}{}
				ds.DevCount++
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}

	flushCoupling(ds, coupCurrentFiles, opt.CoupMaxFiles)
	ds.UniqueFileCount += len(uniqueFiles)

	return nil
}

func finalizeDataset(ds *Dataset) {
	ds.MergeCount = 0
	for sha := range ds.parentCounts {
		if ds.parentCounts[sha] > 1 {
			ds.MergeCount++
		}
	}

	for email, cs := range ds.contributors {
		cs.ActiveDays = len(ds.contribDays[email])
		cs.FilesTouched = len(ds.contribFiles[email])
		if t, ok := ds.contribFirst[email]; ok {
			cs.FirstDate = t.Format("2006-01-02")
		}
		if t, ok := ds.contribLast[email]; ok {
			cs.LastDate = t.Format("2006-01-02")
		}
	}
	ds.contribDays = nil
	ds.contribFiles = nil
	ds.contribFirst = nil
	ds.contribLast = nil
}

func flushCoupling(ds *Dataset, files []string, maxFiles int) {
	if len(files) < 2 || len(files) > maxFiles {
		return
	}

	seen := make(map[string]bool, len(files))
	unique := make([]string, 0, len(files))
	for _, f := range files {
		if !seen[f] {
			seen[f] = true
			unique = append(unique, f)
			ds.couplingFileChanges[f]++
		}
	}

	for i := 0; i < len(unique); i++ {
		for j := i + 1; j < len(unique); j++ {
			a, b := unique[i], unique[j]
			if a > b {
				a, b = b, a
			}
			ds.couplingPairs[filePair{a, b}]++
		}
	}
}

func parseDate(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
