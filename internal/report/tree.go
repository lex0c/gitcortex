package report

import (
	"fmt"
	"io"
	"sort"
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
}

// BuildRepoTree builds a repo structure tree from the hotspots slice.
// maxDepth limits how many levels are expanded (root counts as 0);
// 0 = no limit. Nodes whose subtree is pruned are marked Truncated so
// renderers can signal "... N more" to the reader.
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
			child := findChild(cur, part, !isLeaf)
			if child == nil {
				child = &TreeNode{
					Name:  part,
					Path:  strings.Join(parts[:i+1], "/"),
					IsDir: !isLeaf,
					Depth: i + 1,
				}
				cur.Children = append(cur.Children, child)
			}
			if isLeaf {
				// Leaf: accumulate so rename-induced duplicates within a
				// single Dataset (same canonical path emitted twice under
				// pathological rename chains) don't silently overwrite.
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
	if maxDepth > 0 {
		pruneDepth(root, maxDepth)
	}
	return root
}

// findChild returns the existing sibling that matches both name AND
// directory-ness. A path history where the same name refers to a file in
// one commit and a directory in another (delete-file then mkdir) would
// otherwise corrupt the tree: the file node would grow dir children, or
// the dir node would get leaf values overwritten. Matching on the pair
// lets both coexist as sibling nodes; rare in practice, silent-wrong if
// unhandled.
func findChild(n *TreeNode, name string, wantDir bool) *TreeNode {
	for _, c := range n.Children {
		if c.Name == name && c.IsDir == wantDir {
			return c
		}
	}
	return nil
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

// pruneDepth drops children past maxDepth levels below root. The node at
// the cut line keeps its aggregate counts but is marked Truncated so the
// renderer can emit "... N more" instead of silently hiding data.
func pruneDepth(n *TreeNode, maxDepth int) {
	if n.Depth >= maxDepth && len(n.Children) > 0 {
		n.Truncated = true
		n.Children = nil
		return
	}
	for _, c := range n.Children {
		pruneDepth(c, maxDepth)
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
			if _, err := fmt.Fprintf(w, "%s%s... (subtree hidden, use --depth to expand)\n", prefix, next); err != nil {
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
