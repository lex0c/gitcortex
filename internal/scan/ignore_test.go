package scan

import (
	"os"
	"path/filepath"
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

func TestLoadMatcher_MissingFileIsOK(t *testing.T) {
	m, err := LoadMatcher(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if m == nil {
		t.Fatal("matcher should not be nil")
	}
	if m.Match("anything", false) {
		t.Error("empty matcher should match nothing")
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
