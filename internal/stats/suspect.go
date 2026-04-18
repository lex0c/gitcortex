package stats

import (
	"sort"
	"strings"
)

// suspectWarningMinChurnRatio gates the suspect-path warning: unless the
// matched paths together account for at least this fraction of total repo
// churn, no warning is emitted. Without this guard, a single vendored
// file in a large repo would trigger noise every run.
const suspectWarningMinChurnRatio = 0.10

// SuspectPattern is a path heuristic for likely vendor/generated content
// that would distort hotspots, churn-risk, and bus factor. Glob is the
// bucket label (short, always the same); Suggest returns the actual
// --ignore glob that matches the path supplied, which may differ for
// nested occurrences (e.g. vendor appearing under pkg/, subproject/ ...)
// because extract.shouldIgnore treats directory patterns as repo-root
// prefixes.
type SuspectPattern struct {
	Glob    string
	Reason  string
	Match   func(path string) bool
	Suggest func(path string) string
}

// SuspectBucket is one pattern's match set aggregated across the dataset.
// Suggestions lists the unique extract --ignore globs needed to cover
// every matched path — typically one entry for suffix/basename patterns,
// possibly several for directory patterns that matched at multiple
// depths (e.g. "vendor/*" and "pkg/vendor/*" for the same bucket).
type SuspectBucket struct {
	Pattern     SuspectPattern
	Paths       []string
	Churn       int64
	Suggestions []string
}

// defaultSuspectPatterns covers high-confidence vendor/generated paths.
// Kept conservative so the warning doesn't cry wolf; statistical signals
// (churn/commit outliers, single-author bulk updates) are deliberately
// out of scope until we have evidence they help rather than create
// false positives.
var defaultSuspectPatterns = []SuspectPattern{
	{Glob: "vendor/*", Reason: "vendored third-party code", Match: hasPathSegment("vendor"), Suggest: suggestDirGlob("vendor")},
	{Glob: "node_modules/*", Reason: "npm dependencies", Match: hasPathSegment("node_modules"), Suggest: suggestDirGlob("node_modules")},
	{Glob: "dist/*", Reason: "build artifacts", Match: hasPathSegment("dist"), Suggest: suggestDirGlob("dist")},
	{Glob: "build/*", Reason: "build output", Match: hasPathSegment("build"), Suggest: suggestDirGlob("build")},
	{Glob: "third_party/*", Reason: "third-party code", Match: hasPathSegment("third_party"), Suggest: suggestDirGlob("third_party")},
	{Glob: "*.min.js", Reason: "minified JS", Match: hasSuffixOf(".min.js"), Suggest: constantSuggest("*.min.js")},
	{Glob: "*.min.css", Reason: "minified CSS", Match: hasSuffixOf(".min.css"), Suggest: constantSuggest("*.min.css")},
	{Glob: "*.lock", Reason: "generic lock file", Match: hasSuffixOf(".lock"), Suggest: constantSuggest("*.lock")},
	{Glob: "package-lock.json", Reason: "npm lockfile", Match: basenameEquals("package-lock.json"), Suggest: constantSuggest("package-lock.json")},
	{Glob: "yarn.lock", Reason: "yarn lockfile", Match: basenameEquals("yarn.lock"), Suggest: constantSuggest("yarn.lock")},
	{Glob: "pnpm-lock.yaml", Reason: "pnpm lockfile", Match: basenameEquals("pnpm-lock.yaml"), Suggest: constantSuggest("pnpm-lock.yaml")},
	{Glob: "go.sum", Reason: "go module hashes", Match: basenameEquals("go.sum"), Suggest: constantSuggest("go.sum")},
	{Glob: "Cargo.lock", Reason: "cargo lockfile", Match: basenameEquals("Cargo.lock"), Suggest: constantSuggest("Cargo.lock")},
	{Glob: "poetry.lock", Reason: "poetry lockfile", Match: basenameEquals("poetry.lock"), Suggest: constantSuggest("poetry.lock")},
	{Glob: "*.pb.go", Reason: "protobuf generated (Go)", Match: hasSuffixOf(".pb.go"), Suggest: constantSuggest("*.pb.go")},
	{Glob: "*_pb2.py", Reason: "protobuf generated (Python)", Match: hasSuffixOf("_pb2.py"), Suggest: constantSuggest("*_pb2.py")},
	{Glob: "*.generated.*", Reason: "generated code", Match: basenameContains(".generated."), Suggest: constantSuggest("*.generated.*")},
}

func hasPathSegment(seg string) func(string) bool {
	return func(p string) bool {
		return strings.HasPrefix(p, seg+"/") || strings.Contains(p, "/"+seg+"/")
	}
}

