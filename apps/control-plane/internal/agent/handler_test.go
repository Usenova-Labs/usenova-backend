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

func newTestRouter(t *testing.T) *gin.Engine {
	return newTestRouterWithPlatformToken(t, "")
}

func newTestRouterWithPlatformToken(t *testing.T, platformToken string) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	handler := NewHandlerWithPlatformToken(NewAgentStore(), platformToken, logger)

	router := gin.New()
	agents := router.Group("/api/v1/agents")
	agents.POST("/register", handler.RegisterAgent)
	agents.POST("/register/platform", handler.RegisterPlatformAgent)
	agents.POST("/:agentID/heartbeat", handler.RequireAgentAuth(), handler.HandleHeartbeat)
	agents.GET("", handler.ListAgents)
	agents.GET("/:agentID", handler.RequireAgentAuth(), handler.GetAgent)

	return router
}

func doRequest(t *testing.T, router *gin.Engine, method, path string, body interface{}, token string) *httptest.ResponseRecorder {
	t.Helper()
	return doRequestWithHeaders(t, router, method, path, body, token, nil)
}

func doRequestWithHeaders(t *testing.T, router *gin.Engine, method, path string, body interface{}, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	return recorder
}

func decodeAgent(t *testing.T, recorder *httptest.ResponseRecorder) Agent {
	t.Helper()

	var agent Agent
	if err := json.Unmarshal(recorder.Body.Bytes(), &agent); err != nil {
		t.Fatalf("failed to decode agent: %v", err)
	}

	return agent
}

func decodeRegisterResponse(t *testing.T, recorder *httptest.ResponseRecorder) RegisterResponse {
	t.Helper()

	var resp RegisterResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode registration response: %v", err)
	}
	return resp
}

func decodeErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()

	var resp ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	return resp
}

func registerAgent(t *testing.T, router *gin.Engine, req RegisterRequest) RegisterResponse {
	t.Helper()

	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/register", req, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}
	return decodeRegisterResponse(t, recorder)
}

func registerPlatformAgent(t *testing.T, router *gin.Engine, platformToken string, req RegisterRequest) RegisterResponse {
	t.Helper()

	recorder := doRequestWithHeaders(t, router, http.MethodPost, "/api/v1/agents/register/platform", req, "", map[string]string{
		"X-Platform-Bootstrap-Token": platformToken,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}
	return decodeRegisterResponse(t, recorder)
}

func TestRegisterAgent_ReturnsSystemInfo(t *testing.T) {
	router := newTestRouter(t)

	system := AgentSystemInfo{
		OS:            "linux",
		Arch:          "amd64",
		CPUCount:      8,
		MemoryBytes:   16 * 1024 * 1024 * 1024,
		DockerVersion: "24.0.7",
	}

	reg := registerAgent(t, router, RegisterRequest{
		AgentID:      "agent-1",
		Hostname:     "host-1",
		Version:      "1.0.0",
		Capabilities: AgentCapabilities{Docker: true},
		System:       &system,
	})

	recorder := doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil, reg.SessionToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	agent := decodeAgent(t, recorder)
	if agent.System != system {
		t.Errorf("expected system info %+v, got %+v", system, agent.System)
	}
}

func TestRegisterAgent_WithoutSystemInfoStaysCompatible(t *testing.T) {
	router := newTestRouter(t)

	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/register", map[string]interface{}{
		"agent_id":     "legacy-agent",
		"hostname":     "host-1",
		"version":      "1.0.0",
		"capabilities": map[string]bool{"docker": true},
	}, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}
	reg := decodeRegisterResponse(t, recorder)

	recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents/legacy-agent", nil, reg.SessionToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	agent := decodeAgent(t, recorder)
	if agent.System != (AgentSystemInfo{}) {
		t.Errorf("expected an empty system info, got %+v", agent.System)
	}
}

func TestRegisterAgent_DefaultsToUserOwnershipAndIgnoresSpoofedMetadata(t *testing.T) {
	router := newTestRouter(t)

	reg := registerAgent(t, router, RegisterRequest{
		AgentID:   "agent-1",
		Hostname:  "host-1",
		Version:   "1.0.0",
		OwnerType: OwnerTypePlatform,
		OwnerID:   "spoofed-owner",
		Labels:    map[string]string{"tier": "platform"},
	})

	recorder := doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil, reg.SessionToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	agent := decodeAgent(t, recorder)
	if agent.OwnerType != OwnerTypeUser {
		t.Errorf("owner type = %q, want %q", agent.OwnerType, OwnerTypeUser)
	}
	if agent.OwnerID != "" {
		t.Errorf("owner ID = %q, want empty", agent.OwnerID)
	}
	if len(agent.Labels) != 0 {
		t.Errorf("labels = %v, want empty", agent.Labels)
	}
}

