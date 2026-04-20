package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lex0c/gitcortex/internal/scan"
	"github.com/lex0c/gitcortex/internal/stats"
)

// scanCmd must validate --since BEFORE running the discovery walk
// and extract pool. Without the early check, an obvious typo like
// `--since 1yy` only surfaces after scan.Run has already walked
// every root and extracted every repo found — which can take
// minutes-to-hours on a large workspace and waste the work.
//
// Use an empty TempDir so scan.Run would fail fast with "no git
// repositories found" if we reached it — that error does not mention
// "since", so an error containing "since" proves the early
// validation fired first.
func TestScanCmd_ValidatesSinceBeforeScanning(t *testing.T) {
	cmd := scanCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{
		"--root", t.TempDir(),
		"--output", t.TempDir(),
		"--since", "bogus",
	})
	// Swallow any stderr output cobra might emit so we don't pollute
	// go-test logs on success.
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --since bogus, got nil")
	}
	if !strings.Contains(err.Error(), "since") {
		t.Errorf("expected error to mention --since (proving early validation), got %q", err)
	}
	// Reaching scan.Run on an empty TempDir would produce the
	// discovery error; asserting its absence confirms the walk was
	// not started.
	if strings.Contains(err.Error(), "no git repositories found") {
		t.Errorf("scan.Run was reached before --since was validated — discovery ran on an invalid-flag input: %q", err)
	}
}

// scanCmd must exit non-zero when every discovered repo's extract
// failed, regardless of whether --report was requested. scan.Run
// intentionally treats per-repo failures as non-fatal (a transient
// error on one repo shouldn't tank the whole batch), but if ZERO
// repos succeed there's nothing useful on disk and CI should know.
// Previously the no-report branch returned success unconditionally;
// automation saw exit 0 and "Scan complete: 0 JSONL file(s)" and
// continued with empty artifacts.
//
// Test uses a fake repo (a bare `.git` dir with no history) so
// discovery picks it up but extract fails.
func TestScanCmd_ExitsNonZeroWhenAllExtractsFail(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "fake-repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := scanCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--root", root,
		"--output", t.TempDir(),
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected non-zero exit when all extracts fail; CI would silently treat this as success")
	}
	if !strings.Contains(err.Error(), "all extracts failed") {
		t.Errorf("error should explain every repo failed; got %q", err)
	}
}

// `scan --ignore-file /nonexistent` must fail at the CLI layer —
// silently falling back to zero rules would widen discovery scope
// without telling the user (e.g. node_modules and vendor/ dirs the
// user thought they excluded suddenly appear in the report). The
// default-path lookup in scan.loadMatcher still tolerates a missing
// `.gitcortex-ignore` at the first root; only the explicit flag is
// strict.
func TestScanCmd_RejectsMissingIgnoreFile(t *testing.T) {
	cmd := scanCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--root", t.TempDir(),
		"--output", t.TempDir(),
		"--ignore-file", filepath.Join(t.TempDir(), "typo.ignore"),
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --ignore-file; a typo must not silently widen scope")
	}
	if !strings.Contains(err.Error(), "typo.ignore") {
		t.Errorf("error should identify the missing file; got %q", err)
	}
	// Must fail BEFORE discovery starts — scan on an empty TempDir
	// would otherwise produce "no git repositories found".
	if strings.Contains(err.Error(), "no git repositories found") {
		t.Errorf("ignore-file check ran after discovery; got %q", err)
	}
}

// --report without --email is ambiguous under the "no team-wide
// consolidation" design: the only meaningful single-HTML output
// across multiple repos is a developer profile. The error must
// point the user at the right flag for each intent.
func TestScanCmd_RejectsReportWithoutEmail(t *testing.T) {
	cmd := scanCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--root", t.TempDir(),
		"--output", t.TempDir(),
		"--report", filepath.Join(t.TempDir(), "out.html"),
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --report without --email")
	}
	if !strings.Contains(err.Error(), "--report-dir") || !strings.Contains(err.Error(), "--email") {
		t.Errorf("error should point at both --report-dir and --email as remediation; got %q", err)
	}
}

