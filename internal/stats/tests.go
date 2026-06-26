package stats

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// TestRole is the role a file plays for the test-coverage proxy metrics.
// Every tracked path resolves to exactly one role, so the three buckets
// partition the file set and "test / source" ratios have a well-defined
// denominator.
//
//   - RoleTest   — a test/spec file (hand-authored verification code).
//   - RoleSource — hand-authored production code (the thing tests cover).
//   - RoleOther  — everything excluded from the ratio: vendor/generated
//     output, lockfiles, docs, config, data, assets. Folding these into
//     "source" would deflate the ratio with files no test could ever
//     cover; folding them into "test" would inflate it. They get their
//     own bucket so the ratio stays meaningful.
type TestRole string

const (
	RoleTest   TestRole = "test"
	RoleSource TestRole = "source"
	RoleOther  TestRole = "other"
)

// codeExtensions is the set of extensions treated as hand-authored
// production code. It defines the "source" denominator: a non-test,
// non-suspect file counts as source only when its extension is here.
// Anything else (docs, config, data, assets) lands in RoleOther.
//
// Deliberately curated rather than exhaustive — adding a language is a
// one-line change, and an unknown extension defaulting to "other" is the
// safe failure mode (it sits out of the ratio instead of silently
// inflating the source count with, say, a vendored binary blob).
// Extensions are stored lowercased to match extractExtension's output.
var codeExtensions = map[string]bool{
	".go":     true,
	".js":     true,
	".jsx":    true,
	".mjs":    true,
	".cjs":    true,
	".ts":     true,
	".tsx":    true,
	".py":     true,
	".rb":     true,
	".java":   true,
	".kt":     true,
	".kts":    true,
	".scala":  true,
	".cs":     true,
	".c":      true,
	".cc":     true,
	".cpp":    true,
	".cxx":    true,
	".h":      true,
	".hpp":    true,
	".hxx":    true,
	".rs":     true,
	".swift":  true,
	".m":      true,
	".mm":     true,
	".php":    true,
	".pl":     true,
	".pm":     true,
	".sh":     true,
	".bash":   true,
	".lua":    true,
	".dart":   true,
	".ex":     true,
	".exs":    true,
	".erl":    true,
	".clj":    true,
	".cljs":   true,
	".groovy": true,
	".vue":    true,
	".svelte": true,
	".jl":     true,
	".hs":     true,
	".ml":     true,
	".fs":     true,
	".f90":    true,
	".r":      true,
	".sql":    true,
}

// isCodeExt reports whether ext (as returned by extractExtension, i.e.
// lowercased and dot-prefixed) names a production-code language.
func isCodeExt(ext string) bool {
	return codeExtensions[ext]
}

// testNameMatchers are the "this filename IS a test" rules whose pattern
// embeds the language extension (`_test.go`, `Test.java`), so they're
// unambiguous regardless of directory and need no code-extension gate —
// `foo_test.go` is a test wherever it lives. Kept conservative: only the
// dominant convention per ecosystem, so the rules don't fire on
// coincidental names.
var testNameMatchers = []func(string) bool{
	hasSuffixOf("_test.go"),  // Go
	hasSuffixOf("_test.py"),  // Python (pytest/nose trailing form)
	hasSuffixOf("_spec.rb"),  // Ruby (RSpec)
	hasSuffixOf("Test.java"), // JVM
	hasSuffixOf("Tests.java"),
	hasSuffixOf("Test.kt"),
	hasSuffixOf("Tests.kt"),
	hasSuffixOf("Test.scala"),
	hasSuffixOf("Spec.scala"),
	hasSuffixOf("Test.cs"), // .NET
	hasSuffixOf("Tests.cs"),
	hasSuffixOf("_test.cc"), // C++ (GoogleTest convention)
	hasSuffixOf("_test.cpp"),
	hasSuffixOf("_test.cxx"),
	hasSuffixOf("_test.rs"), // Rust (separate-file convention)
}

// testNameCodeMatchers are extension-agnostic filename conventions — a
// leading `test_` (Python) or an infix `.test.`/`.spec.` (JS/TS). They
// only count when the file is ALSO code, because otherwise data/config
// like `api.spec.yaml`, `users.test.json`, or `test_data.json` would be
// miscounted as tests. isTestFile applies them behind the isCodeExt gate.
var testNameCodeMatchers = []func(string) bool{
	basenamePrefix("test_"),    // Python unittest/pytest leading form
	basenameContains(".test."), // JS/TS foo.test.ts, foo.test.jsx
	basenameContains(".spec."), // JS/TS/Angular foo.spec.ts
}

