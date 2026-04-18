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
// that would distort hotspots, churn-risk, and bus factor. Glob is a
// ready-to-paste pattern for the extract --ignore flag.
type SuspectPattern struct {
	Glob   string
	Reason string
	Match  func(path string) bool
}

// SuspectBucket is one pattern's match set aggregated across the dataset.
type SuspectBucket struct {
	Pattern SuspectPattern
	Paths   []string
	Churn   int64
}

// defaultSuspectPatterns covers high-confidence vendor/generated paths.
// Kept conservative so the warning doesn't cry wolf; statistical signals
// (churn/commit outliers, single-author bulk updates) are deliberately
// out of scope until we have evidence they help rather than create
// false positives.
var defaultSuspectPatterns = []SuspectPattern{
	{Glob: "vendor/*", Reason: "vendored third-party code", Match: hasPathSegment("vendor")},
	{Glob: "node_modules/*", Reason: "npm dependencies", Match: hasPathSegment("node_modules")},
	{Glob: "dist/*", Reason: "build artifacts", Match: hasPathSegment("dist")},
	{Glob: "build/*", Reason: "build output", Match: hasPathSegment("build")},
	{Glob: "third_party/*", Reason: "third-party code", Match: hasPathSegment("third_party")},
	{Glob: "*.min.js", Reason: "minified JS", Match: hasSuffixOf(".min.js")},
	{Glob: "*.min.css", Reason: "minified CSS", Match: hasSuffixOf(".min.css")},
	{Glob: "*.lock", Reason: "generic lock file", Match: hasSuffixOf(".lock")},
	{Glob: "package-lock.json", Reason: "npm lockfile", Match: basenameEquals("package-lock.json")},
	{Glob: "yarn.lock", Reason: "yarn lockfile", Match: basenameEquals("yarn.lock")},
	{Glob: "pnpm-lock.yaml", Reason: "pnpm lockfile", Match: basenameEquals("pnpm-lock.yaml")},
	{Glob: "go.sum", Reason: "go module hashes", Match: basenameEquals("go.sum")},
	{Glob: "Cargo.lock", Reason: "cargo lockfile", Match: basenameEquals("Cargo.lock")},
	{Glob: "poetry.lock", Reason: "poetry lockfile", Match: basenameEquals("poetry.lock")},
	{Glob: "*.pb.go", Reason: "protobuf generated (Go)", Match: hasSuffixOf(".pb.go")},
	{Glob: "*_pb2.py", Reason: "protobuf generated (Python)", Match: hasSuffixOf("_pb2.py")},
	{Glob: "*.generated.*", Reason: "generated code", Match: containsSegment(".generated.")},
}

func hasPathSegment(seg string) func(string) bool {
	return func(p string) bool {
		return strings.HasPrefix(p, seg+"/") || strings.Contains(p, "/"+seg+"/")
	}
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

func containsSegment(seg string) func(string) bool {
	return func(p string) bool { return strings.Contains(p, seg) }
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
			}
			b.Paths = append(b.Paths, path)
			b.Churn += fileChurn
			suspectChurn += fileChurn
			break
		}
	}

	if totalChurn == 0 || len(buckets) == 0 {
		return nil, false
	}

	out := make([]SuspectBucket, 0, len(buckets))
	for _, b := range buckets {
		sort.Strings(b.Paths) // deterministic inner order
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
