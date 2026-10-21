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

// newTestRouter builds a router exposing the agent endpoints, mirroring the
// routes registered by the control plane server.
func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	handler := NewHandler(NewAgentStore(), logger)

	router := gin.New()
	agents := router.Group("/api/v1/agents")
	agents.POST("/register", handler.RegisterAgent)
	agents.POST("/:agentID/heartbeat", handler.HandleHeartbeat)
	agents.GET("", handler.ListAgents)
	agents.GET("/:agentID", handler.GetAgent)

	return router
}

// doRequest performs a JSON request against the router.
func doRequest(t *testing.T, router *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
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

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	return recorder
}

// decodeAgent decodes an agent from a JSON response.
func decodeAgent(t *testing.T, recorder *httptest.ResponseRecorder) Agent {
	t.Helper()

	var agent Agent
	if err := json.Unmarshal(recorder.Body.Bytes(), &agent); err != nil {
		t.Fatalf("failed to decode agent: %v", err)
	}

	return agent
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

	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/register", RegisterRequest{
		AgentID:      "agent-1",
		Hostname:     "host-1",
		Version:      "1.0.0",
		Capabilities: AgentCapabilities{Docker: true},
		System:       &system,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}

	recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil)
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

	// An agent that predates the inventory reports no system object.
	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/register", map[string]interface{}{
		"agent_id":     "legacy-agent",
		"hostname":     "host-1",
		"version":      "1.0.0",
		"capabilities": map[string]bool{"docker": true},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}

	recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents/legacy-agent", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	agent := decodeAgent(t, recorder)
	if agent.System != (AgentSystemInfo{}) {
		t.Errorf("expected an empty system info, got %+v", agent.System)
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
			})
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

	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/register", RegisterRequest{
		AgentID:  "agent-1",
		Hostname: "host-1",
		Version:  "1.0.0",
		System:   &system,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}

	recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents", nil)
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

	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/register", RegisterRequest{
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
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}

	updated := AgentSystemInfo{
		OS:            "linux",
		Arch:          "amd64",
		CPUCount:      16,
		MemoryBytes:   32 * 1024 * 1024 * 1024,
		DockerVersion: "25.0.1",
	}

	recorder = doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{
		Status: AgentStatus{Uptime: 30, RunningServices: 1},
		System: &updated,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil)
	agent := decodeAgent(t, recorder)
	if agent.System != updated {
		t.Errorf("expected refreshed system info %+v, got %+v", updated, agent.System)
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

	recorder := doRequest(t, router, http.MethodPost, "/api/v1/agents/register", RegisterRequest{
		AgentID:  "agent-1",
		Hostname: "host-1",
		Version:  "1.0.0",
		System:   &system,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, recorder.Code, recorder.Body.String())
	}

	recorder = doRequest(t, router, http.MethodPost, "/api/v1/agents/agent-1/heartbeat", HeartbeatRequest{
		Status: AgentStatus{Uptime: 30},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	recorder = doRequest(t, router, http.MethodGet, "/api/v1/agents/agent-1", nil)
	agent := decodeAgent(t, recorder)
	if agent.System != system {
		t.Errorf("expected system info to be preserved, got %+v", agent.System)
	}
}