// testDirSegments are directory names that conventionally hold tests.
// Unlike testNameMatchers, a directory hit alone is NOT enough — a
// `spec/openapi.yaml` or `test/fixtures/data.json` is data, not a test.
// classifyTestRole requires a directory match to ALSO be a code file
// before calling it a test, which keeps non-code payloads in test trees
// out of the test bucket. "it" (Maven Failsafe integration dir) is
// deliberately excluded: a 2-char generic segment collides with common
// non-test dirs (e.g. an `it/` Italian-locale tree), a false-positive
// risk that outweighs the niche convention.
var testDirSegments = []string{"test", "tests", "spec", "specs", "__tests__", "e2e"}

// basenamePrefix matches when the final path segment (the filename)
// starts with pre. Mirrors the basename-scoped matchers in suspect.go so
// `a/test_dir/util.py` is not mistaken for a test on the directory name.
func basenamePrefix(pre string) func(string) bool {
	return func(p string) bool {
		base := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			base = p[i+1:]
		}
		return strings.HasPrefix(base, pre)
	}
}

// isSuspectPath reports whether p matches any vendor/generated heuristic.
// Reuses the suspect detector's pattern table so "what counts as
// generated noise" has one definition shared between the extract warning
// and the test ratio's exclusions. p must already be repo-prefix-stripped
// (the patterns assume bare repo-relative paths, same as
// DetectSuspectFiles).
func isSuspectPath(p string) bool {
	for _, pat := range defaultSuspectPatterns {
		if pat.Match(p) {
			return true
		}
	}
	return false
}

// TestConfig carries the resolved test-detection rules for one stats run.
// It is built once per invocation from the built-in heuristics plus any
// user --test-glob overrides, then threaded into the test-stat functions
// so every metric (summary, trend, per-dev) classifies paths identically.
type TestConfig struct {
	// extra holds matchers compiled from --test-glob. classifyTestRole
	// checks them first — before the suspect gate and the code-extension
	// gate — so an explicit user glob wins unconditionally: data files
	// (golden-file suites under `testdata/*`) and even paths that match a
	// vendor/generated pattern count as tests when the user names them.
	extra []func(string) bool
}

// NewTestConfig compiles user-supplied --test-glob patterns into a
// TestConfig. Each glob is matched (via path.Match) against the
// repo-relative path and every trailing sub-path, so `*_itest.go`
// (basename shape) and `integration/*` (path shape) both work. Empty
// strings are skipped. Malformed patterns are NOT rejected here (path.Match
// has no compile step — a bad glob simply never matches); call
// ValidateTestGlobs at the CLI layer to surface a typo before this runs.
func NewTestConfig(globs []string) TestConfig {
	var cfg TestConfig
	for _, g := range globs {
		if g == "" {
			continue
		}
		cfg.extra = append(cfg.extra, globMatcher(g))
	}
	return cfg
}

// ValidateTestGlobs returns the first --test-glob that is not a valid glob
// (path.Match syntax, e.g. an unterminated `[` class). Call it from the
// command layer before NewTestConfig so a typo fails fast with a clear
// error instead of silently matching nothing and skewing the test counts.
func ValidateTestGlobs(globs []string) error {
	for _, g := range globs {
		if g == "" {
			continue
		}
		// path.Match validates the whole pattern even against an empty
		// name (it walks the pattern to detect bad syntax), so this
		// reliably surfaces ErrBadPattern.
		if _, err := path.Match(g, ""); err != nil {
			return fmt.Errorf("invalid --test-glob %q: %w", g, err)
		}
	}
	return nil
}

// globMatcher returns a predicate that matches glob against the full
// repo-relative path and every trailing sub-path. Testing each tail lets
// a path glob like `testdata/*` match at any depth (`pkg/testdata/x.json`)
// and a bare basename glob like `*.itest.go` match the filename. Uses
// path.Match (not filepath.Match): git paths are always forward-slash, and
// filepath.Match is OS-aware — on Windows its separator is `\`, so `*`
// would cross `/` and break the per-segment guarantee (same reasoning as
// extract.ShouldIgnore). path.Match never crosses `/`, so a glob with N
// slashes only matches a tail holding the same N segments — `testdata/*`
// matches direct children only; `**` is unsupported, express deep intent
// as a basename glob instead.
func globMatcher(glob string) func(string) bool {
	return func(p string) bool {
		if matchGlobOnce(glob, p) {
			return true
		}
		for i := 0; i < len(p); i++ {
			if p[i] == '/' && matchGlobOnce(glob, p[i+1:]) {
				return true
			}
		}
		return false
	}
}