func TestRegisterPlatformAgent_RequiresBootstrapToken(t *testing.T) {
	const platformToken = "platform-secret"
	router := newTestRouterWithPlatformToken(t, platformToken)

	req := RegisterRequest{AgentID: "platform-1", Hostname: "host-1", Version: "1.0.0"}
	tests := []struct {
		name  string
		token string
	}{
		{name: "missing token"},
		{name: "wrong token", token: "not-the-platform-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := doRequestWithHeaders(t, router, http.MethodPost, "/api/v1/agents/register/platform", req, "", map[string]string{
				"X-Platform-Bootstrap-Token": tt.token,
			})
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("expected status %d, got %d: %s", http.StatusUnauthorized, recorder.Code, recorder.Body.String())
			}
			if bytes.Contains(recorder.Body.Bytes(), []byte(platformToken)) {
				t.Errorf("response leaked the platform token: %s", recorder.Body.String())
			}
		})
	}
}

func TestRegisterPlatformAgent_DisabledWithoutConfiguredToken(t *testing.T) {
	router := newTestRouter(t)

	recorder := doRequestWithHeaders(t, router, http.MethodPost, "/api/v1/agents/register/platform", RegisterRequest{
		AgentID:  "platform-1",
		Hostname: "host-1",
		Version:  "1.0.0",
	}, "", map[string]string{
		"X-Platform-Bootstrap-Token": "anything",
	})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d: %s", http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
	}
}

func TestRegisterPlatformAgent_RegistersFleetMetadata(t *testing.T) {
	const platformToken = "platform-secret"
	router := newTestRouterWithPlatformToken(t, platformToken)

	labels := map[string]string{"region": "us-east", "pool": "alpha"}
	reg := registerPlatformAgent(t, router, platformToken, RegisterRequest{
		AgentID:   "platform-1",
		Hostname:  "host-1",
		Version:   "1.0.0",
		OwnerType: OwnerTypeUser,
		OwnerID:   "fleet-1",
		Labels:    labels,
	})

	recorder := doRequest(t, router, http.MethodGet, "/api/v1/agents/platform-1", nil, reg.SessionToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	agent := decodeAgent(t, recorder)
	if agent.OwnerType != OwnerTypePlatform {
		t.Errorf("owner type = %q, want %q", agent.OwnerType, OwnerTypePlatform)
	}
	if agent.OwnerID != "fleet-1" {
		t.Errorf("owner ID = %q, want fleet-1", agent.OwnerID)
	}
	if len(agent.Labels) != len(labels) {
		t.Fatalf("labels = %v, want %v", agent.Labels, labels)
	}
	for key, want := range labels {
		if got := agent.Labels[key]; got != want {
			t.Errorf("label %q = %q, want %q", key, got, want)
		}
	}
}

func TestListAgents_FiltersByOwnershipAndLabels(t *testing.T) {
	const platformToken = "platform-secret"
	router := newTestRouterWithPlatformToken(t, platformToken)

	registerAgent(t, router, RegisterRequest{AgentID: "user-1", Hostname: "host-user", Version: "1.0.0"})
	registerPlatformAgent(t, router, platformToken, RegisterRequest{
		AgentID:  "platform-1",
		Hostname: "host-p1",
		Version:  "1.0.0",
		OwnerID:  "fleet-a",
		Labels:   map[string]string{"region": "us-east", "pool": "alpha"},
	})
	registerPlatformAgent(t, router, platformToken, RegisterRequest{
		AgentID:  "platform-2",
		Hostname: "host-p2",
		Version:  "1.0.0",
		OwnerID:  "fleet-a",
		Labels:   map[string]string{"region": "eu-west", "pool": "beta"},
	})

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "all agents", want: []string{"user-1", "platform-1", "platform-2"}},
		{name: "owner type", query: "?owner_type=platform", want: []string{"platform-1", "platform-2"}},
		{name: "user owner type", query: "?owner_type=user", want: []string{"user-1"}},
		{name: "owner id", query: "?owner_id=fleet-a", want: []string{"platform-1", "platform-2"}},
		{name: "single label", query: "?label=region=us-east", want: []string{"platform-1"}},
		{name: "all labels", query: "?label=region=us-east&label=pool=alpha", want: []string{"platform-1"}},
		{name: "labels list", query: "?labels=region=us-east,pool=alpha", want: []string{"platform-1"}},
		{name: "no match", query: "?label=region=us-east&label=pool=beta"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := doRequest(t, router, http.MethodGet, "/api/v1/agents"+tt.query, nil, "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
			}

			var resp AgentListResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to decode agent list: %v", err)
			}
			if resp.Total != len(tt.want) {
				t.Fatalf("total = %d, want %d (%+v)", resp.Total, len(tt.want), resp.Agents)
			}

			seen := make(map[string]bool)
			for _, agent := range resp.Agents {
				seen[agent.AgentID] = true
			}
			for _, id := range tt.want {
				if !seen[id] {
					t.Errorf("agent %q missing from filtered list", id)
				}
			}
		})
	}
}

