package stats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lex0c/gitcortex/internal/model"
)

type commitEntry struct {
	email   string
	date    time.Time
	add     int64
	del     int64
	files   int
	message string
}

type fileEntry struct {
	commits     int
	additions   int64
	deletions   int64
	devLines    map[string]int64
	devCommits  map[string]int
	recentChurn float64
	firstChange time.Time
	lastChange  time.Time
	monthChurn  map[string]int64 // key: "YYYY-MM"; used for trend classification
}

type filePair struct{ a, b string }

type renameEdge struct {
	oldPath, newPath string
	// commitDate captures when the rename was made. applyRenames sorts by
	// this descending so the "first-seen wins" dedup corresponds to the
	// most recent rename even if the JSONL arrives out of order (e.g. if
	// the extractor ever emits --reverse or a future format change
	// reshuffles the stream).
	commitDate time.Time
}

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

	// Rename edges captured during ingest; resolved in finalizeDataset.
	renameEdges []renameEdge

	// Internal accumulators
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
	uniqueFiles := make(map[string]struct{})
	commitInRange := make(map[string]struct{}) // only stores SHAs that ARE in range

	var fromTime, toTime time.Time
	if opt.From != "" {
		fromTime = parseDate(opt.From + "T00:00:00Z")
	}
	if opt.To != "" {
		toTime = parseDate(opt.To + "T23:59:59Z")
	}
	hasFilter := !fromTime.IsZero() || !toTime.IsZero()

	// Use ds.Latest (set during commit processing) instead of time.Now()
	// for reproducible churn scores from the same dataset.
	halfLife := opt.HalfLifeDays
	if halfLife <= 0 {
		halfLife = 90
	}
	lambda := math.Ln2 / float64(halfLife)

	// Coupling streaming state
	var coupCurrentSHA string
	var coupCurrentFiles []string
	var coupCurrentChurn int64

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
				if !fromTime.IsZero() && !t.IsZero() && t.Before(fromTime) {
					continue
				}
				if !toTime.IsZero() && !t.IsZero() && t.After(toTime) {
					continue
				}
				commitInRange[c.SHA] = struct{}{}
			}

			ds.CommitCount++
			ds.TotalAdditions += c.Additions
			ds.TotalDeletions += c.Deletions
			ds.TotalFilesChanged += int64(c.FilesChanged)

			ds.commits[c.SHA] = &commitEntry{
				email:   c.AuthorEmail,
				date:    t,
				add:     c.Additions,
				del:     c.Deletions,
				files:   c.FilesChanged,
				message: c.Message,
			}

			// Contributors
			cs, ok := ds.contributors[c.AuthorEmail]
			if !ok {
				cs = &ContributorStat{Name: c.AuthorName, Email: c.AuthorEmail}
				ds.contributors[c.AuthorEmail] = cs
			}
			cs.Commits++
			cs.Additions += c.Additions
			cs.Deletions += c.Deletions

			// Contributor detail: active days, first/last date.
			// Active days bucket in UTC so the count is stable across the
			// distributed-team case where two commits near midnight local
			// would otherwise land in different local dates.
			if !t.IsZero() {
				dayKey := t.UTC().Format("2006-01-02")
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
			if hasFilter {
				if _, ok := commitInRange[cp.SHA]; !ok {
					continue
				}
			}
			ds.parentCounts[cp.SHA]++

		case model.CommitFileType:
			var cf model.CommitFileInfo
			if err := json.Unmarshal(line, &cf); err != nil {
				return fmt.Errorf("line %d: parse file: %w", lineNum, err)
			}
			if hasFilter {
				if _, ok := commitInRange[cf.Commit]; !ok {
					continue
				}
			}

			// Capture rename edges (git log emits status "R" followed by
			// similarity score, e.g. "R100"). Chains are resolved in
			// finalizeDataset once all edges are known — required because
			// the log is emitted newest-first, so pre-rename history comes
			// later in the stream than the rename commit itself.
			if strings.HasPrefix(cf.Status, "R") && cf.PathPrevious != "" && cf.PathCurrent != "" && cf.PathPrevious != cf.PathCurrent {
				var edgeDate time.Time
				if cm := ds.commits[cf.Commit]; cm != nil {
					edgeDate = cm.date
				}
				ds.renameEdges = append(ds.renameEdges, renameEdge{
					oldPath:    pathPrefix + cf.PathPrevious,
					newPath:    pathPrefix + cf.PathCurrent,
					commitDate: edgeDate,
				})
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
				fe = &fileEntry{
					devLines:   make(map[string]int64),
					devCommits: make(map[string]int),
					monthChurn: make(map[string]int64),
				}
				ds.files[path] = fe
			}
			fe.commits++
			fe.additions += cf.Additions
			fe.deletions += cf.Deletions

			cm := ds.commits[cf.Commit]
			if cm != nil {
				fe.devLines[cm.email] += cf.Additions + cf.Deletions
				fe.devCommits[cm.email]++

				// Contributor files touched
				if ds.contribFiles[cm.email] == nil {
					ds.contribFiles[cm.email] = make(map[string]struct{})
				}
				ds.contribFiles[cm.email][path] = struct{}{}

				if !cm.date.IsZero() {
					days := ds.Latest.Sub(cm.date).Hours() / 24
					weight := math.Exp(-lambda * days)
					fe.recentChurn += float64(cf.Additions+cf.Deletions) * weight
					if cm.date.After(fe.lastChange) {
						fe.lastChange = cm.date
					}
					if fe.firstChange.IsZero() || cm.date.Before(fe.firstChange) {
						fe.firstChange = cm.date
					}
					fe.monthChurn[cm.date.UTC().Format("2006-01")] += cf.Additions + cf.Deletions
				}
			}

			// Coupling: streaming pair computation
			if cf.Commit != coupCurrentSHA {
				flushCoupling(ds, coupCurrentFiles, coupCurrentChurn, opt.CoupMaxFiles)
				coupCurrentSHA = cf.Commit
				coupCurrentFiles = coupCurrentFiles[:0]
				coupCurrentChurn = 0
			}
			coupCurrentFiles = append(coupCurrentFiles, path)
			coupCurrentChurn += cf.Additions + cf.Deletions

		case model.DevType:
			// dev records are skipped — DevCount is derived from contributors (authors only)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}

	flushCoupling(ds, coupCurrentFiles, coupCurrentChurn, opt.CoupMaxFiles)
	ds.UniqueFileCount += len(uniqueFiles)

	return nil
}

