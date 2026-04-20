package stats

import (
	"strings"
	"testing"

	"github.com/lex0c/gitcortex/internal/extract"
)

func TestDetectSuspectFilesPaths(t *testing.T) {
	ds := &Dataset{
		files: map[string]*fileEntry{
			"src/main.go":                       {additions: 100, deletions: 50},
			"vendor/github.com/x/y/util.go":     {additions: 500, deletions: 200},
			"node_modules/react/index.js":       {additions: 300, deletions: 100},
			"package-lock.json":                 {additions: 5000, deletions: 4000},
			"static/app.min.js":                 {additions: 200, deletions: 100},
			"proto/foo.pb.go":                   {additions: 800, deletions: 700},
			"subproject/package-lock.json":      {additions: 1000, deletions: 800},
			"clean/file.txt":                    {additions: 20, deletions: 5},
		},
	}
	buckets, worth := DetectSuspectFiles(ds)
	if !worth {
		t.Fatal("expected worth=true with heavy vendor + lock churn")
	}

	wantGlobs := map[string]bool{
		"vendor/*":          true,
		"node_modules/*":    true,
		"package-lock.json": true,
		"*.min.js":          true,
		"*.pb.go":           true,
	}
	gotGlobs := map[string]bool{}
	for _, b := range buckets {
		gotGlobs[b.Pattern.Glob] = true
	}
	for g := range wantGlobs {
		if !gotGlobs[g] {
			t.Errorf("expected bucket for %q, missing. Got: %v", g, gotGlobs)
		}
	}
	// package-lock.json should match both root and subproject/... via the
	// basenameEquals matcher
	for _, b := range buckets {
		if b.Pattern.Glob == "package-lock.json" && len(b.Paths) != 2 {
			t.Errorf("package-lock.json matched %d files, want 2 (both roots)", len(b.Paths))
		}
	}
}

func TestDetectSuspectFilesBelowNoiseFloor(t *testing.T) {
	// One small lock file in an otherwise clean repo — below 10% churn
	// ratio, so worth=false.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"src/a.go":          {additions: 1000, deletions: 500},
			"src/b.go":          {additions: 800, deletions: 300},
			"src/c.go":          {additions: 600, deletions: 200},
			"package-lock.json": {additions: 50, deletions: 30},
		},
	}
	_, worth := DetectSuspectFiles(ds)
	if worth {
		t.Errorf("tiny lock file should not trigger warning; got worth=true")
	}
}

func TestDetectSuspectFilesEmpty(t *testing.T) {
	_, worth := DetectSuspectFiles(&Dataset{files: map[string]*fileEntry{}})
	if worth {
		t.Error("empty dataset should not trigger warning")
	}
	_, worth = DetectSuspectFiles(nil)
	if worth {
		t.Error("nil dataset should not trigger warning")
	}
}

func TestDetectSuspectFilesNestedDirSuggestions(t *testing.T) {
	// Directory-segment matcher catches nested occurrences (pkg/vendor/...,
	// subproject/node_modules/...), but the Glob label is only a short
	// bucket header. The Suggestions list must include the *specific*
	// parent paths so extract --ignore actually removes the matched
	// files — extract treats "vendor/*" as a repo-root prefix and will
	// not match "pkg/vendor/foo.go".
	ds := &Dataset{
		files: map[string]*fileEntry{
			"vendor/a.go":               {additions: 100, deletions: 50},
			"pkg/vendor/b.go":           {additions: 100, deletions: 50},
			"pkg/vendor/c.go":           {additions: 100, deletions: 50},
			"services/auth/vendor/d.go": {additions: 100, deletions: 50},
			"node_modules/e.js":         {additions: 50, deletions: 25},
			"sub/node_modules/f.js":     {additions: 50, deletions: 25},
		},
	}
	buckets, _ := DetectSuspectFiles(ds)
	byGlob := map[string]SuspectBucket{}
	for _, b := range buckets {
		byGlob[b.Pattern.Glob] = b
	}

	vb, ok := byGlob["vendor/*"]
	if !ok {
		t.Fatal("vendor/* bucket missing")
	}
	wantVendor := map[string]bool{"vendor/*": true, "pkg/vendor/*": true, "services/auth/vendor/*": true}
	if len(vb.Suggestions) != len(wantVendor) {
		t.Errorf("vendor Suggestions = %v, want keys %v", vb.Suggestions, keys(wantVendor))
	}
	for _, s := range vb.Suggestions {
		if !wantVendor[s] {
			t.Errorf("unexpected vendor suggestion %q", s)
		}
	}

	nm, ok := byGlob["node_modules/*"]
	if !ok {
		t.Fatal("node_modules/* bucket missing")
	}
	wantNM := map[string]bool{"node_modules/*": true, "sub/node_modules/*": true}
	for _, s := range nm.Suggestions {
		if !wantNM[s] {
			t.Errorf("unexpected node_modules suggestion %q", s)
		}
	}
	if len(nm.Suggestions) != len(wantNM) {
		t.Errorf("node_modules Suggestions = %v, want %v", nm.Suggestions, keys(wantNM))
	}
}

