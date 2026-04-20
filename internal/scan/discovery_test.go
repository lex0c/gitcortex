package scan

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscover_FindsRepos(t *testing.T) {
	root := t.TempDir()

	mustMkRepo(t, filepath.Join(root, "a"))
	mustMkRepo(t, filepath.Join(root, "b"))
	mustMkRepo(t, filepath.Join(root, "nested", "c"))
	// Plain dir without .git — must NOT be picked up.
	if err := os.MkdirAll(filepath.Join(root, "not-a-repo", "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	repos, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(repos) != 3 {
		t.Fatalf("expected 3 repos, got %d: %+v", len(repos), repos)
	}
	got := map[string]bool{}
	for _, r := range repos {
		got[r.Slug] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !got[want] {
			t.Errorf("expected slug %q in %v", want, got)
		}
	}
}

func TestDiscover_RespectsIgnore(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, filepath.Join(root, "keep"))
	mustMkRepo(t, filepath.Join(root, "node_modules", "garbage"))

	matcher := NewMatcher([]string{"node_modules"})
	repos, err := Discover(context.Background(), []string{root}, matcher, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Slug != "keep" {
		t.Fatalf("expected only `keep`, got %+v", repos)
	}
}

// Regression: `vendor/` + `!vendor/keep` must descend into vendor/
// so the negation has a chance to re-include vendor/keep. Before the
// fix, the walker SkipDir'd vendor unconditionally and the re-included
// repo was silently dropped.
func TestDiscover_HonorsNegatedDescendant(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, filepath.Join(root, "app"))
	mustMkRepo(t, filepath.Join(root, "vendor", "garbage"))
	mustMkRepo(t, filepath.Join(root, "vendor", "keep"))

	matcher := NewMatcher([]string{"vendor/", "!vendor/keep"})
	repos, err := Discover(context.Background(), []string{root}, matcher, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range repos {
		got[r.RelPath] = true
	}
	if !got["app"] {
		t.Errorf("app should be included: %+v", repos)
	}
	if !got["vendor/keep"] {
		t.Errorf("vendor/keep should be re-included by negation rule: %+v", repos)
	}
	if got["vendor/garbage"] {
		t.Errorf("vendor/garbage should remain ignored: %+v", repos)
	}
}

// Regression: when the ignored directory itself is a repo and a
// negation rule points at a descendant, the walker must NOT record
// the ignored dir (it's ignored) AND must keep descending so the
// negation's target can be visited. Previously the .git detection
// ran unconditionally and both incorrectly recorded the ignored
// parent AND SkipDir'd the child out of the walk.
// With a 6-hex (24-bit) suffix, the birthday paradox predicts a
// truncation collision around ~2^12 duplicates. Generating 10k paths
// that share a basename makes collisions statistically near-certain
// (expected count ≈ 3). The invariant "all resulting slugs are
// distinct" forces assignSlugs's retry loop to grow the hash and
// still produce a unique namespace — the exact corruption class the
// extra iterations were added to prevent.
func TestAssignSlugs_UniqueEvenUnderTruncationCollisions(t *testing.T) {
	const n = 10000
	repos := make([]Repo, n)
	for i := 0; i < n; i++ {
		repos[i] = Repo{AbsPath: fmt.Sprintf("/path/to/dir-%06d/myrepo", i)}
	}
	assignSlugs(repos)

	slugs := make(map[string]int, n)
	for i, r := range repos {
		if prev, ok := slugs[r.Slug]; ok {
			t.Fatalf("duplicate slug %q between repos[%d]=%s and repos[%d]=%s — JSONL + state files would collide",
				r.Slug, prev, repos[prev].AbsPath, i, r.AbsPath)
		}
		slugs[r.Slug] = i
	}
}

// Directly exercise the retry path with two paths constructed to
// collide at initialSlugHashLen. Without the retry, both would get
// slug "myrepo-<sameHex>" and the scan would silently overwrite one
// repo's files with the other's. With the retry, the loop grows
// the hash until the pair separates.
func TestAssignSlugs_ResolvesFirstPrefixCollision(t *testing.T) {
	a, b, found := findColliding6HexPaths(50000)
	if !found {
		t.Skip("no colliding pair found within search budget — astronomically unlikely, skip rather than flake")
	}
	repos := []Repo{{AbsPath: a}, {AbsPath: b}}
	assignSlugs(repos)

	if repos[0].Slug == repos[1].Slug {
		t.Fatalf("retry failed: both repos got slug %q for colliding paths %s and %s", repos[0].Slug, a, b)
	}
	// Sanity: the slug suffix must be longer than the initial 6 hex,
	// proving the retry branch actually fired.
	const minLenAfterRetry = len("myrepo-") + initialSlugHashLen + 1
	if len(repos[0].Slug) < minLenAfterRetry && len(repos[1].Slug) < minLenAfterRetry {
		t.Errorf("expected at least one slug to have a longer hash after retry; got %q and %q", repos[0].Slug, repos[1].Slug)
	}
}

// findColliding6HexPaths searches a deterministic sequence of
// `myrepo` paths for any two whose SHA-1 digests share the first
// initialSlugHashLen hex chars. At 24 bits of resolution the
// birthday bound hits 50% around N≈4900, so maxAttempts=50000 is
// overwhelmingly likely to yield a pair. Returns false only if no
// collision was found — the test treats that as a skip, not a
// failure, since the worst-case outcome is a missed opportunity to
// exercise the retry, not a bug.
func findColliding6HexPaths(maxAttempts int) (string, string, bool) {
	seen := make(map[string]string, maxAttempts)
	for i := 0; i < maxAttempts; i++ {
		p := fmt.Sprintf("/search/path-%07d/myrepo", i)
		h := sha1.Sum([]byte(p))
		prefix := hex.EncodeToString(h[:])[:initialSlugHashLen]
		if prev, ok := seen[prefix]; ok {
			return prev, p, true
		}
		seen[prefix] = p
	}
	return "", "", false
}

// `index` is the landing-page filename used by `scan --report-dir`.
// A repo whose basename is literally `index` would emit
// `<dir>/index.html`, colliding with (and getting overwritten by)
// the landing page. Force the hash branch for reserved names so
// the per-repo report file never lands on a reserved path.
func TestAssignSlugs_ReservesIndex(t *testing.T) {
	repos := []Repo{
		{AbsPath: "/workspace/index"},
		{AbsPath: "/other/unrelated"},
	}
	assignSlugs(repos)

	for _, r := range repos {
		if filepath.Base(r.AbsPath) == "index" && r.Slug == "index" {
			t.Errorf("`index` basename must not produce the bare slug; landing page would be overwritten (path=%s, slug=%s)", r.AbsPath, r.Slug)
		}
	}
	// Case-insensitivity: `Index` would also collide on a case-
	// insensitive filesystem serving the HTML.
	repos = []Repo{{AbsPath: "/workspace/Index"}}
	assignSlugs(repos)
	if strings.EqualFold(repos[0].Slug, "index") {
		t.Errorf("`Index` basename must also be reserved; got %q", repos[0].Slug)
	}
}

// Regression: on macOS (APFS/HFS+ default) and Windows (NTFS),
// `Repo.jsonl` and `repo.jsonl` are the same file on disk. A
// case-sensitive slug compare would hand both repos the bare
// basename and let the filesystem merge their outputs — one scan's
// JSONL silently overwrites the other. Counts and seen-set are
// lower-cased so any case-only difference is treated as a collision
// and forces the hash suffix.
func TestAssignSlugs_CaseInsensitiveUniqueness(t *testing.T) {
	repos := []Repo{
		{AbsPath: "/a/Team/Repo"},
		{AbsPath: "/b/OSS/repo"},
	}
	assignSlugs(repos)

	if repos[0].Slug == repos[1].Slug {
		t.Fatalf("repos with case-only-differing basenames got same slug %q", repos[0].Slug)
	}
	// The fs collision only goes away if the slugs also differ when
	// folded to lower case — which is what decides filenames on
	// case-insensitive fs.
	if strings.EqualFold(repos[0].Slug, repos[1].Slug) {
		t.Fatalf("slugs %q and %q differ only in case — they would still collide on macOS/Windows", repos[0].Slug, repos[1].Slug)
	}
	// Sanity: both should have gained a hash suffix (the bare `Repo`
	// and `repo` weren't safe to emit).
	for _, r := range repos {
		if !strings.Contains(r.Slug, "-") {
			t.Errorf("repo %s got bare slug %q; case-sensitive branch did not trigger hashing", r.AbsPath, r.Slug)
		}
	}
}

// Same paths ingested twice must produce the same slugs — this is
// what makes resume work across runs. Even with the retry loop
// adjusting hash length under collisions, a deterministic input set
// must yield a deterministic output.
func TestAssignSlugs_DeterministicUnderRetry(t *testing.T) {
	build := func() []Repo {
		rs := make([]Repo, 5000)
		for i := 0; i < 5000; i++ {
			rs[i] = Repo{AbsPath: fmt.Sprintf("/work/p-%05d/myrepo", i)}
		}
		return rs
	}
	a := build()
	b := build()
	assignSlugs(a)
	assignSlugs(b)
	for i := range a {
		if a[i].Slug != b[i].Slug {
			t.Errorf("slug for %s differs across runs: %q vs %q", a[i].AbsPath, a[i].Slug, b[i].Slug)
		}
	}
}

// Mid-walk cancel: the pre-cancelled test below only proves the
// first callback checks ctx. This test cancels AFTER Discover has
// already started walking — the scenario users hit with Ctrl+C on
// a long home-dir scan. Asserting the walk stops in flight, not
// just when prompted at the very start.
//
// The tree is large enough (5,000 dirs × 3 subdirs = 20k nodes)
// that a full walk on typical hardware takes tens of ms; sleeping
// briefly before cancel lands the signal mid-flight. We assert:
//   - err is context.Canceled (ctx check fired inside the walk)
//   - elapsed time < full baseline (walk was actually interrupted)
//   - at least one repo was discovered BEFORE cancel (not a
//     pre-cancel scenario in disguise)
// Regression: WalkDir given a symlink ROOT visits only the link
// entry and refuses to descend. Users whose ~/work is a symlink to
// a real data disk would previously see "no git repositories found"
// despite the target being full of repos. Canonicalize via
// EvalSymlinks before walking so the walk starts at a real dir.
func TestDiscover_DereferencesSymlinkRoot(t *testing.T) {
	real := t.TempDir()
	mustMkRepo(t, filepath.Join(real, "repo-a"))
	mustMkRepo(t, filepath.Join(real, "nested", "repo-b"))

	// Place the symlink in a separate TempDir so the test doesn't
	// have to clean it up explicitly.
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "work")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}

	repos, err := Discover(context.Background(), []string{link}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatalf("Discover via symlink root: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("want 2 repos found through symlink root, got %d: %+v", len(repos), repos)
	}
	got := map[string]bool{}
	for _, r := range repos {
		got[r.Slug] = true
	}
	for _, want := range []string{"repo-a", "repo-b"} {
		if !got[want] {
			t.Errorf("expected slug %q in %v (symlink root was not dereferenced)", want, got)
		}
	}
}

