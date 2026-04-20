package main

import (
	"bytes"
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
