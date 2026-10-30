package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	sdk "github.com/byoi-platform/stellar"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// CreateDeploymentRequest is an operator's request to deploy a service to the
// platform fleet.
type CreateDeploymentRequest struct {
	// Service is the full agent service spec. The control plane validates the
	// identifying fields and forwards the payload unchanged.
	Service json.RawMessage `json:"service"`
	// AgentID optionally targets a specific platform agent. When empty the
	// scheduler picks one.
	AgentID string `json:"agent_id,omitempty"`
	// Requester is optional audit metadata (for example a user or automation
	// identifier).
	Requester string `json:"requester,omitempty"`
	// ResourceHints constrain scheduler placement.
	ResourceHints ResourceHints `json:"resource_hints,omitempty"`
	// EscrowID optionally links the new deployment to an existing on-chain
	// escrow. Creating the escrow itself is out of scope.
	EscrowID *uint64 `json:"escrow_id,omitempty"`
}

// DeploymentListResponse is the deployment listing payload.
type DeploymentListResponse struct {
	Deployments []*Deployment `json:"deployments"`
	Total       int           `json:"total"`
}

// DeploymentHandler serves the control-plane deployment API.
type DeploymentHandler struct {
	store         *AgentStore
	deployments   *DeploymentStore
	queue         *CommandQueue
	scheduler     *Scheduler
	operatorToken string
	logger        *logrus.Logger

	// escrowProvider is optional. When nil, escrow linking still works but
	// status reads answer 503 because no Soroban contract is configured.
	escrowProvider EscrowStatusProvider
	escrowNetwork  sdk.Network
}

// NewDeploymentHandler creates a deployment handler.
func NewDeploymentHandler(
	store *AgentStore,
	deployments *DeploymentStore,
	queue *CommandQueue,
	scheduler *Scheduler,
	operatorToken string,
	logger *logrus.Logger,
) *DeploymentHandler {
	return &DeploymentHandler{
		store:         store,
		deployments:   deployments,
		queue:         queue,
		scheduler:     scheduler,
		operatorToken: operatorToken,
		logger:        logger,
	}
}

// RequireOperatorToken rejects operator-facing deployment calls that do not
// carry the operator token.
func (h *DeploymentHandler) RequireOperatorToken() gin.HandlerFunc {
	return requireOperatorToken(h.operatorToken, h.logger)
}

// WithEscrowProvider enables the read-only escrow status endpoint. Passing a
// nil provider leaves status reads disabled.
func (h *DeploymentHandler) WithEscrowProvider(provider EscrowStatusProvider, network sdk.Network) *DeploymentHandler {
	h.escrowProvider = provider
	h.escrowNetwork = network
	return h
}

// CreateDeployment handles POST /api/v1/deployments.
//
// It selects a platform agent (unless one is supplied), enqueues the existing
// remote deploy command and persists a deployment record linked to it.
func (h *DeploymentHandler) CreateDeployment(c *gin.Context) {
	var req CreateDeploymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: err.Error(),
		})
		return
	}

	if err := validateCommandPayload(CommandTypeDeploy, req.Service); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid service spec",
			Message: err.Error(),
		})
		return
	}
	serviceName, image, err := deploymentServiceDetails(req.Service)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid service spec",
			Message: err.Error(),
		})
		return
	}

	agent, err := h.selectAgent(req)
	if err != nil {
		h.writeSelectionError(c, err)
		return
	}

	record, err := h.queue.Enqueue(agent.AgentID, Command{
		Type:    CommandTypeDeploy,
		Payload: req.Service,
	})
	if err != nil {
		if errors.Is(err, ErrCommandQueueFull) {
			h.logger.WithField("agent_id", agent.AgentID).Warn("Command queue is full")
			c.JSON(http.StatusTooManyRequests, ErrorResponse{
				Error:   "Command queue is full",
				Message: "the selected agent has too many commands queued; retry later",
			})
			return
		}
		h.logger.WithError(err).Error("Failed to enqueue deployment command")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to create deployment",
		})
		return
	}

	deployment, err := h.deployments.Create(Deployment{
		AgentID:     agent.AgentID,
		ServiceName: serviceName,
		Image:       image,
		State:       DeploymentStatePending,
		CommandID:   record.Command.ID,
		Requester:   req.Requester,
		EscrowID:    req.EscrowID,
	})
	if err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"agent_id":   agent.AgentID,
			"command_id": record.Command.ID,
		}).Error("Failed to persist deployment record")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to create deployment",
		})
		return
	}

	h.logger.WithFields(logrus.Fields{
		"deployment_id": deployment.ID,
		"agent_id":      deployment.AgentID,
		"command_id":    deployment.CommandID,
		"service":       deployment.ServiceName,
	}).Info("Deployment created")

	c.JSON(http.StatusAccepted, deployment)
}

