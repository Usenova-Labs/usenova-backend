package agent

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Handler handles agent-related HTTP requests
type Handler struct {
	store  *AgentStore
	logger *logrus.Logger
}

// NewHandler creates a new agent handler
func NewHandler(store *AgentStore, logger *logrus.Logger) *Handler {
	return &Handler{
		store:  store,
		logger: logger,
	}
}

// RegisterAgent handles POST /api/v1/agents/register
func (h *Handler) RegisterAgent(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.WithError(err).Warn("Invalid registration request")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: err.Error(),
		})
		return
	}

	// Validate required fields
	if req.Hostname == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Hostname is required",
		})
		return
	}

	if req.Version == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Version is required",
		})
		return
	}

	// The machine inventory is optional, but the values it carries must be
	// sensible when it is reported.
	if req.System != nil {
		if req.System.CPUCount < 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: "CPU count must not be negative",
			})
			return
		}
		if req.System.MemoryBytes < 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: "Memory size must not be negative",
			})
			return
		}
	}

	h.logger.WithFields(logrus.Fields{
		"agent_id":     req.AgentID,
		"hostname":     req.Hostname,
		"version":      req.Version,
		"capabilities": req.Capabilities,
		"system":       req.System,
	}).Info("Registering agent")

	// Register agent in store
	registration := AgentRegistration{
		AgentID:      req.AgentID,
		Hostname:     req.Hostname,
		Version:      req.Version,
		Capabilities: req.Capabilities,
	}
	if req.System != nil {
		registration.System = *req.System
	}

	agent, err := h.store.RegisterAgent(registration)
	if err != nil {
		h.logger.WithError(err).Error("Failed to register agent")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to register agent",
		})
		return
	}

	// Prepare response
	resp := RegisterResponse{
		AgentID:      agent.AgentID,
		SessionToken: agent.SessionToken,
		Message:      "Agent registered successfully",
	}

	h.logger.WithField("agent_id", agent.AgentID).Info("Agent registered successfully")
	c.JSON(http.StatusCreated, resp)
}

// HandleHeartbeat handles POST /api/v1/agents/:agentID/heartbeat
func (h *Handler) HandleHeartbeat(c *gin.Context) {
	agentID := c.Param("agentID")

	var req HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.WithError(err).Warn("Invalid heartbeat request")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: err.Error(),
		})
		return
	}

	h.logger.WithFields(logrus.Fields{
		"agent_id": agentID,
		"uptime":   req.Status.Uptime,
		"services": req.Status.RunningServices,
	}).Debug("Received heartbeat")

	// Update heartbeat in store
	err := h.store.UpdateHeartbeat(agentID, req.Status)
	if err != nil {
		if err == ErrAgentNotFound {
			h.logger.WithField("agent_id", agentID).Warn("Agent not found for heartbeat")
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Agent not found",
			})
			return
		}

		h.logger.WithError(err).Error("Failed to update heartbeat")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to update heartbeat",
		})
		return
	}

	// Refresh the machine inventory when the agent reports one, so dynamic
	// values stay current without re-registering.
	if req.System != nil {
		if err := h.store.UpdateSystemInfo(agentID, *req.System); err != nil {
			if err == ErrAgentNotFound {
				h.logger.WithField("agent_id", agentID).Warn("Agent not found for heartbeat")
				c.JSON(http.StatusNotFound, ErrorResponse{
					Error: "Agent not found",
				})
				return
			}

			h.logger.WithError(err).Error("Failed to update agent inventory")
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error: "Failed to update heartbeat",
			})
			return
		}
	}

	resp := HeartbeatResponse{
		Status:  "ok",
		Message: "Heartbeat received",
	}

	c.JSON(http.StatusOK, resp)
}

// ListAgents handles GET /api/v1/agents (bonus endpoint)
func (h *Handler) ListAgents(c *gin.Context) {
	agents := h.store.ListAgents()

	resp := AgentListResponse{
		Agents: agents,
		Total:  len(agents),
	}

	h.logger.WithField("count", len(agents)).Info("Listed agents")
	c.JSON(http.StatusOK, resp)
}

// GetAgent handles GET /api/v1/agents/:agentID (bonus endpoint)
func (h *Handler) GetAgent(c *gin.Context) {
	agentID := c.Param("agentID")

	agent, err := h.store.GetAgent(agentID)
	if err != nil {
		if err == ErrAgentNotFound {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Agent not found",
			})
			return
		}

		h.logger.WithError(err).Error("Failed to get agent")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to get agent",
		})
		return
	}

	c.JSON(http.StatusOK, agent)
}
