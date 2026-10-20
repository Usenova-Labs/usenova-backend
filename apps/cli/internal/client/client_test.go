package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustNew(t *testing.T, baseURL, token string, timeout time.Duration) *Client {
	t.Helper()
	cl, err := New(baseURL, token, timeout)
	if err != nil {
		t.Fatalf("New(%q, token=%q): %v", baseURL, token, err)
	}
	return cl
}

// TestNewRejectsCleartextTokenToNonLoopback enforces CWE-319: a bearer token
// must never be sent over plaintext HTTP to a non-loopback host.
func TestNewRejectsCleartextTokenToNonLoopback(t *testing.T) {
	rejected := []struct {
		baseURL string
		token   string
	}{
		{"http://192.168.1.10:9090", "secret"},
		{"http://example.com", "secret"},
		{"http://[fd00::1]:9090", "secret"},
		{"http://agent.internal.", "secret"},
	}
	for _, tc := range rejected {
		if _, err := New(tc.baseURL, tc.token, time.Second); err == nil {
			t.Errorf("New(%q, token) = nil error, want plaintext/remote rejection", tc.baseURL)
		} else if !strings.Contains(err.Error(), "plaintext") {
			t.Errorf("New(%q, token) error = %v, want plaintext transport message", tc.baseURL, err)
		}
	}

	allowed := []struct {
		baseURL string
		token   string
	}{
		{"http://127.0.0.1:9090", "secret"},
		{"http://127.0.0.1", "secret"},
		{"http://localhost:9090", "secret"},
		{"http://LOCALHOST:9090", "secret"},
		{"http://[::1]:9090", "secret"},
		{"https://example.com", "secret"},
		{"http://192.168.1.10:9090", ""}, // no token: nothing to expose
		{"", "secret"},                   // empty URL defaults to loopback
	}
	for _, tc := range allowed {
		if _, err := New(tc.baseURL, tc.token, time.Second); err != nil {
			t.Errorf("New(%q, token=%q) = %v, want nil", tc.baseURL, tc.token, err)
		}
	}
}

func TestValidateServiceName(t *testing.T) {
	valid := []string{
		"web",
		"my-service_1",
		"foo..bar",
		"a?b",
		"a#b",
		"a b",
		"a;b",
		"a&b=c",
	}
	for _, name := range valid {
		if err := ValidateServiceName(name); err != nil {
			t.Errorf("ValidateServiceName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"foo/bar",
		"/services/foo",
		"foo/../bar",
		"a\nb",
		"a\x00b",
		"a\x7fb",
	}
	for _, name := range invalid {
		if err := ValidateServiceName(name); err == nil {
			t.Errorf("ValidateServiceName(%q) = nil, want error", name)
		}
	}
}

// TestServiceURLNames asserts names survive as a single path segment: "?"/"#"
// must not turn into query/fragment, and "/" must be rejected before the
// request is built.
func TestServiceURLNames(t *testing.T) {
	var method, path, rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		rawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	cl := mustNew(t, srv.URL, "", time.Second)
	ctx := context.Background()

	if _, err := cl.GetService(ctx, "foo?old"); err != nil {
		t.Fatalf("GetService: %v", err)
	}
	if method != http.MethodGet || path != "/services/foo?old" || rawQuery != "" {
		t.Errorf("GET -> %s %s?%s, want GET /services/foo?old", method, path, rawQuery)
	}

	if err := cl.Stop(ctx, "foo?old"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if method != http.MethodPost || path != "/services/foo?old/stop" {
		t.Errorf("Stop -> %s %s, want POST /services/foo?old/stop", method, path)
	}

	if err := cl.Remove(ctx, "foo#bar"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if method != http.MethodDelete || path != "/services/foo#bar" || rawQuery != "" {
		t.Errorf("Remove -> %s %s?%s, want DELETE /services/foo#bar", method, path, rawQuery)
	}

	if _, err := cl.GetService(ctx, "foo/bar"); err == nil {
		t.Error("GetService(foo/bar) = nil, want validation error")
	}
	if err := cl.Remove(ctx, "foo/old"); err == nil {
		t.Error("Remove(foo/old) = nil, want validation error")
	}
	if err := cl.Stop(ctx, ""); err == nil {
		t.Error("Stop(\"\") = nil, want validation error")
	}
}

// TestLogs builds the request path/query correctly and surfaces agent errors.
func TestLogs(t *testing.T) {
	var method, path, rawQuery, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		rawQuery = r.URL.RawQuery
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("log line\n"))
	}))
	defer srv.Close()

	cl := mustNew(t, srv.URL, "tok", time.Second)
	ctx := context.Background()

	rc, err := cl.Logs(ctx, "web", LogsOptions{Tail: "25", Follow: true, Timestamps: true})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "log line\n" {
		t.Errorf("body = %q", b)
	}
	if method != http.MethodGet || path != "/services/web/logs" {
		t.Errorf("request = %s %s, want GET /services/web/logs", method, path)
	}
	q := parseQuery(t, rawQuery)
	if q.Get("tail") != "25" || q.Get("follow") != "true" || q.Get("timestamps") != "true" {
		t.Errorf("query = %q", rawQuery)
	}
	if auth != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", auth)
	}

	if _, err := cl.Logs(ctx, "foo/bar", LogsOptions{}); err == nil {
		t.Error("Logs(foo/bar) = nil, want validation error")
	}
}