func matchGlobOnce(glob, s string) bool {
	ok, err := path.Match(glob, s)
	return err == nil && ok
}

// isTestFile reports whether p (an already repo-prefix-stripped path) is a
// test file by built-in convention. User --test-glob overrides are handled
// in classifyTestRole, not here, so this is purely the heuristic layer.
func isTestFile(p string) bool {
	// Extension-embedded conventions fire regardless of location.
	for _, m := range testNameMatchers {
		if m(p) {
			return true
		}
	}
	// Extension-agnostic name conventions and test directories require the
	// file to also be code, so fixtures/data/config (`api.spec.yaml`,
	// `test/fixtures/x.json`) don't count as tests.
	if isCodeExt(extractExtension(p)) {
		for _, m := range testNameCodeMatchers {
			if m(p) {
				return true
			}
		}
		for _, seg := range testDirSegments {
			if hasPathSegment(seg)(p) {
				return true
			}
		}
	}
	return false
}

// classifyTestRole assigns path to exactly one role. Order matters:
//
//  1. Explicit --test-glob overrides win over everything, including the
//     suspect gate — if a user names a path as a test, honor it even under
//     vendor/ or *.lock (TestConfig.extra documents this contract).
//  2. Suspect (vendor/generated/lock/minified) → excluded as noise, so a
//     dependency's own test (`node_modules/x/foo.test.js`) doesn't count
//     toward this repo's test ratio.
//  3. Built-in test naming/dir conventions → test.
//  4. Code extension → source; anything left → other.
func classifyTestRole(path string, cfg TestConfig) TestRole {
	p := stripRepoPrefix(path)
	for _, m := range cfg.extra {
		if m(p) {
			return RoleTest
		}
	}
	if isSuspectPath(p) {
		return RoleOther
	}
	if isTestFile(p) {
		return RoleTest
	}
	if isCodeExt(extractExtension(p)) {
		return RoleSource
	}
	return RoleOther
}

// TestSummary is the headline test-coverage proxy for a dataset: how much
// of the production code's footprint is matched by test code. The ratios
// are intentionally test-over-source (not test-over-total) so the
// denominator is "code a test could plausibly cover" — RoleOther files
// (docs/config/vendor/generated) sit out of both numerator and
// denominator. OtherFiles is reported only for transparency about what
// was excluded.
type TestSummary struct {
	TestFiles   int
	SourceFiles int
	OtherFiles  int
	FileRatio   float64 // TestFiles / SourceFiles; 0 when SourceFiles == 0
	TestChurn   int64
	SourceChurn int64
	ChurnRatio  float64 // TestChurn / SourceChurn; 0 when SourceChurn == 0
	ByLanguage  []TestLangStat
}

// TestLangStat is the same ratio sliced to one language bucket (keyed by
// extractExtension of the canonical path). A test file's extension places
// it in its language's bucket — `foo_test.go` and `bar.go` both land
// under ".go" — so each row reads as "this language's test-to-source
// balance".
type TestLangStat struct {
	Ext         string
	TestFiles   int
	SourceFiles int
	TestChurn   int64
	SourceChurn int64
	FileRatio   float64
	ChurnRatio  float64
}

