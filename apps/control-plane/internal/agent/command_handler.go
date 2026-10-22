package agent

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// CommandHandler serves the control-plane command channel.
//
// Two credential classes meet here:
//   - operators (CLI, dashboard, automation) enqueue commands and read their
//     status; they authenticate with the operator token;
//   - agents claim the commands queued for them and report the outcome; they
//     authenticate with the session token issued at registration.
//
// The control plane never calls into an agent: agents poll. That keeps the
// channel usable from behind NAT and keeps the agent's local API untouched.
type CommandHandler struct {
	store  *AgentStore
	queue  *CommandQueue
	logger *logrus.Logger

	// operatorToken authenticates operator-facing calls. When it is empty the
	// operator-facing part of the command API is disabled: an unauthenticated
	// control plane must never be able to enqueue a deployment.
	operatorToken string
}

// NewCommandHandler creates a command handler.
func NewCommandHandler(store *AgentStore, queue *CommandQueue, operatorToken string, logger *logrus.Logger) *CommandHandler {
	return &CommandHandler{
		store:         store,
		queue:         queue,
		logger:        logger,
		operatorToken: operatorToken,
	}
}

// RequireOperatorToken rejects operator-facing calls that do not carry the
// operator token. When no operator token is configured the operator-facing
// part of the command API is disabled entirely.
func (h *CommandHandler) RequireOperatorToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.operatorToken == "" {
			// The startup log already warns about this; logging every
			// request would only add noise.
			h.logger.Debug("Command API is disabled: no operator token configured")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, ErrorResponse{
				Error:   "Command API is disabled",
				Message: "the control plane has no operator token configured",
			})
			return
		}

		token, ok := sessionTokenFromHeader(c.GetHeader("Authorization"))
		if !ok || !sessionTokenEqual(token, h.operatorToken) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "Unauthorized",
			})
			return
		}

		c.Next()
	}
}

// RequireAgentSession rejects requests that do not carry the session token of
// the agent named in the path.
func (h *CommandHandler) RequireAgentSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		err := authorizeAgentSession(h.store, c.Param("agentID"), c.GetHeader("Authorization"))
		switch {
		case err == nil:
			c.Next()
			return
		case errors.Is(err, ErrAgentNotFound):
			h.logger.WithField("agent_id", c.Param("agentID")).Warn("Unknown agent requested a command")
			c.AbortWithStatusJSON(http.StatusNotFound, ErrorResponse{
				Error: "Agent not found",
			})
		default:
			h.logger.WithField("agent_id", c.Param("agentID")).Warn("Rejected command channel request")
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "Unauthorized",
			})
		}
	}
}

// EnqueueCommand handles POST /api/v1/agents/:agentID/commands.
//
// It queues a command for an agent. The agent picks it up on its next poll.
func (h *CommandHandler) EnqueueCommand(c *gin.Context) {
	agentID := c.Param("agentID")

	if _, err := h.store.GetAgent(agentID); err != nil {
		h.logger.WithField("agent_id", agentID).Warn("Cannot enqueue command for unknown agent")
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: "Agent not found",
		})
		return
	}

	var req EnqueueCommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.WithError(err).Warn("Invalid command request")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: err.Error(),
		})
		return
	}

	if err := validateCommandType(req.Type); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Unsupported command type",
			Message: err.Error(),
		})
		return
	}

	if err := validateCommandPayload(req.Type, req.Payload); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid payload",
			Message: err.Error(),
		})
		return
	}

	if req.ExpiresIn < 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid expiry",
			Message: "expires_in_seconds must not be negative",
		})
		return
	}
	if req.ExpiresIn > int(MaxCommandTTL/time.Second) {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid expiry",
			Message: fmt.Sprintf("expires_in_seconds must not exceed %d", int(MaxCommandTTL/time.Second)),
		})
		return
	}

	now := time.Now().UTC()
	cmd := Command{
		Type:      req.Type,
		Payload:   req.Payload,
		CreatedAt: now,
	}
	if req.ExpiresIn > 0 {
		expiresAt := now.Add(time.Duration(req.ExpiresIn) * time.Second)
		cmd.ExpiresAt = &expiresAt
	}

	record, err := h.queue.Enqueue(agentID, cmd)
	if err != nil {
		if errors.Is(err, ErrCommandQueueFull) {
			h.logger.WithField("agent_id", agentID).Warn("Command queue is full")
			c.JSON(http.StatusTooManyRequests, ErrorResponse{
				Error:   "Command queue is full",
				Message: "the agent has too many commands queued; retry later",
			})
			return
		}
		h.logger.WithError(err).Error("Failed to enqueue command")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to enqueue command",
		})
		return
	}

	h.logger.WithFields(logrus.Fields{
		"agent_id":   agentID,
		"command_id": record.Command.ID,
		"type":       record.Command.Type,
	}).Info("Command enqueued")

	c.JSON(http.StatusAccepted, record)
}

