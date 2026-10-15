package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/config"
	"github.com/byoi-platform/agent/internal/controlplane"
)

// Agent represents the main agent orchestrator
type Agent struct {
	config         *config.Config
	controlPlane   *controlplane.Client
	log            *logrus.Logger
	heartbeatStop  chan struct{}
	heartbeatDone  sync.WaitGroup
	registrationID string
}

// New creates a new agent instance
func New(cfg *config.Config, log *logrus.Logger) *Agent {
	controlPlaneClient := controlplane.NewClient(cfg.ControlPlaneURL, cfg.AgentToken, log)

	return &Agent{
		config:        cfg,
		controlPlane:  controlPlaneClient,
		log:           log,
		heartbeatStop: make(chan struct{}),
	}
}

// Start initializes the agent, registers with control plane, and starts background services
func (a *Agent) Start(ctx context.Context) error {
	a.log.WithFields(logrus.Fields{
		"config": a.config.String(),
	}).Info("Starting Usenova Agent")

	// Register with control plane
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	// Start heartbeat loop
	a.startHeartbeat(ctx)

	// TODO: Initialize other agent components
	// - Docker integration
	// - Service manager
	// - Health check system
	// - Metrics collection

	a.log.Info("Usenova Agent started successfully")
	return nil
}

// Stop gracefully shuts down the agent
func (a *Agent) Stop(ctx context.Context) error {
	a.log.Info("Stopping Usenova Agent...")

	// Stop heartbeat
	close(a.heartbeatStop)

	// Wait for heartbeat goroutine to finish with timeout
	done := make(chan struct{})
	go func() {
		a.heartbeatDone.Wait()
		close(done)
	}()

	select {
	case <-done:
		a.log.Debug("Heartbeat stopped successfully")
	case <-ctx.Done():
		a.log.Warn("Heartbeat stop timed out")
	case <-time.After(5 * time.Second):
		a.log.Warn("Heartbeat stop timed out after 5 seconds")
	}

	// TODO: Cleanup other resources
	// - Stop running services
	// - Close Docker connections
	// - Cleanup temporary files

	a.log.Info("Usenova Agent stopped")
	return nil
}

// register handles the registration process with retry logic
func (a *Agent) register(ctx context.Context) error {
	const maxRetries = 3
	const retryDelay = 5 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		a.log.WithField("attempt", attempt).Info("Attempting to register with control plane")

		response, err := a.controlPlane.Register(ctx, a.config.AgentID, a.config.Version)
		if err != nil {
			if attempt == maxRetries {
				return fmt.Errorf("registration failed after %d attempts: %w", maxRetries, err)
			}

			a.log.WithFields(logrus.Fields{
				"error":    err,
				"attempt":  attempt,
				"retry_in": retryDelay,
			}).Warn("Registration attempt failed, retrying...")

			// Wait before retry
			select {
			case <-time.After(retryDelay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Success
		a.registrationID = response.AgentID
		a.log.WithFields(logrus.Fields{
			"agent_id": response.AgentID,
			"message":  response.Message,
		}).Info("Successfully registered with control plane")
		return nil
	}

	return fmt.Errorf("registration failed after %d attempts", maxRetries)
}

// startHeartbeat starts the background heartbeat process
func (a *Agent) startHeartbeat(parentCtx context.Context) {
	a.heartbeatDone.Add(1)

	go func() {
		defer a.heartbeatDone.Done()

		ticker := time.NewTicker(a.config.HeartbeatInterval)
		defer ticker.Stop()

		a.log.WithField("interval", a.config.HeartbeatInterval).Info("Starting heartbeat loop")

		for {
			select {
			case <-ticker.C:
				// Create a context with timeout for each heartbeat
				ctx, cancel := context.WithTimeout(parentCtx, 15*time.Second)

				if err := a.controlPlane.SendHeartbeat(ctx); err != nil {
					a.log.WithError(err).Error("Failed to send heartbeat")
					// Continue running despite heartbeat failures
					// TODO: Implement exponential backoff or circuit breaker
				}

				cancel()

			case <-a.heartbeatStop:
				a.log.Debug("Heartbeat loop stopping...")
				return

			case <-parentCtx.Done():
				a.log.Debug("Heartbeat loop stopping due to context cancellation")
				return
			}
		}
	}()
}

// GetAgentID returns the registered agent ID
func (a *Agent) GetAgentID() string {
	return a.registrationID
}

// GetStatus returns current agent status
func (a *Agent) GetStatus() map[string]interface{} {
	// TODO: Implement comprehensive status reporting
	return map[string]interface{}{
		"agent_id":   a.registrationID,
		"registered": a.registrationID != "",
		"version":    a.config.Version,
	}
}
