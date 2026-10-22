// Package command implements the agent side of the control-plane command
// channel.
//
// The agent polls the control plane for commands, executes them against the
// runtime manager and reports the outcome. The control plane never connects
// to the agent, so agents keep working behind NAT and firewalls without
// exposing their local API.
//
// The agent's local API stays loopback-only and token-protected: it is not
// used by this package and is not exposed to the network.
package command

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/controlplane"
	"github.com/byoi-platform/agent/internal/docker"
	"github.com/byoi-platform/agent/internal/runtime"
)

// ServiceManager is the slice of runtime.Manager the command channel needs.
// Declaring an interface here keeps the command channel decoupled and
// unit-testable.
type ServiceManager interface {
	DeployService(ctx context.Context, spec docker.ServiceSpec) (*runtime.Service, error)
	StopService(ctx context.Context, name string) error
	RemoveService(ctx context.Context, name string) error
}

// CommandSource is the control-plane transport used by the runner.
type CommandSource interface {
	FetchCommand(ctx context.Context) (*controlplane.Command, error)
	ReportCommandResult(ctx context.Context, result controlplane.CommandResult) error
}

// serviceRef is the payload of the stop and remove commands.
type serviceRef struct {
	Name string `json:"name"`
}

// Runner polls the control plane for commands and executes them.
type Runner struct {
	source   CommandSource
	services ServiceManager
	interval time.Duration
	log      *logrus.Logger
}

// NewRunner creates a command runner. A non-positive interval falls back to
// the default.
func NewRunner(source CommandSource, services ServiceManager, interval time.Duration, log *logrus.Logger) *Runner {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return &Runner{
		source:   source,
		services: services,
		interval: interval,
		log:      log,
	}
}

// DefaultPollInterval is how often the agent asks the control plane for work.
const DefaultPollInterval = 10 * time.Second

// Run polls for commands until the context is cancelled.
//
// A failing poll never stops the loop: the control plane may be restarting,
// unreachable or temporarily out of commands.
func (r *Runner) Run(ctx context.Context) {
	r.log.WithField("interval", r.interval).Info("Starting command channel")

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		r.Poll(ctx)

		select {
		case <-ctx.Done():
			r.log.Debug("Command channel stopping due to context cancellation")
			return
		case <-ticker.C:
		}
	}
}

// Poll fetches and executes at most one command. It is exported so the loop
// can be driven deterministically in tests.
func (r *Runner) Poll(ctx context.Context) {
	command, err := r.source.FetchCommand(ctx)
	if err != nil {
		r.log.WithError(err).Error("Failed to fetch command from control plane")
		return
	}
	if command == nil {
		return
	}

	r.log.WithFields(logrus.Fields{
		"command_id": command.ID,
		"type":       command.Type,
	}).Info("Executing command")

	result := r.execute(ctx, command)

	if err := r.source.ReportCommandResult(ctx, result); err != nil {
		r.log.WithError(err).WithField("command_id", command.ID).
			Error("Failed to report command result")
	}
}

// execute runs a single command and returns its result. It never panics: a
// command that cannot be executed is reported as failed.
func (r *Runner) execute(ctx context.Context, command *controlplane.Command) controlplane.CommandResult {
	result := controlplane.CommandResult{
		CommandID:   command.ID,
		Status:      controlplane.CommandStatusSucceeded,
		CompletedAt: time.Now().UTC(),
	}

	// The control plane expires commands it hands out too late, but the agent
	// checks as well: a command that is stale when it arrives must not touch
	// the host.
	if command.ExpiresAt != nil && time.Now().After(*command.ExpiresAt) {
		result.Status = controlplane.CommandStatusFailed
		result.Error = "command expired before it was executed"
		return result
	}

	switch command.Type {
	case controlplane.CommandTypeDeploy:
		var spec docker.ServiceSpec
		if err := json.Unmarshal(command.Payload, &spec); err != nil {
			result.Status = controlplane.CommandStatusFailed
			result.Error = fmt.Sprintf("invalid deploy payload: %v", err)
			return result
		}
		service, err := r.services.DeployService(ctx, spec)
		if err != nil {
			result.Status = controlplane.CommandStatusFailed
			result.Error = err.Error()
			return result
		}
		result.Message = fmt.Sprintf("service %s deployed", service.Spec.Name)

	case controlplane.CommandTypeStop:
		ref, err := decodeServiceRef(command.Payload)
		if err != nil {
			result.Status = controlplane.CommandStatusFailed
			result.Error = err.Error()
			return result
		}
		if err := r.services.StopService(ctx, ref.Name); err != nil {
			result.Status = controlplane.CommandStatusFailed
			result.Error = err.Error()
			return result
		}
		result.Message = fmt.Sprintf("service %s stopped", ref.Name)

	case controlplane.CommandTypeRemove:
		ref, err := decodeServiceRef(command.Payload)
		if err != nil {
			result.Status = controlplane.CommandStatusFailed
			result.Error = err.Error()
			return result
		}
		if err := r.services.RemoveService(ctx, ref.Name); err != nil {
			result.Status = controlplane.CommandStatusFailed
			result.Error = err.Error()
			return result
		}
		result.Message = fmt.Sprintf("service %s removed", ref.Name)

	default:
		result.Status = controlplane.CommandStatusFailed
		result.Error = fmt.Sprintf("unsupported command type %q", command.Type)
	}

	return result
}

// decodeServiceRef decodes the payload of the stop and remove commands.
func decodeServiceRef(payload json.RawMessage) (*serviceRef, error) {
	var ref serviceRef
	if err := json.Unmarshal(payload, &ref); err != nil {
		return nil, fmt.Errorf("invalid payload: %v", err)
	}
	if ref.Name == "" {
		return nil, fmt.Errorf("payload field \"name\" is required")
	}
	return &ref, nil
}
