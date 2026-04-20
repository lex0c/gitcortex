package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