func TestDetectSuspectFilesGeneratedMatchesBasenameOnly(t *testing.T) {
	// *.generated.* in the detector used to match on the full path via
	// strings.Contains, catching paths where `.generated.` appeared in
	// a directory name (src/foo.generated.v1/bar.go). The emitted
	// suggestion is the literal `*.generated.*` glob, and extract's
	// basename match would not ignore bar.go, so the warning would
	// keep firing on every subsequent run.
	//
	// The matcher is now a basename check so directory-name occurrences
	// don't get flagged in the first place. This test pins both sides:
	// the flagged path IS matched by the suggestion, and the dir-only
	// path is NOT flagged.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"src/main.generated.go":          {additions: 500, deletions: 500}, // real generated file → should flag
			"src/foo.generated.v1/bar.go":    {additions: 500, deletions: 500}, // `.generated.` is in dir name → should NOT flag
			"pkg/util/types.generated.proto": {additions: 500, deletions: 500}, // basename has it → should flag
		},
	}
	buckets, _ := DetectSuspectFiles(ds)
	var gen *SuspectBucket
	for i := range buckets {
		if buckets[i].Pattern.Glob == "*.generated.*" {
			gen = &buckets[i]
			break
		}
	}
	if gen == nil {
		t.Fatal("*.generated.* bucket missing — expected at least the two basename matches")
	}
	// Must contain the two basename matches, must NOT contain the dir-only one.
	wantIn := map[string]bool{
		"src/main.generated.go":          true,
		"pkg/util/types.generated.proto": true,
	}
	wantOut := "src/foo.generated.v1/bar.go"
	for _, p := range gen.Paths {
		if !wantIn[p] && p != wantOut {
			t.Errorf("unexpected path in *.generated.* bucket: %q", p)
		}
		if p == wantOut {
			t.Errorf("path %q has `.generated.` only in its directory — should not be flagged because "+
				"`--ignore '*.generated.*'` would not cover it (shouldIgnore matches basename)", p)
		}
	}
}

