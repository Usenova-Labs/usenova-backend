package client

import (
	"encoding/json"
	"time"
)

// ListResponse is the agent's GET /services payload.
type ListResponse struct {
	Running  int       `json:"running"`
	Failed   int       `json:"failed"`
	Services []Service `json:"services"`
}

// ServiceSpec mirrors the snake_case spec fields the agent returns.
type ServiceSpec struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Ports         map[string]string `json:"ports,omitempty"`
	Env           []string          `json:"env,omitempty"`
	Volumes       []string          `json:"volumes,omitempty"`
	HealthCheck   *HealthCheck      `json:"health_check,omitempty"`
	RestartPolicy string            `json:"restart_policy,omitempty"`

	MaxRestarts           int `json:"max_restarts,omitempty"`
	RestartWindowSeconds  int `json:"restart_window_seconds,omitempty"`
	RestartBackoffSeconds int `json:"restart_backoff_seconds,omitempty"`
}

// HealthCheck mirrors the agent-side health check configuration.
type HealthCheck struct {
	Type string `json:"type,omitempty"`
	Path string `json:"path,omitempty"`
	Port int    `json:"port,omitempty"`
}

// Service is the agent's representation of a managed service. The original
// payload is retained so JSON output mode can replay it byte-for-byte,
// including fields the CLI does not model (Health, LastError).
type Service struct {
	Spec         ServiceSpec `json:"Spec"`
	ContainerID  string      `json:"ContainerID"`
	State        string      `json:"State"`
	DesiredState string      `json:"DesiredState"`
	RestartCount int         `json:"RestartCount"`
	DeployedAt   time.Time   `json:"DeployedAt"`

	// Restart breaker state exposed by newer agents. These are optional:
	// older agents omit them and the CLI falls back to RestartCount.
	RestartCountInWindow   int    `json:"restart_count,omitempty"`
	RestartBudgetRemaining int    `json:"restart_budget_remaining,omitempty"`
	RestartWindowSeconds   int    `json:"restart_window_seconds,omitempty"`
	FailedReason           string `json:"failed_reason,omitempty"`

	raw json.RawMessage
}

// UnmarshalJSON decodes the agent payload and keeps a copy for MarshalJSON.
func (s *Service) UnmarshalJSON(data []byte) error {
	type view Service
	var v view
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*s = Service(v)
	s.raw = append([]byte(nil), data...)
	return nil
}

// MarshalJSON replays the original agent payload when available so JSON
// output is identical to what the agent sent.
func (s *Service) MarshalJSON() ([]byte, error) {
	if len(s.raw) > 0 {
		return s.raw, nil
	}
	type view Service
	return json.Marshal(view(*s))
}
