package agent

import (
	"context"
	"errors"
	"net/http"
	"time"

	sdk "github.com/byoi-platform/stellar"
	"github.com/gin-gonic/gin"
)

// EscrowStatusProvider reads the on-chain status of a payment escrow.
// *stellar.Client satisfies it; tests inject a fake to avoid network access.
type EscrowStatusProvider interface {
	GetEscrowStatus(ctx context.Context, escrowID uint64) (sdk.EscrowStatus, error)
}

// LinkDeploymentEscrowRequest is the body for
// PUT /api/v1/deployments/:deploymentID/escrow.
type LinkDeploymentEscrowRequest struct {
	EscrowID *uint64 `json:"escrow_id"`
}

// DeploymentEscrowLinkResponse is returned after linking an escrow.
type DeploymentEscrowLinkResponse struct {
	DeploymentID string `json:"deployment_id"`
	EscrowID     uint64 `json:"escrow_id"`
	Message      string `json:"message,omitempty"`
}

// DeploymentEscrowStatusResponse is the payload for a deployment's escrow
// status query.
type DeploymentEscrowStatusResponse struct {
	DeploymentID string           `json:"deployment_id"`
	EscrowID     uint64           `json:"escrow_id"`
	Network      sdk.Network      `json:"network"`
	Status       sdk.EscrowStatus `json:"status"`
	CheckedAt    time.Time        `json:"checked_at"`
}

// LinkDeploymentEscrow handles
// PUT /api/v1/deployments/:deploymentID/escrow.
//
// It only records the association. Creating or mutating the escrow on-chain
// remains an explicit out-of-band action.
func (h *DeploymentHandler) LinkDeploymentEscrow(c *gin.Context) {
	var req LinkDeploymentEscrowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: err.Error(),
		})
		return
	}
	if req.EscrowID == nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: "escrow_id is required",
		})
		return
	}

	deployment, err := h.deployments.AttachEscrowID(c.Param("deploymentID"), *req.EscrowID)
	if err != nil {
		switch {
		case errors.Is(err, ErrDeploymentNotFound):
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "Deployment not found"})
		case errors.Is(err, ErrEscrowAlreadyLinked):
			c.JSON(http.StatusConflict, ErrorResponse{
				Error:   "Escrow already linked",
				Message: err.Error(),
			})
		default:
			h.logger.WithError(err).Error("Failed to link deployment escrow")
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error: "Failed to link escrow",
			})
		}
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"deployment_id": deployment.ID,
		"escrow_id":     *deployment.EscrowID,
	}).Info("Deployment linked to escrow")

	c.JSON(http.StatusOK, DeploymentEscrowLinkResponse{
		DeploymentID: deployment.ID,
		EscrowID:     *deployment.EscrowID,
		Message:      "Escrow linked successfully",
	})
}

// GetDeploymentEscrow handles
// GET /api/v1/deployments/:deploymentID/escrow.
//
// It reads the escrow status from the configured Stellar/Soroban contract.
// The control plane never submits transactions on this path.
func (h *DeploymentHandler) GetDeploymentEscrow(c *gin.Context) {
	deployment, err := h.deployments.Get(c.Param("deploymentID"))
	if err != nil {
		if errors.Is(err, ErrDeploymentNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "Deployment not found"})
			return
		}
		h.logger.WithError(err).Error("Failed to get deployment escrow")
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Failed to get deployment"})
		return
	}
	if deployment.EscrowID == nil {
		c.JSON(http.StatusConflict, ErrorResponse{
			Error:   "No escrow linked",
			Message: "link an escrow id to this deployment first",
		})
		return
	}
	if h.escrowProvider == nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "Escrow status is disabled",
			Message: "set PAYMENT_ESCROW_CONTRACT_ID to enable escrow status reads",
		})
		return
	}

	escrowID := *deployment.EscrowID
	status, err := h.escrowProvider.GetEscrowStatus(c.Request.Context(), escrowID)
	if err != nil {
		h.logger.WithError(err).WithFields(map[string]interface{}{
			"deployment_id": deployment.ID,
			"escrow_id":     escrowID,
			"network":       h.escrowNetwork,
		}).Error("Failed to fetch escrow status")
		c.JSON(http.StatusBadGateway, ErrorResponse{
			Error:   "Failed to fetch escrow status",
			Message: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, DeploymentEscrowStatusResponse{
		DeploymentID: deployment.ID,
		EscrowID:     escrowID,
		Network:      h.escrowNetwork,
		Status:       status,
		CheckedAt:    time.Now().UTC(),
	})
}
