package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lex0c/gitcortex/internal/stats"
)

func TestBuildRepoTreeAggregatesAndSorts(t *testing.T) {
	hotspots := []stats.FileStat{
		{Path: "cmd/gitcortex/main.go", Commits: 5, Churn: 100},
		{Path: "internal/stats/stats.go", Commits: 20, Churn: 900},
		{Path: "internal/stats/reader.go", Commits: 10, Churn: 400},
		{Path: "internal/report/report.go", Commits: 8, Churn: 300},
		{Path: "README.md", Commits: 2, Churn: 30},
	}

	root := BuildRepoTree(hotspots, 0)

	if root.Files != 5 {
		t.Fatalf("root.Files = %d, want 5", root.Files)
	}
	if root.Churn != 1730 {
		t.Fatalf("root.Churn = %d, want 1730", root.Churn)
	}

	// Directories first, sorted by churn desc. internal (1600) > cmd (100) > README (file).
	if len(root.Children) < 3 {
		t.Fatalf("root.Children = %d, want >= 3", len(root.Children))
	}
	if !root.Children[0].IsDir || root.Children[0].Name != "internal" {
		t.Errorf("first child = %s (dir=%v), want internal/", root.Children[0].Name, root.Children[0].IsDir)
	}
	if root.Children[0].Files != 3 {
		t.Errorf("internal/.Files = %d, want 3", root.Children[0].Files)
	}
	if root.Children[0].Churn != 1600 {
		t.Errorf("internal/.Churn = %d, want 1600", root.Children[0].Churn)
	}

	// README.md is a leaf at root level, should come after all dirs.
	last := root.Children[len(root.Children)-1]
	if last.IsDir || last.Name != "README.md" {
		t.Errorf("last root child = %s (dir=%v), want README.md leaf", last.Name, last.IsDir)
	}

	// Within internal/, stats/ (churn 1300) should come before report/ (churn 300).
	internal := root.Children[0]
	if internal.Children[0].Name != "stats" {
		t.Errorf("first internal child = %s, want stats (higher churn)", internal.Children[0].Name)
	}
}

func TestBuildRepoTreePrunesAndFlagsTruncation(t *testing.T) {
	hotspots := []stats.FileStat{
		{Path: "a/b/c/deep.go", Commits: 1, Churn: 10},
		{Path: "a/b/other.go", Commits: 1, Churn: 10},
	}
	root := BuildRepoTree(hotspots, 2)

	// Root=0, a=1, b=2. b should be truncated (no children), but counts kept.
	a := root.Children[0]
	if a.Name != "a" {
		t.Fatalf("want a, got %s", a.Name)
	}
	b := a.Children[0]
	if b.Name != "b" {
		t.Fatalf("want b, got %s", b.Name)
	}
	if !b.Truncated {
		t.Errorf("b should be truncated at depth 2, got Truncated=false")
	}
	if len(b.Children) != 0 {
		t.Errorf("b.Children should be empty after prune, got %d", len(b.Children))
	}
	if b.Files != 2 {
		t.Errorf("b.Files = %d, want 2 (aggregation preserved)", b.Files)
	}
}

func TestRenderTreeCSVEmitsHeaderAndPreorderRows(t *testing.T) {
	hotspots := []stats.FileStat{
		{Path: "cmd/main.go", Commits: 7, Churn: 42},
		{Path: "README.md", Commits: 3, Churn: 5},
	}
	root := BuildRepoTree(hotspots, 0)

	var buf bytes.Buffer
	if err := RenderTreeCSV(&buf, root); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")

	// Header + root + cmd + main.go + README.md = 5 rows.
	if len(lines) != 5 {
		t.Fatalf("got %d rows, want 5:\n%s", len(lines), buf.String())
	}
	wantHeader := "path,type,depth,commits,churn,files,truncated"
	if lines[0] != wantHeader {
		t.Errorf("header = %q, want %q", lines[0], wantHeader)
	}
	// Root: path resolved to ".", dir, aggregate.
	if !strings.HasPrefix(lines[1], ".,dir,0,0,47,2,false") {
		t.Errorf("root row = %q, want prefix .,dir,0,0,47,2,false", lines[1])
	}
	// Dir row for cmd/: commits should be 0 (not aggregated from children).
	foundCmdDir := false
	for _, ln := range lines[2:] {
		if strings.HasPrefix(ln, "cmd,dir,") {
			foundCmdDir = true
			if !strings.Contains(ln, ",0,42,1,false") {
				t.Errorf("cmd dir row = %q, want commits=0 churn=42 files=1", ln)
			}
		}
	}
	if !foundCmdDir {
		t.Errorf("missing cmd dir row:\n%s", buf.String())
	}
	// File row for cmd/main.go: full path, commits preserved.
	foundLeaf := false
	for _, ln := range lines[2:] {
		if strings.HasPrefix(ln, "cmd/main.go,file,") {
			foundLeaf = true
			if !strings.Contains(ln, ",7,42,0,false") {
				t.Errorf("main.go row = %q, want commits=7 churn=42 files=0", ln)
			}
		}
	}
	if !foundLeaf {
		t.Errorf("missing cmd/main.go row:\n%s", buf.String())
	}
}

