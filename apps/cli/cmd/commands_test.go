package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/byoi-platform/cli/internal/controlplane"
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

func TestParseDeployTarget(t *testing.T) {
	cases := []struct {
		in   string
		want deployTarget
		ok   bool
	}{
		{"", deployTargetLocal, true},
		{"local", deployTargetLocal, true},
		{"LOCAL", deployTargetLocal, true},
		{"byoi", deployTargetLocal, true},
		{"remote", deployTargetRemote, true},
		{"Remote", deployTargetRemote, true},
		{"usenova", deployTargetRemote, true},
		{"server", "", false},
	}
	for _, tc := range cases {
		got, err := parseDeployTarget(tc.in)
		if tc.ok && err != nil {
			t.Errorf("parseDeployTarget(%q) error = %v", tc.in, err)
			continue
		}
		if !tc.ok && err == nil {
			t.Errorf("parseDeployTarget(%q) = nil error, want error", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDeployTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func runDeployAtURL(t *testing.T, baseURL, target string) (string, error) {
	t.Helper()

	oldCP, oldOut, oldToken := controlPlaneURL, output, apiToken
	controlPlaneURL, output, apiToken = baseURL, "text", "operator-token"
	t.Cleanup(func() {
		controlPlaneURL, output, apiToken = oldCP, oldOut, oldToken
	})

	c := deployCmd()
	c.SilenceUsage = true
	c.SilenceErrors = true
	c.SetArgs([]string{
		"--name", "web",
		"--image", "nginx:alpine",
		"--target", target,
		"--port", "8080:80",
	})

	return captureStdout(t, func() error { return c.Execute() })
}

func TestDeployRemoteCreatesDeployment(t *testing.T) {
	var got controlplane.CreateDeploymentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/deployments" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           "dep-1",
			"agent_id":     "platform-1",
			"service_name": "web",
			"image":        "nginx:alpine",
			"state":        "pending",
			"command_id":   "cmd-1",
		})
	}))
	defer srv.Close()

	stdout, err := runDeployAtURL(t, srv.URL, "remote")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if got.Service["name"] != "web" || got.Service["image"] != "nginx:alpine" {
		t.Errorf("service = %+v", got.Service)
	}
	ports, ok := got.Service["ports"].(map[string]any)
	if !ok || ports["8080"] != "80" {
		t.Errorf("ports = %+v", got.Service["ports"])
	}
	if !strings.Contains(stdout, "Deployed web") || !strings.Contains(stdout, "dep-1") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestDeployRemoteNoCapacity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "No platform capacity available",
			"message": "no healthy platform agent with enough capacity is available; retry later",
		})
	}))
	defer srv.Close()

	_, err := runDeployAtURL(t, srv.URL, "usenova")
	if err == nil || !strings.Contains(err.Error(), "no platform capacity available") {
		t.Fatalf("error = %v, want capacity error", err)
	}
}

func TestDeployRemoteControlPlaneDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "Command API is disabled",
			"message": "the control plane has no operator token configured",
		})
	}))
	defer srv.Close()

	_, err := runDeployAtURL(t, srv.URL, "remote")
	if err == nil || !strings.Contains(err.Error(), "not configured for remote deploy") {
		t.Fatalf("error = %v, want not-configured error", err)
	}
}

func TestDeployRemoteUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	_, err := runDeployAtURL(t, url, "remote")
	if err == nil || !strings.Contains(err.Error(), "could not reach control plane") {
		t.Fatalf("error = %v, want unreachable error", err)
	}
}
