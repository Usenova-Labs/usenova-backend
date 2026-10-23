package shared

// Common types and utilities shared across Usenova components

// ServiceStatus represents the current state of a service
type ServiceStatus string

const (
	ServiceStatusPending ServiceStatus = "pending"
	ServiceStatusRunning ServiceStatus = "running"
	ServiceStatusStopped ServiceStatus = "stopped"
	ServiceStatusFailed  ServiceStatus = "failed"
)

// AgentInfo contains information about a registered agent
type AgentInfo struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Region   string `json:"region"`
	Status   string `json:"status"`
}

// ServiceConfig defines configuration for a deployed service
type ServiceConfig struct {
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	Port     int               `json:"port"`
	Env      map[string]string `json:"env"`
	Replicas int               `json:"replicas"`
}
