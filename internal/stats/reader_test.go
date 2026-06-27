package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lex0c/gitcortex/internal/model"
)

// peekType must agree with a full unmarshal on the discriminator for every
// line our own marshaller emits (the fast path), and must signal ok=false
// for anything it can't read cheaply so the caller falls back to a real
// parse. A disagreement here would silently misroute records during load.
func TestPeekType(t *testing.T) {
	// Marshal a real record of each type so the fixtures match the exact
	// bytes the extractor writes (Type is the first field, so "type" leads).
	marshal := func(v interface{}) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	cases := []struct {
		name string
		line []byte
		want string
	}{
		{"commit", marshal(model.CommitInfo{Type: model.CommitType, SHA: "abc"}), model.CommitType},
		{"commit_file", marshal(model.CommitFileInfo{Type: model.CommitFileType, Commit: "abc"}), model.CommitFileType},
		{"commit_parent", marshal(model.CommitParentInfo{Type: model.CommitParentType, SHA: "abc"}), model.CommitParentType},
		{"dev", marshal(model.DevInfo{Type: model.DevType, Email: "a@b.c"}), model.DevType},
	}
	for _, c := range cases {
		got, ok := peekType(c.line)
		if !ok {
			t.Errorf("%s: fast path missed (would fall back, defeating the optimization): %s", c.name, c.line)
			continue
		}
		if got != c.want {
			t.Errorf("%s: peekType = %q, want %q", c.name, got, c.want)
		}
		// Cross-check against the authoritative full parse.
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(c.line, &probe); err != nil {
			t.Fatalf("%s: control unmarshal: %v", c.name, err)
		}
		if got != probe.Type {
			t.Errorf("%s: peekType %q disagrees with unmarshal %q", c.name, got, probe.Type)
		}
	}
}

// Lines that don't fit the fast shape must return ok=false (never a wrong
// type) so the caller's json.Unmarshal fallback handles them.
func TestPeekTypeFallback(t *testing.T) {
	fallbacks := []string{
		`{"sha":"abc","type":"commit"}`, // type not first → fast path declines
		`{ "type":"commit"}`,            // leading space before key
		`{"type": "commit"}`,            // space after colon
		`not json at all`,
		``,
		`{"other":"x"}`, // no type key at all
	}
	for _, line := range fallbacks {
		if _, ok := peekType([]byte(line)); ok {
			t.Errorf("expected fast-path decline (ok=false) for %q", line)
		}
	}
}

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
