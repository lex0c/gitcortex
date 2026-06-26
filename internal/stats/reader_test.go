package stats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateMessage(t *testing.T) {
	short := "fix: one-line subject"
	if got := truncateMessage(short); got != short {
		t.Errorf("short message altered: %q", got)
	}
	long := strings.Repeat("x", maxStoredMessageBytes+50)
	if got := truncateMessage(long); len(got) != maxStoredMessageBytes {
		t.Errorf("len = %d, want %d", len(got), maxStoredMessageBytes)
	}
	exact := strings.Repeat("y", maxStoredMessageBytes)
	if got := truncateMessage(exact); got != exact {
		t.Errorf("at-bound message altered (len %d)", len(got))
	}
}

// A commit whose author date does not parse must NOT be counted inside a
// bounded --from/--to (or --since) window — it can't be placed in time, so
// it should be excluded rather than slip past the zero-time guard into
// every window. Regression for reader.go's window filter.
func TestWindowExcludesUndatedCommits(t *testing.T) {
	dated := `{"type":"commit","sha":"aaa","author_name":"A","author_email":"a@x","author_date":"2026-03-15T10:00:00Z","additions":10,"deletions":2,"files_changed":1}`
	undated := `{"type":"commit","sha":"bbb","author_name":"B","author_email":"b@x","author_date":"","additions":99,"deletions":7,"files_changed":1}`

	dir := t.TempDir()
	path := filepath.Join(dir, "git_data.jsonl")
	if err := os.WriteFile(path, []byte(dated+"\n"+undated+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No window: both commits load.
	all, err := LoadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if all.CommitCount != 2 {
		t.Fatalf("unfiltered CommitCount = %d, want 2", all.CommitCount)
	}

	// Bounded window containing the dated commit: the undated one is dropped.
	win, err := LoadJSONL(path, LoadOptions{From: "2026-03-01", To: "2026-03-31"})
	if err != nil {
		t.Fatal(err)
	}
	if win.CommitCount != 1 {
		t.Errorf("windowed CommitCount = %d, want 1 (undated commit excluded)", win.CommitCount)
	}
	if win.TotalAdditions != 10 {
		t.Errorf("windowed TotalAdditions = %d, want 10 (undated 99 must not leak in)", win.TotalAdditions)
	}
}