// TestLogsError surfaces the agent's status and message.
func TestLogsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":404,"message":"service not found"}`))
	}))
	defer srv.Close()

	cl := mustNew(t, srv.URL, "", time.Second)
	_, err := cl.Logs(context.Background(), "ghost", LogsOptions{})
	if err == nil {
		t.Fatal("Logs: expected error")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "service not found") {
		t.Errorf("error = %v, want 404 + service not found", err)
	}
}

func parseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", raw, err)
	}
	return v
}

func TestIsTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	cl := mustNew(t, srv.URL, "", 50*time.Millisecond)
	if _, err := cl.ListServices(context.Background()); err == nil {
		t.Fatal("ListServices: expected timeout error")
	} else if !IsTimeout(err) {
		t.Errorf("IsTimeout(%v) = false, want true", err)
	}

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":404}`))
	}))
	defer fast.Close()

	cl = mustNew(t, fast.URL, "", time.Second)
	_, err := cl.GetService(context.Background(), "missing")
	if err == nil {
		t.Fatal("GetService: expected error")
	}
	if IsTimeout(err) {
		t.Errorf("IsTimeout(%v) = true, want false for a server error", err)
	}
	if IsTimeout(nil) {
		t.Error("IsTimeout(nil) = true, want false")
	}
}

// TestServiceRawPassthrough: Service decodes into typed fields for text
// output while replaying the original agent payload for -o json, so fields
// the CLI does not model (Health, LastError) are not lost.
func TestServiceRawPassthrough(t *testing.T) {
	payload := `{"Spec":{"name":"web","image":"nginx:alpine","ports":{"8080":"80"},"health_check":{"type":"http","path":"/health","port":8080}},"ContainerID":"abc123","State":"running","DesiredState":"running","Health":{"Type":"http"},"DeployedAt":"2026-10-18T12:00:00Z","RestartCount":2,"LastError":null}`

	var s Service
	if err := json.Unmarshal([]byte(payload), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Spec.Name != "web" || s.Spec.Image != "nginx:alpine" || s.State != "running" || s.RestartCount != 2 {
		t.Errorf("typed fields lost: %+v", s)
	}
	if hc := s.Spec.HealthCheck; hc == nil || hc.Type != "http" || hc.Path != "/health" || hc.Port != 8080 {
		t.Errorf("HealthCheck = %+v, want http /health :8080", hc)
	}

	out, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != payload {
		t.Errorf("JSON replay = %s\nwant original payload %s", out, payload)
	}
}
