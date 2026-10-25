package agent

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Handler handles agent-related HTTP requests
type Handler struct {
	store  *AgentStore
	logger *logrus.Logger

	// platformBootstrapToken authenticates the platform registration
	// endpoint. When empty, platform registration is disabled.
	platformBootstrapToken string
}

// NewHandler creates a new agent handler without platform registration.
func NewHandler(store *AgentStore, logger *logrus.Logger) *Handler {
	return NewHandlerWithPlatformToken(store, "", logger)
}

// NewHandlerWithPlatformToken creates an agent handler that accepts platform
// registrations authenticated by platformBootstrapToken.
func NewHandlerWithPlatformToken(store *AgentStore, platformBootstrapToken string, logger *logrus.Logger) *Handler {
	return &Handler{
		store:                  store,
		logger:                 logger,
		platformBootstrapToken: platformBootstrapToken,
	}
}

// RegisterAgent handles POST /api/v1/agents/register.
//
// This is the BYOI registration path: ownership metadata in the body is
// ignored and every agent registered here belongs to a user.
func (h *Handler) RegisterAgent(c *gin.Context) {
	h.registerAgent(c, OwnerTypeUser, false)
}

// RegisterPlatformAgent handles POST /api/v1/agents/register/platform.
//
// It is separate from the default registration path so a BYOI agent can never
// claim platform ownership. The caller must present the platform bootstrap
// token in the X-Platform-Bootstrap-Token header.
func (h *Handler) RegisterPlatformAgent(c *gin.Context) {
	if !h.requirePlatformBootstrapToken(c) {
		return
	}
	h.registerAgent(c, OwnerTypePlatform, true)
}

func (h *Handler) requirePlatformBootstrapToken(c *gin.Context) bool {
	if h.platformBootstrapToken == "" {
		h.logger.Debug("Platform registration is disabled: no platform token configured")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "Platform registration is disabled",
			Message: "the control plane has no platform bootstrap token configured",
		})
		return false
	}

	token := c.GetHeader("X-Platform-Bootstrap-Token")
	if token == "" || !sessionTokenEqual(token, h.platformBootstrapToken) {
		h.logger.Warn("Rejected platform registration with invalid bootstrap token")
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error: "Unauthorized",
		})
		return false
	}
	return true
}

func (h *Handler) registerAgent(c *gin.Context, ownerType OwnerType, useOwnership bool) {
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
	if h.rejectInvalidSystemInfo(c, req.System) {
		return
	}

	registration := AgentRegistration{
		AgentID:      req.AgentID,
		Hostname:     req.Hostname,
		Version:      req.Version,
		Capabilities: req.Capabilities,
		OwnerType:    ownerType,
	}
	if req.System != nil {
		registration.System = *req.System
	}
	if useOwnership {
		for key := range req.Labels {
			if strings.TrimSpace(key) == "" {
				c.JSON(http.StatusBadRequest, ErrorResponse{
					Error: "Label keys must not be empty",
				})
				return
			}
		}
		registration.OwnerID = req.OwnerID
		registration.Labels = req.Labels
	}

	h.logger.WithFields(logrus.Fields{
		"agent_id":     req.AgentID,
		"hostname":     req.Hostname,
		"version":      req.Version,
		"capabilities": req.Capabilities,
		"system":       req.System,
		"owner_type":   ownerType,
		"owner_id":     registration.OwnerID,
	}).Info("Registering agent")

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

	h.logger.WithFields(logrus.Fields{
		"agent_id":   agent.AgentID,
		"owner_type": agent.OwnerType,
	}).Info("Agent registered successfully")
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

	// The machine inventory is optional, but the values it carries must be
	// sensible when it is reported. An invalid inventory is rejected before
	// the store is touched.
	if h.rejectInvalidSystemInfo(c, req.System) {
		return
	}

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

// rejectInvalidSystemInfo validates a reported machine inventory and answers
// with 400 when it is not sensible.
//
// The inventory is optional: a nil inventory is accepted as is. It reports
// whether the request was rejected.
func (h *Handler) rejectInvalidSystemInfo(c *gin.Context, system *AgentSystemInfo) bool {
	if system == nil {
		return false
	}

	var err error
	switch {
	case system.CPUCount < 0:
		err = errors.New("CPU count must not be negative")
	case system.MemoryBytes < 0:
		err = errors.New("Memory size must not be negative")
	default:
		return false
	}

	h.logger.WithError(err).Warn("Invalid agent inventory")
	c.JSON(http.StatusBadRequest, ErrorResponse{
		Error: err.Error(),
	})

	return true
}

// ListAgents handles GET /api/v1/agents.
//
// Supported filters are owner_type, owner_id and repeatable label filters
// such as ?label=region=us-east&label=pool=alpha. All supplied filters must
// match.
func (h *Handler) ListAgents(c *gin.Context) {
	filter, err := agentFilterFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid agent filter",
			Message: err.Error(),
		})
		return
	}

	agents := h.store.ListAgentsFiltered(filter)

	resp := AgentListResponse{
		Agents: agents,
		Total:  len(agents),
	}

	h.logger.WithField("count", len(agents)).Info("Listed agents")
	c.JSON(http.StatusOK, resp)
}

func agentFilterFromQuery(c *gin.Context) (AgentFilter, error) {
	filter := AgentFilter{
		OwnerID: c.Query("owner_id"),
	}

	if raw := c.Query("owner_type"); raw != "" {
		filter.OwnerType = OwnerType(raw)
		if !filter.OwnerType.Valid() {
			return AgentFilter{}, errors.New("owner_type must be user or platform")
		}
	}

	labelFilters := append(c.QueryArray("label"), c.QueryArray("labels")...)
	for _, raw := range labelFilters {
		for _, pair := range strings.Split(raw, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			key, value, ok := strings.Cut(pair, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				return AgentFilter{}, errors.New("labels must use key=value format")
			}
			if filter.Labels == nil {
				filter.Labels = make(map[string]string)
			}
			filter.Labels[key] = strings.TrimSpace(value)
		}
	}

	return filter, nil
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
