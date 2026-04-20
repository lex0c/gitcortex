package main

import (
	"strings"
	"testing"
)

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
