package agent

// RegisterRequest represents the request body for agent registration
type RegisterRequest struct {
	AgentID      string            `json:"agent_id,omitempty"`
	Hostname     string            `json:"hostname"`
	Version      string            `json:"version"`
	Capabilities AgentCapabilities `json:"capabilities"`
	// System is the machine inventory reported by the agent. It is optional
	// so agents that predate the inventory keep registering successfully.
	System *AgentSystemInfo `json:"system,omitempty"`
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
	// System refreshes the machine inventory recorded at registration time.
	// It is optional: agents that do not report it keep the inventory the
	// control plane already holds.
	System *AgentSystemInfo `json:"system,omitempty"`
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