func finalizeDataset(ds *Dataset) {
	ds.DevCount = len(ds.contributors)
	ds.MergeCount = 0
	for sha := range ds.parentCounts {
		if ds.parentCounts[sha] > 1 {
			ds.MergeCount++
		}
	}

	applyRenames(ds)
	// UniqueFileCount reflects post-merge canonical paths.
	ds.UniqueFileCount = len(ds.files)

	for email, cs := range ds.contributors {
		cs.ActiveDays = len(ds.contribDays[email])
		cs.FilesTouched = len(ds.contribFiles[email])
		if t, ok := ds.contribFirst[email]; ok {
			cs.FirstDate = t.UTC().Format("2006-01-02")
		}
		if t, ok := ds.contribLast[email]; ok {
			cs.LastDate = t.UTC().Format("2006-01-02")
		}
	}
	ds.contribDays = nil
	ds.contribFiles = nil
	ds.contribFirst = nil
	ds.contribLast = nil
}

// applyRenames resolves rename edges captured during ingest and re-keys
// ds.files / coupling maps to their canonical (latest) path. Safe to call
// with no edges. Defensive against cycles (A→B→A) — rare but possible if
// a repo reverted a rename.
func applyRenames(ds *Dataset) {
	if len(ds.renameEdges) == 0 {
		return
	}

	// Distinguish two scenarios that both produce duplicate oldPaths:
	//
	//   Path reuse (different lineages):
	//     t1: A → B      t2: (unrelated file created at A)      t3: A → D
	//   The repeated A has no intermediate edge pointing at it — it
	//   simply reappears. ds.files["A"] contains two lineages merged at
	//   ingest time and we cannot disambiguate without per-commit
	//   temporal tracking. Skip migration.
	//
	//   Rename-back chain (same lineage):
	//     t1: A → B      t2: B → A (rename back)      t3: A → C
	//   The repeated A is recreated by an explicit rename edge (B → A).
	//   Both segments of the chain belong to the same lineage and both
	//   should end up at C. Migrate normally.
	//
	// Heuristic: treat oldPath as reuse iff count > 1 AND no edge has
	// it as newPath. Imperfect on exotic mixed cases (A→B, C→A, A→D)
	// but correctly handles pure reuse and the common rename-back flow.

	// Sort edges by commitDate desc so the dedup below ("first seen for
	// each oldPath wins") corresponds to the chronologically newest
	// rename regardless of the order the stream arrived in. Otherwise we
	// would silently break if the extractor ever changed from newest-
	// first emission.
	sort.SliceStable(ds.renameEdges, func(i, j int) bool {
		return ds.renameEdges[i].commitDate.After(ds.renameEdges[j].commitDate)
	})

	oldPathCounts := make(map[string]int)
	isRenameTarget := make(map[string]bool)
	for _, e := range ds.renameEdges {
		oldPathCounts[e.oldPath]++
		isRenameTarget[e.newPath] = true
	}

	direct := make(map[string]string, len(ds.renameEdges))
	for _, e := range ds.renameEdges {
		isDuplicate := oldPathCounts[e.oldPath] > 1
		wasRecreated := isRenameTarget[e.oldPath]
		if isDuplicate && !wasRecreated {
			continue // path reused — refuse to migrate (multi-edge pattern)
		}
		// Single-edge reuse pattern: oldPath appears in exactly one rename,
		// but a new unrelated file was created at oldPath after that
		// rename. The multi-edge heuristic can't see this — detect it by
		// comparing the oldPath's lastChange to the rename's commitDate.
		// If lastChange is after the rename, the activity belongs to a
		// second lineage that must not leak into newPath.
		//
		// Guard with `!wasRecreated`: when the oldPath is also the target
		// of some other rename (rename-back chain or cycle A↔B), the
		// later rename commit updates ds.files[oldPath].lastChange to
		// its own date, so lastChange.After(commitDate) would trigger
		// spuriously. wasRecreated signals that the activity after the
		// original rename is itself coming from a rename edge, not from
		// a brand-new unrelated file.
		//
		// Requires both dates to be known; the check is defensive and
		// only fires when we have enough signal to be certain.
		if fe, ok := ds.files[e.oldPath]; ok && !wasRecreated &&
			!e.commitDate.IsZero() && !fe.lastChange.IsZero() &&
			fe.lastChange.After(e.commitDate) {
			continue
		}
		// First edge wins per oldPath; edges are pre-sorted so this is
		// the chronologically newest rename (the direction the lineage
		// continues in).
		if _, ok := direct[e.oldPath]; !ok {
			direct[e.oldPath] = e.newPath
		}
	}

	canonical := func(p string) string {
		seen := map[string]struct{}{}
		for {
			if _, ok := seen[p]; ok {
				return p // cycle detected — bail with current
			}
			seen[p] = struct{}{}
			next, ok := direct[p]
			if !ok || next == p {
				return p
			}
			p = next
		}
	}

	// Re-key file entries, merging colliders.
	newFiles := make(map[string]*fileEntry, len(ds.files))
	for path, fe := range ds.files {
		c := canonical(path)
		if existing, ok := newFiles[c]; ok {
			mergeFileEntry(existing, fe)
		} else {
			newFiles[c] = fe
		}
	}
	ds.files = newFiles

	// Re-key coupling denominator.
	newChanges := make(map[string]int, len(ds.couplingFileChanges))
	for path, count := range ds.couplingFileChanges {
		newChanges[canonical(path)] += count
	}
	ds.couplingFileChanges = newChanges

	// Re-key coupling pairs. Drop pairs that collapse onto themselves
	// (both sides resolved to the same canonical path = rename chain).
	newPairs := make(map[filePair]int, len(ds.couplingPairs))
	for pair, count := range ds.couplingPairs {
		ca, cb := canonical(pair.a), canonical(pair.b)
		if ca == cb {
			continue
		}
		if ca > cb {
			ca, cb = cb, ca
		}
		newPairs[filePair{a: ca, b: cb}] += count
	}
	ds.couplingPairs = newPairs

	// Re-key contributor file sets so FilesTouched reflects canonical paths.
	// Without this, a dev who edited old.go and new.go across a rename would
	// be counted as touching two files instead of one.
	for email, paths := range ds.contribFiles {
		newSet := make(map[string]struct{}, len(paths))
		for p := range paths {
			newSet[canonical(p)] = struct{}{}
		}
		ds.contribFiles[email] = newSet
	}
}