func TestListAgents_RejectsInvalidFilters(t *testing.T) {
	router := newTestRouter(t)

	tests := []string{
		"?owner_type=admin",
		"?label=missing-equals",
		"?label==value",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			recorder := doRequest(t, router, http.MethodGet, "/api/v1/agents"+query, nil, "")
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRegisterAgent_RejectsInvalidSystemInfo(t *testing.T) {
	tests := []struct {
		name   string
		system AgentSystemInfo
	}{
		{name: "negative cpu count", system: AgentSystemInfo{CPUCount: -1}},
		{name: "negative memory size", system: AgentSystemInfo{MemoryBytes: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestRouter(t)

			system := tt.system
			recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/register", RegisterRequest{
				AgentID:  "agent-1",
				Hostname: "host-1",
				Version:  "1.0.0",
				System:   &system,
			}, "")
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestListAgents_ReturnsSystemInfo(t *testing.T) {
	router := newTestRouter(t)

	system := AgentSystemInfo{
		OS:          "linux",
		Arch:        "arm64",
		CPUCount:    2,
		MemoryBytes: 4 * 1024 * 1024 * 1024,
	}

	registerAgent(t, router, RegisterRequest{
		AgentID:  "agent-1",
		Hostname: "host-1",
		Version:  "1.0.0",
		System:   &system,
	})

	recorder := doRequest(t, router, http.MethodGet, "/api/v1/agents", nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	var resp AgentListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode agent list: %v", err)
	}

	if resp.Total != 1 || len(resp.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", resp.Total)
	}
	if resp.Agents[0].System != system {
		t.Errorf("expected system info %+v, got %+v", system, resp.Agents[0].System)
	}
}

func TestHeartbeat_RefreshesSystemInfo(t *testing.T) {
	router := newTestRouter(t)

	reg := registerAgent(t, router, RegisterRequest{
		AgentID:  "agent-1",
		Hostname: "host-1",
		Version:  "1.0.0",
		System: &AgentSystemInfo{
			OS:            "linux",
			Arch:          "amd64",
			CPUCount:      4,
			MemoryBytes:   8 * 1024 * 1024 * 1024,
			DockerVersion: "24.0.7",
		},
	})

	updated := AgentSystemInfo{
		OS:            "linux",
		Arch:          "amd64",
		CPUCount:      16,
		MemoryBytes:   32 * 1024 * 1024 * 1024,
		DockerVersion: "25.0.1",
	}

	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{
		Status: AgentStatus{Uptime: 30, RunningServices: 1},
		System: &updated,
	}, reg.SessionToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil, reg.SessionToken)
	agent := decodeAgent(t, recorder)
	if agent.System != updated {
		t.Errorf("expected refreshed system info %+v, got %+v", updated, agent.System)
	}
}

func TestHeartbeat_RejectsInvalidSystemInfo(t *testing.T) {
	tests := []struct {
		name   string
		system AgentSystemInfo
	}{
		{name: "negative cpu count", system: AgentSystemInfo{CPUCount: -1}},
		{name: "negative memory size", system: AgentSystemInfo{MemoryBytes: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestRouter(t)

			stored := AgentSystemInfo{
				OS:          "linux",
				Arch:        "amd64",
				CPUCount:    4,
				MemoryBytes: 8 * 1024 * 1024 * 1024,
			}

			reg := registerAgent(t, router, RegisterRequest{
				AgentID:  "agent-1",
				Hostname: "host-1",
				Version:  "1.0.0",
				System:   &stored,
			})

			system := tt.system
			recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{
				Status: AgentStatus{Uptime: 30},
				System: &system,
			}, reg.SessionToken)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
			}

			recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil, reg.SessionToken)
			agent := decodeAgent(t, recorder)
			if agent.System != stored {
				t.Errorf("expected system info %+v to be preserved, got %+v", stored, agent.System)
			}
		})
	}
}

func TestHeartbeat_WithoutSystemInfoKeepsInventory(t *testing.T) {
	router := newTestRouter(t)

	system := AgentSystemInfo{
		OS:          "linux",
		Arch:        "amd64",
		CPUCount:    4,
		MemoryBytes: 8 * 1024 * 1024 * 1024,
	}

	reg := registerAgent(t, router, RegisterRequest{
		AgentID:  "agent-1",
		Hostname: "host-1",
		Version:  "1.0.0",
		System:   &system,
	})

	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{
		Status: AgentStatus{Uptime: 30},
	}, reg.SessionToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil, reg.SessionToken)
	agent := decodeAgent(t, recorder)
	if agent.System != system {
		t.Errorf("expected system info to be preserved, got %+v", agent.System)
	}
}