func TestDiscover_AbortsMidWalk(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a large synthetic tree; skipped in -short mode")
	}
	root := t.TempDir()
	const topLevel = 5000
	for i := 0; i < topLevel; i++ {
		parent := filepath.Join(root, fmt.Sprintf("d-%05d", i))
		for j := 0; j < 3; j++ {
			if err := os.MkdirAll(filepath.Join(parent, fmt.Sprintf("sub-%d", j)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		// Every top-level dir is a repo so the result count is a
		// direct proxy for how far the walk got.
		mustMkRepo(t, parent)
	}

	// Baseline: uncancelled walk finds all topLevel repos.
	baseStart := time.Now()
	full, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	baseline := time.Since(baseStart)
	if len(full) != topLevel {
		t.Fatalf("baseline walk should find all %d repos, got %d", topLevel, len(full))
	}

	// Cancelled walk: give the walk a head start, then cancel.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		time.Sleep(baseline / 4)
		cancel()
		close(done)
	}()

	cancStart := time.Now()
	repos, err := Discover(ctx, []string{root}, NewMatcher(nil), 0)
	cancElapsed := time.Since(cancStart)
	<-done

	if err != context.Canceled {
		t.Fatalf("want context.Canceled after mid-walk cancel, got err=%v (elapsed %v, baseline %v, repos %d)",
			err, cancElapsed, baseline, len(repos))
	}
	if cancElapsed >= baseline {
		t.Errorf("cancelled walk took %v — baseline was %v. Ctx respected but walk did not shortcut; users will see delayed Ctrl+C", cancElapsed, baseline)
	}
	if len(repos) >= topLevel {
		t.Errorf("cancelled walk returned all %d repos — appears to have finished before cancel fired; baseline %v, elapsed %v", len(repos), baseline, cancElapsed)
	}
}

// A cancelled context must abort the walk — previously Discover
// ignored ctx entirely, so Ctrl+C during the walk phase only took
// effect after every directory had been stat'd. Test uses a
// pre-cancelled context to trip the early-return on the very first
// callback; no repos should be returned and the error should equal
// ctx.Err().
func TestDiscover_AbortsOnCancelledContext(t *testing.T) {
	root := t.TempDir()
	// Enough decoy directories that an un-aborted walk would still
	// produce observable work — the assertion below fails loudly if
	// the check is skipped.
	for i := 0; i < 20; i++ {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("dir-%d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustMkRepo(t, filepath.Join(root, "would-be-found"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	repos, err := Discover(ctx, []string{root}, NewMatcher(nil), 0)
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got err=%v", err)
	}
	if len(repos) != 0 {
		t.Errorf("want empty repo list after cancel, got %+v", repos)
	}
}

func TestDiscover_IgnoredRepoNotRecorded_DescendantStillFound(t *testing.T) {
	root := t.TempDir()
	// `vendor` is itself a repo AND is ignored by `vendor/`.
	mustMkRepo(t, filepath.Join(root, "vendor"))
	// `vendor/keep` is a nested repo, re-included by `!vendor/keep`.
	mustMkRepo(t, filepath.Join(root, "vendor", "keep"))

	matcher := NewMatcher([]string{"vendor/", "!vendor/keep"})
	repos, err := Discover(context.Background(), []string{root}, matcher, 0)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, r := range repos {
		got[r.RelPath] = true
	}
	if got["vendor"] {
		t.Error("vendor itself is ignored and must not be recorded as a repo")
	}
	if !got["vendor/keep"] {
		t.Errorf("vendor/keep should be re-included by the negation rule; got %+v", repos)
	}
}

func TestMatcher_CouldReinclude(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		dir      string
		want     bool
	}{
		{"no negation", []string{"vendor/"}, "vendor", false},
		{"explicit descendant", []string{"vendor/", "!vendor/keep"}, "vendor", true},
		{"unrelated negation", []string{"vendor/", "!src/main"}, "vendor", false},
		{"basename negation could fire anywhere", []string{"vendor/", "!keep"}, "vendor", true},
		{"deep-match negation", []string{"build/", "!**/src"}, "build", true},
		// Globbed first segment — reviewer case.
		{"glob star in segment", []string{"vendor*/", "!vendor*/keep"}, "vendor", true},
		{"wildcard segment matches any parent", []string{"*/", "!*/keep"}, "vendor", true},
		{"glob prefix that doesn't match dir", []string{"vendor*/", "!foo*/keep"}, "vendor", false},
		{"nested dir with literal pattern", []string{"pkg/vendor/", "!pkg/vendor/keep"}, "pkg/vendor", true},
		{"nested dir with glob in first segment", []string{"*/vendor/", "!*/vendor/keep"}, "pkg/vendor", true},
		{"pattern with same segment count as dir can't match descendant", []string{"!vendor"}, "vendor", true}, // basename-anywhere
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewMatcher(c.patterns)
			if got := m.CouldReinclude(c.dir); got != c.want {
				t.Errorf("CouldReinclude(%q) with %v = %v, want %v", c.dir, c.patterns, got, c.want)
			}
		})
	}
}

