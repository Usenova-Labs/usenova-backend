package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DeploymentState is the lifecycle state of a deployment record.
type DeploymentState string

const (
	// DeploymentStatePending means the deploy command is queued for an agent.
	DeploymentStatePending DeploymentState = "pending"
	// DeploymentStateRunning means an agent claimed the deploy command.
	DeploymentStateRunning DeploymentState = "running"
	// DeploymentStateSucceeded means the agent deployed the service.
	DeploymentStateSucceeded DeploymentState = "succeeded"
	// DeploymentStateFailed means the agent could not deploy the service.
	DeploymentStateFailed DeploymentState = "failed"
)

// Valid reports whether the state is known.
func (s DeploymentState) Valid() bool {
	switch s {
	case DeploymentStatePending, DeploymentStateRunning, DeploymentStateSucceeded, DeploymentStateFailed:
		return true
	default:
		return false
	}
}

// Deployment is the control plane's durable record of a remote deployment.
//
// The full service spec is intentionally not stored: it may contain secrets in
// environment variables. The record keeps the identifying fields needed to
// list and audit deployments.
type Deployment struct {
	ID          string            `json:"id"`
	AgentID     string            `json:"agent_id"`
	ServiceName string            `json:"service_name"`
	Image       string            `json:"image"`
	State       DeploymentState   `json:"state"`
	CommandID   string            `json:"command_id,omitempty"`
	Requester   string            `json:"requester,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// DeploymentFilter narrows the deployments returned by List. Zero values
// match every deployment.
type DeploymentFilter struct {
	AgentID string
	State   DeploymentState
}

// DeploymentStore is a thread-safe store for deployment records. When a
// database handle is attached every mutation is written through to SQLite so
// records survive control-plane restarts.
type DeploymentStore struct {
	mu          sync.RWMutex
	deployments map[string]*Deployment
	byCommand   map[string]string
	db          *sql.DB
}

// NewDeploymentStore creates a new in-memory-only DeploymentStore.
func NewDeploymentStore() *DeploymentStore {
	return &DeploymentStore{
		deployments: make(map[string]*Deployment),
		byCommand:   make(map[string]string),
	}
}

// NewPersistentDeploymentStore creates a DeploymentStore backed by SQLite and
// loads every previously recorded deployment into memory.
func NewPersistentDeploymentStore(db *sql.DB) (*DeploymentStore, error) {
	s := NewDeploymentStore()
	s.db = db
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *DeploymentStore) load() error {
	rows, err := s.db.Query(`
		SELECT id, agent_id, service_name, image, state, command_id, requester,
		       metadata, created_at, updated_at
		FROM deployments`)
	if err != nil {
		return fmt.Errorf("load deployments: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			d         Deployment
			state     string
			metadata  string
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(
			&d.ID, &d.AgentID, &d.ServiceName, &d.Image, &state, &d.CommandID,
			&d.Requester, &metadata, &createdAt, &updatedAt,
		); err != nil {
			return fmt.Errorf("scan deployment: %w", err)
		}

		d.State = DeploymentState(state)
		if !d.State.Valid() {
			return fmt.Errorf("deployment %q has unknown state %q", d.ID, state)
		}
		if metadata != "" {
			if err := json.Unmarshal([]byte(metadata), &d.Metadata); err != nil {
				return fmt.Errorf("parse metadata for deployment %q: %w", d.ID, err)
			}
		}
		d.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return fmt.Errorf("parse created_at for deployment %q: %w", d.ID, err)
		}
		d.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return fmt.Errorf("parse updated_at for deployment %q: %w", d.ID, err)
		}

		s.deployments[d.ID] = &d
		if d.CommandID != "" {
			s.byCommand[d.CommandID] = d.ID
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate deployments: %w", err)
	}
	return nil
}

// Create stores a new deployment record. Required fields are validated and an
// ID is generated when it is empty.
func (s *DeploymentStore) Create(d Deployment) (*Deployment, error) {
	if d.AgentID == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	if d.ServiceName == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if d.Image == "" {
		return nil, fmt.Errorf("image is required")
	}
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.State == "" {
		d.State = DeploymentStatePending
	}
	if !d.State.Valid() {
		return nil, fmt.Errorf("invalid deployment state %q", d.State)
	}

	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = now
	}
	if d.Metadata == nil {
		d.Metadata = map[string]string{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deployments[d.ID]; exists {
		return nil, fmt.Errorf("deployment %q already exists", d.ID)
	}
	if d.CommandID != "" {
		if existingID, exists := s.byCommand[d.CommandID]; exists {
			return nil, fmt.Errorf("command %q already belongs to deployment %q", d.CommandID, existingID)
		}
	}

	if s.db != nil {
		if err := s.persistDeployment(&d); err != nil {
			return nil, fmt.Errorf("persist deployment: %w", err)
		}
	}

	stored := cloneDeployment(&d)
	s.deployments[d.ID] = stored
	if d.CommandID != "" {
		s.byCommand[d.CommandID] = d.ID
	}
	return cloneDeployment(stored), nil
}

// Get returns a deployment by ID.
func (s *DeploymentStore) Get(id string) (*Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	d, exists := s.deployments[id]
	if !exists {
		return nil, ErrDeploymentNotFound
	}
	return cloneDeployment(d), nil
}

// List returns deployments matching filter, newest first.
func (s *DeploymentStore) List(filter DeploymentFilter) []*Deployment {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Deployment, 0, len(s.deployments))
	for _, d := range s.deployments {
		if filter.AgentID != "" && d.AgentID != filter.AgentID {
			continue
		}
		if filter.State != "" && d.State != filter.State {
			continue
		}
		out = append(out, cloneDeployment(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// UpdateState updates a deployment's lifecycle state.
func (s *DeploymentStore) UpdateState(id string, state DeploymentState) (*Deployment, error) {
	if !state.Valid() {
		return nil, fmt.Errorf("invalid deployment state %q", state)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	d, exists := s.deployments[id]
	if !exists {
		return nil, ErrDeploymentNotFound
	}
	return s.updateStateLocked(d, state)
}

// UpdateStateByCommandID updates the deployment linked to commandID. It
// returns ErrDeploymentNotFound when the command does not belong to a
// deployment, so callers can ignore unrelated command-channel traffic.
func (s *DeploymentStore) UpdateStateByCommandID(commandID string, state DeploymentState) (*Deployment, error) {
	if commandID == "" {
		return nil, ErrDeploymentNotFound
	}
	if !state.Valid() {
		return nil, fmt.Errorf("invalid deployment state %q", state)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id, exists := s.byCommand[commandID]
	if !exists {
		return nil, ErrDeploymentNotFound
	}
	d, exists := s.deployments[id]
	if !exists {
		return nil, ErrDeploymentNotFound
	}
	return s.updateStateLocked(d, state)
}

func (s *DeploymentStore) updateStateLocked(d *Deployment, state DeploymentState) (*Deployment, error) {
	// Terminal states are final. A re-delivered command or a duplicate result
	// must not move a finished deployment back to running.
	if d.State == DeploymentStateSucceeded || d.State == DeploymentStateFailed {
		return cloneDeployment(d), nil
	}

	now := time.Now().UTC()
	if s.db != nil {
		if _, err := s.db.Exec(
			`UPDATE deployments SET state = ?, updated_at = ? WHERE id = ?`,
			string(state), now.Format(time.RFC3339Nano), d.ID,
		); err != nil {
			return nil, fmt.Errorf("update deployment %q: %w", d.ID, err)
		}
	}

	d.State = state
	d.UpdatedAt = now
	return cloneDeployment(d), nil
}

func (s *DeploymentStore) persistDeployment(d *Deployment) error {
	metadata, err := json.Marshal(d.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO deployments
			(id, agent_id, service_name, image, state, command_id, requester, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID,
		d.AgentID,
		d.ServiceName,
		d.Image,
		string(d.State),
		d.CommandID,
		d.Requester,
		string(metadata),
		d.CreatedAt.Format(time.RFC3339Nano),
		d.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert deployment %q: %w", d.ID, err)
	}
	return nil
}

func cloneDeployment(d *Deployment) *Deployment {
	cp := *d
	if d.Metadata != nil {
		cp.Metadata = make(map[string]string, len(d.Metadata))
		for k, v := range d.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp
}

// ErrDeploymentNotFound means no deployment with the requested ID exists.
var ErrDeploymentNotFound = &AgentError{"deployment not found"}
