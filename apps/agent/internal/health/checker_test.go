package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func testLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return log
}

func TestChecker_HTTP(t *testing.T) {
	checker := NewChecker(testLogger())

	t.Run("healthy on 2xx", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		result := checker.Check(context.Background(), CheckConfig{
			Type:     HTTP,
			Endpoint: srv.URL,
			Timeout:  time.Second,
		})
		if !result.Healthy {
			t.Errorf("expected healthy, got error: %v", result.Error)
		}
	})

	t.Run("unhealthy on 5xx", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		result := checker.Check(context.Background(), CheckConfig{
			Type:     HTTP,
			Endpoint: srv.URL,
			Timeout:  time.Second,
		})
		if result.Healthy {
			t.Error("expected unhealthy for 500 response")
		}
	})

	t.Run("unhealthy when endpoint unreachable", func(t *testing.T) {
		result := checker.Check(context.Background(), CheckConfig{
			Type:     HTTP,
			Endpoint: "http://127.0.0.1:1/health",
			Timeout:  200 * time.Millisecond,
		})
		if result.Healthy {
			t.Error("expected unhealthy for unreachable endpoint")
		}
	})

	t.Run("unhealthy when endpoint missing", func(t *testing.T) {
		result := checker.Check(context.Background(), CheckConfig{Type: HTTP})
		if result.Healthy || result.Error == nil {
			t.Error("expected error for missing endpoint")
		}
	})
}

func TestChecker_TCP(t *testing.T) {
	checker := NewChecker(testLogger())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	tcpAddr := ln.Addr().(*net.TCPAddr)

	t.Run("healthy when port open", func(t *testing.T) {
		result := checker.Check(context.Background(), CheckConfig{
			Type:    TCP,
			Host:    "127.0.0.1",
			Port:    tcpAddr.Port,
			Timeout: time.Second,
		})
		if !result.Healthy {
			t.Errorf("expected healthy, got error: %v", result.Error)
		}
	})

	t.Run("unhealthy when port closed", func(t *testing.T) {
		result := checker.Check(context.Background(), CheckConfig{
			Type:    TCP,
			Host:    "127.0.0.1",
			Port:    1, // unlikely to be open
			Timeout: 200 * time.Millisecond,
		})
		if result.Healthy {
			t.Error("expected unhealthy for closed port")
		}
	})
}

func TestChecker_UnsupportedType(t *testing.T) {
	checker := NewChecker(testLogger())
	result := checker.Check(context.Background(), CheckConfig{Type: CheckType("udp")})
	if result.Healthy || result.Error == nil {
		t.Error("expected error for unsupported check type")
	}
}