func TestHeartbeat_WithValidSessionToken(t *testing.T) {
	router := newTestRouter(t)
	reg := registerAgent(t, router, RegisterRequest{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})

	rec := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{Status: AgentStatus{RunningServices: 1}}, reg.SessionToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHeartbeat_RejectsMissingToken(t *testing.T) {
	router := newTestRouter(t)
	registerAgent(t, router, RegisterRequest{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})

	rec := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{Status: AgentStatus{RunningServices: 1}}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHeartbeat_RejectsInvalidToken(t *testing.T) {
	router := newTestRouter(t)
	registerAgent(t, router, RegisterRequest{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})

	rec := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{Status: AgentStatus{RunningServices: 1}}, "not-the-real-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHeartbeat_RejectsTokenForOtherAgent(t *testing.T) {
	router := newTestRouter(t)
	regA := registerAgent(t, router, RegisterRequest{AgentID: "agent-a", Hostname: "host-a", Version: "1.0.0"})
	registerAgent(t, router, RegisterRequest{AgentID: "agent-b", Hostname: "host-b", Version: "1.0.0"})

	rec := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-b/heartbeat", HeartbeatRequest{Status: AgentStatus{RunningServices: 1}}, regA.SessionToken)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for cross-agent token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHeartbeat_InvalidTokenReturnsSpecificError(t *testing.T) {
	router := newTestRouter(t)
	registerAgent(t, router, RegisterRequest{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})

	token := "not-the-real-token"
	rec := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{Status: AgentStatus{RunningServices: 1}}, token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid token, got %d (%s)", rec.Code, rec.Body.String())
	}

	resp := decodeErrorResponse(t, rec)
	if resp.Error != "invalid session token" {
		t.Errorf("expected error %q, got %q", "invalid session token", resp.Error)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(token)) {
		t.Errorf("response leaked the session token: %s", rec.Body.String())
	}
}

func TestHeartbeat_TokenForOtherAgentReturnsSpecificError(t *testing.T) {
	router := newTestRouter(t)
	regA := registerAgent(t, router, RegisterRequest{AgentID: "agent-a", Hostname: "host-a", Version: "1.0.0"})
	registerAgent(t, router, RegisterRequest{AgentID: "agent-b", Hostname: "host-b", Version: "1.0.0"})

	rec := doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-b/heartbeat", HeartbeatRequest{Status: AgentStatus{RunningServices: 1}}, regA.SessionToken)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for cross-agent token, got %d (%s)", rec.Code, rec.Body.String())
	}

	resp := decodeErrorResponse(t, rec)
	if resp.Error != "session token does not match agent" {
		t.Errorf("expected error %q, got %q", "session token does not match agent", resp.Error)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(regA.SessionToken)) {
		t.Errorf("response leaked the session token: %s", rec.Body.String())
	}
}

func TestHeartbeat_UnknownAgentReturnsNotFound(t *testing.T) {
	router := newTestRouter(t)
	reg := registerAgent(t, router, RegisterRequest{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})

	rec := doRequest(t, router, http.MethodPost, "/api/v1/agents/ghost/heartbeat", HeartbeatRequest{Status: AgentStatus{RunningServices: 1}}, reg.SessionToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown agent, got %d (%s)", rec.Code, rec.Body.String())
	}

	resp := decodeErrorResponse(t, rec)
	if resp.Error != "agent not found" {
		t.Errorf("expected error %q, got %q", "agent not found", resp.Error)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(reg.SessionToken)) {
		t.Errorf("response leaked the session token: %s", rec.Body.String())
	}
}

func TestGetAgent_RequiresAuth(t *testing.T) {
	router := newTestRouter(t)
	reg := registerAgent(t, router, RegisterRequest{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})

	rec := doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	rec = doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil, reg.SessionToken)
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
	a, err := s.RegisterAgent(AgentRegistration{AgentID: "agent-1", Hostname: "host-1", Version: "1.0.0"})
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

	b, err := s.RegisterAgent(AgentRegistration{AgentID: "agent-2", Hostname: "host-2", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("register second agent: %v", err)
	}
	if err := s.ValidateSessionToken("agent-2", a.SessionToken); err != ErrTokenForOtherAgent {
		t.Errorf("expected ErrTokenForOtherAgent, got %v", err)
	}
	if err := s.ValidateSessionToken("agent-2", b.SessionToken); err != nil {
		t.Errorf("valid token for agent-2 rejected: %v", err)
	}
}
