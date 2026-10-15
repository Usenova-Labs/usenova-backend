package controlplane

import "time"

// AgentCapabilities represents what this agent can do
type AgentCapabilities struct {
	Docker bool `json:"docker"`
	// TODO: Add more capabilities as needed
	// Kubernetes bool `json:"kubernetes"`
	// Systemd    bool `json:"systemd"`
}

// RegisterRequest is sent to register this agent with the control plane
type RegisterRequest struct {
	AgentID      string            `json:"agent_id,omitempty"`
	Hostname     string            `json:"hostname"`
	Version      string            `json:"version"`
	Capabilities AgentCapabilities `json:"capabilities"`
}

// RegisterResponse is received after successful registration
type RegisterResponse struct {
	AgentID      string `json:"agent_id"`
	SessionToken string `json:"session_token,omitempty"`
	Message      string `json:"message,omitempty"`
}

// AgentStatus represents the current status of the agent
type AgentStatus struct {
	Uptime          time.Duration `json:"uptime"`
	RunningServices int           `json:"running_services"`
	// TODO: Add more status fields as needed
	// MemoryUsage     int64 `json:"memory_usage"`
	// CPUUsage        float64 `json:"cpu_usage"`
}

// HeartbeatRequest is sent periodically to maintain agent session
type HeartbeatRequest struct {
	Status AgentStatus `json:"status"`
}

// HeartbeatResponse is received after heartbeat
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
