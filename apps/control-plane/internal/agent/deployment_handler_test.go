package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/byoi-platform/stellar"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

type fakeEscrowStatusProvider struct {
	status sdk.EscrowStatus
	err    error
	gotID  uint64
	calls  int
}

func (f *fakeEscrowStatusProvider) GetEscrowStatus(_ context.Context, escrowID uint64) (sdk.EscrowStatus, error) {
	f.calls++
	f.gotID = escrowID
	if f.err != nil {
		return "", f.err
	}
	return f.status, nil
}

func testDeploymentRouter(t *testing.T) (*gin.Engine, *AgentStore, *DeploymentStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	store := NewAgentStore()
	deployments := NewDeploymentStore()
	queue := NewCommandQueue(0, 0)
	scheduler := NewSchedulerWithOptions(store, time.Minute, time.Now)
	deploymentHandler := NewDeploymentHandler(store, deployments, queue, scheduler, testOperatorToken, log).
		WithEscrowProvider(&fakeEscrowStatusProvider{status: sdk.EscrowStatusPending}, sdk.NetworkTestnet)
	commandHandler := NewCommandHandler(store, queue, testOperatorToken, log).WithDeploymentStore(deployments)

	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.POST("/deployments", deploymentHandler.RequireOperatorToken(), deploymentHandler.CreateDeployment)
	v1.GET("/deployments", deploymentHandler.RequireOperatorToken(), deploymentHandler.ListDeployments)
	v1.GET("/deployments/:deploymentID", deploymentHandler.RequireOperatorToken(), deploymentHandler.GetDeployment)
	v1.PUT("/deployments/:deploymentID/escrow", deploymentHandler.RequireOperatorToken(), deploymentHandler.LinkDeploymentEscrow)
	v1.GET("/deployments/:deploymentID/escrow", deploymentHandler.RequireOperatorToken(), deploymentHandler.GetDeploymentEscrow)
	v1.POST("/scheduler/select", deploymentHandler.RequireOperatorToken(), deploymentHandler.SelectAgent)
	v1.GET("/agents/:agentID/commands/next", commandHandler.RequireAgentSession(), commandHandler.ClaimCommand)
	v1.POST("/agents/:agentID/commands/:commandID/result", commandHandler.RequireAgentSession(), commandHandler.CompleteCommand)

	return r, store, deployments
}

func registerDeploymentAgent(t *testing.T, store *AgentStore, id string, owner OwnerType) *Agent {
	t.Helper()
	agent, err := store.RegisterAgent(AgentRegistration{
		AgentID:      id,
		Hostname:     "host-" + id,
		Version:      "1.0.0",
		Capabilities: AgentCapabilities{Docker: true},
		System: AgentSystemInfo{
			OS:          "linux",
			Arch:        "amd64",
			CPUCount:    8,
			MemoryBytes: 16 << 30,
		},
		OwnerType: owner,
	})
	if err != nil {
		t.Fatalf("RegisterAgent(%q): %v", id, err)
	}
	return agent
}

func decodeDeployment(t *testing.T, recorder *httptest.ResponseRecorder) Deployment {
	t.Helper()
	var deployment Deployment
	if err := json.Unmarshal(recorder.Body.Bytes(), &deployment); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	return deployment
}

func TestCreateDeployment_SchedulesAgentAndTracksCommand(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	agent := registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx:alpine"}`),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}

	deployment := decodeDeployment(t, rec)
	if deployment.ID == "" || deployment.CommandID == "" {
		t.Fatalf("deployment missing IDs: %+v", deployment)
	}
	if deployment.AgentID != agent.AgentID {
		t.Errorf("agent = %q, want %q", deployment.AgentID, agent.AgentID)
	}
	if deployment.State != DeploymentStatePending {
		t.Errorf("state = %q, want pending", deployment.State)
	}

	// The agent claims the command.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/agents/platform-1/commands/next", agent.SessionToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var command Command
	if err := json.Unmarshal(rec.Body.Bytes(), &command); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	if command.ID != deployment.CommandID {
		t.Errorf("command = %q, want %q", command.ID, deployment.CommandID)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/deployments/"+deployment.ID, testOperatorToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get running: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeDeployment(t, rec); got.State != DeploymentStateRunning {
		t.Errorf("state after claim = %q, want running", got.State)
	}

	// The agent reports success.
	rec = doJSON(t, r, http.MethodPost,
		"/api/v1/agents/platform-1/commands/"+command.ID+"/result", agent.SessionToken,
		CommandResult{Status: CommandStatusSucceeded, Message: "deployed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/deployments/"+deployment.ID, testOperatorToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get succeeded: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeDeployment(t, rec); got.State != DeploymentStateSucceeded {
		t.Errorf("state after result = %q, want succeeded", got.State)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/deployments?state=succeeded", testOperatorToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var list DeploymentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || list.Deployments[0].ID != deployment.ID {
		t.Errorf("list = %+v", list)
	}
}

func TestCreateDeployment_NoCapacity(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	registerDeploymentAgent(t, store, "user-1", OwnerTypeUser)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx"}`),
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "capacity") {
		t.Errorf("error should mention capacity: %s", rec.Body.String())
	}
}

func TestCreateDeployment_RequiresOperatorToken(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", "", CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx"}`),
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreateDeployment_ValidatesService(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web"}`),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreateDeployment_ExplicitAgent(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)
	registerDeploymentAgent(t, store, "user-1", OwnerTypeUser)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx"}`),
		AgentID: "platform-1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeDeployment(t, rec); got.AgentID != "platform-1" {
		t.Errorf("agent = %q, want platform-1", got.AgentID)
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx"}`),
		AgentID: "user-1",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a user agent, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx"}`),
		AgentID: "missing",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a missing agent, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestSchedulerSelectEndpoint(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	agent := registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/scheduler/select", testOperatorToken, ResourceHints{})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var selection SchedulerSelection
	if err := json.Unmarshal(rec.Body.Bytes(), &selection); err != nil {
		t.Fatalf("decode selection: %v", err)
	}
	if selection.AgentID != agent.AgentID {
		t.Errorf("selected %q, want %q", selection.AgentID, agent.AgentID)
	}
	if strings.Contains(rec.Body.String(), "session_token") {
		t.Errorf("selection leaked the session token: %s", rec.Body.String())
	}
}

func TestCreateDeployment_TracksFailure(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	agent := registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx"}`),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	deployment := decodeDeployment(t, rec)

	rec = doJSON(t, r, http.MethodGet, "/api/v1/agents/platform-1/commands/next", agent.SessionToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var command Command
	if err := json.Unmarshal(rec.Body.Bytes(), &command); err != nil {
		t.Fatalf("decode command: %v", err)
	}

	rec = doJSON(t, r, http.MethodPost,
		"/api/v1/agents/platform-1/commands/"+command.ID+"/result", agent.SessionToken,
		CommandResult{Status: CommandStatusFailed, Error: "image pull failed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/deployments/"+deployment.ID, testOperatorToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeDeployment(t, rec); got.State != DeploymentStateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
}

func TestGetDeployment_NotFound(t *testing.T) {
	r, _, _ := testDeploymentRouter(t)

	rec := doJSON(t, r, http.MethodGet, "/api/v1/deployments/missing", testOperatorToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestListDeployments_InvalidState(t *testing.T) {
	r, _, _ := testDeploymentRouter(t)

	rec := doJSON(t, r, http.MethodGet, "/api/v1/deployments?state=bogus", testOperatorToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}
