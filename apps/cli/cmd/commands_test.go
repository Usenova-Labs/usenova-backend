package cmd

import (
	"reflect"
	"testing"

	"github.com/byoi-platform/cli/internal/client"
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

func TestFormatRestarts(t *testing.T) {
	cases := []struct {
		name string
		svc  client.Service
		want string
	}{
		{
			name: "legacy agent falls back to total",
			svc:  client.Service{RestartCount: 2},
			want: "2",
		},
		{
			name: "breaker budget remaining",
			svc: client.Service{
				RestartCountInWindow:   1,
				RestartBudgetRemaining: 2,
				RestartWindowSeconds:   600,
			},
			want: "1/3",
		},
		{
			name: "breaker tripped",
			svc: client.Service{
				RestartCountInWindow:   3,
				RestartBudgetRemaining: 0,
				RestartWindowSeconds:   600,
				FailedReason:           "circuit_breaker",
			},
			want: "3/3 (circuit breaker tripped)",
		},
		{
			name: "zero attempts in current window",
			svc: client.Service{
				RestartCount:           3,
				RestartCountInWindow:   0,
				RestartBudgetRemaining: 2,
				RestartWindowSeconds:   600,
			},
			want: "0/2",
		},
		{
			name: "unlimited restarts",
			svc: client.Service{
				RestartCountInWindow:   4,
				RestartBudgetRemaining: -1,
				RestartWindowSeconds:   600,
			},
			want: "4/unlimited",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatRestarts(&tc.svc); got != tc.want {
				t.Errorf("formatRestarts() = %q, want %q", got, tc.want)
			}
		})
	}
}
