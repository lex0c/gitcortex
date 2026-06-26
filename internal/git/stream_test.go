package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogStreamerUnquotesUTF8Paths is a regression test for the
// core.quotePath=false flag on the git log invocation: without it git
// C-quotes/octal-escapes non-ASCII pathnames ("caf\303\251.txt"), which
// the raw/numstat parsers store verbatim — corrupting path-based stats and
// resolving renamed-unicode-file churn to 0. The path must arrive as clean
// UTF-8 in both the raw entries and the numstat map.
func TestLogStreamerUnquotesUTF8Paths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	// Isolate from ambient gitconfig: a global core.quotePath=false on the
	// dev/CI box would mask a removal of the production flag and defeat this
	// regression guard, so force git's default (quotePath=true).
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@t.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@t.com")
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q") // no -b: branch="" → HEAD, and -b needs git ≥2.28
	if err := os.WriteFile(filepath.Join(dir, "café.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("commit", "-qm", "add café")
	// Rename the unicode file (with a small edit) — this is the most severe
	// failure mode: when the path is C-quoted, the numstat rename key stops
	// matching the raw PathNew and the file's churn resolves to 0.
	runGit("mv", "café.txt", "résumé.txt")
	if err := os.WriteFile(filepath.Join(dir, "résumé.txt"), []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("commit", "-qam", "rename to résumé")

	ls, err := NewLogStreamer(context.Background(), dir, "", "", false, false, false)
	if err != nil {
		t.Fatalf("NewLogStreamer: %v", err)
	}
	defer ls.Close()

	// Newest-first: the first block is the rename commit.
	c, err := ls.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if c == nil {
		t.Fatal("no commit streamed")
	}

	// No path may carry surrounding quotes or escape backslashes.
	for _, r := range c.Raw {
		for _, p := range []string{r.PathOld, r.PathNew} {
			if strings.ContainsAny(p, "\"\\") {
				t.Errorf("path is quoted/escaped: %q", p)
			}
		}
	}
	// The rename must be detected with clean old/new UTF-8 paths...
	sawRename := false
	for _, r := range c.Raw {
		if strings.HasPrefix(r.Status, "R") && r.PathOld == "café.txt" && r.PathNew == "résumé.txt" {
			sawRename = true
		}
	}
	if !sawRename {
		t.Errorf("clean unicode rename café.txt→résumé.txt not found in raw: %+v", c.Raw)
	}
	// ...and its new path must have a resolvable numstat entry (else the
	// file's churn would silently resolve to 0).
	if _, ok := c.Numstats["résumé.txt"]; !ok {
		t.Errorf("numstat key \"résumé.txt\" missing — churn would resolve to 0; keys=%v", c.Numstats)
	}
}

func TestParseRawLine(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		want   RawEntry
		wantOK bool
	}{
		{
			name:   "modify",
			line:   ":100644 100644 aaaa bbbb M\tsrc/main.go",
			want:   RawEntry{Status: "M", OldHash: "aaaa", NewHash: "bbbb", PathOld: "src/main.go", PathNew: "src/main.go"},
			wantOK: true,
		},
		{
			name:   "add",
			line:   ":000000 100644 0000 abcd A\tnew_file.txt",
			want:   RawEntry{Status: "A", OldHash: "0000", NewHash: "abcd", PathOld: "new_file.txt", PathNew: "new_file.txt"},
			wantOK: true,
		},
		{
			name:   "delete",
			line:   ":100644 000000 abcd 0000 D\told_file.txt",
			want:   RawEntry{Status: "D", OldHash: "abcd", NewHash: "0000", PathOld: "old_file.txt", PathNew: ""},
			wantOK: true,
		},
		{
			name:   "rename",
			line:   ":100644 100644 aaaa bbbb R100\told.go\tnew.go",
			want:   RawEntry{Status: "R100", OldHash: "aaaa", NewHash: "bbbb", PathOld: "old.go", PathNew: "new.go"},
			wantOK: true,
		},
		{
			name:   "copy",
			line:   ":100644 100644 aaaa bbbb C075\tsrc.go\tdst.go",
			want:   RawEntry{Status: "C075", OldHash: "aaaa", NewHash: "bbbb", PathOld: "src.go", PathNew: "dst.go"},
			wantOK: true,
		},
		{
			name:   "no colon prefix",
			line:   "100644 100644 aaaa bbbb M\tfile.go",
			wantOK: false,
		},
		{
			name:   "no tab",
			line:   ":100644 100644 aaaa bbbb M",
			wantOK: false,
		},
		{
			name:   "insufficient fields",
			line:   ":100644 100644 aaaa\tfile.go",
			wantOK: false,
		},
		{
			name:   "empty",
			line:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRawLine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("parseRawLine(%q) ok = %v, want %v", tt.line, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got != tt.want {
				t.Errorf("parseRawLine(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestResolveRenamePath(t *testing.T) {
	tests := []struct {
		in      string
		wantOld string
		wantNew string
	}{
		{"file.go", "file.go", "file.go"},
		{"old.txt => new.txt", "old.txt", "new.txt"},
		{"src/{old.go => new.go}", "src/old.go", "src/new.go"},
		{"{old => new}/file.go", "old/file.go", "new/file.go"},
		{"a/{b => c}/d.txt", "a/b/d.txt", "a/c/d.txt"},
		{"dir/{old_name.go => new_name.go}/sub", "dir/old_name.go/sub", "dir/new_name.go/sub"},
	}

	for _, tt := range tests {
		gotOld, gotNew := resolveRenamePath(tt.in)
		if gotOld != tt.wantOld || gotNew != tt.wantNew {
			t.Errorf("resolveRenamePath(%q) = (%q, %q), want (%q, %q)",
				tt.in, gotOld, gotNew, tt.wantOld, tt.wantNew)
		}
	}
}

func TestParseDiffSection(t *testing.T) {
	diff := `
:100644 100644 aaa111 bbb222 M	src/main.go
:100644 000000 ccc333 000000 D	old.go
:000000 100644 000000 ddd444 A	new.go

10	5	src/main.go
0	20	old.go
30	0	new.go
`
	raw, numstats, totals := parseDiffSection(diff)

	if len(raw) != 3 {
		t.Fatalf("raw entries = %d, want 3", len(raw))
	}
	if raw[0].Status != "M" || raw[0].PathNew != "src/main.go" {
		t.Errorf("raw[0] = %+v", raw[0])
	}
	if raw[1].Status != "D" || raw[1].PathNew != "" {
		t.Errorf("raw[1] = %+v", raw[1])
	}
	if raw[2].Status != "A" || raw[2].PathNew != "new.go" {
		t.Errorf("raw[2] = %+v", raw[2])
	}

	if ns, ok := numstats["src/main.go"]; !ok || ns.Additions != 10 || ns.Deletions != 5 {
		t.Errorf("numstats[src/main.go] = %+v", numstats["src/main.go"])
	}

	if totals.Additions != 40 || totals.Deletions != 25 {
		t.Errorf("totals = %+v, want {40, 25}", totals)
	}
}

func TestParseDiffSectionRename(t *testing.T) {
	diff := `:100644 100644 aaa bbb R100	old.go	new.go

5	3	old.go => new.go
`
	raw, numstats, _ := parseDiffSection(diff)

	if len(raw) != 1 {
		t.Fatalf("raw entries = %d, want 1", len(raw))
	}
	if raw[0].PathOld != "old.go" || raw[0].PathNew != "new.go" {
		t.Errorf("raw[0] = %+v", raw[0])
	}

	// Numstat should be indexed by both old and new path
	if _, ok := numstats["new.go"]; !ok {
		t.Error("numstats missing new.go key")
	}
	if _, ok := numstats["old.go"]; !ok {
		t.Error("numstats missing old.go key")
	}
}

func TestParseDiffSectionEmpty(t *testing.T) {
	raw, numstats, totals := parseDiffSection("")
	if len(raw) != 0 {
		t.Errorf("raw = %d, want 0", len(raw))
	}
	if len(numstats) != 0 {
		t.Errorf("numstats = %d, want 0", len(numstats))
	}
	if totals.Additions != 0 || totals.Deletions != 0 {
		t.Errorf("totals = %+v", totals)
	}
}

func TestParseCommitBlock(t *testing.T) {
	// Simulate a commit block: metadata\x1e\x02\ndiff lines
	block := []byte(
		"abc123\x1ftree456\x1fparent1 parent2\x1fAlice\x1falice@test.com\x1f2024-01-15T10:30:00Z\x1fBob\x1fbob@test.com\x1f2024-01-15T10:31:00Z\x1ffix bug\x1e\x02\n" +
			":100644 100644 aaa bbb M\tmain.go\n" +
			"\n" +
			"10\t5\tmain.go\n",
	)

	commit, err := parseCommitBlock(block)
	if err != nil {
		t.Fatalf("parseCommitBlock: %v", err)
	}

	if commit.Meta.SHA != "abc123" {
		t.Errorf("SHA = %q", commit.Meta.SHA)
	}
	if len(commit.Meta.Parents) != 2 {
		t.Errorf("Parents = %v", commit.Meta.Parents)
	}
	if commit.Meta.AuthorName != "Alice" {
		t.Errorf("AuthorName = %q", commit.Meta.AuthorName)
	}
	if commit.Meta.Message != "fix bug" {
		t.Errorf("Message = %q", commit.Meta.Message)
	}
	if len(commit.Raw) != 1 {
		t.Fatalf("Raw entries = %d", len(commit.Raw))
	}
	if commit.Totals.Additions != 10 || commit.Totals.Deletions != 5 {
		t.Errorf("Totals = %+v", commit.Totals)
	}
}

func TestParseCommitBlockNoMessage(t *testing.T) {
	block := []byte(
		"sha1\x1ftree\x1f\x1fDev\x1fdev@x.com\x1f2024-01-01T00:00:00Z\x1fDev\x1fdev@x.com\x1f2024-01-01T00:00:00Z\x1f\x1e\x02\n",
	)

	commit, err := parseCommitBlock(block)
	if err != nil {
		t.Fatalf("parseCommitBlock: %v", err)
	}
	if commit.Meta.Message != "" {
		t.Errorf("Message = %q, want empty", commit.Meta.Message)
	}
	if len(commit.Meta.Parents) != 0 {
		t.Errorf("Parents = %v, want empty", commit.Meta.Parents)
	}
}
