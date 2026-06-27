package stats

import "testing"

func TestClassifyTestRole(t *testing.T) {
	cfg := NewTestConfig(nil)
	cases := []struct {
		path string
		want TestRole
	}{
		// --- Test naming conventions (fire regardless of directory) ---
		{"internal/stats/stats_test.go", RoleTest},      // Go
		{"foo_test.py", RoleTest},                       // Python trailing
		{"tests/test_login.py", RoleTest},               // Python leading
		{"src/components/Button.test.tsx", RoleTest},    // JS/TS .test.
		{"src/app/user.spec.ts", RoleTest},              // Angular .spec.
		{"spec/models/user_spec.rb", RoleTest},          // Ruby RSpec
		{"src/main/java/com/x/FooTest.java", RoleTest},  // JVM
		{"src/main/java/com/x/FooTests.java", RoleTest}, // JVM plural
		{"app/UserServiceTest.kt", RoleTest},            // Kotlin
		{"Calc/CalcTests.cs", RoleTest},                 // .NET
		{"src/widget_test.cc", RoleTest},                // C++ GoogleTest
		{"src/lib_test.rs", RoleTest},                   // Rust separate file

		// --- Test directories: code in a test dir is a test ---
		{"test/handler.go", RoleTest},
		{"src/test/java/com/x/Helper.java", RoleTest}, // Maven layout
		{"e2e/checkout.ts", RoleTest},
		{"__tests__/reducer.js", RoleTest},

		// --- Test directories with NON-code payload stay "other" ---
		{"spec/openapi.yaml", RoleOther},       // API spec, not a test
		{"test/fixtures/data.json", RoleOther}, // fixture data
		{"tests/golden/output.txt", RoleOther},

		// --- Name conventions on NON-code payload stay "other": the
		// extension-agnostic .test./.spec./test_ rules are code-gated.
		{"api.spec.yaml", RoleOther},        // OpenAPI spec, not a test
		{"data/users.test.json", RoleOther}, // fixture data
		{"test_data.json", RoleOther},       // fixture data, not a Python test

		// --- "it" is NOT a test dir segment (locale-collision guard) ---
		{"web/i18n/it/strings.go", RoleSource},

		// --- Source: code that is not a test ---
		{"internal/stats/stats.go", RoleSource},
		{"src/components/Button.tsx", RoleSource},
		{"cmd/gitcortex/main.go", RoleSource},
		{"lib/user.rb", RoleSource},

		// --- Other: docs, config, data ---
		{"README.md", RoleOther},
		{"docs/guide.rst", RoleOther},
		{"config.yaml", RoleOther},
		{"data/seed.json", RoleOther},
		{"assets/logo.svg", RoleOther},
		{"Makefile", RoleOther},

		// --- Suspect wins over test: a dependency's own test is noise ---
		{"node_modules/lib/foo.test.js", RoleOther},
		{"vendor/pkg/util_test.go", RoleOther},
		{"dist/bundle.min.js", RoleOther},
		{"go.sum", RoleOther},
		{"package-lock.json", RoleOther},

		// --- Multi-repo prefix is stripped before classification ---
		{"myrepo:internal/stats/stats_test.go", RoleTest},
		{"myrepo:cmd/main.go", RoleSource},
		{"myrepo:vendor/x/a_test.go", RoleOther},
	}

	for _, c := range cases {
		if got := classifyTestRole(c.path, cfg); got != c.want {
			t.Errorf("classifyTestRole(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// The Python `test_` prefix rule must scope to the basename, not the
// path: util.go lives in a "test_utils" directory but is production code.
// "test_utils" is also not one of the test dir segments ("test"/"tests"),
// so the file should classify as plain source.
func TestTestNamePrefixScopesToBasename(t *testing.T) {
	cfg := NewTestConfig(nil)
	if got := classifyTestRole("test_utils/util.go", cfg); got != RoleSource {
		t.Errorf("classifyTestRole(test_utils/util.go) = %q, want source", got)
	}
}

func TestComputeTestSummary(t *testing.T) {
	ds := &Dataset{files: map[string]*fileEntry{
		"main.go":          {additions: 100, deletions: 0},
		"util.go":          {additions: 40, deletions: 10},
		"main_test.go":     {additions: 50, deletions: 5},
		"util_test.go":     {additions: 20, deletions: 0},
		"README.md":        {additions: 30, deletions: 0}, // other
		"app/user.ts":      {additions: 80, deletions: 20},
		"app/user.spec.ts": {additions: 60, deletions: 0},
		"vendor/x_test.go": {additions: 999, deletions: 0}, // suspect → other
	}}

	s := ComputeTestSummary(ds, NewTestConfig(nil), 0)

	if s.TestFiles != 3 || s.SourceFiles != 3 || s.OtherFiles != 2 {
		t.Fatalf("counts: test=%d source=%d other=%d, want 3/3/2", s.TestFiles, s.SourceFiles, s.OtherFiles)
	}
	if s.TestChurn != 135 || s.SourceChurn != 250 {
		t.Fatalf("churn: test=%d source=%d, want 135/250", s.TestChurn, s.SourceChurn)
	}
	if s.FileRatio != 1.0 {
		t.Errorf("FileRatio = %v, want 1.0", s.FileRatio)
	}
	if s.ChurnRatio != 135.0/250.0 {
		t.Errorf("ChurnRatio = %v, want %v", s.ChurnRatio, 135.0/250.0)
	}

	// The vendored *_test.go must not leak into the test bucket.
	if s.TestChurn >= 999 {
		t.Errorf("vendored test churn leaked into TestChurn (%d)", s.TestChurn)
	}

	// By-language: .go busiest (385 churn) before .ts (260).
	if len(s.ByLanguage) != 2 {
		t.Fatalf("ByLanguage len = %d, want 2", len(s.ByLanguage))
	}
	if s.ByLanguage[0].Ext != ".go" || s.ByLanguage[1].Ext != ".ts" {
		t.Fatalf("ByLanguage order = [%s %s], want [.go .ts]", s.ByLanguage[0].Ext, s.ByLanguage[1].Ext)
	}
	goLang := s.ByLanguage[0]
	if goLang.TestFiles != 2 || goLang.SourceFiles != 2 || goLang.TestChurn != 75 || goLang.SourceChurn != 150 {
		t.Errorf(".go lang = %+v, want test 2/75 source 2/150", goLang)
	}
}

func TestComputeTestSummaryNoSource(t *testing.T) {
	// A repo of only docs/config: zero source means the ratio is
	// undefined, not infinite. Guard the denominator.
	ds := &Dataset{files: map[string]*fileEntry{
		"README.md":   {additions: 10},
		"config.yaml": {additions: 5},
	}}
	s := ComputeTestSummary(ds, NewTestConfig(nil), 0)
	if s.SourceFiles != 0 || s.TestFiles != 0 {
		t.Fatalf("counts: test=%d source=%d, want 0/0", s.TestFiles, s.SourceFiles)
	}
	if s.FileRatio != 0 || s.ChurnRatio != 0 {
		t.Errorf("ratios should be 0 (guarded) when no source; got file=%v churn=%v", s.FileRatio, s.ChurnRatio)
	}
}

func TestTestRatioOverTime(t *testing.T) {
	ds := &Dataset{files: map[string]*fileEntry{
		"main.go":      {monthChurn: map[string]int64{"2024-01": 100, "2024-02": 50}},
		"main_test.go": {monthChurn: map[string]int64{"2024-02": 40, "2024-03": 20}},
		"README.md":    {monthChurn: map[string]int64{"2024-01": 999}}, // other → skipped
	}}

	got := TestRatioOverTime(ds, NewTestConfig(nil), "month")
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (2024-01..03)", len(got))
	}
	if got[0].Period != "2024-01" || got[0].SourceChurn != 100 || got[0].TestChurn != 0 {
		t.Errorf("bucket[0] = %+v, want 2024-01 src=100 test=0", got[0])
	}
	if got[1].Period != "2024-02" || got[1].SourceChurn != 50 || got[1].TestChurn != 40 || got[1].Ratio != 40.0/50.0 {
		t.Errorf("bucket[1] = %+v, want 2024-02 src=50 test=40 ratio=0.8", got[1])
	}
	if got[2].Period != "2024-03" || got[2].TestChurn != 20 || got[2].SourceChurn != 0 || got[2].Ratio != 0 {
		t.Errorf("bucket[2] = %+v, want 2024-03 test=20 src=0 ratio=0(guarded)", got[2])
	}
	if got[0].SourceChurn >= 999 {
		t.Errorf("RoleOther file (README.md) leaked into the trend")
	}

	// Year rollup folds every month into a single "2024" bucket.
	yr := TestRatioOverTime(ds, NewTestConfig(nil), "year")
	if len(yr) != 1 || yr[0].Period != "2024" || yr[0].SourceChurn != 150 || yr[0].TestChurn != 60 {
		t.Fatalf("year rollup = %+v, want one 2024 bucket src=150 test=60", yr)
	}
}

// A file renamed across the test/source boundary (src/foo.go → foo_test.go)
// must attribute pre-rename churn to source and post-rename churn to test,
// not classify its whole history by the final (test) name. Exercised via
// byPath, which the loader populates for renamed lineages.
func TestRenameAcrossTestBoundary(t *testing.T) {
	ds := &Dataset{files: map[string]*fileEntry{
		// Canonical (final) path is a test file, but the lineage was
		// production code for most of its life.
		"foo_test.go": {byPath: map[string]*pathEra{
			"foo.go":      {churn: 100, monthChurn: map[string]int64{"2024-01": 100}},
			"foo_test.go": {churn: 30, monthChurn: map[string]int64{"2024-06": 30}},
		}},
		"bar.go": {additions: 50, monthChurn: map[string]int64{"2024-03": 50}}, // unrenamed → fallback
	}}

	s := ComputeTestSummary(ds, NewTestConfig(nil), 0)
	// Pre-rename 100 stays SOURCE; only the 30 post-rename is test.
	if s.TestChurn != 30 || s.SourceChurn != 150 {
		t.Errorf("churn: test=%d source=%d, want 30/150 (pre-rename 100 must stay source)", s.TestChurn, s.SourceChurn)
	}
	// The renamed lineage counts as both a test file and a source file.
	if s.TestFiles != 1 || s.SourceFiles != 2 {
		t.Errorf("files: test=%d source=%d, want 1/2", s.TestFiles, s.SourceFiles)
	}

	tr := TestRatioOverTime(ds, NewTestConfig(nil), "month")
	got := map[string]TestRatioBucket{}
	for _, b := range tr {
		got[b.Period] = b
	}
	if b := got["2024-01"]; b.SourceChurn != 100 || b.TestChurn != 0 {
		t.Errorf("2024-01 = %+v, want source=100 test=0 (pre-rename month must be source)", b)
	}
	if b := got["2024-06"]; b.TestChurn != 30 || b.SourceChurn != 0 {
		t.Errorf("2024-06 = %+v, want test=30 source=0", b)
	}
}

// --test-glob overrides count their matches as tests unconditionally,
// even when the path is data the built-in rules would call "other".
func TestTestConfigExtraGlobs(t *testing.T) {
	cfg := NewTestConfig([]string{"testdata/*", "*.itest.go", "fixtures/snap/*"})
	cases := []struct {
		path string
		want TestRole
	}{
		{"pkg/testdata/golden.json", RoleTest}, // would be "other" by default
		{"pkg/flow.itest.go", RoleTest},        // custom basename suffix
		{"pkg/flow.go", RoleSource},            // unaffected
		// A user glob wins even over the suspect (vendor/lock) gate.
		{"vendor/fixtures/snap/x.json", RoleTest},
	}
	for _, c := range cases {
		if got := classifyTestRole(c.path, cfg); got != c.want {
			t.Errorf("classifyTestRole(%q) with globs = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestValidateTestGlobs(t *testing.T) {
	if err := ValidateTestGlobs([]string{"testdata/*", "*.go", ""}); err != nil {
		t.Errorf("valid globs rejected: %v", err)
	}
	if err := ValidateTestGlobs([]string{"ok/*", "[unterminated"}); err == nil {
		t.Errorf("malformed glob '[unterminated' was not rejected")
	}
}
