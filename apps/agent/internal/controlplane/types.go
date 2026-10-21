package controlplane

import (
	"encoding/json"
	"time"
)

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

// Command types the control plane can send to an agent.
const (
	// CommandTypeDeploy asks the agent to deploy a service.
	CommandTypeDeploy = "deploy"
	// CommandTypeStop asks the agent to stop a running service.
	CommandTypeStop = "stop"
	// CommandTypeRemove asks the agent to remove a service.
	CommandTypeRemove = "remove"
)

// Command result statuses.
const (
	// CommandStatusSucceeded means the command was executed successfully.
	CommandStatusSucceeded = "succeeded"
	// CommandStatusFailed means the command could not be executed.
	CommandStatusFailed = "failed"
)

// Command is a unit of work issued by the control plane. The payload is
// command-type specific and owned by the agent.
type Command struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
}

// CommandResult is the outcome of a command executed by the agent.
type CommandResult struct {
	CommandID   string    `json:"command_id"`
	Status      string    `json:"status"`
	Message     string    `json:"message,omitempty"`
	Error       string    `json:"error,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}
