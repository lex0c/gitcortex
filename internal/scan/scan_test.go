package scan

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lex0c/gitcortex/internal/extract"
	"github.com/lex0c/gitcortex/internal/stats"
)

func TestRun_EndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	root := t.TempDir()
	makeRealRepo(t, filepath.Join(root, "alpha"), map[string]string{
		"main.go":     "package main\nfunc main() {}\n",
		"README.md":   "# alpha\n",
	})
	makeRealRepo(t, filepath.Join(root, "beta"), map[string]string{
		"app.py": "print('hi')\n",
	})
	// One node_modules-style decoy that should be ignored.
	makeRealRepo(t, filepath.Join(root, "node_modules", "garbage"), map[string]string{
		"x.js": "x\n",
	})

	output := filepath.Join(t.TempDir(), "out")
	cfg := Config{
		Roots:    []string{root},
		Output:   output,
		Parallel: 2,
		Extract: extract.Config{
			BatchSize:      100,
			CommandTimeout: extract.DefaultCommandTimeout,
		},
	}
	cfg.IgnoreFile = writeIgnoreFile(t, root, "node_modules\n")

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(res.JSONLs) != 2 {
		t.Fatalf("expected 2 JSONLs, got %d (%v)", len(res.JSONLs), res.JSONLs)
	}

	// Manifest sanity
	manifestPath := filepath.Join(output, "manifest.json")
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(m.Repos) != 2 {
		t.Fatalf("manifest should have 2 repos, got %d", len(m.Repos))
	}
	for _, r := range m.Repos {
		if r.Status != "ok" {
			t.Errorf("repo %s status=%s err=%s", r.Slug, r.Status, r.Error)
		}
	}

	// Consolidated load + breakdown
	ds, err := stats.LoadMultiJSONL(res.JSONLs)
	if err != nil {
		t.Fatalf("LoadMultiJSONL: %v", err)
	}
	breakdown := stats.RepoBreakdown(ds, "")
	if len(breakdown) != 2 {
		t.Fatalf("expected breakdown across 2 repos, got %d: %+v", len(breakdown), breakdown)
	}
	got := map[string]int{}
	for _, b := range breakdown {
		got[b.Repo] = b.Commits
	}
	for _, name := range []string{"alpha", "beta"} {
		if got[name] == 0 {
			t.Errorf("repo %s missing or has 0 commits in breakdown: %v", name, got)
		}
	}
}

// makeRealRepo initializes a git repo with the given files and a single
// commit using a deterministic identity so the assertions don't depend on
// the developer's git config.
func makeRealRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "-q", "-b", "main")
	runGit("add", ".")
	runGit("commit", "-q", "-m", "initial")
}

func writeIgnoreFile(t *testing.T, root, contents string) string {
	t.Helper()
	path := filepath.Join(root, ".gitcortex-ignore")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
