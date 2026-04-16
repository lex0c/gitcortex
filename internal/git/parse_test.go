package git

import "testing"

func TestParseInt64(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"0", 0, false},
		{"42", 42, false},
		{"-", 0, false},
		{"1000000", 1000000, false},
		{"-5", -5, false},
		{"", 0, true},
		{"abc", 0, true},
		{"9223372036854775807", 9223372036854775807, false},
	}

	for _, tt := range tests {
		got, err := parseInt64(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseInt64(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseInt64(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestIsValidSHA(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"a" + "0123456789abcdef0123456789abcdef0123456", true},
		{"A0123456789ABCDEF0123456789ABCDEF01234567", false}, // uppercase not valid hex pair? Actually hex.DecodeString accepts uppercase
		{"0000000000000000000000000000000000000000", true},
		{"abc123", false},
		{"", false},
		{"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
		{"0123456789abcdef0123456789abcdef0123456", false}, // 39 chars
		{"0123456789abcdef0123456789abcdef012345678", false}, // 41 chars
	}

	for _, tt := range tests {
		got := IsValidSHA(tt.in)
		if got != tt.want {
			t.Errorf("IsValidSHA(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