// suggestDirGlob returns, for the path that matched seg somewhere in its
// directory chain, the specific "...parent/.../seg/*" glob that extract
// --ignore will actually act on. extract.shouldIgnore reads "dist/*" as
// a repo-root prefix, so emitting just that for a match on
// "pkg/dist/foo.js" would tell the user to run a fix that doesn't
// remove the offending paths and the warning would keep firing.
func suggestDirGlob(seg string) func(string) string {
	return func(p string) string {
		if strings.HasPrefix(p, seg+"/") {
			return seg + "/*"
		}
		marker := "/" + seg + "/"
		if i := strings.Index(p, marker); i >= 0 {
			// p[:i] is everything before "/seg/", add "/seg/*" back.
			return p[:i] + "/" + seg + "/*"
		}
		return seg + "/*"
	}
}

func constantSuggest(glob string) func(string) string {
	// Suffix and basename patterns work at any depth via extract's
	// basename match, so a single glob suffices regardless of where the
	// file lives.
	return func(string) string { return glob }
}

func hasSuffixOf(suf string) func(string) bool {
	return func(p string) bool { return strings.HasSuffix(p, suf) }
}

func basenameEquals(name string) func(string) bool {
	return func(p string) bool {
		if i := strings.LastIndex(p, "/"); i >= 0 {
			return p[i+1:] == name
		}
		return p == name
	}
}

// basenameContains matches when seg appears in the final path segment
// (the filename), not in any intermediate directory. Aligns with how
// extract.ShouldIgnore applies `*.generated.*` style globs — if the
// detector matches on a directory but the Suggest glob only targets
// basenames, the user's copy-pasted --ignore would leave the file in.
func basenameContains(seg string) func(string) bool {
	return func(p string) bool {
		base := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			base = p[i+1:]
		}
		return strings.Contains(base, seg)
	}
}

// DetectSuspectFiles scans the dataset for paths matching known
// vendor/generated heuristics. Returns buckets sorted by churn desc.
// Second return value is true when the aggregate suspect churn crosses
// suspectWarningMinChurnRatio of total repo churn — i.e. the warning is
// worth showing. Callers that want to surface findings regardless (e.g.
// a --explain flag) can ignore the boolean.
func DetectSuspectFiles(ds *Dataset) ([]SuspectBucket, bool) {
	if ds == nil || len(ds.files) == 0 {
		return nil, false
	}

	buckets := make(map[string]*SuspectBucket)
	// Per-bucket suggestion sets so identical globs aren't listed twice.
	suggSets := make(map[string]map[string]struct{})
	var totalChurn, suspectChurn int64

	for path, fe := range ds.files {
		fileChurn := fe.additions + fe.deletions
		totalChurn += fileChurn
		// A file might match multiple patterns (e.g. vendor/*.pb.go).
		// Attribute it to the first match so totals don't double-count;
		// the first-match wins keeps output predictable.
		for _, pat := range defaultSuspectPatterns {
			if !pat.Match(path) {
				continue
			}
			b, ok := buckets[pat.Glob]
			if !ok {
				b = &SuspectBucket{Pattern: pat}
				buckets[pat.Glob] = b
				suggSets[pat.Glob] = map[string]struct{}{}
			}
			b.Paths = append(b.Paths, path)
			b.Churn += fileChurn
			suspectChurn += fileChurn
			if pat.Suggest != nil {
				suggSets[pat.Glob][pat.Suggest(path)] = struct{}{}
			}
			break
		}
	}

	if totalChurn == 0 || len(buckets) == 0 {
		return nil, false
	}

	out := make([]SuspectBucket, 0, len(buckets))
	for _, b := range buckets {
		sort.Strings(b.Paths) // deterministic inner order
		if set := suggSets[b.Pattern.Glob]; set != nil {
			b.Suggestions = make([]string, 0, len(set))
			for s := range set {
				b.Suggestions = append(b.Suggestions, s)
			}
			sort.Strings(b.Suggestions)
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Churn != out[j].Churn {
			return out[i].Churn > out[j].Churn
		}
		return out[i].Pattern.Glob < out[j].Pattern.Glob
	})

	worth := float64(suspectChurn)/float64(totalChurn) >= suspectWarningMinChurnRatio
	return out, worth
}

// ShellQuoteSingle wraps s in POSIX single quotes, escaping any
// embedded single quotes via the `'\''` sequence. Git paths can
// legally contain single quotes (and every other shell metacharacter),
// so suggestion globs derived from real repo paths must not be
// interpolated into copy-paste commands with naive `'%s'` formatting —
// one stray `'` in a subdirectory name would split the command into
// multiple words and break everything after it.
func ShellQuoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CollectAllSuggestions returns deduplicated --ignore globs across
// every bucket, preserving bucket order followed by in-bucket order.
// Callers surface a subset of buckets in their UI (e.g. top-N display)
// but must emit remediation suggestions over the full set — the
// warning's noise-floor check is computed against every bucket, so
// trimming suggestions to just the displayed subset would leave
// unshown suspects untouched and cause the warning to persist after
// the suggested fix.
func CollectAllSuggestions(buckets []SuspectBucket) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, b := range buckets {
		for _, s := range b.Suggestions {
			if _, ok := seen[s]; ok {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
