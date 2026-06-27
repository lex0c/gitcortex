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
	"unicode/utf8"

	"github.com/lex0c/gitcortex/internal/model"
)

type commitEntry struct {
	email   string
	date    time.Time
	add     int64
	del     int64
	files   int
	message string
	// repo carries the LoadMultiJSONL pathPrefix (without the trailing
	// colon) so RepoBreakdown can attribute commits back to their source
	// repository on multi-repo scans. Empty for single-file loads, which
	// keeps single-repo callers' behavior unchanged.
	repo string
}

// maxStoredMessageBytes caps the commit message retained per commit. The
// only consumers (TopCommits, per-dev TopCommits) show the first line
// truncated to 77 chars; retaining full multi-line bodies for every
// commit adds hundreds of MB on large repos (linux: 1.44M commits) for
// bytes nothing renders. The bound keeps ample headroom over the 80-char
// display cap.
const maxStoredMessageBytes = 200

func truncateMessage(s string) string {
	if len(s) <= maxStoredMessageBytes {
		return s
	}
	// Back up to a rune boundary so the retained string is always valid
	// UTF-8 (current display re-slices to ~77 chars, but keep it clean for
	// any future full-message consumer).
	b := maxStoredMessageBytes
	for b > 0 && !utf8.RuneStart(s[b]) {
		b--
	}
	return s[:b]
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
	// byExt splits this file's churn across extensions as they appeared
	// at each change, so a rename-across-extensions (foo.js → foo.ts)
	// keeps per-era attribution correct even after applyRenames merges
	// the lineage onto one canonical path. Populated at ingest in
	// lockstep with additions/deletions/recentChurn; consumed only by
	// ExtensionStats. nil for hand-built fileEntries in tests — the
	// aggregator falls back to the canonical path's extension when so.
	byExt map[string]*extContribution

	// byPath splits this file's churn (total + per month) across the
	// distinct paths it occupied over its lifetime, so a rename that
	// crosses the test/source boundary (src/foo.go → foo_test.go, or a
	// move into tests/) keeps per-era ROLE attribution correct after
	// applyRenames merges the lineage onto one canonical path — otherwise
	// pre-rename production churn would be counted as test (and the trend
	// would mislabel old months). Populated at ingest (path-at-commit,
	// pre-rename) in lockstep with byExt/monthChurn, unioned by
	// mergeFileEntry, and consumed only by the test-stat functions.
	// finalizeDataset drops it for files that only ever held one path, so
	// the common (unrenamed) case carries no extra map and the test stats
	// fall back to classifying the canonical path with the file totals —
	// exact for a single era. nil for hand-built fileEntries (tests).
	byPath map[string]*pathEra
}

type extContribution struct {
	churn       int64
	recentChurn float64
	firstChange time.Time
	lastChange  time.Time
}

// pathEra is one path's contribution to a file lineage (see byPath): the
// churn attributed while the file lived at that path, the per-month
// breakdown the test-ratio trend needs to place each era in time, and the
// per-developer churn for that era so the dev profile can split a
// cross-boundary rename's churn by role per dev. devChurn is captured only
// during a rename merge (mergeFileEntry) from each era's pre-merge
// devLines — it is nil for an un-merged (single-era) file, which never
// needs it (the canonical fallback is exact for one era).
type pathEra struct {
	churn      int64
	monthChurn map[string]int64 // "YYYY-MM" → churn
	devChurn   map[string]int64 // email → churn, captured at rename merge
}

