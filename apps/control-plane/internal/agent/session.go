package agent

import (
	"crypto/subtle"
	"strings"
)

// sessionTokenFromHeader extracts the bearer token from an Authorization
// header. The scheme is matched case-insensitively ("Bearer" and "bearer" are
// both accepted, as required by RFC 6750), and an empty token is rejected.
func sessionTokenFromHeader(header string) (string, bool) {
	const prefix = "Bearer "

	if len(header) <= len(prefix) {
		return "", false
	}
	if !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}

	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// sessionTokenEqual compares two credentials in constant time.
func sessionTokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// authorizeAgentSession checks the credential in the Authorization header
// against the session token the control plane issued to the named agent.
//
// The token is bound to exactly one agent, so a token issued to agent A can
// never act for agent B. This is what makes it safe to expose the command
// channel to agents.
//
// NOTE: the agent session token is also used by the heartbeat and agent
// endpoints. Those share the same credential model, but this helper exists so
// the command channel does not depend on that code path.
func authorizeAgentSession(store *AgentStore, agentID, header string) error {
	token, ok := sessionTokenFromHeader(header)
	if !ok {
		return ErrCommandUnauthorized
	}

	agent, err := store.GetAgent(agentID)
	if err != nil {
		return err
	}

	if !sessionTokenEqual(token, agent.SessionToken) {
		return ErrCommandUnauthorized
	}
	return nil
}
