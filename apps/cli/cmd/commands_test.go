package cmd

import (
	"reflect"
	"testing"
)

func TestSplitLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"trailing newline dropped", "a\nb\n", []string{"a", "b"}},
		{"no trailing newline", "a\nb", []string{"a", "b"}},
		{"blank lines preserved", "a\n\nb\n", []string{"a", "", "b"}},
	}
	for _, tc := range cases {
		if got := splitLines(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitLines(%s) = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

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
