package agent

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CommandType identifies the operation an agent has to perform.
type CommandType string

const (
	// CommandTypeDeploy asks the agent to deploy a service.
	CommandTypeDeploy CommandType = "deploy"
	// CommandTypeStop asks the agent to stop a running service.
	CommandTypeStop CommandType = "stop"
	// CommandTypeRemove asks the agent to remove a service.
	CommandTypeRemove CommandType = "remove"
)

// CommandStatus is the lifecycle state of a command.
type CommandStatus string

const (
	// CommandStatusPending means the command is queued and not yet claimed.
	CommandStatusPending CommandStatus = "pending"
	// CommandStatusRunning means an agent claimed the command.
	CommandStatusRunning CommandStatus = "running"
	// CommandStatusSucceeded means the agent executed the command successfully.
	CommandStatusSucceeded CommandStatus = "succeeded"
	// CommandStatusFailed means the agent could not execute the command.
	CommandStatusFailed CommandStatus = "failed"
)

// Command is a unit of work the control plane hands to an agent. The payload
// is opaque to the control plane: the agent owns its schema.
type Command struct {
	ID        string          `json:"id"`
	Type      CommandType     `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
}

// CommandResult is the outcome of a command executed by an agent.
type CommandResult struct {
	CommandID   string        `json:"command_id"`
	Status      CommandStatus `json:"status"`
	Message     string        `json:"message,omitempty"`
	Error       string        `json:"error,omitempty"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
}

// CommandRecord is the control plane's view of a command: the command itself
// plus its delivery state.
type CommandRecord struct {
	Command     Command        `json:"command"`
	Status      CommandStatus  `json:"status"`
	Result      *CommandResult `json:"result,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`

	// leaseExpiresAt is the moment a claimed command may be handed out again
	// because the agent never reported a result. It is internal state and not
	// part of the API payload.
	leaseExpiresAt time.Time
}

// terminal reports whether the command reached a final state.
func (r *CommandRecord) terminal() bool {
	return r.Status == CommandStatusSucceeded || r.Status == CommandStatusFailed
}

const (
	// DefaultMaxCommandDepth caps how many commands are retained per agent.
	// It bounds both the queue depth and the memory a single agent can use.
	DefaultMaxCommandDepth = 32
	// DefaultCommandLease is how long a claimed command stays leased before
	// the control plane hands it out again (the agent presumably died).
	DefaultCommandLease = 5 * time.Minute
	// DefaultCommandTTL is the default lifetime of a command that does not
	// specify one.
	DefaultCommandTTL = 30 * time.Minute
	// MaxCommandTTL caps the lifetime an operator may request.
	MaxCommandTTL = 24 * time.Hour
)

// CommandQueue stores the commands queued for each agent.
//
// Delivery is at-least-once: a command is handed out once, but if the agent
// does not report a result before the lease expires it becomes available
// again. Commands are therefore required to be idempotent.
type CommandQueue struct {
	mu       sync.Mutex
	commands map[string][]*CommandRecord
	maxDepth int
	lease    time.Duration
	now      func() time.Time
}

// NewCommandQueue creates a command queue. Non-positive values fall back to
// the defaults.
func NewCommandQueue(maxDepth int, lease time.Duration) *CommandQueue {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxCommandDepth
	}
	if lease <= 0 {
		lease = DefaultCommandLease
	}
	return &CommandQueue{
		commands: make(map[string][]*CommandRecord),
		maxDepth: maxDepth,
		lease:    lease,
		now:      time.Now,
	}
}

