package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	sdk "github.com/byoi-platform/stellar"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func TestLinkAndGetDeploymentEscrow(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx:alpine"}`),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create deployment: expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	deployment := decodeDeployment(t, rec)

	rec = doJSON(t, r, http.MethodGet, "/api/v1/deployments/"+deployment.ID+"/escrow", testOperatorToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("unlinked status: expected 409, got %d (%s)", rec.Code, rec.Body.String())
	}

	escrowID := uint64(42)
	rec = doJSON(t, r, http.MethodPut, "/api/v1/deployments/"+deployment.ID+"/escrow", testOperatorToken,
		LinkDeploymentEscrowRequest{EscrowID: &escrowID})
	if rec.Code != http.StatusOK {
		t.Fatalf("link escrow: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var linked DeploymentEscrowLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &linked); err != nil {
		t.Fatalf("decode link response: %v", err)
	}
	if linked.DeploymentID != deployment.ID || linked.EscrowID != escrowID {
		t.Errorf("link response = %+v", linked)
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/deployments/"+deployment.ID+"/escrow", testOperatorToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var status DeploymentEscrowStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if status.DeploymentID != deployment.ID || status.EscrowID != escrowID {
		t.Errorf("status response = %+v", status)
	}
	if status.Status != sdk.EscrowStatusPending || status.Network != sdk.NetworkTestnet {
		t.Errorf("status = %+v", status)
	}
	if status.CheckedAt.IsZero() {
		t.Error("checked_at must be set")
	}

	// Re-linking the same id is idempotent; a different id is refused.
	rec = doJSON(t, r, http.MethodPut, "/api/v1/deployments/"+deployment.ID+"/escrow", testOperatorToken,
		LinkDeploymentEscrowRequest{EscrowID: &escrowID})
	if rec.Code != http.StatusOK {
		t.Fatalf("relink same escrow: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	otherID := uint64(43)
	rec = doJSON(t, r, http.MethodPut, "/api/v1/deployments/"+deployment.ID+"/escrow", testOperatorToken,
		LinkDeploymentEscrowRequest{EscrowID: &otherID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("relink different escrow: expected 409, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/deployments/"+deployment.ID, testOperatorToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get deployment: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeDeployment(t, rec); got.EscrowID == nil || *got.EscrowID != escrowID {
		t.Errorf("deployment escrow_id = %v, want %d", got.EscrowID, escrowID)
	}
}

func TestCreateDeploymentWithEscrowID(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)

	escrowID := uint64(7)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service:  json.RawMessage(`{"name":"web","image":"nginx:alpine"}`),
		EscrowID: &escrowID,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeDeployment(t, rec); got.EscrowID == nil || *got.EscrowID != escrowID {
		t.Errorf("escrow_id = %v, want %d", got.EscrowID, escrowID)
	}
}

func TestLinkDeploymentEscrow_Validation(t *testing.T) {
	r, store, _ := testDeploymentRouter(t)
	registerDeploymentAgent(t, store, "platform-1", OwnerTypePlatform)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/deployments", testOperatorToken, CreateDeploymentRequest{
		Service: json.RawMessage(`{"name":"web","image":"nginx:alpine"}`),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create deployment: expected 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	deployment := decodeDeployment(t, rec)

	rec = doJSON(t, r, http.MethodPut, "/api/v1/deployments/"+deployment.ID+"/escrow", testOperatorToken,
		LinkDeploymentEscrowRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing escrow_id: expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}

	escrowID := uint64(1)
	rec = doJSON(t, r, http.MethodPut, "/api/v1/deployments/missing/escrow", testOperatorToken,
		LinkDeploymentEscrowRequest{EscrowID: &escrowID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing deployment: expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodGet, "/api/v1/deployments/missing/escrow", testOperatorToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing deployment status: expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestGetDeploymentEscrow_ProviderError(t *testing.T) {
	r, deployments := deploymentEscrowTestRouter(t, &fakeEscrowStatusProvider{
		err: errors.New("soroban rpc unavailable"),
	})

	escrowID := uint64(9)
	deployment, err := deployments.Create(Deployment{
		AgentID: "platform-1", ServiceName: "web", Image: "nginx", EscrowID: &escrowID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rec := doJSON(t, r, http.MethodGet, "/api/v1/deployments/"+deployment.ID+"/escrow", testOperatorToken, nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestGetDeploymentEscrow_DisabledWithoutProvider(t *testing.T) {
	r, deployments := deploymentEscrowTestRouter(t, nil)

	escrowID := uint64(9)
	deployment, err := deployments.Create(Deployment{
		AgentID: "platform-1", ServiceName: "web", Image: "nginx", EscrowID: &escrowID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rec := doJSON(t, r, http.MethodGet, "/api/v1/deployments/"+deployment.ID+"/escrow", testOperatorToken, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func deploymentEscrowTestRouter(t *testing.T, provider EscrowStatusProvider) (*gin.Engine, *DeploymentStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	deployments := NewDeploymentStore()
	handler := NewDeploymentHandler(NewAgentStore(), deployments, NewCommandQueue(0, 0),
		NewSchedulerWithOptions(NewAgentStore(), time.Minute, time.Now), testOperatorToken, log)
	if provider != nil {
		handler.WithEscrowProvider(provider, sdk.NetworkTestnet)
	}

	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.GET("/deployments/:deploymentID/escrow", handler.RequireOperatorToken(), handler.GetDeploymentEscrow)

	return r, deployments
}
