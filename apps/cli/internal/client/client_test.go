package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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

	cl := New(srv.URL, "", time.Second)
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

	cl := New(srv.URL, "", 50*time.Millisecond)
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

	cl = New(fast.URL, "", time.Second)
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
