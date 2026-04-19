package report

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/lex0c/gitcortex/internal/stats"
)

// TreeNode is a single node in the repo-structure tree. Files carry
// Commits + Churn for that file; directories aggregate Churn + Files over
// all descendants but leave Commits = 0, because summing per-file commit
// counts across a directory double-counts any commit that touches
// multiple files — a trap DirStat.Commits fell into before it was
// renamed to FileTouches (see internal/stats/stats.go). The tree is
// derived from paths seen in git history (stats.FileHotspots), so it
// includes files that existed at some point — not just those present at
// HEAD. That matches the rest of the report's historical lens.
type TreeNode struct {
	Name  string
	Path  string
	IsDir bool
	// Commits is populated for file leaves only; dirs leave it zero so
	// JSON consumers don't mistake the per-file sum for a distinct-commit
	// count.
	Commits  int
	Churn    int64
	Files    int
	Children []*TreeNode
	// Depth is the distance from root (root = 0). Pre-computed so HTML
	// template can indent without recursion.
	Depth int
	// Truncated flags a directory whose subtree was cut by the depth
	// limit. CLI/HTML surfaces show an ellipsis marker so the reader
	// knows there's more below.
	Truncated bool
	// HiddenChildren counts children dropped by a per-dir render cap
	// (applied for the HTML surface so wide directories don't blow up
	// the page). CLI does not cap. Zero when no cap was applied.
	HiddenChildren int

	// childIndex is an O(1) lookup for BuildRepoTree. Unexported so it
	// doesn't show up in JSON. Cleared after build to release memory
	// (the Children slice is the durable view; the map is scaffolding).
	childIndex map[childKey]*TreeNode `json:"-"`
}

// childKey disambiguates file/dir with the same name across history
// (delete-file then mkdir). Both coexist as siblings under the same
// parent and the index keeps them addressable.
type childKey struct {
	name  string
	isDir bool
}

// BuildRepoTree builds a repo structure tree from the hotspots slice.
// maxDepth limits how many levels are expanded (root counts as 0);
// 0 = no limit. Nodes whose subtree would extend past maxDepth are
// marked Truncated and their aggregate counts still reflect everything
// underneath, so renderers can signal "... N more" without losing the
// totals.
//
// Complexity: O(F × D) where F is the number of hotspots and D is the
// average path depth. A per-node map index (childIndex) keeps child
// lookup at O(1); without it, wide flat directories degrade the build
// to quadratic. Pruning happens during descent (not as a post-pass), so
// nodes past the cap are never allocated.
func BuildRepoTree(hotspots []stats.FileStat, maxDepth int) *TreeNode {
	root := &TreeNode{Name: ".", Path: "", IsDir: true}

	for _, h := range hotspots {
		if h.Path == "" {
			continue
		}
		parts := strings.Split(h.Path, "/")
		cur := root
		cur.Churn += h.Churn
		cur.Files++

		for i, part := range parts {
			isLeaf := i == len(parts)-1
			currentDepth := i + 1

			// Depth cap: stop creating deeper nodes, but leave the
			// aggregates already applied on ancestors intact. cur here
			// is the node at exactly maxDepth; mark it Truncated so
			// renderers can show "subtree hidden".
			if maxDepth > 0 && currentDepth > maxDepth {
				cur.Truncated = true
				break
			}

			child := cur.getChild(part, !isLeaf)
			if child == nil {
				child = &TreeNode{
					Name:  part,
					Path:  strings.Join(parts[:i+1], "/"),
					IsDir: !isLeaf,
					Depth: currentDepth,
				}
				cur.putChild(child)
			}
			if isLeaf {
				// Defense-in-depth: FileHotspots iterates a map keyed
				// by path, so no duplicates reach this loop in
				// practice. If dupes ever did arrive, ancestor .Files
				// would also over-count — this += is not sufficient on
				// its own, just a cheap safety net.
				child.Commits += h.Commits
				child.Churn += h.Churn
			} else {
				// Dir: aggregate churn + descendant file count only.
				// Commits is intentionally left at zero (see type docs).
				child.Churn += h.Churn
				child.Files++
			}
			cur = child
		}
	}

	sortTree(root)
	clearChildIndex(root)
	return root
}

// getChild / putChild keep child lookup O(1) during BuildRepoTree. The
// map is pure build-time scaffolding; clearChildIndex drops it so the
// tree kept in ReportData is exactly the exported fields.
func (n *TreeNode) getChild(name string, isDir bool) *TreeNode {
	if n.childIndex == nil {
		return nil
	}
	return n.childIndex[childKey{name, isDir}]
}

func (n *TreeNode) putChild(c *TreeNode) {
	if n.childIndex == nil {
		n.childIndex = make(map[childKey]*TreeNode)
	}
	n.childIndex[childKey{c.Name, c.IsDir}] = c
	n.Children = append(n.Children, c)
}

