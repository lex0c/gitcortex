package extract

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lex0c/gitcortex/internal/git"
)

// emitCommit drives the blob-size gating: when the resolver is disabled the
// caller passes a nil sizeMap, and old_size/new_size must be absent from the
// JSONL (omitempty), not emitted as ":0". When sizes are supplied they must
// appear. A regression here would either resurrect the per-line dead bytes or
// drop sizes that --blob-sizes promised.
func TestEmitCommitBlobSizeGating(t *testing.T) {
	commit := &git.StreamCommit{
		Meta: git.CommitMeta{
			SHA:         "abc123",
			AuthorName:  "Alice",
			AuthorEmail: "alice@example.com",
			AuthorDate:  "2024-01-01T00:00:00Z",
		},
		Raw: []git.RawEntry{{
			Status:  "M",
			OldHash: "aaa",
			NewHash: "bbb",
			PathOld: "src/main.go",
			PathNew: "src/main.go",
		}},
		Numstats: map[string]git.NumstatEntry{
			"src/main.go": {Additions: 10, Deletions: 3},
		},
	}

	render := func(sizeMap map[string]int64) string {
		var buf bytes.Buffer
		w := bufio.NewWriter(&buf)
		if err := emitCommit(w, commit, sizeMap, map[string]struct{}{}, nil); err != nil {
			t.Fatalf("emitCommit: %v", err)
		}
		if err := w.Flush(); err != nil {
			t.Fatalf("flush: %v", err)
		}
		return buf.String()
	}

	// Disabled (nil map): no size fields at all.
	off := render(nil)
	if strings.Contains(off, "old_size") || strings.Contains(off, "new_size") {
		t.Errorf("disabled path emitted size fields:\n%s", off)
	}
	// Sanity: the file record (and its churn) is still present.
	if !strings.Contains(off, `"path_current":"src/main.go"`) || !strings.Contains(off, `"additions":10`) {
		t.Errorf("disabled path dropped non-size file data:\n%s", off)
	}

	// Enabled: sizes flow through.
	on := render(map[string]int64{"aaa": 1024, "bbb": 2048})
	if !strings.Contains(on, `"old_size":1024`) || !strings.Contains(on, `"new_size":2048`) {
		t.Errorf("enabled path missing sizes:\n%s", on)
	}

	// Resolved 0 (empty blob) MUST be emitted, not collapsed to "absent".
	// This is the contract --blob-sizes promises: a 0-byte blob is a real,
	// distinguishable size. A scalar omitempty int would drop it here.
	zero := render(map[string]int64{"aaa": 0, "bbb": 2048})
	if !strings.Contains(zero, `"old_size":0`) {
		t.Errorf("resolved 0-byte size was dropped (omitempty regression):\n%s", zero)
	}
	if !strings.Contains(zero, `"new_size":2048`) {
		t.Errorf("enabled path missing non-zero size:\n%s", zero)
	}

	// A hash absent from the map (null hash / unresolved) stays omitted even
	// when blob sizes are on — "no blob" must not masquerade as a 0-byte one.
	absent := render(map[string]int64{"bbb": 2048})
	if strings.Contains(absent, "old_size") {
		t.Errorf("unresolved hash emitted a size; should be absent:\n%s", absent)
	}
	if !strings.Contains(absent, `"new_size":2048`) {
		t.Errorf("enabled path missing the resolved size:\n%s", absent)
	}
}

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
		// Portability regression: pattern with a slash glob.
		// filepath.Match on Windows uses "\\" as separator and would
		// silently fail to match "src/generated/types.go" against
		// "src/generated/*.go". path.Match keeps forward-slash
		// semantics and matches on every platform.
		{"src/generated/types.go", []string{"src/generated/*.go"}, true},
		{"src/hand/types.go", []string{"src/generated/*.go"}, false},
	}

	for _, tt := range tests {
		got := ShouldIgnore(tt.path, tt.patterns)
		if got != tt.want {
			t.Errorf("ShouldIgnore(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
		}
	}
}
