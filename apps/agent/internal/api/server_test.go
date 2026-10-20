package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/docker"
	"github.com/byoi-platform/agent/internal/runtime"
)

// fakeManager implements ServiceManager for handler tests. Only the methods
// exercised by the logs endpoint carry behavior; the rest are no-ops.
type fakeManager struct {
	logs       map[string]string
	logsErr    map[string]error
	lastOpts   docker.LogOptions
	lastLogSvc string
	logCalls   int
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		logs:    make(map[string]string),
		logsErr: make(map[string]error),
	}
}

func (f *fakeManager) DeployService(context.Context, docker.ServiceSpec) (*runtime.Service, error) {
	return nil, nil
}
func (f *fakeManager) StopService(context.Context, string) error   { return nil }
func (f *fakeManager) RemoveService(context.Context, string) error { return nil }
func (f *fakeManager) ListServices() []*runtime.Service            { return nil }
func (f *fakeManager) GetService(string) (*runtime.Service, bool)  { return nil, false }
func (f *fakeManager) RunningCount() int                           { return 0 }

func (f *fakeManager) ServiceLogs(_ context.Context, name string, opts docker.LogOptions) (io.ReadCloser, error) {
	f.logCalls++
	f.lastLogSvc = name
	f.lastOpts = opts
	if err, ok := f.logsErr[name]; ok {
		return nil, err
	}
	body, ok := f.logs[name]
	if !ok {
		return nil, runtime.ErrServiceNotFound
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func testServer(t *testing.T, mgr ServiceManager) *Server {
	t.Helper()
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	srv, err := NewServer("127.0.0.1:0", "secret-token", mgr, log)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	return srv
}

// do issues a request against the server's mux, bypassing the network.
func do(t *testing.T, srv *Server, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/services", srv.withAuth(srv.handleServices))
	mux.HandleFunc("/services/", srv.withAuth(srv.handleService))

	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestLogsHandler_StreamsTail(t *testing.T) {
	mgr := newFakeManager()
	mgr.logs["web"] = "line one\nline two\n"
	srv := testServer(t, mgr)

	rec := do(t, srv, http.MethodGet, "/services/web/logs?tail=25&timestamps=true", "secret-token")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "line one\nline two\n" {
		t.Errorf("unexpected body %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain content-type, got %q", ct)
	}
	if mgr.lastLogSvc != "web" {
		t.Errorf("expected manager called with web, got %q", mgr.lastLogSvc)
	}
	if mgr.lastOpts.Tail != "25" || !mgr.lastOpts.Timestamps {
		t.Errorf("options not forwarded: %+v", mgr.lastOpts)
	}
}

func TestLogsHandler_DefaultTail(t *testing.T) {
	mgr := newFakeManager()
	mgr.logs["web"] = "x\n"
	srv := testServer(t, mgr)

	do(t, srv, http.MethodGet, "/services/web/logs", "secret-token")
	if mgr.lastOpts.Tail != "100" {
		t.Errorf("expected default tail 100, got %q", mgr.lastOpts.Tail)
	}
}

func TestLogsHandler_InvalidTail(t *testing.T) {
	srv := testServer(t, newFakeManager())
	rec := do(t, srv, http.MethodGet, "/services/web/logs?tail=-3", "secret-token")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestLogsHandler_NotFound(t *testing.T) {
	srv := testServer(t, newFakeManager())
	rec := do(t, srv, http.MethodGet, "/services/ghost/logs", "secret-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "service not found") {
		t.Errorf("expected 'service not found', got %s", rec.Body.String())
	}
}

func TestLogsHandler_DockerUnavailable(t *testing.T) {
	mgr := newFakeManager()
	mgr.logsErr["web"] = runtime.ErrRuntimeUnavailable
	srv := testServer(t, mgr)

	rec := do(t, srv, http.MethodGet, "/services/web/logs", "secret-token")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestLogsHandler_RequiresAuth(t *testing.T) {
	mgr := newFakeManager()
	mgr.logs["web"] = "secret log\n"
	srv := testServer(t, mgr)

	rec := do(t, srv, http.MethodGet, "/services/web/logs", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if mgr.logCalls != 0 {
		t.Error("manager must not be reached without auth")
	}
}

func TestLogsHandler_RejectsNonGET(t *testing.T) {
	srv := testServer(t, newFakeManager())
	rec := do(t, srv, http.MethodPost, "/services/web/logs", "secret-token")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestLogsHandler_FollowStreams(t *testing.T) {
	mgr := newFakeManager()
	mgr.logs["web"] = "streamed\n"
	srv := testServer(t, mgr)

	rec := do(t, srv, http.MethodGet, "/services/web/logs?follow=true", "secret-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "streamed\n" {
		t.Errorf("unexpected body %q", rec.Body.String())
	}
	if !mgr.lastOpts.Follow {
		t.Error("expected follow option forwarded")
	}
}

func TestParseTail(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "100", false},
		{"all", "all", false},
		{"0", "0", false},
		{"10", "10", false},
		{"-1", "", true},
		{"abc", "", true},
	}
	for _, c := range cases {
		got, err := parseTail(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseTail(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTail(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseTail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewServerRejectsBadConfig(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	if _, err := NewServer("127.0.0.1:9090", "", nil, log); err == nil {
		t.Error("expected error for empty token")
	}
	if _, err := NewServer("0.0.0.0:9090", "tok", nil, log); err == nil {
		t.Error("expected error for non-loopback bind")
	}
}