func clearChildIndex(n *TreeNode) {
	n.childIndex = nil
	for _, c := range n.Children {
		clearChildIndex(c)
	}
}

// CapChildrenPerDir keeps the top `limit` children of each directory
// and records how many were dropped in HiddenChildren so the renderer
// can show "… N more hidden". Applied only to the HTML surface — a
// chromium-scale dir at depth 2 can have thousands of leaves, and the
// tree section was meant to tame "too much output" not reintroduce it.
// The CLI intentionally skips this cap because a piped tree is expected
// to be exhaustive within the --tree-depth limit.
//
// Children are already sorted (dirs first, churn desc within kind), so
// the top N favours structure over noise: at a wide dir with dozens of
// subdirs and hundreds of files, the dirs remain visible and the tail
// of thin files collapses into the counter.
func CapChildrenPerDir(n *TreeNode, limit int) {
	if limit <= 0 {
		return
	}
	if len(n.Children) > limit {
		n.HiddenChildren = len(n.Children) - limit
		n.Children = n.Children[:limit]
	}
	for _, c := range n.Children {
		CapChildrenPerDir(c, limit)
	}
}

// sortTree orders children deterministically: directories first (so the
// architectural shape reads top-down), then by churn desc as a proxy for
// importance, then by name asc for ties.
func sortTree(n *TreeNode) {
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		if a.Churn != b.Churn {
			return a.Churn > b.Churn
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}

// RenderTreeText prints the tree in the style of `tree(1)`: unicode
// box-drawing prefixes, directories annotated with file/churn counts,
// files with commits/churn. The output is UTF-8; callers that need
// ASCII-only should wrap with a transform.
func RenderTreeText(w io.Writer, root *TreeNode) error {
	if _, err := fmt.Fprintf(w, "%s\n", root.Name); err != nil {
		return err
	}
	return renderChildren(w, root, "")
}

func renderChildren(w io.Writer, n *TreeNode, prefix string) error {
	for i, c := range n.Children {
		last := i == len(n.Children)-1
		branch := "├── "
		next := "│   "
		if last {
			branch = "└── "
			next = "    "
		}
		if _, err := fmt.Fprintf(w, "%s%s%s\n", prefix, branch, formatNodeLabel(c)); err != nil {
			return err
		}
		if c.Truncated {
			if _, err := fmt.Fprintf(w, "%s%s... (subtree hidden, use --tree-depth to expand)\n", prefix, next); err != nil {
				return err
			}
			continue
		}
		if len(c.Children) > 0 {
			if err := renderChildren(w, c, prefix+next); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatNodeLabel(n *TreeNode) string {
	if n.IsDir {
		return fmt.Sprintf("%s/  (%d files, %s churn)", n.Name, n.Files, humanize(n.Churn))
	}
	return fmt.Sprintf("%s  (%d commits, %s churn)", n.Name, n.Commits, humanize(n.Churn))
}

// RenderTreeCSV emits the tree as a flat CSV, one row per node in DFS
// preorder so the traversal order matches the text renderer. Honors the
// "single clean table per --stat" contract: downstream tools can read
// the same columns whether the user asked for `--stat structure` or
// another stat. Commits is 0 for dir rows (see TreeNode doc — per-file
// commit sums would double-count), so consumers wanting a directory-
// level "activity" signal should use Churn or Files instead.
func RenderTreeCSV(w io.Writer, root *TreeNode) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"path", "type", "depth", "commits", "churn", "files", "truncated"}); err != nil {
		return err
	}
	if err := writeTreeCSVRow(cw, root); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// RenderTreeForFormat dispatches tree rendering to the writer matching
// the CLI's --format. Centralizing the switch here means the earlier
// bug ("--format csv silently wrote a Unicode tree") can't recur: every
// CLI caller goes through this function, and the table-driven test in
// tree_test.go asserts one writer per format. Unknown formats fall
// through to the text renderer for backward compatibility with users
// who pipe into their own tooling and pass through unrelated --format
// values (e.g. "table").
func RenderTreeForFormat(w io.Writer, root *TreeNode, format string) error {
	switch format {
	case "csv":
		return RenderTreeCSV(w, root)
	default:
		return RenderTreeText(w, root)
	}
}

func writeTreeCSVRow(cw *csv.Writer, n *TreeNode) error {
	kind := "file"
	if n.IsDir {
		kind = "dir"
	}
	// The root node's Path is empty by construction; emit "." so
	// consumers don't get a blank cell as the first row.
	path := n.Path
	if path == "" {
		path = n.Name
	}
	if err := cw.Write([]string{
		path,
		kind,
		strconv.Itoa(n.Depth),
		strconv.Itoa(n.Commits),
		strconv.FormatInt(n.Churn, 10),
		strconv.Itoa(n.Files),
		strconv.FormatBool(n.Truncated),
	}); err != nil {
		return err
	}
	for _, c := range n.Children {
		if err := writeTreeCSVRow(cw, c); err != nil {
			return err
		}
	}
	return nil
}