func TestRenderTreeTextProducesBoxPrefixes(t *testing.T) {
	hotspots := []stats.FileStat{
		{Path: "cmd/main.go", Commits: 1, Churn: 10},
		{Path: "README.md", Commits: 1, Churn: 5},
	}
	root := BuildRepoTree(hotspots, 0)

	var buf bytes.Buffer
	if err := RenderTreeText(&buf, root); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Root name, dir branch, nested file, and final sibling file.
	for _, want := range []string{".\n", "├── cmd/", "│   └── main.go", "└── README.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestBuildRepoTreeEmpty(t *testing.T) {
	root := BuildRepoTree(nil, 0)
	if root == nil {
		t.Fatal("empty tree should still return a root node")
	}
	if len(root.Children) != 0 {
		t.Errorf("empty input: root.Children = %d, want 0", len(root.Children))
	}
}

// Regression: dir nodes must NOT carry aggregated Commits. Summing per-
// file commit counts double-counts any commit that touches multiple
// files in the directory (a single commit touching all 3 files here
// would be reported as Commits=30 under the broken aggregation).
func TestBuildRepoTreeDirCommitsZero(t *testing.T) {
	hotspots := []stats.FileStat{
		{Path: "foo/a.go", Commits: 10, Churn: 100},
		{Path: "foo/b.go", Commits: 10, Churn: 100},
		{Path: "foo/c.go", Commits: 10, Churn: 100},
	}
	root := BuildRepoTree(hotspots, 0)
	foo := root.Children[0]
	if !foo.IsDir || foo.Name != "foo" {
		t.Fatalf("expected foo/ dir, got %s (dir=%v)", foo.Name, foo.IsDir)
	}
	if foo.Commits != 0 {
		t.Errorf("dir foo/.Commits = %d, want 0 (aggregating per-file commits double-counts)", foo.Commits)
	}
	if foo.Files != 3 {
		t.Errorf("foo/.Files = %d, want 3", foo.Files)
	}
	if foo.Churn != 300 {
		t.Errorf("foo/.Churn = %d, want 300 (churn aggregation still valid)", foo.Churn)
	}
	// Root also left at zero on Commits.
	if root.Commits != 0 {
		t.Errorf("root.Commits = %d, want 0", root.Commits)
	}
}

// Regression: the --format csv path once silently wrote a Unicode tree.
// Assert RenderTreeForFormat routes each format to the correct writer
// by peeking at the first bytes of output — CSV starts with the header
// row, text starts with the root label. Cheap and stable.
func TestRenderTreeForFormatDispatches(t *testing.T) {
	hotspots := []stats.FileStat{{Path: "cmd/main.go", Commits: 1, Churn: 10}}
	root := BuildRepoTree(hotspots, 0)

	cases := []struct {
		format   string
		wantHead string
	}{
		{"csv", "path,type,depth,commits,churn,files,truncated\n"},
		{"table", ".\n"},     // text renderer starts with root name
		{"", ".\n"},          // empty format falls through to default (text)
		{"garbage", ".\n"},   // unknown format falls through to default (text)
	}
	for _, c := range cases {
		t.Run(c.format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := RenderTreeForFormat(&buf, root, c.format); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(buf.String(), c.wantHead) {
				t.Errorf("format=%q: output did not start with %q; got:\n%s",
					c.format, c.wantHead, buf.String())
			}
		})
	}
}