// safeRatio is num/den as a float, guarding the zero-denominator case
// (no source code, or no source churn) by returning 0 instead of Inf/NaN.
// Callers that must distinguish "0 because empty" from "genuinely 0"
// check the underlying denominator themselves.
func safeRatio(num, den int64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// ComputeTestSummary classifies every tracked file once and aggregates
// the test/source ratio overall and per language. Churn is whole-file
// (additions+deletions) attributed to the file's canonical path; unlike
// ExtensionStats it does not split a rename-across-extensions lineage,
// because a file is wholly a test or wholly source — the per-era split
// would not change which side of the ratio it lands on.
//
// langTop bounds the ByLanguage slice (0 = all), ranking languages by
// combined test+source churn so the busiest languages surface first.
func ComputeTestSummary(ds *Dataset, cfg TestConfig, langTop int) TestSummary {
	type langAcc struct {
		testFiles, sourceFiles int
		testChurn, sourceChurn int64
	}
	langs := map[string]*langAcc{}
	getLang := func(ext string) *langAcc {
		la, ok := langs[ext]
		if !ok {
			la = &langAcc{}
			langs[ext] = la
		}
		return la
	}

	var s TestSummary
	for path, fe := range ds.files {
		churn := fe.additions + fe.deletions
		switch classifyTestRole(path, cfg) {
		case RoleTest:
			s.TestFiles++
			s.TestChurn += churn
			la := getLang(extractExtension(path))
			la.testFiles++
			la.testChurn += churn
		case RoleSource:
			s.SourceFiles++
			s.SourceChurn += churn
			la := getLang(extractExtension(path))
			la.sourceFiles++
			la.sourceChurn += churn
		default:
			s.OtherFiles++
		}
	}

	s.FileRatio = safeRatio(int64(s.TestFiles), int64(s.SourceFiles))
	s.ChurnRatio = safeRatio(s.TestChurn, s.SourceChurn)

	s.ByLanguage = make([]TestLangStat, 0, len(langs))
	for ext, la := range langs {
		s.ByLanguage = append(s.ByLanguage, TestLangStat{
			Ext:         ext,
			TestFiles:   la.testFiles,
			SourceFiles: la.sourceFiles,
			TestChurn:   la.testChurn,
			SourceChurn: la.sourceChurn,
			FileRatio:   safeRatio(int64(la.testFiles), int64(la.sourceFiles)),
			ChurnRatio:  safeRatio(la.testChurn, la.sourceChurn),
		})
	}
	// Busiest language first; ext asc breaks ties so output is stable.
	sortTestLangs(s.ByLanguage)
	if langTop > 0 && langTop < len(s.ByLanguage) {
		s.ByLanguage = s.ByLanguage[:langTop]
	}
	return s
}

// TestRatioBucket is the test:source churn ratio for one time period.
// Periods are "YYYY-MM" (month) or "YYYY" (year).
type TestRatioBucket struct {
	Period      string
	TestChurn   int64
	SourceChurn int64
	Ratio       float64 // TestChurn / SourceChurn; 0 when SourceChurn == 0
}

// TestRatioOverTime tracks how the test:source churn ratio moved over the
// life of the repo — "is this codebase getting more or less tested?".
//
// Resolution is monthly because the only per-file time series retained on
// the Dataset is fileEntry.monthChurn (keyed "YYYY-MM"). granularity
// "year" rolls months up to "YYYY"; "month" passes through; finer values
// ("day"/"week") have no backing data and fall back to monthly buckets.
// RoleOther files are skipped so the denominator matches ComputeTestSummary.
func TestRatioOverTime(ds *Dataset, cfg TestConfig, granularity string) []TestRatioBucket {
	type acc struct{ test, source int64 }
	buckets := map[string]*acc{}

	periodKey := func(month string) string {
		if granularity == "year" && len(month) >= 4 {
			return month[:4]
		}
		return month
	}

	for path, fe := range ds.files {
		role := classifyTestRole(path, cfg)
		if role == RoleOther {
			continue
		}
		for month, churn := range fe.monthChurn {
			k := periodKey(month)
			b, ok := buckets[k]
			if !ok {
				b = &acc{}
				buckets[k] = b
			}
			if role == RoleTest {
				b.test += churn
			} else {
				b.source += churn
			}
		}
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys) // chronological: "YYYY-MM"/"YYYY" sort lexically

	out := make([]TestRatioBucket, 0, len(keys))
	for _, k := range keys {
		b := buckets[k]
		out = append(out, TestRatioBucket{
			Period:      k,
			TestChurn:   b.test,
			SourceChurn: b.source,
			Ratio:       safeRatio(b.test, b.source),
		})
	}
	return out
}

func sortTestLangs(ls []TestLangStat) {
	sort.Slice(ls, func(i, j int) bool {
		ci := ls[i].TestChurn + ls[i].SourceChurn
		cj := ls[j].TestChurn + ls[j].SourceChurn
		if ci != cj {
			return ci > cj
		}
		return ls[i].Ext < ls[j].Ext
	})
}
