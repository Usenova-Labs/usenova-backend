package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

const testOperatorToken = "operator-secret"

func testCommandRouter(t *testing.T) (*gin.Engine, *AgentStore, *CommandQueue) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	store := NewAgentStore()
	queue := NewCommandQueue(0, 0)
	handler := NewCommandHandler(store, queue, testOperatorToken, log)

	r := gin.New()
	agents := r.Group("/api/v1/agents")
	agents.POST("/:agentID/commands", handler.RequireOperatorToken(), handler.EnqueueCommand)
	agents.GET("/:agentID/commands", handler.RequireOperatorToken(), handler.ListCommands)
	agents.GET("/:agentID/commands/next", handler.RequireAgentSession(), handler.ClaimCommand)
	agents.GET("/:agentID/commands/:commandID", handler.RequireOperatorToken(), handler.GetCommand)
	agents.POST("/:agentID/commands/:commandID/result", handler.RequireAgentSession(), handler.CompleteCommand)

	return r, store, queue
}

// registerTestAgent registers an agent and returns its session token.
func registerTestAgent(t *testing.T, store *AgentStore, agentID string) string {
	t.Helper()

	a, err := store.RegisterAgent(agentID, "host-1", "1.0.0", AgentCapabilities{})
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	return a.SessionToken
}

func doJSON(t *testing.T, r *gin.Engine, method, path, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestEnqueueCommand_RequiresOperatorToken(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", "", EnqueueCommandRequest{
		Type:    CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api","image":"nginx"}`),
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", "wrong-token", EnqueueCommandRequest{
		Type:    CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api","image":"nginx"}`),
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with a wrong token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestEnqueueCommand_DisabledWithoutOperatorToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	store := NewAgentStore()
	handler := NewCommandHandler(store, NewCommandQueue(0, 0), "", log)

	r := gin.New()
	r.POST("/api/v1/agents/:agentID/commands", handler.RequireOperatorToken(), handler.EnqueueCommand)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", testOperatorToken, EnqueueCommandRequest{
		Type:    CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api","image":"nginx"}`),
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no operator token is configured, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestEnqueueCommand_ValidatesPayload(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", testOperatorToken, EnqueueCommandRequest{
		Type:    CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api"}`),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an incomplete payload, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", testOperatorToken, EnqueueCommandRequest{
		Type: "restart",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unsupported command type, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestEnqueueCommand_ExpiresIn(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", testOperatorToken, EnqueueCommandRequest{
		Type:      CommandTypeStop,
		Payload:   json.RawMessage(`{"name":"api"}`),
		ExpiresIn: 60,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}

	var record CommandRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record.Command.ExpiresAt == nil {
		t.Fatal("expected an expiry to be set")
	}
	if time.Until(*record.Command.ExpiresAt) > time.Minute {
		t.Errorf("expected the command to expire within a minute, got %v", record.Command.ExpiresAt)
	}
}

func TestEnqueueCommand_RejectsInvalidExpiry(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", testOperatorToken, EnqueueCommandRequest{
		Type:      CommandTypeStop,
		Payload:   json.RawMessage(`{"name":"api"}`),
		ExpiresIn: -1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a negative expiry, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", testOperatorToken, EnqueueCommandRequest{
		Type:      CommandTypeStop,
		Payload:   json.RawMessage(`{"name":"api"}`),
		ExpiresIn: int(MaxCommandTTL/time.Second) + 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an expiry beyond the maximum, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestEnqueueCommand_UnknownAgent(t *testing.T) {
	r, _, _ := testCommandRouter(t)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/ghost/commands", testOperatorToken, EnqueueCommandRequest{
		Type:    CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api","image":"nginx"}`),
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown agent, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestClaimCommand_RequiresAgentSessionToken(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodGet, "/api/v1/agents/agent-1/commands/next", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/agents/agent-1/commands/next", "not-the-token", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with a wrong token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestClaimCommand_RejectsTokenOfAnotherAgent(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	tokenA := registerTestAgent(t, store, "agent-a")
	registerTestAgent(t, store, "agent-b")

	rec := doJSON(t, r, http.MethodGet, "/api/v1/agents/agent-b/commands/next", tokenA, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token of another agent, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestCommandFlow_EnqueueClaimComplete(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	agentToken := registerTestAgent(t, store, "agent-1")

	// 1. The operator enqueues a deploy command.
	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", testOperatorToken, EnqueueCommandRequest{
		Type:    CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api","image":"nginx"}`),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}

	var record CommandRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record.Command.ID == "" {
		t.Fatal("expected a command ID")
	}
	if record.Status != CommandStatusPending {
		t.Errorf("expected status %q, got %q", CommandStatusPending, record.Status)
	}

	// 2. The agent claims it.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/agents/agent-1/commands/next", agentToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var command Command
	if err := json.Unmarshal(rec.Body.Bytes(), &command); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	if command.ID != record.Command.ID {
		t.Errorf("expected command %q, got %q", record.Command.ID, command.ID)
	}
	if command.Type != CommandTypeDeploy {
		t.Errorf("expected type %q, got %q", CommandTypeDeploy, command.Type)
	}

	// 3. The agent reports the outcome.
	rec = doJSON(t, r, http.MethodPost,
		"/api/v1/agents/agent-1/commands/"+command.ID+"/result", agentToken,
		CommandResult{Status: CommandStatusSucceeded, Message: "service api deployed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 4. The operator reads the status.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/agents/agent-1/commands/"+command.ID, testOperatorToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record.Status != CommandStatusSucceeded {
		t.Errorf("expected status %q, got %q", CommandStatusSucceeded, record.Status)
	}
	if record.Result == nil || record.Result.Message != "service api deployed" {
		t.Errorf("expected the result to be recorded, got %+v", record.Result)
	}
}

func TestClaimCommand_NoPendingCommand(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	agentToken := registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodGet, "/api/v1/agents/agent-1/commands/next", agentToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestCompleteCommand_RejectsInvalidStatus(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	agentToken := registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands/cmd-1/result", agentToken,
		CommandResult{Status: "running"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestCompleteCommand_UnknownCommand(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	agentToken := registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands/missing/result", agentToken,
		CommandResult{Status: CommandStatusSucceeded})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestListCommands(t *testing.T) {
	r, store, _ := testCommandRouter(t)
	registerTestAgent(t, store, "agent-1")

	rec := doJSON(t, r, http.MethodPost, "/api/v1/agents/agent-1/commands", testOperatorToken, EnqueueCommandRequest{
		Type:    CommandTypeStop,
		Payload: json.RawMessage(`{"name":"api"}`),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/agents/agent-1/commands", testOperatorToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var list CommandListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", list.Total)
	}
}

func TestSessionTokenFromHeader(t *testing.T) {
	cases := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"Bearer  abc ", "abc", true},
		{"Bearer  ", "", false},
		{"", "", false},
		{"Basic abc", "", false},
		{"abc", "", false},
	}

	for _, c := range cases {
		got, ok := sessionTokenFromHeader(c.header)
		if ok != c.ok || got != c.want {
			t.Errorf("sessionTokenFromHeader(%q) = (%q, %v), want (%q, %v)", c.header, got, ok, c.want, c.ok)
		}
	}
}