// mergeFileEntry folds src into dst: sums scalars, unions maps, keeps the
// widest firstChange→lastChange span.
func mergeFileEntry(dst, src *fileEntry) {
	dst.commits += src.commits
	dst.additions += src.additions
	dst.deletions += src.deletions
	dst.recentChurn += src.recentChurn

	if dst.devLines == nil {
		dst.devLines = make(map[string]int64)
	}
	for k, v := range src.devLines {
		dst.devLines[k] += v
	}
	if dst.devCommits == nil {
		dst.devCommits = make(map[string]int)
	}
	for k, v := range src.devCommits {
		dst.devCommits[k] += v
	}
	if dst.monthChurn == nil {
		dst.monthChurn = make(map[string]int64)
	}
	for k, v := range src.monthChurn {
		dst.monthChurn[k] += v
	}

	if !src.firstChange.IsZero() {
		if dst.firstChange.IsZero() || src.firstChange.Before(dst.firstChange) {
			dst.firstChange = src.firstChange
		}
	}
	if src.lastChange.After(dst.lastChange) {
		dst.lastChange = src.lastChange
	}
}

// isMechanicalRefactor returns true when a commit's shape matches a likely
// rename / format / lint pass: many files with very low mean churn per file.
// Such commits generate spurious coupling pairs and are filtered in
// flushCoupling. Thresholds are package constants (refactor*).
func isMechanicalRefactor(fileCount int, commitChurn int64) bool {
	if fileCount < refactorMinFiles {
		return false
	}
	return float64(commitChurn)/float64(fileCount) < refactorMaxChurnPerFile
}

func flushCoupling(ds *Dataset, files []string, commitChurn int64, maxFiles int) {
	// Always count file changes (denominator for coupling %)
	// so single-file commits are included in the base rate.
	seen := make(map[string]bool, len(files))
	unique := make([]string, 0, len(files))
	for _, f := range files {
		if !seen[f] {
			seen[f] = true
			unique = append(unique, f)
			ds.couplingFileChanges[f]++
		}
	}

	// Only count pairs for multi-file commits within size limit
	if len(unique) < 2 || len(unique) > maxFiles {
		return
	}

	// Global renames, format/lint passes, and similar mechanical commits
	// would otherwise generate spurious coupling pairs. Denominator above
	// is still incremented so ChangesA/ChangesB stay honest.
	if isMechanicalRefactor(len(unique), commitChurn) {
		return
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
