package extract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStateEmpty(t *testing.T) {
	s, err := LoadState("/nonexistent/path", -1, "")
	if err != nil {
		t.Fatalf("LoadState nonexistent: %v", err)
	}
	if s.CommitOffset != 0 || s.LastProcessedSHA != "" {
		t.Errorf("empty state = %+v", s)
	}
}

func TestLoadStateFromFlags(t *testing.T) {
	s, err := LoadState("/nonexistent", 42, "")
	if err != nil {
		t.Fatalf("LoadState offset: %v", err)
	}
	if s.CommitOffset != 42 {
		t.Errorf("offset = %d, want 42", s.CommitOffset)
	}

	sha := "a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1"
	s, err = LoadState("/nonexistent", -1, sha)
	if err != nil {
		t.Fatalf("LoadState sha: %v", err)
	}
	if s.LastProcessedSHA != sha {
		t.Errorf("sha = %q", s.LastProcessedSHA)
	}
}

func TestLoadStateConflictingFlags(t *testing.T) {
	_, err := LoadState("/nonexistent", 10, "a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1")
	if err == nil {
		t.Error("expected error for conflicting flags")
	}
}

func TestLoadStateInvalidSHA(t *testing.T) {
	_, err := LoadState("/nonexistent", -1, "not-a-sha")
	if err == nil {
		t.Error("expected error for invalid SHA")
	}
}

func TestLoadStateJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	os.WriteFile(path, []byte(`{"last_processed_sha":"a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1","commit_offset":500}`), 0o644)

	s, err := LoadState(path, -1, "")
	if err != nil {
		t.Fatalf("LoadState json: %v", err)
	}
	if s.CommitOffset != 500 {
		t.Errorf("offset = %d", s.CommitOffset)
	}
	if s.LastProcessedSHA != "a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1" {
		t.Errorf("sha = %q", s.LastProcessedSHA)
	}
}

func TestLoadStateLegacyInteger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	os.WriteFile(path, []byte("250"), 0o644)

	s, err := LoadState(path, -1, "")
	if err != nil {
		t.Fatalf("LoadState legacy: %v", err)
	}
	if s.CommitOffset != 250 {
		t.Errorf("offset = %d, want 250", s.CommitOffset)
	}
}

func TestLoadStateNegativeOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	os.WriteFile(path, []byte("-5"), 0o644)

	_, err := LoadState(path, -1, "")
	if err == nil {
		t.Error("expected error for negative offset")
	}
}

func TestLoadStateGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	os.WriteFile(path, []byte("not-json-not-number"), 0o644)

	_, err := LoadState(path, -1, "")
	if err == nil {
		t.Error("expected error for garbage state file")
	}
}

func TestShouldIgnore(t *testing.T) {
	tests := []struct {
		path     string
		patterns []string
		want     bool
	}{
		{"package-lock.json", []string{"package-lock.json"}, true},
		{"src/main.go", []string{"package-lock.json"}, false},
		{"dist/app.min.js", []string{"*.min.js"}, true},
		{"src/app.js", []string{"*.min.js"}, false},
		{"vendor/lib/foo.go", []string{"vendor/*"}, true},   // directory prefix match
		{"vendor/foo.go", []string{"vendor/*"}, true},       // direct child
		{"src/vendor/foo.go", []string{"vendor/*"}, false},  // not a prefix
		{"dist/js/app.js", []string{"dist/"}, true},         // trailing slash
		{"dist/deep/nested/f.js", []string{"dist/*"}, true}, // deep nested
		{"go.sum", []string{"go.sum", "go.mod"}, true},
		{"go.mod", []string{"go.sum", "go.mod"}, true},
		{"readme.md", []string{"*.md"}, true},
		{"docs/guide.md", []string{"*.md"}, true},  // basename match
		{"src/main.go", nil, false},
		{"src/main.go", []string{}, false},
		{"", []string{"*.go"}, false},
	}

	for _, tt := range tests {
		got := ShouldIgnore(tt.path, tt.patterns)
		if got != tt.want {
			t.Errorf("ShouldIgnore(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
		}
	}
}
