package cmd

import "testing"

func TestParseMemoryBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"512", 512},
		{"512b", 512},
		{"1k", 1 << 10},
		{"1kib", 1 << 10},
		{"1kb", 1000},
		{"512m", 512 << 20},
		{"1.5g", 3 << 29},
		{"2GiB", 2 << 30},
	}
	for _, tc := range cases {
		got, err := parseMemoryBytes(tc.in)
		if err != nil {
			t.Errorf("parseMemoryBytes(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseMemoryBytes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}

	for _, in := range []string{"-1m", "not-a-size"} {
		if _, err := parseMemoryBytes(in); err == nil {
			t.Errorf("parseMemoryBytes(%q) = nil error, want error", in)
		}
	}
}