// Enqueue adds a command to an agent's queue. It returns an error when the
// queue is full so the caller can apply backpressure instead of growing the
// queue without bound.
func (q *CommandQueue) Enqueue(agentID string, cmd Command) (*CommandRecord, error) {
	if agentID == "" {
		return nil, &AgentError{Message: "agent id is required"}
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	records := q.commands[agentID]

	// Drop finished commands first: they are history, not pending work.
	records = pruneTerminal(records)
	if len(records) >= q.maxDepth {
		return nil, ErrCommandQueueFull
	}

	now := q.now().UTC()
	if cmd.ID == "" {
		cmd.ID = uuid.NewString()
	}
	if cmd.CreatedAt.IsZero() {
		cmd.CreatedAt = now
	}

	record := &CommandRecord{
		Command:   cmd,
		Status:    CommandStatusPending,
		CreatedAt: now,
	}
	q.commands[agentID] = append(records, record)
	return record, nil
}

// Next returns the next command for an agent and marks it as running. It
// returns ErrNoPendingCommand when there is nothing to do.
func (q *CommandQueue) Next(agentID string) (*CommandRecord, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.now().UTC()
	records := q.commands[agentID]

	for _, record := range records {
		if record.terminal() {
			continue
		}

		// A command nobody picked up in time is not worth executing anymore.
		if record.Status == CommandStatusPending && record.Command.ExpiresAt != nil && now.After(*record.Command.ExpiresAt) {
			record.Status = CommandStatusFailed
			record.CompletedAt = &now
			record.Result = &CommandResult{
				CommandID:   record.Command.ID,
				Status:      CommandStatusFailed,
				Error:       "command expired before it was picked up",
				CompletedAt: now,
			}
			continue
		}

		switch record.Status {
		case CommandStatusPending:
			// Hand it out.
		case CommandStatusRunning:
			// The agent claimed it but never reported back. The lease
			// expired, so the command is offered again.
			if now.Before(record.leaseExpiresAt) {
				continue
			}
		default:
			continue
		}

		record.Status = CommandStatusRunning
		record.StartedAt = &now
		record.leaseExpiresAt = now.Add(q.lease)
		return record, nil
	}

	return nil, ErrNoPendingCommand
}

// Complete records the result of a command. Completing a command twice keeps
// the first result, so a retried report is not an error.
func (q *CommandQueue) Complete(agentID, commandID string, result CommandResult) (*CommandRecord, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if result.Status != CommandStatusSucceeded && result.Status != CommandStatusFailed {
		return nil, &AgentError{Message: "invalid command result status"}
	}

	records := q.commands[agentID]
	for _, record := range records {
		if record.Command.ID != commandID {
			continue
		}
		if record.terminal() {
			return record, nil
		}

		now := q.now().UTC()
		record.Status = result.Status
		record.Result = &result
		record.CompletedAt = &now
		record.leaseExpiresAt = time.Time{}
		return record, nil
	}

	return nil, ErrCommandNotFound
}

// Get returns a single command record.
func (q *CommandQueue) Get(agentID, commandID string) (*CommandRecord, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, record := range q.commands[agentID] {
		if record.Command.ID == commandID {
			return record, nil
		}
	}
	return nil, ErrCommandNotFound
}

// List returns the command records of an agent, oldest first.
func (q *CommandQueue) List(agentID string) []*CommandRecord {
	q.mu.Lock()
	defer q.mu.Unlock()

	records := q.commands[agentID]
	out := make([]*CommandRecord, 0, len(records))
	out = append(out, records...)
	return out
}

// pruneTerminal drops finished records, oldest first, and returns the
// remaining ones.
func pruneTerminal(records []*CommandRecord) []*CommandRecord {
	out := records[:0]
	for _, record := range records {
		if !record.terminal() {
			out = append(out, record)
		}
	}
	return out
}

// Errors returned by the command queue.
var (
	// ErrCommandQueueFull means the agent already has too many commands.
	ErrCommandQueueFull = &AgentError{Message: "command queue is full"}
	// ErrCommandNotFound means no such command exists for the agent.
	ErrCommandNotFound = &AgentError{Message: "command not found"}
	// ErrNoPendingCommand means the agent has nothing to do.
	ErrNoPendingCommand = &AgentError{Message: "no pending command"}
	// ErrCommandUnauthorized means the caller did not present a valid
	// credential for the agent it claims to act for.
	ErrCommandUnauthorized = &AgentError{Message: "unauthorized"}
)

// validateCommandType reports whether the command type is supported.
func validateCommandType(cmdType CommandType) error {
	switch cmdType {
	case CommandTypeDeploy, CommandTypeStop, CommandTypeRemove:
		return nil
	default:
		return fmt.Errorf("unsupported command type %q", cmdType)
	}
}

// validateCommandPayload performs a minimal sanity check on the payload so
// obviously broken commands are rejected before they reach an agent. The
// control plane does not know the full payload schema; the agent validates
// it again before executing it.
func validateCommandPayload(cmdType CommandType, payload json.RawMessage) error {
	if len(payload) == 0 {
		return fmt.Errorf("payload is required for command type %q", cmdType)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return fmt.Errorf("payload must be a JSON object: %w", err)
	}

	switch cmdType {
	case CommandTypeDeploy:
		for _, key := range []string{"name", "image"} {
			value, ok := fields[key]
			if !ok {
				return fmt.Errorf("payload field %q is required for a deploy command", key)
			}
			var s string
			if err := json.Unmarshal(value, &s); err != nil {
				return fmt.Errorf("payload field %q must be a string", key)
			}
			if s == "" {
				return fmt.Errorf("payload field %q must not be empty", key)
			}
		}
	case CommandTypeStop, CommandTypeRemove:
		value, ok := fields["name"]
		if !ok {
			return fmt.Errorf("payload field \"name\" is required for a %s command", cmdType)
		}
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return fmt.Errorf("payload field \"name\" must be a string")
		}
		if s == "" {
			return fmt.Errorf("payload field \"name\" must not be empty")
		}
	}

	return nil
}
