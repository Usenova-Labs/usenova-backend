package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func testHandler(t *testing.T) (*Handler, *AgentStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	store := NewAgentStore()
	return NewHandler(store, log), store
}

// newRouter mirrors the production route wiring for agent-scoped endpoints.
func newRouter(h *Handler) *gin.Engine {
	r := gin.New()
	agents := r.Group("/api/v1/agents")
	agents.POST("/register", h.RegisterAgent)
	agents.POST("/:agentID/heartbeat", h.RequireAgentAuth(), h.HandleHeartbeat)
	agents.GET("/:agentID", h.RequireAgentAuth(), h.GetAgent)
	return r
}

// register is a helper that registers an agent and returns the issued token.
func register(t *testing.T, r *gin.Engine, agentID string) string {
	t.Helper()
	body, _ := json.Marshal(RegisterRequest{Hostname: "host-1", Version: "1.0.0"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp RegisterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("register decode: %v", err)
	}
	return resp.SessionToken
}

func heartbeatReq(agentID, token string) *http.Request {
	body, _ := json.Marshal(HeartbeatRequest{Status: AgentStatus{RunningServices: 1}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestHeartbeat_WithValidSessionToken(t *testing.T) {
	h, _ := testHandler(t)
	r := newRouter(h)

	// The server generates its own agent ID when none is provided; use a fixed
	// one by registering it explicitly.
	body, _ := json.Marshal(RegisterRequest{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var resp RegisterResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.SessionToken == "" {
		t.Fatal("expected a session token from registration")
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, heartbeatReq("agent-1", resp.SessionToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHeartbeat_RejectsMissingToken(t *testing.T) {
	h, _ := testHandler(t)
	r := newRouter(h)
	_ = register(t, r, "")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, heartbeatReq("some-agent", ""))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHeartbeat_RejectsInvalidToken(t *testing.T) {
	h, _ := testHandler(t)
	r := newRouter(h)

	body, _ := json.Marshal(RegisterRequest{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, heartbeatReq("agent-1", "not-the-real-token"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHeartbeat_RejectsTokenForOtherAgent(t *testing.T) {
	h, store := testHandler(t)
	r := newRouter(h)

	a, err := store.RegisterAgent("agent-a", "host-a", "1.0.0", AgentCapabilities{})
	if err != nil {
		t.Fatalf("register agent-a: %v", err)
	}
	if _, err := store.RegisterAgent("agent-b", "host-b", "1.0.0", AgentCapabilities{}); err != nil {
		t.Fatalf("register agent-b: %v", err)
	}

	// agent-a's token must not authenticate a heartbeat for agent-b.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, heartbeatReq("agent-b", a.SessionToken))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for cross-agent token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestGetAgent_RequiresAuth(t *testing.T) {
	h, store := testHandler(t)
	r := newRouter(h)

	a, err := store.RegisterAgent("agent-1", "host-1", "1.0.0", AgentCapabilities{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-1", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-1", nil)
	req.Header.Set("Authorization", "Bearer "+a.SessionToken)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"Bearer  ", "", false},
		{"", "", false},
		{"Token abc", "", false},
		{"abc", "", false},
	}
	for _, c := range cases {
		got, ok := bearerToken(c.header)
		if ok != c.ok || got != c.want {
			t.Errorf("bearerToken(%q) = (%q, %v), want (%q, %v)", c.header, got, ok, c.want, c.ok)
		}
	}
}

func TestValidateSessionToken(t *testing.T) {
	s := NewAgentStore()
	a, err := s.RegisterAgent("agent-1", "host-1", "1.0.0", AgentCapabilities{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := s.ValidateSessionToken("agent-1", a.SessionToken); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}
	if err := s.ValidateSessionToken("agent-1", "wrong"); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
	if err := s.ValidateSessionToken("agent-1", ""); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for empty, got %v", err)
	}
	if err := s.ValidateSessionToken("ghost", a.SessionToken); err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}