// eachEra invokes f once per (path, churn, monthChurn) era of the file so
// callers can attribute role per era. A renamed lineage iterates its
// byPath entries (the distinct pre-merge paths it held); an unrenamed file
// (byPath dropped in finalizeDataset) yields a single era for its
// canonical path with the file totals. canonical is the file's ds.files
// key. Used by the test-stat functions to keep a cross-boundary rename
// from mislabeling pre-rename history.
func (fe *fileEntry) eachEra(canonical string, f func(path string, churn int64, monthChurn map[string]int64)) {
	if len(fe.byPath) == 0 {
		f(canonical, fe.additions+fe.deletions, fe.monthChurn)
		return
	}
	for p, pe := range fe.byPath {
		f(p, pe.churn, pe.monthChurn)
	}
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

	// commitsByRepo preserves commit records per repository so the
	// per-repo breakdown stays correct when two repos share a SHA
	// (forks, mirrors, cherry-picks between sibling projects). The
	// commits map above is keyed by SHA and drops the losing side of
	// a collision — acceptable for file-side lookups where a single
	// record per SHA is enough — but RepoBreakdown would then
	// silently under-count the overwritten repo's commits and churn.
	// Populated at ingest in lockstep with commits; a single-repo
	// load (no pathPrefix) keeps all entries under the "(repo)" key.
	commitsByRepo map[string][]*commitEntry

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
		commitsByRepo:       make(map[string][]*commitEntry),
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

	// Per-commit decay weight + month key, recomputed only when the commit
	// changes. A commit's file lines are emitted contiguously (no other
	// commit record between them — extract.emitCommit writes the block
	// atomically), so ds.Latest is constant across them and this collapses
	// to one math.Exp + time.Format per commit instead of one per file.
	// Kept loop-local (not on commitEntry) so the scratch isn't retained
	// for the whole Dataset lifetime.
	var weightSHA string
	var commitWeight float64
	var commitMonthKey string

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
				// A commit with no parseable date can't be placed inside a
				// bounded window — exclude it (and, via commitInRange, its
				// file/parent records) rather than letting the IsZero guard
				// fall through and count it in every --since/--from/--to run.
				if t.IsZero() {
					continue
				}
				if !fromTime.IsZero() && t.Before(fromTime) {
					continue
				}
				if !toTime.IsZero() && t.After(toTime) {
					continue
				}
				commitInRange[c.SHA] = struct{}{}
			}

			ds.CommitCount++
			ds.TotalAdditions += c.Additions
			ds.TotalDeletions += c.Deletions
			ds.TotalFilesChanged += int64(c.FilesChanged)

			entry := &commitEntry{
				email:   c.AuthorEmail,
				date:    t,
				add:     c.Additions,
				del:     c.Deletions,
				files:   c.FilesChanged,
				message: truncateMessage(c.Message),
				repo:    strings.TrimSuffix(pathPrefix, ":"),
			}
			ds.commits[c.SHA] = entry
			// Record separately per-repo so RepoBreakdown survives
			// SHA collisions across repositories (forks/mirrors/
			// cherry-picks). Key defaults to "(repo)" in single-repo
			// loads so the fallback row in RepoBreakdown still works.
			repoKey := entry.repo
			if repoKey == "" {
				repoKey = "(repo)"
			}
			ds.commitsByRepo[repoKey] = append(ds.commitsByRepo[repoKey], entry)

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

			// byExt: attribute this change to the extension the path had
			// AT THIS COMMIT, not the canonical post-rename extension.
			// This is the only place the pre-rename path is still
			// available; after applyRenames all entries collapse onto
			// the final path and the split would be lost.
			changeExt := extractExtension(path)
			if fe.byExt == nil {
				fe.byExt = make(map[string]*extContribution)
			}
			ec, ok := fe.byExt[changeExt]
			if !ok {
				ec = &extContribution{}
				fe.byExt[changeExt] = ec
			}
			ec.churn += cf.Additions + cf.Deletions

			// byPath: attribute this change to the path the file had AT THIS
			// COMMIT (pre-rename), so the per-era role split survives the
			// applyRenames merge. Same lifecycle as byExt above.
			if fe.byPath == nil {
				fe.byPath = make(map[string]*pathEra)
			}
			pe, ok := fe.byPath[path]
			if !ok {
				pe = &pathEra{monthChurn: make(map[string]int64)}
				fe.byPath[path] = pe
			}
			pe.churn += cf.Additions + cf.Deletions

			cm := ds.commits[cf.Commit]
			if cm != nil {
				// Only record a devLines entry when the change actually
				// carried lines. Pure renames (R100 with 0/0 numstat)
				// would otherwise create a zero-valued map entry that
				// survives as "dev touched this file" into every
				// downstream consumer — bus factor, unique-dev counts,
				// dev network, and DevProfile authored counts — inflating
				// them with contributions that are not authored work.
				// devCommits still increments unconditionally so the
				// "appeared on this file" signal stays available for
				// callers that want it.
				if lines := cf.Additions + cf.Deletions; lines > 0 {
					fe.devLines[cm.email] += lines
				}
				fe.devCommits[cm.email]++

				// Contributor files touched
				if ds.contribFiles[cm.email] == nil {
					ds.contribFiles[cm.email] = make(map[string]struct{})
				}
				ds.contribFiles[cm.email][path] = struct{}{}

				if !cm.date.IsZero() {
					// Recompute the decay weight + month key only when the
					// commit changes (see weightSHA decl): collapses to one
					// math.Exp + time.Format per commit instead of per file.
					if cf.Commit != weightSHA {
						days := ds.Latest.Sub(cm.date).Hours() / 24
						commitWeight = math.Exp(-lambda * days)
						commitMonthKey = cm.date.UTC().Format("2006-01")
						weightSHA = cf.Commit
					}
					fe.recentChurn += float64(cf.Additions+cf.Deletions) * commitWeight
					ec.recentChurn += float64(cf.Additions+cf.Deletions) * commitWeight
					if cm.date.After(fe.lastChange) {
						fe.lastChange = cm.date
					}
					if fe.firstChange.IsZero() || cm.date.Before(fe.firstChange) {
						fe.firstChange = cm.date
					}
					if cm.date.After(ec.lastChange) {
						ec.lastChange = cm.date
					}
					if ec.firstChange.IsZero() || cm.date.Before(ec.firstChange) {
						ec.firstChange = cm.date
					}
					fe.monthChurn[commitMonthKey] += cf.Additions + cf.Deletions
					pe.monthChurn[commitMonthKey] += cf.Additions + cf.Deletions
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

	// Drop byPath for files that only ever held one path: a single era's
	// role is exactly the canonical path's role, so the test stats can fall
	// back to the file totals and skip the per-path map. Only renamed
	// lineages (the minority) keep byPath, so the per-era split costs
	// memory only where it changes the answer.
	for _, fe := range ds.files {
		if len(fe.byPath) <= 1 {
			fe.byPath = nil
		}
	}

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
	// maxEdgeDate[path] holds the most recent commitDate of any edge
	// involving `path` — as source OR target. The single-edge reuse
	// check below compares ds.files[path].lastChange against this, so
	// legitimate rename-back activity (which is bounded by a later
	// rename's commitDate) never looks like reuse.
	maxEdgeDate := make(map[string]time.Time)
	for _, e := range ds.renameEdges {
		oldPathCounts[e.oldPath]++
		isRenameTarget[e.newPath] = true
		if e.commitDate.After(maxEdgeDate[e.oldPath]) {
			maxEdgeDate[e.oldPath] = e.commitDate
		}
		if e.commitDate.After(maxEdgeDate[e.newPath]) {
			maxEdgeDate[e.newPath] = e.commitDate
		}
	}

	direct := make(map[string]string, len(ds.renameEdges))
	for _, e := range ds.renameEdges {
		isDuplicate := oldPathCounts[e.oldPath] > 1
		wasRecreated := isRenameTarget[e.oldPath]
		if isDuplicate && !wasRecreated {
			continue // path reused — refuse to migrate (multi-edge pattern)
		}
		// Single-edge reuse: oldPath has activity AFTER every rename it
		// participates in. Common case: A → B, then an unrelated file
		// is created at A and never renamed. Catches both simple reuse
		// (A→B + new-A) and the chain+reuse case (D→A, A→B, then new-A)
		// where the wasRecreated signal alone can't tell them apart.
		//
		// Rename-back chains (A→B→A→C) and cycles (A↔B) naturally pass
		// this check because maxEdgeDate includes later edges that
		// recreate the path, bounding lastChange from above.
		//
		// Both dates must be non-zero — defensive when fileEntry is
		// hand-built in tests without dates. In real ingest both are
		// populated from the JSONL stream.
		if fe, ok := ds.files[e.oldPath]; ok &&
			!maxEdgeDate[e.oldPath].IsZero() && !fe.lastChange.IsZero() &&
			fe.lastChange.After(maxEdgeDate[e.oldPath]) {
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
// captureEraDevChurn snapshots fe.devLines into any byPath entry that has
// not recorded its per-dev churn yet. Called from mergeFileEntry on a
// pre-merge fileEntry, where its single uncaptured byPath entry IS its own
// era and fe.devLines is exactly that era's per-dev churn. A copy is taken
// because the caller is about to merge (mutate) fe.devLines. Entries
// already captured (transferred from an earlier merge in a rename chain)
// are skipped so their per-era snapshot is not overwritten with summed
// totals.
func captureEraDevChurn(fe *fileEntry) {
	if len(fe.byPath) == 0 {
		return
	}
	for _, pe := range fe.byPath {
		// devChurn != nil means already captured (this era was a src/dst in
		// an earlier merge). Capture even when devLines is empty (a
		// pure-rename era with no authored churn) — the empty non-nil map
		// MARKS the entry captured, so a later merge can't re-capture it
		// from the by-then-summed devLines. Skipping the empty case caused
		// an order-dependent over-count on rename chains.
		if pe.devChurn != nil {
			continue
		}
		pe.devChurn = make(map[string]int64, len(fe.devLines))
		for email, n := range fe.devLines {
			pe.devChurn[email] = n
		}
	}
}

func mergeFileEntry(dst, src *fileEntry) {
	// Snapshot each side's own-era per-dev churn into its byPath entry
	// BEFORE the devLines sum below merges them. Each side's not-yet-
	// captured byPath entry is its own un-merged era, whose per-dev churn
	// is exactly that side's current devLines; copy it (dst.devLines is
	// about to be mutated). Already-captured entries (transferred from a
	// prior merge) are left alone. Only renamed lineages reach here, so the
	// dev dimension on byPath costs memory only where it changes the answer.
	captureEraDevChurn(dst)
	captureEraDevChurn(src)

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

	// byExt: merge per-ext buckets. When rename collapses foo.js → foo.ts,
	// this preserves ".js" churn on the ts-keyed entry so ExtensionStats
	// can split it back. Same bucket on both sides = add; new bucket =
	// transfer the pointer (src is discarded afterwards so ownership
	// transfer is safe).
	if src.byExt != nil {
		if dst.byExt == nil {
			dst.byExt = make(map[string]*extContribution, len(src.byExt))
		}
		for ext, srcEC := range src.byExt {
			if dstEC, ok := dst.byExt[ext]; ok {
				dstEC.churn += srcEC.churn
				dstEC.recentChurn += srcEC.recentChurn
				if !srcEC.firstChange.IsZero() && (dstEC.firstChange.IsZero() || srcEC.firstChange.Before(dstEC.firstChange)) {
					dstEC.firstChange = srcEC.firstChange
				}
				if srcEC.lastChange.After(dstEC.lastChange) {
					dstEC.lastChange = srcEC.lastChange
				}
			} else {
				dst.byExt[ext] = srcEC
			}
		}
	}

	// byPath: union per-path eras. A rename gives src and dst distinct path
	// keys, so this is normally a pointer transfer; the same-key branch
	// (a path reused across a merged lineage) sums defensively.
	if src.byPath != nil {
		if dst.byPath == nil {
			dst.byPath = make(map[string]*pathEra, len(src.byPath))
		}
		for p, srcPE := range src.byPath {
			if dstPE, ok := dst.byPath[p]; ok {
				dstPE.churn += srcPE.churn
				if dstPE.monthChurn == nil {
					dstPE.monthChurn = make(map[string]int64, len(srcPE.monthChurn))
				}
				for m, c := range srcPE.monthChurn {
					dstPE.monthChurn[m] += c
				}
				// Both sides' devChurn were snapshotted by captureEraDevChurn
				// above; sum them so a path reused across merged lineages
				// keeps every era's per-dev churn.
				if srcPE.devChurn != nil {
					if dstPE.devChurn == nil {
						dstPE.devChurn = make(map[string]int64, len(srcPE.devChurn))
					}
					for e, c := range srcPE.devChurn {
						dstPE.devChurn[e] += c
					}
				}
			} else {
				dst.byPath[p] = srcPE
			}
		}
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
