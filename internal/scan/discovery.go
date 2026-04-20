package scan

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Repo describes one git repository discovered during scan.
type Repo struct {
	// AbsPath is the absolute path to the working tree root.
	AbsPath string
	// RelPath is AbsPath relative to the scan root it was found under.
	RelPath string
	// Slug is a filesystem-safe identifier used to derive the output
	// JSONL file name and the path prefix that stats will use as the
	// repo label. Must be unique across the scan (collisions are resolved
	// by appending a short hash of AbsPath).
	Slug string
}

// Discover walks the given roots and returns every git repository it finds.
// Directories matched by the ignore matcher are pruned. Each repo's Slug is
// unique across the full result so downstream JSONL files don't collide,
// and the prefix LoadMultiJSONL derives (basename-minus-ext) still groups
// correctly.
func Discover(roots []string, matcher *Matcher, maxDepth int) ([]Repo, error) {
	if matcher == nil {
		matcher = NewMatcher(nil)
	}
	var repos []Repo
	seen := make(map[string]bool)

	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", root, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", abs, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", abs)
		}

		err = filepath.WalkDir(abs, func(path string, d os.DirEntry, werr error) error {
			if werr != nil {
				// Permission errors on one subtree shouldn't abort the
				// whole scan. Log and keep walking.
				log.Printf("scan: skip %s: %v", path, werr)
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}

			rel, _ := filepath.Rel(abs, path)
			rel = filepath.ToSlash(rel)

			if maxDepth > 0 && rel != "." {
				depth := strings.Count(rel, "/") + 1
				if depth > maxDepth {
					return filepath.SkipDir
				}
			}

			if rel != "." && matcher.Match(rel, true) {
				return filepath.SkipDir
			}

			// Two repo shapes to detect:
			//   1. Working tree:   path/.git is a dir (normal) or file
			//      (worktree/submodule pointer).
			//   2. Bare repo:      path itself contains HEAD + objects/
			//      + refs/ — common for clones used as fixtures or
			//      mirrors (`git clone --bare`, GitHub-style server dirs
			//      named foo.git).
			// Lstat (not Stat) on the .git entry so a symlink named .git
			// pointing somewhere arbitrary doesn't get treated as a real
			// repo. The bare check uses os.Stat by way of isBareRepo,
			// which inspects three required entries — much harder to
			// trick than a single dirent check.
			gitEntry := filepath.Join(path, ".git")
			isWorkingTree := false
			if info, statErr := os.Lstat(gitEntry); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
				isWorkingTree = true
			}
			if isWorkingTree || isBareRepo(path) {
				if seen[path] {
					return filepath.SkipDir
				}
				seen[path] = true
				repos = append(repos, Repo{
					AbsPath: path,
					RelPath: rel,
				})
				// Don't descend into a repo — nested repos (submodules,
				// vendored repos) get picked up separately only if they
				// live outside this repo's worktree, which is rare. If
				// users need submodule coverage they can list the parent
				// and the submodule paths as separate roots.
				return filepath.SkipDir
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	assignSlugs(repos)
	// Sort by slug so output ordering is deterministic across runs
	// (WalkDir ordering is, but concatenating multiple roots could shuffle).
	sort.Slice(repos, func(i, j int) bool { return repos[i].Slug < repos[j].Slug })
	return repos, nil
}

// assignSlugs derives a unique slug per repo from its basename, falling
// back to `<basename>-<shortHash(absPath)>` when two repos share a name.
// The slug is also the JSONL filename stem and the persistence key
// (`<slug>.state`), so uniqueness AND determinism across runs both matter:
// if a re-run swaps which sibling gets the bare name, the per-repo
// state file is orphaned and the affected repos are re-extracted from
// scratch (or worse, two repos collide onto one state).
//
// Two-pass: count basenames first, then suffix with a hash whenever the
// base appears more than once anywhere in the result. This makes the
// slug a pure function of (absPath, set of basenames seen), independent
// of WalkDir traversal order.
func assignSlugs(repos []Repo) {
	counts := make(map[string]int)
	bases := make([]string, len(repos))
	for i := range repos {
		base := sanitizeSlug(filepath.Base(repos[i].AbsPath))
		if base == "" {
			base = "repo"
		}
		bases[i] = base
		counts[base]++
	}
	for i := range repos {
		base := bases[i]
		slug := base
		if counts[base] > 1 {
			h := sha1.Sum([]byte(repos[i].AbsPath))
			slug = fmt.Sprintf("%s-%s", base, hex.EncodeToString(h[:])[:6])
		}
		repos[i].Slug = slug
	}
}

// isBareRepo returns true when path is a bare git repository — i.e. the
// directory itself holds HEAD, objects/, and refs/ rather than wrapping
// them in a .git subdirectory. All three entries are required because
// a stray HEAD file or empty refs/ dir alone is not enough to be a real
// repo and we don't want false positives polluting the manifest.
func isBareRepo(path string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			return false
		}
	}
	return true
}

// sanitizeSlug strips characters that would break the LoadMultiJSONL prefix
// contract (`<slug>:`) or filesystem paths. Keeps alphanumerics, dash,
// underscore, and dot.
func sanitizeSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