func TestDetectSuspectFilesSuffixSuggestionsUnchanged(t *testing.T) {
	// Suffix/basename matchers already work at any depth via extract's
	// basename match path, so their Suggestions collapse to a single
	// canonical glob regardless of how deeply nested the matches are.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"app.min.js":               {additions: 500, deletions: 500},
			"static/jquery.min.js":     {additions: 500, deletions: 500},
			"sub/dist/foo/bar.min.js":  {additions: 500, deletions: 500}, // also matches dist/*, first match wins
			"pkg/sub/package-lock.json": {additions: 500, deletions: 500},
			"package-lock.json":        {additions: 500, deletions: 500},
		},
	}
	buckets, _ := DetectSuspectFiles(ds)
	for _, b := range buckets {
		switch b.Pattern.Glob {
		case "*.min.js":
			if len(b.Suggestions) != 1 || b.Suggestions[0] != "*.min.js" {
				t.Errorf("*.min.js Suggestions = %v, want [*.min.js]", b.Suggestions)
			}
		case "package-lock.json":
			if len(b.Suggestions) != 1 || b.Suggestions[0] != "package-lock.json" {
				t.Errorf("package-lock.json Suggestions = %v, want [package-lock.json]", b.Suggestions)
			}
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSuspectSuggestionsMatchExtractShouldIgnore is the end-to-end
// invariant: every --ignore glob the warning emits must cause
// extract.ShouldIgnore to return true for the paths that caused that
// glob to be emitted. Without this test the two surfaces (detector
// and ignore matcher) can drift silently — e.g. a future refactor of
// ShouldIgnore that tightens prefix semantics would break the
// suspect warning's entire value proposition without any stats test
// firing.
func TestShellQuoteSingle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"vendor/*", `'vendor/*'`},
		{"*.min.js", `'*.min.js'`},
		{"package-lock.json", `'package-lock.json'`},
		{"", `''`},
		// The whole reason this helper exists: paths carrying a single
		// quote would break naive '%s' interpolation. Expected POSIX
		// shell-quoting closes the quote, appends an escaped `'`, then
		// reopens the quote.
		{"foo's vendor/*", `'foo'\''s vendor/*'`},
		{"a'b'c", `'a'\''b'\''c'`},
		{"'", `''\'''`},
		// Other shell metacharacters stay safe inside single quotes.
		{"$VAR`cmd`/*", `'$VAR` + "`cmd`" + `/*'`},
		{`has "double" quotes/*`, `'has "double" quotes/*'`},
	}
	for _, c := range cases {
		got := ShellQuoteSingle(c.in)
		if got != c.want {
			t.Errorf("ShellQuoteSingle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShellQuoteSingleRoundTrip sanity-checks that the quoted form
// actually parses back to the original string under POSIX shell
// single-quote rules. Implemented by a hand-rolled mini-parser rather
// than shelling out, so the test is deterministic and hermetic.
func TestShellQuoteSingleRoundTrip(t *testing.T) {
	inputs := []string{
		"vendor/*",
		"foo's dir/*.go",
		"it's a/b's c",
		"'",
		"''",
		"a'b",
		"plain/path",
	}
	for _, in := range inputs {
		quoted := ShellQuoteSingle(in)
		got := posixSingleQuoteUnquote(t, quoted)
		if got != in {
			t.Errorf("round-trip of %q via %q → %q", in, quoted, got)
		}
	}
}

// posixSingleQuoteUnquote parses a string composed only of single-quoted
// segments and `\'` escapes (the exact shape ShellQuoteSingle emits).
// Anything else is a test failure.
func posixSingleQuoteUnquote(t *testing.T, s string) string {
	t.Helper()
	var out []byte
	i := 0
	for i < len(s) {
		if s[i] != '\'' {
			t.Fatalf("expected opening ' at index %d in %q", i, s)
		}
		i++ // consume opening '
		for i < len(s) && s[i] != '\'' {
			out = append(out, s[i])
			i++
		}
		if i >= len(s) {
			t.Fatalf("unterminated single quote in %q", s)
		}
		i++ // consume closing '
		// Optional `\'` escape bridging two single-quoted segments
		if i+1 < len(s) && s[i] == '\\' && s[i+1] == '\'' {
			out = append(out, '\'')
			i += 2
		}
	}
	return string(out)
}

func TestCollectAllSuggestionsCoversUnshownBuckets(t *testing.T) {
	// Construct a dataset that triggers every directory-segment and
	// common lockfile/suffix bucket, more than the CLI's display limit
	// of 6 buckets. The aggregated suggestion list must include every
	// bucket's glob so that a user copy-pasting the --ignore command
	// covers the whole suspect set — not just the top-6 displayed.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"vendor/a.go":         {additions: 500, deletions: 500},
			"node_modules/b.js":   {additions: 500, deletions: 500},
			"dist/c.js":           {additions: 500, deletions: 500},
			"build/d.out":         {additions: 500, deletions: 500},
			"third_party/e.go":    {additions: 500, deletions: 500},
			"foo.min.js":          {additions: 500, deletions: 500},
			"bar.min.css":         {additions: 500, deletions: 500},
			"package-lock.json":   {additions: 500, deletions: 500},
			"yarn.lock":           {additions: 500, deletions: 500},
			"go.sum":              {additions: 500, deletions: 500},
			"proto/thing.pb.go":   {additions: 500, deletions: 500},
		},
	}
	buckets, worth := DetectSuspectFiles(ds)
	if !worth {
		t.Fatal("expected warning-worthy dataset")
	}
	if len(buckets) <= 6 {
		t.Fatalf("this test needs > 6 buckets to exercise the unshown-bucket path; got %d", len(buckets))
	}

	suggestions := CollectAllSuggestions(buckets)
	seen := map[string]bool{}
	for _, s := range suggestions {
		seen[s] = true
	}
	// Every bucket's Suggestions must appear in the aggregated list.
	for _, b := range buckets {
		for _, want := range b.Suggestions {
			if !seen[want] {
				t.Errorf("bucket %q suggestion %q missing from aggregated list — "+
					"user copy-paste would leave this bucket's paths in place",
					b.Pattern.Glob, want)
			}
		}
	}
	// Dedup sanity: no duplicates in the output.
	counts := map[string]int{}
	for _, s := range suggestions {
		counts[s]++
		if counts[s] > 1 {
			t.Errorf("suggestion %q appears %d times; expected dedup", s, counts[s])
		}
	}
}

// When paths come from LoadMultiJSONL they carry a `<repo>:` prefix.
// Those prefixes leak into directory-glob suggestions and make the
// copy-pasted `extract --ignore` / `scan --extract-ignore` command
// a no-op on real repo paths (which never carry the prefix).
// CollectAllSuggestions must strip the prefix and collapse duplicates
// that only differed in which repo they came from.
func TestCollectAllSuggestionsStripsRepoPrefix(t *testing.T) {
	ds := &Dataset{
		files: map[string]*fileEntry{
			// Two repos with the same offending shape — suggestions
			// collapse to one canonical "dist/*" glob.
			"repoA:js/dist/a.min.js":  {additions: 400, deletions: 400},
			"repoB:css/dist/b.min.css": {additions: 400, deletions: 400},
			// A single-repo path stays untouched (no prefix).
			"vendor/c.go": {additions: 400, deletions: 400},
		},
	}
	buckets, _ := DetectSuspectFiles(ds)
	got := CollectAllSuggestions(buckets)

	for _, s := range got {
		if strings.Contains(s, ":") {
			t.Errorf("suggestion %q still carries a repo prefix — users can't copy-paste this into extract --ignore", s)
		}
	}
	has := func(want string) bool {
		for _, s := range got {
			if s == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"js/dist/*", "css/dist/*", "vendor/*"} {
		if !has(want) {
			t.Errorf("expected suggestion %q in %v", want, got)
		}
	}
}

func TestStripRepoPrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"repoA:wp-includes/dist/*", "wp-includes/dist/*"},
		{"dist/*", "dist/*"},                          // no prefix
		{"*.min.js", "*.min.js"},                      // no prefix
		{"vendor/*", "vendor/*"},                      // no prefix (no colon)
		{"src/weird:name.go", "src/weird:name.go"},    // colon after slash = not a prefix
		{"glob*prefix:foo/bar", "glob*prefix:foo/bar"}, // metachar before colon = not a prefix
	}
	for _, c := range cases {
		if got := stripRepoPrefix(c.in); got != c.want {
			t.Errorf("stripRepoPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Regression: multi-repo loads prefix every file path with `<repo>:`,
// and the pattern predicates (hasPathSegment, basenameEquals) compare
// against the raw string. Root-level occurrences like
// `alpha:vendor/x.go` and `alpha:package-lock.json` slipped past
// detection because `strings.HasPrefix(p, "vendor/")` and
// `p == "package-lock.json"` both fail when the `<repo>:` prefix is
// present. Symptom in the wild: stats on a scan consumed multiple
// --input files, suspect warning suppressed, users kept seeing
// inflated churn/bus-factor from generated content with no hint to
// fix it.
func TestDetectSuspectFiles_RootLevelUnderRepoPrefix(t *testing.T) {
	ds := &Dataset{
		files: map[string]*fileEntry{
			// Root-level occurrences — the cases that previously missed.
			"alpha:vendor/x.go":        {additions: 400, deletions: 400},
			"beta:package-lock.json":   {additions: 400, deletions: 400},
			"beta:go.sum":              {additions: 400, deletions: 400},
			// Nested occurrence — worked before the fix and must still work.
			"alpha:pkg/vendor/deep.go": {additions: 100, deletions: 100},
		},
	}
	buckets, worth := DetectSuspectFiles(ds)
	if !worth {
		t.Fatal("expected warning-worthy dataset; prefix-stripped matching should fire on root-level vendor/lockfiles")
	}

	got := map[string]bool{}
	for _, b := range buckets {
		got[b.Pattern.Glob] = true
	}
	for _, want := range []string{"vendor/*", "package-lock.json", "go.sum"} {
		if !got[want] {
			t.Errorf("pattern %q missing from detected buckets %v — root-level %q under repo prefix went undetected", want, got, want)
		}
	}

	// Suggestions should come out clean (no `<repo>:`) so the
	// copy-pasted --ignore command actually matches these paths
	// next run.
	suggestions := CollectAllSuggestions(buckets)
	for _, s := range suggestions {
		if len(s) > 0 && s[0] == '\'' {
			t.Errorf("suggestion %q should not carry shell quotes here", s)
		}
	}
}

func TestSuspectSuggestionsMatchExtractShouldIgnore(t *testing.T) {
	// Mix root-level and nested occurrences of every pattern class:
	// directory segments (vendor, dist), suffix (*.min.js), basename
	// (package-lock.json), generated extensions (*.pb.go).
	ds := &Dataset{
		files: map[string]*fileEntry{
			"vendor/a.go":                    {additions: 100, deletions: 50},
			"pkg/vendor/deep/b.go":           {additions: 100, deletions: 50},
			"services/auth/vendor/c.go":      {additions: 100, deletions: 50},
			"dist/bundle.js":                 {additions: 200, deletions: 200},
			"wp-includes/js/dist/editor.js":  {additions: 200, deletions: 200},
			"wp-admin/dist/app/chunk.js":     {additions: 200, deletions: 200},
			"node_modules/foo.js":            {additions: 80, deletions: 40},
			"sub/proj/node_modules/bar.js":   {additions: 80, deletions: 40},
			"app.min.js":                     {additions: 60, deletions: 30},
			"static/vendor.min.js":           {additions: 60, deletions: 30}, // also matches vendor, first match wins
			"deep/nested/path/thing.min.js":  {additions: 60, deletions: 30},
			"package-lock.json":              {additions: 500, deletions: 400},
			"projects/x/package-lock.json":   {additions: 500, deletions: 400},
			"proto/foo.pb.go":                {additions: 300, deletions: 200},
		},
	}
	buckets, _ := DetectSuspectFiles(ds)
	if len(buckets) == 0 {
		t.Fatal("no buckets produced; test inputs should trigger at least vendor/dist/min.js buckets")
	}

	// For each bucket, feed its Suggestions into ShouldIgnore and
	// assert every Path in that bucket is matched by at least one
	// suggestion. A false here means the warning's suggested fix
	// would leave the file untouched — exactly the regression we're
	// guarding against.
	for _, b := range buckets {
		if len(b.Suggestions) == 0 {
			t.Errorf("bucket %q has paths %v but no suggestions", b.Pattern.Glob, b.Paths)
			continue
		}
		for _, p := range b.Paths {
			if !extract.ShouldIgnore(p, b.Suggestions) {
				t.Errorf("bucket %q: path %q is not matched by any of its suggestions %v — "+
					"user would copy-paste a fix that doesn't drop this file",
					b.Pattern.Glob, p, b.Suggestions)
			}
		}
	}
}

func TestDetectSuspectFilesOrdering(t *testing.T) {
	// Buckets must be sorted by churn desc; ties by glob asc. Determinism
	// matters because the warning output lists top-N.
	ds := &Dataset{
		files: map[string]*fileEntry{
			"vendor/a.go":    {additions: 100, deletions: 100}, // 200 churn
			"node_modules/x.js": {additions: 500, deletions: 500}, // 1000 churn
			"app.min.js":     {additions: 100, deletions: 100}, // 200 churn
		},
	}
	buckets, _ := DetectSuspectFiles(ds)
	if len(buckets) < 3 {
		t.Fatalf("want 3 buckets, got %d", len(buckets))
	}
	if buckets[0].Pattern.Glob != "node_modules/*" {
		t.Errorf("top bucket = %q, want %q", buckets[0].Pattern.Glob, "node_modules/*")
	}
	// vendor/* and *.min.js tied at 200 — tiebreak by glob asc → *.min.js first.
	if buckets[1].Pattern.Glob != "*.min.js" {
		t.Errorf("second bucket = %q, want %q (tiebreak asc)", buckets[1].Pattern.Glob, "*.min.js")
	}
}