// Truncation markers: the text renderer must surface the "subtree
// hidden" hint with the correct flag name, and the CSV row for a
// truncated dir must carry truncated=true. Both are user-visible and
// neither was previously asserted.
func TestTruncationMarkersInRenderers(t *testing.T) {
	hotspots := []stats.FileStat{{Path: "a/b/c.go", Commits: 1, Churn: 10}}
	root := BuildRepoTree(hotspots, 2)

	var txt bytes.Buffer
	if err := RenderTreeText(&txt, root); err != nil {
		t.Fatal(err)
	}
	// Must reference the actual flag, not the --depth typo.
	if !strings.Contains(txt.String(), "subtree hidden, use --tree-depth to expand") {
		t.Errorf("text render missing truncation hint with correct flag:\n%s", txt.String())
	}

	var cs bytes.Buffer
	if err := RenderTreeCSV(&cs, root); err != nil {
		t.Fatal(err)
	}
	// The dir row at depth 2 (b/) is the truncated one; it must set
	// truncated=true in the last column.
	found := false
	for _, line := range strings.Split(cs.String(), "\n") {
		if strings.HasPrefix(line, "a/b,dir,") && strings.HasSuffix(line, ",true") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CSV missing truncated=true on a/b dir row:\n%s", cs.String())
	}
}

// HTML cap: CapChildrenPerDir should retain the top-N and set
// HiddenChildren on the overflow, preserving churn-desc order from the
// prior sort. CLI callers that never invoke it must not see counts
// change.
func TestCapChildrenPerDir(t *testing.T) {
	// 5 siblings, cap at 2.
	var hotspots []stats.FileStat
	for i, name := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		hotspots = append(hotspots, stats.FileStat{
			Path:    "dir/" + name,
			Commits: 1,
			Churn:   int64(100 - i*10), // a=100, b=90, c=80, d=70, e=60
		})
	}
	root := BuildRepoTree(hotspots, 0)
	dir := root.Children[0]
	if dir.Name != "dir" || len(dir.Children) != 5 {
		t.Fatalf("expected dir with 5 children, got %s with %d", dir.Name, len(dir.Children))
	}

	CapChildrenPerDir(root, 2)
	if dir.HiddenChildren != 3 {
		t.Errorf("HiddenChildren = %d, want 3", dir.HiddenChildren)
	}
	if len(dir.Children) != 2 {
		t.Fatalf("capped children = %d, want 2", len(dir.Children))
	}
	// Top two must be the highest-churn survivors (a.go, b.go) in that
	// order — the cap is a prefix trim on the already-sorted slice.
	if dir.Children[0].Name != "a.go" || dir.Children[1].Name != "b.go" {
		t.Errorf("top-2 after cap = [%s, %s], want [a.go, b.go]",
			dir.Children[0].Name, dir.Children[1].Name)
	}
}

// Regression: file and directory can share a name across history
// (path deleted, then recreated as a directory). Each must get its own
// node rather than the second path corrupting the first.
func TestBuildRepoTreeFileDirNameCollision(t *testing.T) {
	hotspots := []stats.FileStat{
		{Path: "foo", Commits: 3, Churn: 30},          // file at root called "foo"
		{Path: "foo/bar.go", Commits: 5, Churn: 50},   // later a dir with the same name
	}
	root := BuildRepoTree(hotspots, 0)

	var fileNode, dirNode *TreeNode
	for _, c := range root.Children {
		if c.Name != "foo" {
			continue
		}
		if c.IsDir {
			dirNode = c
		} else {
			fileNode = c
		}
	}
	if fileNode == nil {
		t.Fatal("expected file node named foo at root, got none")
	}
	if dirNode == nil {
		t.Fatal("expected dir node named foo at root, got none")
	}
	if fileNode.Commits != 3 || fileNode.Churn != 30 {
		t.Errorf("file foo: commits=%d churn=%d, want 3/30", fileNode.Commits, fileNode.Churn)
	}
	if dirNode.Files != 1 || dirNode.Churn != 50 {
		t.Errorf("dir foo/: files=%d churn=%d, want 1/50", dirNode.Files, dirNode.Churn)
	}
	// The dir should hold bar.go, not the file node.
	if len(dirNode.Children) != 1 || dirNode.Children[0].Name != "bar.go" {
		t.Errorf("dir foo/ children = %+v, want [bar.go]", dirNode.Children)
	}
	if len(fileNode.Children) != 0 {
		t.Errorf("file foo has children; node was corrupted into a dir: %+v", fileNode.Children)
	}
}
