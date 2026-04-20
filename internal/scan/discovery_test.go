package scan

import (
	"os"
	"path/filepath"
	"testing"
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

	repos, err := Discover([]string{root}, NewMatcher(nil), 0)
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
	repos, err := Discover([]string{root}, matcher, 0)
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
	repos, err := Discover([]string{root}, matcher, 0)
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

func TestDiscover_DoesNotDescendIntoRepo(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	mustMkRepo(t, parent)
	// A nested .git inside an already-discovered repo is treated as a
	// submodule and skipped — we don't double-count.
	mustMkRepo(t, filepath.Join(parent, "submodule"))

	repos, err := Discover([]string{root}, NewMatcher(nil), 0)
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

	repos, err := Discover([]string{root}, NewMatcher(nil), 0)
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

	first, err := Discover([]string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Discover([]string{root}, NewMatcher(nil), 0)
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

	repos, err := Discover([]string{root}, NewMatcher(nil), 0)
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

	repos, err := Discover([]string{root}, NewMatcher(nil), 2)
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

	repos, err := Discover([]string{root}, NewMatcher(nil), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Slug != "myrepo.git" {
		t.Fatalf("expected single myrepo.git, got %+v", repos)
	}
}
