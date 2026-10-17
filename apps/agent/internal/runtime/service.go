package runtime

import (
	"time"

	"github.com/byoi-platform/agent/internal/docker"
	"github.com/byoi-platform/agent/internal/health"
)

// ServiceState represents the desired+observed state of a managed service
type ServiceState string

const (
	// ServiceStatePending means the service has been accepted but not yet deployed
	ServiceStatePending ServiceState = "pending"
	// ServiceStateRunning means the container is deployed and running
	ServiceStateRunning ServiceState = "running"
	// ServiceStateStopped means the service was intentionally stopped
	ServiceStateStopped ServiceState = "stopped"
	// ServiceStateFailed means the last deploy/restart attempt failed
	ServiceStateFailed ServiceState = "failed"
	// ServiceStateUnhealthy means the container is running but failing health checks
	ServiceStateUnhealthy ServiceState = "unhealthy"
)

// RestartPolicy constants (mirror docker policies for agent-side decisions)
const (
	RestartPolicyNo            = "no"
	RestartPolicyAlways        = "always"
	RestartPolicyOnFailure     = "on-failure"
	RestartPolicyUnlessStopped = "unless-stopped"
)

// Service is the agent's representation of a managed service: the desired
// spec plus the observed container state.
type Service struct {
	// Spec is the desired configuration
	Spec docker.ServiceSpec

	// ContainerID is the ID of the container currently backing this service
	ContainerID string

	// State is the last observed state
	State ServiceState

	// Health is the derived agent-side health check configuration (nil if none)
	Health *health.CheckConfig

	// DeployedAt is when the current container was created
	DeployedAt time.Time

	// RestartCount counts agent-initiated restarts (health or crash)
	RestartCount int

	// LastError holds the last error observed for this service, if any
	LastError error
}

// ShortID returns a shortened container ID for logging
func (s *Service) ShortID() string {
	if len(s.ContainerID) > 12 {
		return s.ContainerID[:12]
	}
	return s.ContainerID
}