func (h *DeploymentHandler) selectAgent(req CreateDeploymentRequest) (*Agent, error) {
	if req.AgentID == "" {
		return h.scheduler.Select(req.ResourceHints)
	}

	agent, err := h.store.GetAgent(req.AgentID)
	if err != nil {
		return nil, err
	}
	if agent.OwnerType != OwnerTypePlatform {
		return nil, ErrAgentNotPlatform
	}
	return agent, nil
}

func (h *DeploymentHandler) writeSelectionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNoCapacity):
		h.logger.Warn("Remote deployment rejected: no platform capacity")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "No platform capacity available",
			Message: "no healthy platform agent with enough capacity is available; retry later",
		})
	case errors.Is(err, ErrInvalidResourceHints):
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid resource hints",
			Message: err.Error(),
		})
	case errors.Is(err, ErrAgentNotFound):
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "Agent not found",
		})
	case errors.Is(err, ErrAgentNotPlatform):
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid target agent",
			Message: "remote deployments can only target platform agents",
		})
	default:
		h.logger.WithError(err).Error("Failed to select deployment agent")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to select deployment agent",
		})
	}
}

// ListDeployments handles GET /api/v1/deployments.
//
// Supported filters are agent_id and state.
func (h *DeploymentHandler) ListDeployments(c *gin.Context) {
	filter := DeploymentFilter{AgentID: c.Query("agent_id")}
	if raw := c.Query("state"); raw != "" {
		filter.State = DeploymentState(raw)
		if !filter.State.Valid() {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "Invalid deployment state",
				Message: "state must be pending, running, succeeded or failed",
			})
			return
		}
	}

	deployments := h.deployments.List(filter)
	c.JSON(http.StatusOK, DeploymentListResponse{
		Deployments: deployments,
		Total:       len(deployments),
	})
}

// GetDeployment handles GET /api/v1/deployments/:deploymentID.
func (h *DeploymentHandler) GetDeployment(c *gin.Context) {
	deployment, err := h.deployments.Get(c.Param("deploymentID"))
	if err != nil {
		if errors.Is(err, ErrDeploymentNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Deployment not found",
			})
			return
		}
		h.logger.WithError(err).Error("Failed to get deployment")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to get deployment",
		})
		return
	}

	c.JSON(http.StatusOK, deployment)
}

// SelectAgent handles POST /api/v1/scheduler/select.
//
// It exposes the scheduler policy independently of deployment creation so
// operators can inspect placement. The body is optional resource hints.
func (h *DeploymentHandler) SelectAgent(c *gin.Context) {
	var hints ResourceHints
	if err := c.ShouldBindJSON(&hints); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: err.Error(),
		})
		return
	}

	agent, err := h.scheduler.Select(hints)
	if err != nil {
		h.writeSelectionError(c, err)
		return
	}

	c.JSON(http.StatusOK, agent.Selection())
}

// deploymentServiceDetails extracts the identifying fields from a deploy
// payload. validateCommandPayload has already checked that both are present
// and non-empty.
func deploymentServiceDetails(payload json.RawMessage) (name, image string, err error) {
	var spec struct {
		Name  string `json:"name"`
		Image string `json:"image"`
	}
	if err := json.Unmarshal(payload, &spec); err != nil {
		return "", "", fmt.Errorf("service spec must be a JSON object: %w", err)
	}
	return spec.Name, spec.Image, nil
}

// ErrAgentNotPlatform means a deployment was explicitly targeted at an agent
// that does not belong to the platform fleet.
var ErrAgentNotPlatform = &AgentError{"agent is not a platform agent"}
