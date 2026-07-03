package fmtutil

import "testing"

func TestRepeatChar(t *testing.T) {
	tests := []struct {
		name string
		c    byte
		n    int
		want string
	}{
		{"zero count", '-', 0, ""},
		{"single char", '-', 1, "-"},
		{"multiple chars", '=', 5, "====="},
		{"space char", ' ', 3, "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepeatChar(tt.c, tt.n)
			if got != tt.want {
				t.Fatalf("RepeatChar(%q, %d) = %q, want %q", tt.c, tt.n, got, tt.want)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"empty string", "", 10, ""},
		{"shorter than max", "hello", 10, "hello"},
		{"exactly max length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"very short max", "hello", 3, "hel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateString(tt.input, tt.maxLen)
			if got != tt.want {
				t.Fatalf("TruncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
			if len(got) > tt.maxLen {
				t.Fatalf("TruncateString result length %d exceeds maxLen %d", len(got), tt.maxLen)
			}
		})
	}
}

func TestTruncateStringPlain(t *testing.T) {
	got := TruncateStringPlain("hello world", 8)
	if got != "hello wo" {
		t.Fatalf("TruncateStringPlain = %q, want %q", got, "hello wo")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		name      string
		bytes     int64
		precision int
		want      string
	}{
		{"bytes", 512, 2, "512 B"},
		{"kilobytes two decimals", 1536, 2, "1.50 KB"},
		{"megabytes two decimals", 2 * 1024 * 1024, 2, "2.00 MB"},
		{"kilobytes one decimal", 1536, 1, "1.5 KB"},
		{"gigabytes one decimal", 2 * 1024 * 1024 * 1024, 1, "2.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatBytes(tt.bytes, tt.precision)
			if got != tt.want {
				t.Fatalf("FormatBytes(%d, %d) = %q, want %q", tt.bytes, tt.precision, got, tt.want)
			}
		})
	}
}
