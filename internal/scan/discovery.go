package scan

import (
	"context"
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
//
// A cancelled ctx aborts the walk promptly — important because on large
// roots (home directory scans, monorepos of repos) discovery is often
// the longest phase of scan and Ctrl+C needs to land in real time, not
// "after the walk naturally finishes". Each filepath.WalkDir callback
// checks ctx.Done() before doing any filesystem work, so the abort
// window is bounded by the cost of one stat call per dir entry.
func Discover(ctx context.Context, roots []string, matcher *Matcher, maxDepth int) ([]Repo, error) {
	if matcher == nil {
		matcher = NewMatcher(nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var repos []Repo
	seen := make(map[string]bool)

	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", root, err)
		}
		// Canonicalize via EvalSymlinks. os.Stat above followed the
		// symlink for the is-directory check, but filepath.WalkDir
		// treats a symlink ROOT specially: it visits only the link
		// itself (reported with ModeSymlink) and does NOT descend
		// into the target. For a user whose ~/work is a symlink to
		// /mnt/data/work — a common setup — the walk would yield
		// zero callbacks past the root and scan would conclude "no
		// git repositories found under ..." despite the target being
		// full of repos. EvalSymlinks dereferences the root once so
		// WalkDir starts with a real directory path; links ENCOUNTERED
		// during the walk are still left as-is (default WalkDir
		// behavior), which is what we want — we don't chase every
		// symlink we come across, only the ones the user explicitly
		// named as a root.
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("resolve symlinks for %s: %w", abs, err)
		}
		abs = resolved
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", abs, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", abs)
		}

		err = filepath.WalkDir(abs, func(path string, d os.DirEntry, werr error) error {
			// Return ctx.Err() (not SkipDir) so WalkDir short-circuits
			// the entire walk. Without this check, a cancelled scan
			// would keep stat'ing every directory in a large tree
			// before the caller saw the interrupt.
			if err := ctx.Err(); err != nil {
				return err
			}
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
				// Don't SkipDir blindly — if any negation rule could
				// re-include a descendant (e.g. `vendor/` + `!vendor/keep`),
				// pruning here would drop the re-inclusion before its
				// target is visited.
				if !matcher.CouldReinclude(rel) {
					return filepath.SkipDir
				}
				// The dir itself is ignored; we only walk in so a
				// negated descendant can be visited in its own turn.
				// Return early so the .git detection below doesn't
				// record this ignored directory as a repo (and so its
				// `return filepath.SkipDir` doesn't cut the descent
				// off before the negation's target is seen). A
				// descendant whose rel path is re-included by `!rule`
				// will be examined by the next WalkDir callback
				// invocation.
				return nil
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

// initialSlugHashLen controls the default hash-suffix length in hex
// chars when two repos share a basename. Six chars (24 bits) gives
// readable slugs in the common case; the retry loop in assignSlugs
// grows the length if a truncation collision occurs.
const initialSlugHashLen = 6

// isReservedSlug reports whether base would collide with a name
// downstream consumers already use for their own output file.
// `scan --report-dir` writes <dir>/index.html as the landing page;
// a repo whose basename was literally `index` would overwrite it
// (or be overwritten by it). Forcing the hash branch for reserved
// names avoids the collision without losing the repo.
//
// Switch, not a package-level map, to keep the reserved set
// immutable — a mutable map would let one test silently leak an
// entry into every other test that runs afterward. Case folded to
// align with the case-insensitivity elsewhere in assignSlugs.
func isReservedSlug(base string) bool {
	switch strings.ToLower(base) {
	case "index":
		return true
	}
	return false
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
//
// Short-hash truncation (6 hex = 24 bits) admits a small but nonzero
// collision probability — two absolute paths whose SHA-1 digests share
// the same 6-hex prefix would land on the same slug, and scan would
// silently overwrite one repo's JSONL + state file with the other's.
// The retry loop walks the proposed slug set; on any duplicate we
// redo the pass with a longer hash. Grows up to the full 40 hex chars
// before panicking — needing that much is a cryptographic event, not
// a scan-time bug.
//
// Case-insensitive uniqueness: on macOS (APFS/HFS+ in the default
// configuration) and Windows (NTFS), `Repo.jsonl` and `repo.jsonl`
// resolve to the same file on disk. A case-sensitive slug compare
// would treat the two repos as distinct, hand each the bare basename,
// and then quietly let the filesystem merge their JSONL and state
// files. Fold to lower case for BOTH the duplicate-detection count
// and the uniqueness seen-set so collision-triggered hashing fires
// on case-only differences. The emitted slug retains the original
// case for readability (so paths named Repo and repo produce
// `Repo-<hash>` and `repo-<hash>` — visually distinct to humans,
// case-insensitively distinct on disk).
func assignSlugs(repos []Repo) {
	counts := make(map[string]int)
	bases := make([]string, len(repos))
	for i := range repos {
		base := sanitizeSlug(filepath.Base(repos[i].AbsPath))
		if base == "" {
			base = "repo"
		}
		bases[i] = base
		counts[strings.ToLower(base)]++
	}

	for hashLen := initialSlugHashLen; hashLen <= 40; hashLen += 2 {
		proposed := make([]string, len(repos))
		seen := make(map[string]int, len(repos))
		collided := false
		for i := range repos {
			base := bases[i]
			slug := base
			if counts[strings.ToLower(base)] > 1 || isReservedSlug(base) {
				h := sha1.Sum([]byte(repos[i].AbsPath))
				slug = fmt.Sprintf("%s-%s", base, hex.EncodeToString(h[:])[:hashLen])
			}
			key := strings.ToLower(slug)
			if prev, ok := seen[key]; ok && prev != i {
				collided = true
				break
			}
			seen[key] = i
			proposed[i] = slug
		}
		if !collided {
			for i := range repos {
				repos[i].Slug = proposed[i]
			}
			return
		}
	}
	panic("scan: slug assignment failed to find unique hash within SHA-1 range")
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