// `scan --report-dir <dir>` writes index.html plus one HTML per
// successful repo, with each per-repo HTML rendered from a
// single-input Dataset (no cross-repo path prefix). End-to-end
// check against two real repos built with git.
func TestScanCmd_ReportDirGeneratesPerRepoHTMLAndIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	makeRepoWithCommit(t, filepath.Join(root, "alpha"), "a.go", "package a\n")
	makeRepoWithCommit(t, filepath.Join(root, "beta"), "b.py", "print('b')\n")

	scanOut := t.TempDir()
	reportDir := t.TempDir()

	cmd := scanCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--root", root,
		"--output", scanOut,
		"--report-dir", reportDir,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan --report-dir: %v", err)
	}

	// Every successful repo gets a standalone HTML named <slug>.html.
	for _, slug := range []string{"alpha", "beta"} {
		path := filepath.Join(reportDir, slug+".html")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("per-repo report missing: %v", err)
		}
		if !strings.Contains(string(b), "<!DOCTYPE html>") {
			t.Errorf("%s should be a full HTML document", path)
		}
		// Paths inside the per-repo report must NOT carry a
		// `<repo>:` prefix — each tab is standalone, so LoadJSONL
		// (not LoadMultiJSONL) was used and file paths appear raw.
		if strings.Contains(string(b), slug+":") {
			t.Errorf("per-repo report %s contains a `%s:` path prefix; Dataset was loaded as multi-repo", path, slug)
		}
	}

	// index.html links to each per-repo report.
	indexBytes, err := os.ReadFile(filepath.Join(reportDir, "index.html"))
	if err != nil {
		t.Fatalf("index.html missing: %v", err)
	}
	index := string(indexBytes)
	if !strings.Contains(index, `href="alpha.html"`) || !strings.Contains(index, `href="beta.html"`) {
		t.Errorf("index.html should link to both per-repo reports; got body excerpt: %.300s", index)
	}
	if !strings.Contains(index, "<h1>Index") {
		t.Errorf("index.html missing title block")
	}
}

