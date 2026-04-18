package stats

import (
	"testing"
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
