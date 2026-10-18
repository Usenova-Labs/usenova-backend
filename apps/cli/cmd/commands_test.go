package cmd

import "testing"

func TestFormatPorts(t *testing.T) {
	cases := []struct {
		name  string
		ports map[string]string
		want  string
	}{
		{"empty", nil, "-"},
		{"single", map[string]string{"8080": "80"}, "8080:80"},
		{"sorted", map[string]string{"443": "443", "8080": "80"}, "443:443,8080:80"},
	}
	for _, tc := range cases {
		if got := formatPorts(tc.ports); got != tc.want {
			t.Errorf("formatPorts(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