// renderScanReportDir must render the index for a manifest that
// mixes ok / failed / pending statuses. Failed entries show their
// error message, pending entries render with the pending pill, and
// neither inflates the "failed" count in the summary card.
func TestRenderScanReportDir_MixedStatuses(t *testing.T) {
	dir := t.TempDir()
	okJSONL := filepath.Join(dir, "good.jsonl")
	jsonlContent := `{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","author_email":"me@x.com","author_name":"Me","author_date":"2024-01-01T00:00:00Z","additions":10,"deletions":0,"files_changed":1}
{"type":"commit_file","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","path_current":"a.go","additions":10,"deletions":0}
`
	if err := os.WriteFile(okJSONL, []byte(jsonlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	reportDir := t.TempDir()
	result := &scan.Result{
		OutputDir: dir,
		Manifest: scan.Manifest{
			Roots: []string{"/fake/root"},
			Repos: []scan.ManifestRepo{
				{Slug: "good", Path: "/fake/root/good", Status: "ok", JSONL: okJSONL},
				{Slug: "broken", Path: "/fake/root/broken", Status: "failed", Error: "simulated extract crash"},
				{Slug: "skipped", Path: "/fake/root/skipped", Status: "pending"},
			},
		},
	}

	err := renderScanReportDir(result, reportDir,
		stats.LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50},
		stats.StatsFlags{CouplingMinChanges: 1, NetworkMinFiles: 1},
		10)
	if err != nil {
		t.Fatalf("renderScanReportDir returned error despite inline-failure policy: %v", err)
	}

	// OK entry's report file exists; failed/pending do not.
	if _, err := os.Stat(filepath.Join(reportDir, "good.html")); err != nil {
		t.Errorf("good.html missing: %v", err)
	}
	for _, unwanted := range []string{"broken.html", "skipped.html"} {
		if _, err := os.Stat(filepath.Join(reportDir, unwanted)); err == nil {
			t.Errorf("%s should NOT be written — status was not ok", unwanted)
		}
	}

	indexB, err := os.ReadFile(filepath.Join(reportDir, "index.html"))
	if err != nil {
		t.Fatalf("index.html: %v", err)
	}
	index := string(indexB)

	// Summary card splits failed and pending correctly: "(1 failed)"
	// AND "(1 pending)" appear, not "(2 failed)".
	if strings.Contains(index, "(2 failed)") {
		t.Error("summary miscounted pending as failed — `(2 failed)` appeared instead of separate buckets")
	}
	if !strings.Contains(index, "1 failed") {
		t.Errorf("summary should show `1 failed`; index excerpt: %.600s", index)
	}
	if !strings.Contains(index, "1 pending") {
		t.Errorf("summary should show `1 pending`; index excerpt: %.600s", index)
	}

	// Failed entry's error bubbles up to the card.
	if !strings.Contains(index, "simulated extract crash") {
		t.Error("failed entry's error message missing from index")
	}

	// Status pills render only for non-ok entries (ok is implicit
	// given the link itself and the presence of metric cells). Assert
	// the two non-ok pills exist and the ok one does not.
	for _, want := range []string{"status-failed", "status-pending"} {
		if !strings.Contains(index, want) {
			t.Errorf("index missing %q pill — status branch lost its style", want)
		}
	}
	if strings.Contains(index, `class="status-pill status-ok"`) {
		t.Error("ok entries should not render a status pill — redundant with the link and metric cells")
	}

	// Only OK entries carry an href; failed/pending must not link to
	// a file that doesn't exist.
	if strings.Contains(index, `href="broken.html"`) || strings.Contains(index, `href="skipped.html"`) {
		t.Error("index links to a non-existent per-repo HTML for a non-ok entry")
	}
}

// Render with zero successful repos must still emit an index
// without tripping on the MaxCommits==0 guard in the bar-width
// template expression.
func TestRenderScanReportDir_AllFailedStillEmitsIndex(t *testing.T) {
	reportDir := t.TempDir()
	result := &scan.Result{
		OutputDir: t.TempDir(),
		Manifest: scan.Manifest{
			Repos: []scan.ManifestRepo{
				{Slug: "a", Status: "failed", Error: "boom"},
				{Slug: "b", Status: "failed", Error: "also boom"},
			},
		},
	}
	err := renderScanReportDir(result, reportDir,
		stats.LoadOptions{HalfLifeDays: 90, CoupMaxFiles: 50},
		stats.StatsFlags{CouplingMinChanges: 1, NetworkMinFiles: 1},
		10)
	if err != nil {
		t.Fatalf("render should not fail on all-failed manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(reportDir, "index.html")); err != nil {
		t.Fatalf("index.html must be emitted even when every repo failed: %v", err)
	}
}

// makeRepoWithCommit initializes a git repo with one file and one
// commit using a deterministic identity.
func makeRepoWithCommit(t *testing.T, dir, file, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
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
	run("init", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "initial")
}

func TestValidateDate(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		// Happy path.
		{"2024-01-01", false},
		{"2026-04-20", false},
		// Empty means "no bound" — the report command treats it as
		// "don't apply this end of the window".
		{"", false},
		// Rejections — each must fail BEFORE LoadJSONL runs so the
		// user sees a CLI-shaped error, not a silent empty dataset.
		{"2024-13-01", true}, // month out of range
		{"2024-02-30", true}, // day impossible for February
		{"2024/01/01", true}, // wrong separator
		{"20240101", true},   // wrong format
		{"not-a-date", true},
		{"Q1 2024", true}, // common user mistake
	}
	for _, c := range cases {
		err := validateDate(c.in, "--from")
		if c.wantErr && err == nil {
			t.Errorf("validateDate(%q) = nil, want error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("validateDate(%q) = %v, want nil", c.in, err)
		}
		// When error is emitted, the flag name must appear so the CLI
		// message points the user at the right arg. Prevents a future
		// refactor from dropping the context.
		if err != nil && !strings.Contains(err.Error(), "--from") {
			t.Errorf("validateDate(%q) error %q does not mention flag name", c.in, err)
		}
	}
}
