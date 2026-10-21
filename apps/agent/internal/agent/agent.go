package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/api"
	"github.com/byoi-platform/agent/internal/config"
	"github.com/byoi-platform/agent/internal/controlplane"
	"github.com/byoi-platform/agent/internal/docker"
	"github.com/byoi-platform/agent/internal/runtime"
	"github.com/byoi-platform/agent/internal/systeminfo"
)

// Agent represents the main agent orchestrator
type Agent struct {
	config         *config.Config
	controlPlane   *controlplane.Client
	dockerClient   *docker.Client
	runtimeMgr     *runtime.Manager
	apiSrv         *api.Server
	log            *logrus.Logger
	heartbeatStop  chan struct{}
	heartbeatDone  sync.WaitGroup
	registrationID string
}

// New creates a new agent instance
func New(cfg *config.Config, log *logrus.Logger) *Agent {
	controlPlaneClient := controlplane.NewClient(cfg.ControlPlaneURL, cfg.AgentToken, log)

	// Docker client. Failure here is non-fatal: the daemon may simply not be
	// running yet; capability detection and deploys will report/handle it.
	dockerClient, err := docker.NewClient(log)
	if err != nil {
		log.WithError(err).Warn("Failed to create Docker client; Docker features disabled")
		dockerClient = nil
	}

	var runtimeMgr *runtime.Manager
	if dockerClient != nil {
		runtimeMgr = runtime.NewManager(dockerClient, log)
		// Restart limits: keep a crash-looping service from being restarted
		// forever. Services may tighten or loosen this per service.
		runtimeMgr.SetRestartPolicy(runtime.RestartPolicy{
			MaxRestarts: cfg.RestartMaxRestarts,
			Window:      cfg.RestartWindow,
			Backoff:     cfg.RestartBackoff,
			MaxBackoff:  runtime.DefaultRestartMaxBackoff,
		})
	}

	// Feed real capability + service count into heartbeats/registration.
	controlPlaneClient.SetDockerDetector(func(ctx context.Context) bool {
		if dockerClient == nil {
			return false
		}
		return dockerClient.IsAvailable(ctx)
	})
	controlPlaneClient.SetServicesCounter(func() int {
		if runtimeMgr == nil {
			return 0
		}
		return runtimeMgr.RunningCount()
	})
	// Report the Docker engine version as part of the machine inventory.
	controlPlaneClient.SetDockerVersionDetector(func(ctx context.Context) string {
		if dockerClient == nil {
			return ""
		}
		version, err := dockerClient.ServerVersion(ctx)
		if err != nil {
			log.WithError(err).Debug("Failed to determine Docker version")
			return ""
		}
		return version
	})
	controlPlaneClient.SetFailedServicesCounter(func() int {
		if runtimeMgr == nil {
			return 0
		}
		return runtimeMgr.FailedCount()
	})

	return &Agent{
		config:        cfg,
		controlPlane:  controlPlaneClient,
		dockerClient:  dockerClient,
		runtimeMgr:    runtimeMgr,
		log:           log,
		heartbeatStop: make(chan struct{}),
	}
}

// Start initializes the agent, registers with control plane, and starts background services
func (a *Agent) Start(ctx context.Context) error {
	a.log.WithFields(logrus.Fields{
		"config": a.config.String(),
	}).Info("Starting Usenova Agent")

	// Start the runtime manager (health monitors + reconcile loop) before
	// registration so the first heartbeat already reports real counts.
	if a.runtimeMgr != nil {
		if err := a.runtimeMgr.Start(ctx); err != nil {
			return fmt.Errorf("failed to start runtime manager: %w", err)
		}
	} else {
		a.log.Warn("Runtime manager not available (no Docker client)")
	}

	// Start optional local control API (loopback-only, token required).
	if a.config.APIAddr != "" && a.runtimeMgr != nil {
		srv, err := api.NewServer(a.config.APIAddr, a.config.APIToken, a.runtimeMgr, a.log)
		if err != nil {
			return fmt.Errorf("failed to configure local control API: %w", err)
		}
		if err := srv.Start(); err != nil {
			return fmt.Errorf("failed to start local control API: %w", err)
		}
		a.apiSrv = srv
	} else if a.config.APIAddr != "" && a.runtimeMgr == nil {
		a.log.Warn("Local control API requested but runtime manager unavailable; skipping API start")
	}

	// Register with control plane
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	// Start heartbeat loop
	a.startHeartbeat(ctx)

	// TODO: Metrics collection, control-plane deploy command channel

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

	// Stop local control API first to release network resources.
	if a.apiSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := a.apiSrv.Shutdown(shutdownCtx); err != nil {
			a.log.WithError(err).Warn("Local control API shutdown failed")
		}
		cancel()
	}

	// Stop health monitors and reconcile loop.
	// Managed containers are left running: the Docker restart policy keeps
	// them alive across agent restarts. TODO: optional graceful "stop
	// services on agent shutdown" mode via config.
	if a.runtimeMgr != nil {
		a.runtimeMgr.Stop(ctx)
	}

	// Close Docker connection
	if a.dockerClient != nil {
		if err := a.dockerClient.Close(); err != nil {
			a.log.WithError(err).Warn("Failed to close Docker client")
		}
	}

	a.log.Info("Usenova Agent stopped")
	return nil
}

// RuntimeManager exposes the service lifecycle manager (for CLI / future
// control-plane deploy commands).
func (a *Agent) RuntimeManager() *runtime.Manager {
	return a.runtimeMgr
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
	running := 0
	failed := 0
	if a.runtimeMgr != nil {
		running = a.runtimeMgr.RunningCount()
		failed = a.runtimeMgr.FailedCount()
	}

	dockerAvailable := false
	if a.dockerClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		dockerAvailable = a.dockerClient.IsAvailable(ctx)
		cancel()
	}

	system := systeminfo.Detect()

	return map[string]interface{}{
		"agent_id":         a.registrationID,
		"registered":       a.registrationID != "",
		"version":          a.config.Version,
		"docker_available": dockerAvailable,
		"running_services": running,
		"os":               system.OS,
		"arch":             system.Arch,
		"cpu_count":        system.CPUCount,
		"memory_bytes":     system.MemoryBytes,
		"failed_services":  failed,
	}
}
