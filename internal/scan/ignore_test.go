package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatcher_Basics(t *testing.T) {
	m := NewMatcher([]string{
		"# comment line",
		"",
		"node_modules",
		"vendor/",
		"archive/*",
		"*.log",
		"!important.log",
		"**/generated",
	})

	cases := []struct {
		name    string
		path    string
		isDir   bool
		want    bool
	}{
		{"basename dir match", "a/b/node_modules", true, true},
		{"basename file match in middle", "a/node_modules/c.go", false, true},
		{"vendor dir-only match", "src/vendor", true, true},
		{"vendor on file does not match", "src/vendor", false, false},
		{"glob in subdir", "archive/2024", true, true},
		{"glob extension", "out/build.log", false, true},
		{"negation re-includes", "out/important.log", false, false},
		{"deep generated", "src/foo/generated", true, true},
		{"unrelated path", "src/main.go", false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := m.Match(c.path, c.isDir)
			if got != c.want {
				t.Errorf("Match(%q, dir=%v) = %v, want %v", c.path, c.isDir, got, c.want)
			}
		})
	}
}

// LoadMatcher is called when the user supplied an explicit
// --ignore-file; a missing file there is almost always a typo. If
// we silently returned an empty matcher, every discovery would
// widen to include node_modules/, vendor/, chromium-clones/, etc.
// the user thought they had excluded — and they'd have no way to
// tell from the console output. Fail loudly instead; the
// default-path lookup in scan.loadMatcher handles the "silent when
// absent" case via its own os.Stat before calling here.
func TestLoadMatcher_MissingFileFails(t *testing.T) {
	_, err := LoadMatcher(filepath.Join(t.TempDir(), "typo.ignore"))
	if err == nil {
		t.Fatal("expected error for missing explicit ignore file; a typo must not silently disable rules")
	}
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "typo.ignore") {
		t.Errorf("error should identify the missing path; got %q", err)
	}
}

func TestLoadMatcher_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitcortex-ignore")
	contents := "# comment\nnode_modules\nvendor/\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadMatcher(path)
	if err != nil {
		t.Fatalf("LoadMatcher: %v", err)
	}
	if !m.Match("foo/node_modules", true) {
		t.Error("node_modules should match")
	}
	if !m.Match("vendor", true) {
		t.Error("vendor/ should match dirs")
	}
	if m.Match("vendor", false) {
		t.Error("vendor/ should not match files")
	}
}