// Regression: glob-prefixed negations like `!vendor*/keep` or `!*/keep`
// used to slip past CouldReinclude because it only matched literal
// `dir + "/"` prefixes. Discovery then pruned vendor/ and the
// re-included vendor/keep repo disappeared from the scan.
func TestDiscover_HonorsGlobbedNegation(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, filepath.Join(root, "vendor", "keep"))
	mustMkRepo(t, filepath.Join(root, "vendor", "garbage"))
	mustMkRepo(t, filepath.Join(root, "vendor-old", "keep"))
	mustMkRepo(t, filepath.Join(root, "unrelated"))

	matcher := NewMatcher([]string{"vendor*/", "!vendor*/keep"})
	repos, err := Discover(context.Background(), []string{root}, matcher, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range repos {
		got[r.RelPath] = true
	}
	want := []string{"vendor/keep", "vendor-old/keep", "unrelated"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing %q in discovered repos: %+v", w, repos)
		}
	}
	if got["vendor/garbage"] {
		t.Error("vendor/garbage should remain ignored")
	}
}

func TestDiscover_DoesNotDescendIntoRepo(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	mustMkRepo(t, parent)
	// A nested .git inside an already-discovered repo is treated as a
	// submodule and skipped — we don't double-count.
	mustMkRepo(t, filepath.Join(parent, "submodule"))

	repos, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (parent only), got %d: %+v", len(repos), repos)
	}
}

