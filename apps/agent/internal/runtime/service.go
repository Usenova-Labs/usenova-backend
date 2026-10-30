package runtime

import (
	"context"
	"encoding/json"
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
	// ServiceStateRemoved means removal was requested; ownership is retained
	// until the container is actually gone so it cannot escape management.
	ServiceStateRemoved ServiceState = "removed"
)

// RestartPolicy constants (mirror docker policies for agent-side decisions)
const (
	RestartPolicyNo            = "no"
	RestartPolicyAlways        = "always"
	RestartPolicyOnFailure     = "on-failure"
	RestartPolicyUnlessStopped = "unless-stopped"
)

// FailedReasonCircuitBreaker is reported in the service status when the agent
// stopped restarting a service because it exhausted its restart budget.
const FailedReasonCircuitBreaker = "circuit_breaker"

// Service is the agent's representation of a managed service: the desired
// spec plus the observed container state.
type Service struct {
	// Spec is the desired configuration
	Spec docker.ServiceSpec

	// ContainerID is the ID of the container currently backing this service
	ContainerID string

	// State is the last observed state
	State ServiceState

	// DesiredState is the intent: running / stopped / removed. Reconciliation
	// drives the observed state toward this value.
	DesiredState ServiceState

	// Health is the derived agent-side health check configuration (nil if none)
	Health *health.CheckConfig

	// DeployedAt is when the current container was created
	DeployedAt time.Time

	// RestartCount counts agent-initiated restarts (health or crash)
	RestartCount int

	// RestartAttempts counts restart attempts inside the current restart
	// window. It is what the restart breaker compares against its limit.
	RestartAttempts int

	// RestartBudgetRemaining is how many restart attempts are left in the
	// current window. A negative value means restarts are unlimited.
	RestartBudgetRemaining int `json:"restart_budget_remaining"`

	// RestartWindowSeconds is the sliding window used by the restart breaker.
	RestartWindowSeconds int `json:"restart_window_seconds"`

	// FailedReason explains why the service was marked failed. It is empty
	// unless a specific breaker decision caused the failure.
	FailedReason string `json:"failed_reason,omitempty"`

	// LastRestartAt is when the last restart attempt was made
	LastRestartAt time.Time

	// LastError holds the last error observed for this service, if any
	LastError error

	// generation is bumped on every intent change (deploy/stop/remove). Long
	// running operations capture it and only commit if it is still current,
	// which fences stop/remove against in-flight deploys.
	generation uint64

	// cancel cancels the in-flight deploy for this incarnation, if any.
	cancel context.CancelFunc

	// restart is the circuit breaker that bounds how often this service may
	// be restarted. It is nil only for services that were never deployed.
	restart *restartBreaker

	// restartExhausted marks a service that used up its restart budget. The
	// reconcile loop leaves it alone: it stays failed until it is deployed
	// again.
	restartExhausted bool

	// restartPendingSeq is the sequence number of the deferred restart that
	// is currently queued for this service (0 means none is queued). The
	// number identifies the queued attempt, so a goroutine that finishes late
	// cannot clear a newer pending attempt.
	restartPendingSeq uint64
}

// MarshalJSON keeps the original PascalCase fields for backward compatibility
// while exposing the restart breaker state under the snake_case names used by
// the status contract.
func (s Service) MarshalJSON() ([]byte, error) {
	type alias Service
	return json.Marshal(&struct {
		*alias
		RestartCount int `json:"restart_count"`
	}{
		alias:        (*alias)(&s),
		RestartCount: s.RestartAttempts,
	})
}

// ShortID returns a shortened container ID for logging
func (s *Service) ShortID() string {
	if len(s.ContainerID) > 12 {
		return s.ContainerID[:12]
	}
	return s.ContainerID
}
