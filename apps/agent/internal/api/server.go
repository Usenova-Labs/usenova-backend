// Package api provides a small, loopback-only HTTP control surface so the
// runtime manager can be exercised manually before a real command channel from
// the control plane exists.
//
// SECURITY: this is a deployment-command ingress. It is deliberately locked
// down: it refuses to start on a non-loopback address, and it requires a bearer
// token on every request. There is no multi-tenant authorization here; the
// trust boundary is "process/host that holds the agent token". TODO: replace
// with a mutually-authenticated control-plane command channel.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/docker"
	"github.com/byoi-platform/agent/internal/runtime"
)

// ServiceManager is the slice of runtime.Manager the API needs. Declaring an
// interface here keeps the HTTP layer decoupled and unit-testable.
type ServiceManager interface {
	DeployService(ctx context.Context, spec docker.ServiceSpec) (*runtime.Service, error)
	StopService(ctx context.Context, name string) error
	RemoveService(ctx context.Context, name string) error
	ListServices() []*runtime.Service
	GetService(name string) (*runtime.Service, bool)
	ServiceLogs(ctx context.Context, name string, opts docker.LogOptions) (io.ReadCloser, error)
	RunningCount() int
}

// Server is a loopback-only HTTP control API.
type Server struct {
	addr     string
	token    string
	mgr      ServiceManager
	log      *logrus.Logger
	httpSrv  *http.Server
	listener net.Listener
}

// NewServer validates the bind address and token and returns a Server.
// It returns an error rather than silently weakening the trust boundary.
func NewServer(addr, token string, mgr ServiceManager, log *logrus.Logger) (*Server, error) {
	if addr == "" {
		return nil, errors.New("api: address is required")
	}
	if token == "" {
		return nil, errors.New("api: a non-empty bearer token is required (refusing to expose a deployment endpoint without auth)")
	}
	if !isLoopback(addr) {
		return nil, fmt.Errorf("api: refusing to bind to non-loopback address %q; this endpoint is for local testing only", addr)
	}
	return &Server{addr: addr, token: token, mgr: mgr, log: log}, nil
}

// Start binds the listener and serves in a background goroutine.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("api: listen on %s: %w", s.addr, err)
	}
	s.listener = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/services", s.withAuth(s.handleServices))
	mux.HandleFunc("/services/", s.withAuth(s.handleService))

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.WithError(err).Error("Local control API stopped unexpectedly")
		}
	}()

	s.log.WithField("addr", ln.Addr().String()).Info("Local control API listening (loopback, token required)")
	return nil
}

// Shutdown stops the HTTP server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// Addr returns the actual bound address (useful when addr used port 0).
func (s *Server) Addr() string {
	if s.listener == nil {
		return s.addr
	}
	return s.listener.Addr().String()
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) || !subtleConstantTimeEq(strings.TrimPrefix(auth, prefix), s.token) {
			s.writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// handleServices routes GET /services and POST /services.
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"running":  s.mgr.RunningCount(),
			"services": s.mgr.ListServices(),
		})
	case http.MethodPost:
		var spec docker.ServiceSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid service spec: "+err.Error())
			return
		}
		svc, err := s.mgr.DeployService(r.Context(), spec)
		if err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusCreated, svc)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleService routes /services/{name} (GET, DELETE) and
// /services/{name}/stop (POST).
func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/services/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		s.writeErr(w, http.StatusBadRequest, "service name required")
		return
	}
	name := parts[0]

	if len(parts) == 2 && parts[1] == "logs" {
		if r.Method != http.MethodGet {
			s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleServiceLogs(w, r, name)
		return
	}

	if len(parts) == 2 && parts[1] == "stop" {
		if r.Method != http.MethodPost {
			s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := s.mgr.StopService(r.Context(), name); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "stopping", "service": name})
		return
	}

	switch r.Method {
	case http.MethodGet:
		svc, ok := s.mgr.GetService(name)
		if !ok {
			s.writeErr(w, http.StatusNotFound, "service not found")
			return
		}
		s.writeJSON(w, http.StatusOK, svc)
	case http.MethodDelete:
		if err := s.mgr.RemoveService(r.Context(), name); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "service": name})
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleServiceLogs streams a service's container logs as text/plain.
//
// Query parameters: tail (default "100"), follow (bool), timestamps (bool).
// tail accepts a non-negative integer or "all"; anything else is a 400.
func (s *Server) handleServiceLogs(w http.ResponseWriter, r *http.Request, name string) {
	tail, err := parseTail(r.URL.Query().Get("tail"))
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	follow := parseBool(r.URL.Query().Get("follow"))
	timestamps := parseBool(r.URL.Query().Get("timestamps"))

	rc, err := s.mgr.ServiceLogs(r.Context(), name, docker.LogOptions{
		Tail:       tail,
		Follow:     follow,
		Timestamps: timestamps,
	})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, runtime.ErrServiceNotFound):
			status = http.StatusNotFound
		case errors.Is(err, runtime.ErrRuntimeUnavailable):
			status = http.StatusServiceUnavailable
		}
		s.writeErr(w, status, err.Error())
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	if follow {
		// Flush each write so the client sees log lines as they are produced
		// rather than buffered until the stream ends.
		_, _ = io.Copy(&flushWriter{w: w, f: http.NewResponseController(w)}, rc)
		return
	}
	_, _ = io.Copy(w, rc)
}

// flushWriter flushes the underlying response after every write. Flush errors
// are ignored (the write result already reports a broken connection).
type flushWriter struct {
	w io.Writer
	f interface{ Flush() error }
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err == nil {
		_ = fw.f.Flush()
	}
	return n, err
}

// parseTail validates the tail query parameter. An empty value means "default"
// (100 lines); "all" requests the full history; otherwise a non-negative
// integer is required.
func parseTail(v string) (string, error) {
	if v == "" {
		return "100", nil
	}
	if v == "all" {
		return "all", nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return "", fmt.Errorf("invalid tail %q: must be a non-negative integer or \"all\"", v)
	}
	return strconv.Itoa(n), nil
}

// parseBool parses an optional boolean query flag; unrecognized values are
// treated as false.
func parseBool(v string) bool {
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]interface{}{
		"error":   code,
		"message": msg,
	})
}

// isLoopback reports whether the address binds only to the loopback interface.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Bare host/port forms we accept as non-loopback to stay strict.
		return false
	}
	if host == "" || host == "localhost" {
		// Empty host means "all interfaces" in Go, which is NOT loopback-only.
		return host == "localhost"
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// subtleConstantTimeEq compares two strings in constant time.
func subtleConstantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
