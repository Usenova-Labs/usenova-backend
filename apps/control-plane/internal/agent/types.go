package agent

import "encoding/json"

// RegisterRequest represents the request body for agent registration
type RegisterRequest struct {
	AgentID      string            `json:"agent_id,omitempty"`
	Hostname     string            `json:"hostname"`
	Version      string            `json:"version"`
	Capabilities AgentCapabilities `json:"capabilities"`
}

// RegisterResponse represents the response for agent registration
type RegisterResponse struct {
	AgentID      string `json:"agent_id"`
	SessionToken string `json:"session_token,omitempty"`
	Message      string `json:"message,omitempty"`
}

// HeartbeatRequest represents the request body for agent heartbeat
type HeartbeatRequest struct {
	Status AgentStatus `json:"status"`
}

// HeartbeatResponse represents the response for agent heartbeat
type HeartbeatResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ErrorResponse represents API error responses
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

// AgentListResponse represents response for listing all agents
type AgentListResponse struct {
	Agents []*Agent `json:"agents"`
	Total  int      `json:"total"`
}

// EnqueueCommandRequest represents the operator's request to run a command on
// an agent.
type EnqueueCommandRequest struct {
	// Type is the operation to perform: deploy, stop or remove.
	Type CommandType `json:"type"`
	// Payload is the command-specific input. Its schema is owned by the
	// agent; the control plane only checks that the obvious fields are
	// present.
	Payload json.RawMessage `json:"payload,omitempty"`
	// ExpiresIn is the command lifetime in seconds. Zero means the default
	// lifetime, negative values are rejected.
	ExpiresIn int `json:"expires_in_seconds,omitempty"`
}

// CommandListResponse represents the commands queued for an agent.
type CommandListResponse struct {
	Commands []*CommandRecord `json:"commands"`
	Total    int              `json:"total"`
}
