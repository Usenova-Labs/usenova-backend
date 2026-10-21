package agent

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// sessionTokenContextKey is where the authenticated agent ID is stashed.
const sessionTokenContextKey = "agent_id"

// RequireAgentAuth returns middleware that authenticates a request against the
// session token issued at registration. The agent ID is read from the :agentID
// path parameter and the bearer token from the Authorization header; a missing
// or invalid token is rejected with 401.
//
// Used to protect agent-scoped routes (heartbeat, status). Registration itself
// must stay unauthenticated: that is where the token is first issued.
func (h *Handler) RequireAgentAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		agentID := c.Param("agentID")
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			h.logger.WithField("agent_id", agentID).Warn("Missing agent authorization header")
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "Missing or malformed Authorization header",
			})
			return
		}

		if err := h.store.ValidateSessionToken(agentID, token); err != nil {
			h.logger.WithFields(map[string]interface{}{
				"agent_id": agentID,
				"error":    err.Error(),
			}).Warn("Rejected agent request with invalid session token")
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: "Invalid session token",
			})
			return
		}

		c.Set(sessionTokenContextKey, agentID)
		c.Next()
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header, returning false if the scheme is missing or not Bearer.
func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}