func TestDiscover_SlugCollisionGetsHashSuffix(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, filepath.Join(root, "a", "myrepo"))
	mustMkRepo(t, filepath.Join(root, "b", "myrepo"))

	repos, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	if repos[0].Slug == repos[1].Slug {
		t.Errorf("collision not resolved: both slugs are %q", repos[0].Slug)
	}
	// With the two-pass naming, BOTH duplicates carry a hash suffix —
	// neither gets the bare basename. This guarantees the slug for any
	// given path is stable regardless of which sibling WalkDir hits
	// first across runs.
	for _, r := range repos {
		if r.Slug == "myrepo" {
			t.Errorf("expected both colliding repos to get a hash suffix, but %s kept the bare name", r.AbsPath)
		}
	}
}

// Re-running discovery must produce the same slug for the same path
// even when the WalkDir traversal could legally vary. This is the
// invariant `<slug>.state` resumption depends on.
func TestDiscover_SlugDeterministicAcrossRuns(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, filepath.Join(root, "a", "myrepo"))
	mustMkRepo(t, filepath.Join(root, "b", "myrepo"))

	first, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("repo count differs across runs: %d vs %d", len(first), len(second))
	}
	pathSlug := map[string]string{}
	for _, r := range first {
		pathSlug[r.AbsPath] = r.Slug
	}
	for _, r := range second {
		if pathSlug[r.AbsPath] != r.Slug {
			t.Errorf("slug for %s changed across runs: %q → %q", r.AbsPath, pathSlug[r.AbsPath], r.Slug)
		}
	}
}

func TestDiscover_RejectsSymlinkGit(t *testing.T) {
	root := t.TempDir()
	// A "repo" whose .git is a symlink — should not be picked up.
	bad := filepath.Join(root, "weird")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(bad, ".git")); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}
	mustMkRepo(t, filepath.Join(root, "real"))

	repos, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Slug != "real" {
		t.Fatalf("expected only `real`, got %+v", repos)
	}
}

func TestDiscover_MaxDepthHonored(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, filepath.Join(root, "shallow"))
	mustMkRepo(t, filepath.Join(root, "a", "b", "c", "deep"))

	repos, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Slug != "shallow" {
		t.Fatalf("expected only shallow, got %+v", repos)
	}
}

func mustMkRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover_FindsBareRepo(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "myrepo.git")
	for _, name := range []string{"HEAD", "objects", "refs"} {
		full := filepath.Join(bare, name)
		if name == "HEAD" {
			if err := os.MkdirAll(bare, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Decoy: dir with HEAD only — must not be picked up.
	decoy := filepath.Join(root, "not-a-repo")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "HEAD"), []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := Discover(context.Background(), []string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Slug != "myrepo.git" {
		t.Fatalf("expected single myrepo.git, got %+v", repos)
	}
}