// ListCommands handles GET /api/v1/agents/:agentID/commands.
func (h *CommandHandler) ListCommands(c *gin.Context) {
	agentID := c.Param("agentID")

	records := h.queue.List(agentID)
	c.JSON(http.StatusOK, CommandListResponse{
		Commands: records,
		Total:    len(records),
	})
}

// GetCommand handles GET /api/v1/agents/:agentID/commands/:commandID.
func (h *CommandHandler) GetCommand(c *gin.Context) {
	agentID := c.Param("agentID")
	commandID := c.Param("commandID")

	record, err := h.queue.Get(agentID, commandID)
	if err != nil {
		if errors.Is(err, ErrCommandNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Command not found",
			})
			return
		}
		h.logger.WithError(err).Error("Failed to get command")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to get command",
		})
		return
	}

	c.JSON(http.StatusOK, record)
}

// ClaimCommand handles GET /api/v1/agents/:agentID/commands/next.
//
// It hands the oldest pending command to the agent and answers 204 when
// there is nothing to do.
func (h *CommandHandler) ClaimCommand(c *gin.Context) {
	agentID := c.Param("agentID")

	record, err := h.queue.Next(agentID)
	if err != nil {
		if errors.Is(err, ErrNoPendingCommand) {
			c.Status(http.StatusNoContent)
			return
		}
		h.logger.WithError(err).Error("Failed to claim command")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to claim command",
		})
		return
	}

	h.logger.WithFields(logrus.Fields{
		"agent_id":   agentID,
		"command_id": record.Command.ID,
		"type":       record.Command.Type,
	}).Info("Command claimed")

	c.JSON(http.StatusOK, record.Command)
}

// CompleteCommand handles POST /api/v1/agents/:agentID/commands/:commandID/result.
//
// It records the outcome of a command. Reporting the same command twice is
// not an error: the first result wins.
func (h *CommandHandler) CompleteCommand(c *gin.Context) {
	agentID := c.Param("agentID")
	commandID := c.Param("commandID")

	var result CommandResult
	if err := c.ShouldBindJSON(&result); err != nil {
		h.logger.WithError(err).Warn("Invalid command result")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request body",
			Message: err.Error(),
		})
		return
	}

	if result.Status != CommandStatusSucceeded && result.Status != CommandStatusFailed {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid status",
			Message: "status must be \"succeeded\" or \"failed\"",
		})
		return
	}

	result.CommandID = commandID
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now().UTC()
	}

	record, err := h.queue.Complete(agentID, commandID, result)
	if err != nil {
		if errors.Is(err, ErrCommandNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Command not found",
			})
			return
		}
		h.logger.WithError(err).Error("Failed to complete command")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to complete command",
		})
		return
	}

	h.logger.WithFields(logrus.Fields{
		"agent_id":   agentID,
		"command_id": commandID,
		"status":     record.Status,
	}).Info("Command completed")

	c.JSON(http.StatusOK, record)
}
